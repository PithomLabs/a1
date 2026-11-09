package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// admissionExpSQL is the Gate 1 candidate mechanism: session-barrier
// wrappers with a hold table, and the XID admission ticket lifecycle.
// Test-harness scope ONLY: applied to disposable databases by these
// tests, never to migrations/. Proven designs graduate to migration
// 00013 after Gate 1 approval with this file's tests as the
// acceptance battery.
const admissionExpSQL = `
CREATE UNLOGGED TABLE IF NOT EXISTS exp_admission_ticket (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL,
    employee_ids uuid[] NOT NULL DEFAULT '{}',
    pack_ids text[] NOT NULL DEFAULT '{}',
    backend_pid integer NOT NULL,
    ticket_xid xid8 NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    consumed boolean NOT NULL DEFAULT false,
    released boolean NOT NULL DEFAULT false
);
REVOKE ALL ON TABLE public.exp_admission_ticket FROM PUBLIC;

CREATE UNLOGGED TABLE IF NOT EXISTS exp_barrier_hold (
    backend_pid integer NOT NULL,
    kind text NOT NULL CHECK (kind IN ('population', 'input', 'pack')),
    identity text NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid,
    pack text,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (backend_pid, identity)
);
REVOKE ALL ON TABLE public.exp_barrier_hold FROM PUBLIC;

-- Canonical key expressions, byte-identical to the frozen
-- transaction-scope helpers (00011 acquire_payroll_input_lock,
-- 00012 acquire_payroll_population_lock / acquire_rule_pack_lock):
-- session and transaction locks share one advisory namespace, so
-- readers and writers on the same identity mutually exclude. A
-- compatibility test proves the keys match; 00013 introduces one
-- canonical key function to end the duplication.

CREATE OR REPLACE FUNCTION exp_admission_lock(p_kind text, p_employer uuid, p_employee uuid, p_pack text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_ident text;
    v_key bigint;
BEGIN
    IF p_kind NOT IN ('population', 'input', 'pack') THEN
        RAISE EXCEPTION 'unknown admission barrier kind %', p_kind;
    END IF;
    IF p_kind = 'population' AND (p_employer IS NULL OR p_employee IS NOT NULL OR p_pack IS NOT NULL) THEN
        RAISE EXCEPTION 'population barrier takes employer only';
    END IF;
    IF p_kind = 'input' AND (p_employer IS NULL OR p_employee IS NULL OR p_pack IS NOT NULL) THEN
        RAISE EXCEPTION 'input barrier takes employer and employee only';
    END IF;
    IF p_kind = 'pack' AND (p_employer IS NULL OR p_employee IS NOT NULL OR p_pack IS NULL) THEN
        RAISE EXCEPTION 'pack barrier takes employer and pack only';
    END IF;
    v_ident := p_kind || ':' || p_employer::text || ':' || coalesce(p_employee::text, '') || ':' || coalesce(p_pack, '');
    -- Idempotent: re-taking a held session lock would bump its
    -- count and strand it past a single unlock.
    PERFORM 1 FROM public.exp_barrier_hold WHERE backend_pid = pg_backend_pid() AND identity = v_ident;
    IF FOUND THEN RETURN; END IF;
    IF p_kind = 'population' THEN
        v_key := hashtext('pay-population:' || p_employer::text);
    ELSIF p_kind = 'input' THEN
        v_key := hashtext('pay-input:' || p_employer::text || ':' || p_employee::text);
    ELSE
        v_key := hashtext('pay-pack:' || p_employer::text || ':' || p_pack);
    END IF;
    PERFORM pg_advisory_lock(v_key);
    INSERT INTO public.exp_barrier_hold (backend_pid, kind, identity, employer_id, employee_id, pack)
    VALUES (pg_backend_pid(), p_kind, v_ident, p_employer, p_employee, p_pack);
END $$;

CREATE OR REPLACE FUNCTION exp_admission_unlock(p_kind text, p_employer uuid, p_employee uuid, p_pack text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_ident text;
    v_key bigint;
    v_ok boolean;
BEGIN
    IF p_kind NOT IN ('population', 'input', 'pack') THEN
        RAISE EXCEPTION 'unknown admission barrier kind %', p_kind;
    END IF;
    v_ident := p_kind || ':' || p_employer::text || ':' || coalesce(p_employee::text, '') || ':' || coalesce(p_pack, '');
    PERFORM 1 FROM public.exp_barrier_hold WHERE backend_pid = pg_backend_pid() AND identity = v_ident;
    IF NOT FOUND THEN RETURN false; END IF;
    IF p_kind = 'population' THEN
        v_key := hashtext('pay-population:' || p_employer::text);
    ELSIF p_kind = 'input' THEN
        v_key := hashtext('pay-input:' || p_employer::text || ':' || p_employee::text);
    ELSE
        v_key := hashtext('pay-pack:' || p_employer::text || ':' || p_pack);
    END IF;
    SELECT pg_advisory_unlock(v_key) INTO v_ok;
    DELETE FROM public.exp_barrier_hold WHERE backend_pid = pg_backend_pid() AND identity = v_ident;
    RETURN v_ok;
END $$;

CREATE OR REPLACE FUNCTION exp_assert_holds(p_backend integer, p_employer uuid, p_employees uuid[], p_packs text[])
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM 1 FROM public.exp_barrier_hold
    WHERE backend_pid = p_backend AND kind = 'population' AND employer_id = p_employer;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'admission population barrier not held';
    END IF;
    IF EXISTS (SELECT 1 FROM unnest(coalesce(p_employees, '{}')) e WHERE NOT EXISTS (
        SELECT 1 FROM public.exp_barrier_hold h WHERE h.backend_pid = p_backend
          AND h.kind = 'input' AND h.employer_id = p_employer AND h.employee_id = e)) THEN
        RAISE EXCEPTION 'admission employee barriers incomplete';
    END IF;
    IF EXISTS (SELECT 1 FROM unnest(coalesce(p_packs, '{}')) p WHERE NOT EXISTS (
        SELECT 1 FROM public.exp_barrier_hold h WHERE h.backend_pid = p_backend
          AND h.kind = 'pack' AND h.employer_id = p_employer AND h.pack = p)) THEN
        RAISE EXCEPTION 'admission pack barriers incomplete';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION exp_issue_ticket(p_employer uuid, p_employees uuid[], p_packs text[])
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_id uuid;
BEGIN
    -- The ticket must commit in a transaction that predates the RR
    -- snapshot. Issuance inside a snapshot transaction is rejected:
    -- its own XID would be invisible to its own snapshot, and the
    -- ordering proof would be vacuous.
    IF current_setting('transaction_isolation') IS DISTINCT FROM 'read committed' THEN
        RAISE EXCEPTION 'admission tickets issue only in READ COMMITTED, not %',
            current_setting('transaction_isolation');
    END IF;
    PERFORM public.exp_assert_holds(pg_backend_pid(), p_employer, p_employees, p_packs);
    INSERT INTO public.exp_admission_ticket (employer_id, employee_ids, pack_ids, backend_pid, ticket_xid)
    VALUES (p_employer, coalesce(p_employees, '{}'), coalesce(p_packs, '{}'),
            pg_backend_pid(), pg_current_xact_id()::xid8)
    RETURNING id INTO v_id;
    RETURN v_id;
END $$;

CREATE OR REPLACE FUNCTION exp_verify_ticket(p_ticket uuid, p_employer uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    r RECORD;
BEGIN
    -- Row visibility IS the snapshot test: a ticket committed after
    -- this snapshot began (or aborted, or unknown) is simply absent.
    SELECT * INTO r FROM public.exp_admission_ticket WHERE id = p_ticket;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'admission ticket not visible in this snapshot (issued after the snapshot began, aborted, or unknown)';
    END IF;
    -- Belt and braces on the XID itself: never an xmin comparison.
    IF NOT pg_visible_in_snapshot(r.ticket_xid, pg_current_snapshot()) THEN
        RAISE EXCEPTION 'admission ticket committed after this snapshot began';
    END IF;
    IF r.backend_pid != pg_backend_pid() THEN
        RAISE EXCEPTION 'admission ticket belongs to another backend';
    END IF;
    IF r.employer_id IS DISTINCT FROM p_employer THEN
        RAISE EXCEPTION 'admission ticket is for another employer';
    END IF;
    IF r.consumed THEN
        RAISE EXCEPTION 'admission ticket already consumed';
    END IF;
    IF r.released THEN
        RAISE EXCEPTION 'admission ticket released';
    END IF;
    IF r.created_at + make_interval(mins => 5) < now() THEN
        RAISE EXCEPTION 'admission ticket expired';
    END IF;
    -- The ticketed barriers must STILL be held: use-after-unlock is
    -- rejected here, inside the same call the writer will make at
    -- derivation time.
    PERFORM public.exp_assert_holds(pg_backend_pid(), p_employer, r.employee_ids, r.pack_ids);
    UPDATE public.exp_admission_ticket SET consumed = true WHERE id = p_ticket;
END $$;

CREATE OR REPLACE FUNCTION exp_release_ticket(p_ticket uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    r RECORD;
BEGIN
    SELECT * INTO r FROM public.exp_admission_ticket WHERE id = p_ticket;
    IF NOT FOUND THEN RETURN; END IF;
    IF r.backend_pid != pg_backend_pid()
       AND NOT (SELECT rolsuper FROM pg_roles WHERE rolname = session_user) THEN
        RAISE EXCEPTION 'cannot release another backend''s ticket';
    END IF;
    UPDATE public.exp_admission_ticket SET released = true WHERE id = p_ticket;
END $$;

CREATE OR REPLACE FUNCTION exp_gc_admission()
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    -- Time-based garbage collection of ephemeral rows only; never
    -- part of the ordering proof. Crashed backends lose UNLOGGED
    -- rows automatically; discarded connections leave rows that age
    -- out here.
    DELETE FROM public.exp_admission_ticket WHERE consumed AND created_at < now() - make_interval(hours => 1);
    DELETE FROM public.exp_barrier_hold WHERE granted_at < now() - make_interval(hours => 1);
END $$;

CREATE TABLE IF NOT EXISTS exp_membership (
    employer_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('employee', 'pack')),
    member_id text NOT NULL,
    PRIMARY KEY (employer_id, kind, member_id)
);
ALTER TABLE public.exp_membership OWNER TO migration_admin;
GRANT SELECT, INSERT, DELETE ON TABLE public.exp_membership TO app;

ALTER TABLE public.exp_admission_ticket OWNER TO migration_admin;
ALTER TABLE public.exp_barrier_hold OWNER TO migration_admin;
ALTER FUNCTION public.exp_admission_lock(text, uuid, uuid, text) OWNER TO migration_admin;
ALTER FUNCTION public.exp_admission_unlock(text, uuid, uuid, text) OWNER TO migration_admin;
ALTER FUNCTION public.exp_assert_holds(integer, uuid, uuid[], text[]) OWNER TO migration_admin;
ALTER FUNCTION public.exp_issue_ticket(uuid, uuid[], text[]) OWNER TO migration_admin;
ALTER FUNCTION public.exp_verify_ticket(uuid, uuid) OWNER TO migration_admin;
ALTER FUNCTION public.exp_release_ticket(uuid) OWNER TO migration_admin;
ALTER FUNCTION public.exp_gc_admission() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.exp_admission_lock(text, uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.exp_admission_lock(text, uuid, uuid, text) TO app;
REVOKE ALL ON FUNCTION public.exp_admission_unlock(text, uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.exp_admission_unlock(text, uuid, uuid, text) TO app;
REVOKE ALL ON FUNCTION public.exp_assert_holds(integer, uuid, uuid[], text[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.exp_issue_ticket(uuid, uuid[], text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.exp_issue_ticket(uuid, uuid[], text[]) TO app;
REVOKE ALL ON FUNCTION public.exp_verify_ticket(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.exp_verify_ticket(uuid, uuid) TO app;
REVOKE ALL ON FUNCTION public.exp_release_ticket(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.exp_release_ticket(uuid) TO app;
REVOKE ALL ON FUNCTION public.exp_gc_admission() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.exp_gc_admission() TO app;
`

// advisoryRevokeSQL revokes every advisory-lock overload from PUBLIC
// and grants the set to migration_admin, so DEFINER wrappers keep
// working while direct app use fails. Superuser-only (pg_catalog
// ownership); generated programmatically so a future overload
// cannot slip the inventory — the test asserts the exact count.
const advisoryRevokeSQL = `
DO $$DECLARE r RECORD; BEGIN
  FOR r IN SELECT 'pg_catalog.' || quote_ident(proname) || '(' || pg_get_function_identity_arguments(oid) || ')' AS sig
           FROM pg_proc WHERE pronamespace = 'pg_catalog'::regnamespace AND proname LIKE '%advisory%' LOOP
    EXECUTE 'REVOKE ALL ON FUNCTION ' || r.sig || ' FROM PUBLIC';
    EXECUTE 'GRANT EXECUTE ON FUNCTION ' || r.sig || ' TO migration_admin';
  END LOOP;
END $$;
`

var (
	admissionOnce sync.Once
	admissionDB   dbutil.TestDB
	admissionErr  error

	revokeOnce sync.Once
	revokeDB   dbutil.TestDB
	revokeErr  error
)

func testCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

func migrateUp(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := testCtx()
	defer cancel()
	if _, err := MigrateUp(ctx, dsn, coremigrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// admissionSetup provisions the shared experiment database (migrated
// to head plus the experimental admission SQL, idempotent).
func admissionSetup(t *testing.T) dbutil.TestDB {
	t.Helper()
	admissionOnce.Do(func() {
		ctx, cancel := testCtx()
		defer cancel()
		dsn := dbutil.DSN(t)
		base := dbName(t, dsn)
		var err error
		admissionDB, err = dbutil.SetupDB(ctx, dsn, base+"_admission",
			func(ctx context.Context, migDSN string) error {
				_, err := MigrateUp(ctx, migDSN, coremigrations.FS)
				return err
			})
		if err != nil {
			admissionErr = err
			return
		}
		c, err := pgx.Connect(ctx, admissionDB.MigDSN)
		if err != nil {
			admissionErr = err
			return
		}
		defer c.Close(ctx)
		_, admissionErr = c.Exec(ctx, admissionExpSQL)
	})
	if admissionErr != nil {
		t.Fatalf("admission setup: %v", admissionErr)
	}
	return admissionDB
}

// revokeSetup provisions an isolated database with the advisory
// revoke applied (superuser). The database is dropped first under
// the disposable-identity guard: loopback host plus a fixed
// experiment suffix, never the shared base name.
func revokeSetup(t *testing.T) dbutil.TestDB {
	t.Helper()
	revokeOnce.Do(func() {
		ctx, cancel := testCtx()
		defer cancel()
		dsn := dbutil.DSN(t)
		name := dbName(t, dsn) + "_admrevoke"
		assertDisposableDrop(dsn, name)
		maint, err := pgx.Connect(ctx, dsn)
		if err != nil {
			revokeErr = err
			return
		}
		if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			revokeErr = err
			maint.Close(ctx)
			return
		}
		maint.Close(ctx)
		revokeDB, err = dbutil.SetupDB(ctx, dsn, name,
			func(ctx context.Context, migDSN string) error {
				_, err := MigrateUp(ctx, migDSN, coremigrations.FS)
				return err
			})
		if err != nil {
			revokeErr = err
			return
		}
		c, err := pgx.Connect(ctx, revokeDB.MigDSN)
		if err != nil {
			revokeErr = err
			return
		}
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, admissionExpSQL); err != nil {
			revokeErr = err
			return
		}
		su, err := pgx.Connect(ctx, dbutil.WithDB(dsn, name))
		if err != nil {
			revokeErr = err
			return
		}
		defer su.Close(ctx)
		var isSuper bool
		if err := su.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&isSuper); err != nil || !isSuper {
			revokeErr = fmt.Errorf("revoke requires a superuser connection: %v", err)
			return
		}
		_, revokeErr = su.Exec(ctx, advisoryRevokeSQL)
	})
	if revokeErr != nil {
		t.Fatalf("revoke setup: %v", revokeErr)
	}
	return revokeDB
}

func dbName(t *testing.T, dsn string) string {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	base := cfg.ConnConfig.Database
	if base == "" || base == "postgres" {
		t.Fatalf("test DSN must name a dedicated test database")
	}
	return base
}

// assertDisposableDrop is the review6.1 §K prototype: a drop is
// allowed only against a loopback server and a fixed experiment
// suffix — a bare naming convention alone is never sufficient.
func assertDisposableDrop(dsn, name string) {
	u, err := url.Parse(dsn)
	if err != nil {
		panic("unparseable test DSN")
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		panic("refusing to drop on non-loopback host " + host)
	}
	switch {
	case strings.HasSuffix(name, "_admrevoke"),
		strings.HasSuffix(name, "_admgcycle"),
		strings.HasSuffix(name, "_admredef"),
		strings.HasSuffix(name, "_gate2plain"),
		strings.HasSuffix(name, "_gate2revoked"),
		strings.HasSuffix(name, "_gate2b"):
	default:
		panic("refusing to drop non-experiment database " + name)
	}
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := testCtx()
	defer cancel()
	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openConn(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	ctx, cancel := testCtx()
	defer cancel()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func appPool(t *testing.T, db dbutil.TestDB) *pgxpool.Pool { return openPool(t, db.AppDSN) }

func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func execConn(t *testing.T, c *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := c.Exec(tctx(t), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func newEmpID(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var id string
	if err := c.QueryRow(tctx(t), `SELECT gen_random_uuid()`).Scan(&id); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

func lockPop(t *testing.T, c *pgx.Conn, emp string) {
	t.Helper()
	execConn(t, c, `SELECT exp_admission_lock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

func ticketForPop(t *testing.T, c *pgx.Conn, emp string) string {
	t.Helper()
	var id string
	if err := c.QueryRow(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp).Scan(&id); err != nil {
		t.Fatalf("issue: %v", err)
	}
	return id
}

func expectErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want error containing %q, got %v", want, err)
	}
}

// T1: a ticket committed before the snapshot verifies.
func TestTicketBeforeSnapshotAccepted(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, c, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	execConn(t, c, `COMMIT`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// T2: a ticket committed after the snapshot began is rejected BY
// VISIBILITY (not by the pid check): the row is absent from the
// reader's snapshot. This is the stale-RR attack failing.
func TestTicketAfterSnapshotRejected(t *testing.T) {
	db := admissionSetup(t)
	x := openConn(t, db.AppDSN)
	y := openConn(t, db.AppDSN)
	emp := newEmpID(t, x)
	execConn(t, x, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, x, `SELECT 1`)
	lockPop(t, y, emp)
	var ticket string
	if err := y.QueryRow(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp).Scan(&ticket); err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, err := x.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "not visible in this snapshot")
	if strings.Contains(err.Error(), "another backend") {
		t.Fatalf("wrong gate fired (want visibility, got pid): %v", err)
	}
	execConn(t, x, `ROLLBACK`)
	execConn(t, y, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// T3: issuance inside a snapshot transaction is rejected at issuance.
func TestTicketNestedIssueRejected(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp)
	expectErr(t, err, "only in READ COMMITTED")
	execConn(t, c, `ROLLBACK`)
}

// T4: a timely ticket from another backend is rejected BY PID (row
// and XID both visible — only ownership fails).
func TestTicketForeignBackendRejected(t *testing.T) {
	db := admissionSetup(t)
	x := openConn(t, db.AppDSN)
	y := openConn(t, db.AppDSN)
	emp := newEmpID(t, x)
	lockPop(t, y, emp)
	var ticket string
	if err := y.QueryRow(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp).Scan(&ticket); err != nil {
		t.Fatalf("issue: %v", err)
	}
	execConn(t, x, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := x.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "another backend")
	execConn(t, x, `ROLLBACK`)
	execConn(t, y, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// T5: expired and released tickets are rejected even when timely.
func TestTicketExpiredRejected(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	execConn(t, su, `UPDATE exp_admission_ticket SET created_at = now() - make_interval(hours => 1) WHERE id = $1::uuid`, ticket)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "expired")
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

func TestTicketReleasedRejected(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	execConn(t, c, `SELECT exp_release_ticket($1::uuid)`, ticket)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "released")
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// T6: an unrelated xid-bearing transaction pinning global xmin does
// NOT false-reject a valid ticket: visibility is per-XID, and the
// test proves the pressure existed (holder has a backend_xmin).
func TestTicketOldXminAccepted(t *testing.T) {
	db := admissionSetup(t)
	h := openConn(t, db.AppDSN)
	execConn(t, h, `BEGIN`)
	// An uncommitted writer pins global xmin (temp tables are
	// unavailable: SetupDB revokes TEMPORARY from app, so the
	// pressure comes from a real uncommitted row instead).
	execConn(t, h, `INSERT INTO exp_membership (employer_id, kind, member_id)
		VALUES (gen_random_uuid(), 'employee', 'xmin-pressure')`)
	var holderXmin *uint32
	if err := h.QueryRow(tctx(t),
		`SELECT backend_xmin FROM pg_stat_activity WHERE pid = pg_backend_pid()`).Scan(&holderXmin); err != nil {
		t.Fatalf("holder xmin: %v", err)
	}
	if holderXmin == nil {
		t.Fatal("holder has no XID: pressure not established")
	}
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, c, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	execConn(t, c, `COMMIT`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
	execConn(t, h, `ROLLBACK`)
}

// T7: a ticket from an aborted transaction is invisible and rejected.
func TestTicketAbortedRejected(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	execConn(t, c, `BEGIN`)
	var ticket string
	if err := c.QueryRow(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp).Scan(&ticket); err != nil {
		t.Fatalf("issue: %v", err)
	}
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "not visible in this snapshot")
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// Single-use across commits; reuse after rollback is SOUND (barriers
// still held, snapshot still newer than barrier acquisition) and
// therefore accepted by design.
func TestTicketSingleUseAcrossCommits(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, c, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	execConn(t, c, `COMMIT`)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "already consumed")
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

func TestTicketReusableAfterRollback(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, c, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, c, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	execConn(t, c, `COMMIT`)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// Issuance requires the full barrier set held by this backend.
func TestTicketRequiresBarriers(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	_, err := c.Exec(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp)
	expectErr(t, err, "population barrier not held")
	lockPop(t, c, emp)
	var e1 string
	if err := c.QueryRow(tctx(t), `SELECT gen_random_uuid()`).Scan(&e1); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	_, err = c.Exec(tctx(t),
		`SELECT exp_issue_ticket($1::uuid, ARRAY[$2::uuid], '{}'::text[])`, emp, e1)
	expectErr(t, err, "employee barriers incomplete")
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// Verification requires the ticketed holds STILL present:
// use-after-unlock is rejected.
func TestVerifyRequiresHoldsPresent(t *testing.T) {
	db := admissionSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	lockPop(t, c, emp)
	ticket := ticketForPop(t, c, emp)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	expectErr(t, err, "not held")
	execConn(t, c, `ROLLBACK`)
}

// The revoke inventory is fail-closed: exactly the known overload
// set, every one denied to app/PUBLIC and granted to
// migration_admin. A future PostgreSQL overload breaks this count
// loudly instead of slipping the boundary.
func TestAdvisoryRevokeInventory(t *testing.T) {
	db := revokeSetup(t)
	c := openConn(t, db.AppDSN)
	var n int
	if err := c.QueryRow(tctx(t),
		`SELECT count(*) FROM pg_proc
		 WHERE pronamespace = 'pg_catalog'::regnamespace AND proname LIKE '%advisory%'`).Scan(&n); err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if n != 21 {
		t.Fatalf("advisory overloads = %d, want 21: inventory changed, re-audit the revoke", n)
	}
	rows, err := c.Query(tctx(t),
		`SELECT proname || '(' || pg_get_function_identity_arguments(oid) || ')' FROM pg_proc
		 WHERE pronamespace = 'pg_catalog'::regnamespace AND proname LIKE '%advisory%' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer rows.Close()
	var sigs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		sigs = append(sigs, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	for _, s := range sigs {
		var appOK, pubOK, migOK bool
		q := `SELECT has_function_privilege('app', $1, 'EXECUTE'),
		             has_function_privilege('public', $1, 'EXECUTE'),
		             has_function_privilege('migration_admin', $1, 'EXECUTE')`
		if err := su.QueryRow(tctx(t), q, s).Scan(&appOK, &pubOK, &migOK); err != nil {
			t.Fatalf("privs %s: %v", s, err)
		}
		if appOK || pubOK || !migOK {
			t.Fatalf("%s: app=%v public=%v migration_admin=%v, want false/false/true", s, appOK, pubOK, migOK)
		}
	}
	// Live denial in every overload family, as app.
	for _, call := range []string{
		`SELECT pg_advisory_lock(101)`,
		`SELECT pg_advisory_lock(102, 103)`,
		`SELECT pg_advisory_unlock(104)`,
		`SELECT pg_advisory_unlock_all()`,
		`SELECT pg_try_advisory_lock(105)`,
		`SELECT pg_advisory_xact_lock(106)`,
		`SELECT pg_advisory_lock_shared(107)`,
		`SELECT pg_try_advisory_xact_lock_shared(108, 109)`,
	} {
		_, err := c.Exec(tctx(t), call)
		expectErr(t, err, "permission denied")
	}
}

// Post-revoke, DEFINER paths keep working while the three frozen
// INVOKER helpers fail: the revoke boundary requires 00013 to
// redefine them as DEFINER (proven by
// TestRevokeDefinerRedefinition). SHG add succeeds because nesting
// preserves the definer context: add_shg_instruction is DEFINER,
// so its internal acquire_payroll_input_lock call runs as
// migration_admin, which holds EXECUTE.
func TestRevokeDefinersSurvive(t *testing.T) {
	db := revokeSetup(t)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	// PINNED GAP (Gate 1): the frozen helpers are SECURITY
	// INVOKER, so direct app use dies on the inner advisory
	// call. 00013 redefines all three as DEFINER with
	// byte-identical keys; this assertion is rewritten to
	// success once the redefinition lands.
	for _, call := range []string{
		`SELECT acquire_payroll_population_lock($1::uuid)`,
		`SELECT acquire_rule_pack_lock($1::uuid, 'SG-MOM-OT-001')`,
	} {
		_, err := c.Exec(tctx(t), call, emp)
		expectErr(t, err, "permission denied")
	}
	// Experimental session wrappers are DEFINER: unaffected.
	lockPop(t, c, emp)
	execConn(t, c, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
	// PINNED GAP: employee INSERT as app fires the population
	// trigger through the INVOKER helper. (Employer INSERT as
	// app works: no lock trigger there.)
	var realEmp, e1 string
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := c.QueryRow(tctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ('RVK'||$1, 'Revoke', 'UEN-RVK'||$1, 'a') RETURNING id`, tag).Scan(&realEmp); err != nil {
		t.Fatalf("employer: %v", err)
	}
	_, err := c.Exec(tctx(t),
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, 'RX', 'RX', 'a', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE)`, realEmp)
	expectErr(t, err, "permission denied")
	// Function-internal advisory use through a DEFINER writer:
	// the employee fixture runs as migration_admin (see the
	// pinned gap above; restoration proven in
	// TestRevokeDefinerRedefinition). The SHG call below is
	// the as-app assertion.
	m := openConn(t, db.MigDSN)
	if err := m.QueryRow(tctx(t),
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, 'R1', 'R1', 'a', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE) RETURNING id`, realEmp).Scan(&e1); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := c.Exec(tctx(t),
		`SELECT add_shg_instruction($1::uuid, $2::uuid, 'CDAC', 'pending', NULL, 'revoke probe',
		  '2026-01-01'::date, NULL, NULL, NULL, NULL, 'test-op')`, realEmp, e1); err != nil {
		t.Fatalf("shg add as app post-revoke: %v", err)
	}
}

// freshRevokedDB provisions a private drop-first database at head
// plus the experimental SQL and the advisory revoke: one test's
// mutations (Down cycles, redefinitions) never leak into another's.
func freshRevokedDB(t *testing.T, suffix string) dbutil.TestDB {
	t.Helper()
	ctx, cancel := testCtx()
	defer cancel()
	dsn := dbutil.DSN(t)
	name := dbName(t, dsn) + suffix
	assertDisposableDrop(dsn, name)
	maint, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("maint: %v", err)
	}
	if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	maint.Close(ctx)
	db, err := dbutil.SetupDB(ctx, dsn, name,
		func(ctx context.Context, migDSN string) error {
			_, err := MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	mig := openConn(t, db.MigDSN)
	if _, err := mig.Exec(tctx(t), admissionExpSQL); err != nil {
		t.Fatalf("exp sql: %v", err)
	}
	su := openConn(t, dbutil.WithDB(dsn, name))
	if _, err := su.Exec(tctx(t), advisoryRevokeSQL); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	return db
}

// Goose migrates as migration_admin, which keeps EXECUTE: a full
// down/up cycle must work on a revoked database. The cycle runs on
// its own empty database: the shared revoke DB is populated by
// other tests and the guarded Down rightly refuses it.
func TestRevokeGooseCycle(t *testing.T) {
	db := freshRevokedDB(t, "_admgcycle")
	if err := downOnce(t, db.MigDSN); err != nil {
		t.Fatalf("down: %v", err)
	}
	migrateUp(t, db.MigDSN)
	if head := headVersion(t, db.MigDSN); head != 12 {
		t.Fatalf("head=%d, want 12 after re-apply", head)
	}
}

// The 00013 privilege core, proven on a disposable database:
// redefining the three frozen INVOKER helpers as SECURITY DEFINER
// with byte-identical bodies restores every writer path as app —
// direct helper calls, the DEFINER SHG writer, and the INVOKER
// timesheet trigger that fires the helper transitively (its
// post-lock nextval proves the lock ran inside the trigger).
// CREATE OR REPLACE preserves owner and grants, so callers see
// only the privilege path change. 00013 graduates this block
// verbatim (plus the canonical key function, proven separately).
func TestRevokeDefinerRedefinition(t *testing.T) {
	db := freshRevokedDB(t, "_admredef")
	mig := openConn(t, db.MigDSN)
	execConn(t, mig, `
CREATE OR REPLACE FUNCTION acquire_payroll_input_lock(p_employer uuid, p_employee uuid)
RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pay-input:' || p_employer::text || ':' || p_employee::text));
END
$$;
CREATE OR REPLACE FUNCTION acquire_payroll_population_lock(p_employer uuid)
RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pay-population:' || p_employer::text));
END
$$;
CREATE OR REPLACE FUNCTION acquire_rule_pack_lock(p_employer uuid, p_pack text)
RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pay-pack:' || p_employer::text || ':' || p_pack));
END
$$;`)
	c := openConn(t, db.AppDSN)
	emp := newEmpID(t, c)
	for _, call := range []string{
		`SELECT acquire_payroll_population_lock($1::uuid)`,
		`SELECT acquire_rule_pack_lock($1::uuid, 'SG-MOM-OT-001')`,
	} {
		if _, err := c.Exec(tctx(t), call, emp); err != nil {
			t.Fatalf("redefined helper %q as app: %v", call, err)
		}
	}
	var realEmp, e1, pp string
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := c.QueryRow(tctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ('RDF'||$1, 'Redef', 'UEN-RDF'||$1, 'a') RETURNING id`, tag).Scan(&realEmp); err != nil {
		t.Fatalf("employer: %v", err)
	}
	if err := c.QueryRow(tctx(t),
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, 'R1', 'R1', 'a', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE) RETURNING id`, realEmp).Scan(&e1); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := c.Exec(tctx(t),
		`SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, realEmp, e1); err != nil {
		t.Fatalf("redefined input helper as app: %v", err)
	}
	if _, err := c.Exec(tctx(t),
		`SELECT add_shg_instruction($1::uuid, $2::uuid, 'CDAC', 'pending', NULL, 'redef probe',
		  '2026-01-01'::date, NULL, NULL, NULL, NULL, 'test-op')`, realEmp, e1); err != nil {
		t.Fatalf("shg add as app post-redefinition: %v", err)
	}
	if err := c.QueryRow(tctx(t),
		`INSERT INTO pay_period (id, employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES (gen_random_uuid(), $1::uuid, 'RD1', '2026-01-01', '2026-01-31', '2026-02-05', '2026-02-05', '2026-02-05')
		 RETURNING id`, realEmp).Scan(&pp); err != nil {
		t.Fatalf("pay period: %v", err)
	}
	var seq int64
	if err := c.QueryRow(tctx(t),
		`INSERT INTO timesheet (id, employer_id, employee_id, pay_period_id, status)
		 VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3::uuid, 'approved') RETURNING basis_seq`,
		realEmp, e1, pp).Scan(&seq); err != nil {
		t.Fatalf("approved timesheet as app post-redefinition: %v", err)
	}
	if seq <= 0 {
		t.Fatalf("basis_seq=%d, want the trigger's post-lock nextval", seq)
	}
}

// PINNED FOLLOW-UP (Gate 1): employer bootstrap takes a direct
// advisory lock as app (internal/employer/employer.go) and therefore
// breaks under the revoke. 00013 must route bootstrap through a
// wrapper; this test is rewritten to assert success then. It pins
// the exact failing statement today.
func TestRevokeBootstrapGap(t *testing.T) {
	db := revokeSetup(t)
	c := openConn(t, db.AppDSN)
	_, err := c.Exec(tctx(t), `SELECT pg_advisory_xact_lock(hashtext('employer bootstrap'))`)
	expectErr(t, err, "permission denied")
}

// Session-held (wrapper) and transaction-held (helper) locks on the
// same identity share one namespace: the wrapper-held lock blocks
// the helper's key, proving byte-identical key derivation without
// sharing code with the frozen helpers.
func TestSessionXactKeyCompatibility(t *testing.T) {
	db := admissionSetup(t)
	a := openConn(t, db.AppDSN)
	b := openConn(t, db.AppDSN)
	emp := newEmpID(t, a)
	lockPop(t, a, emp)
	var blocked bool
	if err := b.QueryRow(tctx(t),
		`SELECT pg_try_advisory_xact_lock(hashtext('pay-population:' || $1::text))`, emp).Scan(&blocked); err != nil {
		t.Fatalf("try: %v", err)
	}
	if blocked {
		t.Fatal("xact try-lock succeeded against a wrapper-held session lock: key mismatch")
	}
	execConn(t, a, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
	if err := b.QueryRow(tctx(t),
		`SELECT pg_try_advisory_xact_lock(hashtext('pay-population:' || $1::text))`, emp).Scan(&blocked); err != nil {
		t.Fatalf("try after release: %v", err)
	}
	if !blocked {
		t.Fatal("xact try-lock still blocked after wrapper release")
	}
}

// Double-lock through the wrapper stays at count one: a single
// unlock fully releases.
func TestWrapperLockIdempotent(t *testing.T) {
	db := admissionSetup(t)
	a := openConn(t, db.AppDSN)
	b := openConn(t, db.AppDSN)
	emp := newEmpID(t, a)
	lockPop(t, a, emp)
	lockPop(t, a, emp)
	execConn(t, a, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
	var free bool
	if err := b.QueryRow(tctx(t),
		`SELECT pg_try_advisory_xact_lock(hashtext('pay-population:' || $1::text))`, emp).Scan(&free); err != nil {
		t.Fatalf("try: %v", err)
	}
	if !free {
		t.Fatal("lock count stranded above one: double-lock was not idempotent")
	}
	var unlocked bool
	if err := a.QueryRow(tctx(t),
		`SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp).Scan(&unlocked); err != nil {
		t.Fatalf("second unlock: %v", err)
	}
	if unlocked {
		t.Fatal("unlock without hold reported success")
	}
}

func seedMembers(t *testing.T, pool *pgxpool.Pool, emp string, employees, packs []string) {
	t.Helper()
	for _, e := range employees {
		if _, err := pool.Exec(tctx(t),
			`INSERT INTO exp_membership (employer_id, kind, member_id) VALUES ($1::uuid, 'employee', $2)
			 ON CONFLICT DO NOTHING`, emp, e); err != nil {
			t.Fatalf("seed employee: %v", err)
		}
	}
	for _, p := range packs {
		if _, err := pool.Exec(tctx(t),
			`INSERT INTO exp_membership (employer_id, kind, member_id) VALUES ($1::uuid, 'pack', $2)
			 ON CONFLICT DO NOTHING`, emp, p); err != nil {
			t.Fatalf("seed pack: %v", err)
		}
	}
}

func memberEnumerate(emp string) func(context.Context, RowQuerier) (LockSet, error) {
	return func(ctx context.Context, q RowQuerier) (LockSet, error) {
		var set LockSet
		rows, err := q.Query(ctx,
			`SELECT kind, member_id FROM exp_membership WHERE employer_id = $1::uuid`, emp)
		if err != nil {
			return set, err
		}
		defer rows.Close()
		for rows.Next() {
			var kind, id string
			if err := rows.Scan(&kind, &id); err != nil {
				return set, err
			}
			if kind == "employee" {
				set.Employees = append(set.Employees, id)
			} else {
				set.Packs = append(set.Packs, id)
			}
		}
		return set, rows.Err()
	}
}

func tryPopFree(t *testing.T, c *pgx.Conn, emp string) bool {
	t.Helper()
	var free bool
	if err := c.QueryRow(tctx(t),
		`SELECT pg_try_advisory_xact_lock(hashtext('pay-population:' || $1::text))`, emp).Scan(&free); err != nil {
		t.Fatalf("try: %v", err)
	}
	return free
}

var empUUIDSeq atomic.Int64

func empUUID(t *testing.T) string {
	t.Helper()
	// Unique identity per call: rerun-safe, no FK, no
	// same-nanosecond collision.
	return fmt.Sprintf("%08x-0000-4000-8000-%012x",
		time.Now().UnixNano()&0xffffffff,
		empUUIDSeq.Add(1)&0xffffffffffff)
}

// Full 8-step admission over table-backed enumeration: barriers,
// ticket, snapshot, verify, revalidation, work, commit, release.
func TestAdmissionHappy(t *testing.T) {
	db := admissionSetup(t)
	pool := appPool(t, db)
	c := openConn(t, db.AppDSN)
	emp := empUUID(t)
	e1 := empUUID(t)
	seedMembers(t, pool, emp, []string{e1}, []string{"pack-a"})
	var saw int
	out, err := Do(tctx(t), pool, emp, memberEnumerate(emp),
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM exp_membership WHERE employer_id = $1::uuid`, emp).Scan(&saw)
		})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Attempts != 1 || !out.Released || out.Discarded || out.TicketID == "" {
		t.Fatalf("outcome=%+v", out)
	}
	if saw != 2 {
		t.Fatalf("work saw %d members, want 2", saw)
	}
	if !tryPopFree(t, c, emp) {
		t.Fatal("population barrier still held after clean admission")
	}
}

// One divergence then stability: Do retries once and succeeds.
func TestAdmissionMismatchRetriesThenSucceeds(t *testing.T) {
	db := admissionSetup(t)
	pool := appPool(t, db)
	emp := empUUID(t)
	e1, e2 := empUUID(t), empUUID(t)
	var calls int
	enumerate := func(context.Context, RowQuerier) (LockSet, error) {
		calls++
		if calls == 1 {
			return LockSet{Employees: []string{e1}}, nil
		}
		return LockSet{Employees: []string{e1, e2}}, nil
	}
	out, err := Do(tctx(t), pool, emp, enumerate, func(context.Context, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Attempts != 2 || !out.Released || out.Discarded {
		t.Fatalf("outcome=%+v, want 2 attempts then release", out)
	}
}

// Persistent divergence exhausts the bound and surfaces the
// mismatch; cleanup still runs.
func TestAdmissionMismatchExhausts(t *testing.T) {
	db := admissionSetup(t)
	pool := appPool(t, db)
	emp := empUUID(t)
	e1, e2 := empUUID(t), empUUID(t)
	var calls int
	enumerate := func(context.Context, RowQuerier) (LockSet, error) {
		calls++
		if calls%2 == 1 {
			return LockSet{Employees: []string{e1}}, nil
		}
		return LockSet{Employees: []string{e1, e2}}, nil
	}
	out, err := Do(tctx(t), pool, emp, enumerate, func(context.Context, pgx.Tx) error { return nil })
	var mm *MismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("err=%v, want MismatchError", err)
	}
	if out.Attempts != 3 || !out.Released || out.Discarded {
		t.Fatalf("outcome=%+v, want 3 attempts then release", out)
	}
}

// Legitimate concurrent admissions serialize on the population
// barrier and all succeed with one attempt each: no false-reject
// livelock, across same and different employers.
func TestAdmissionConcurrentLegitimate(t *testing.T) {
	db := admissionSetup(t)
	pool := appPool(t, db)
	empA := empUUID(t)
	empB := empUUID(t)
	seedMembers(t, pool, empA, []string{empUUID(t)}, []string{"pack-a"})
	seedMembers(t, pool, empB, []string{empUUID(t)}, nil)
	type res struct {
		out Outcome
		err error
	}
	run := func(emp string) res {
		o, e := Do(tctx(t), pool, emp, memberEnumerate(emp),
			func(context.Context, pgx.Tx) error { return nil })
		return res{o, e}
	}
	results := make(chan res, 3)
	go func() { results <- run(empA) }()
	go func() { results <- run(empA) }()
	go func() { results <- run(empB) }()
	for i := 0; i < 3; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("concurrent admission: %v", r.err)
			}
			if r.out.Attempts != 1 || !r.out.Released || r.out.Discarded {
				t.Fatalf("outcome=%+v", r.out)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("concurrent admission timed out")
		}
	}
}

func TestAdmissionCleanupMatrix(t *testing.T) {
	db := admissionSetup(t)
	pool := appPool(t, db)
	probe := openConn(t, db.AppDSN)
	fresh := func() string {
		emp := empUUID(t)
		seedMembers(t, pool, emp, []string{empUUID(t)}, nil)
		return emp
	}

	t.Run("work error rolls back and releases", func(t *testing.T) {
		emp := fresh()
		out, err := Do(tctx(t), pool, emp, memberEnumerate(emp),
			func(context.Context, pgx.Tx) error { return errors.New("boom") })
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err=%v", err)
		}
		if !out.Released || out.Discarded {
			t.Fatalf("outcome=%+v", out)
		}
		if !tryPopFree(t, probe, emp) {
			t.Fatal("barrier held after work error")
		}
	})

	t.Run("work SQL error rolls back and releases", func(t *testing.T) {
		emp := fresh()
		out, err := Do(tctx(t), pool, emp, memberEnumerate(emp),
			func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT no_such_function_xyz()`)
				return err
			})
		if err == nil {
			t.Fatal("want SQL error")
		}
		if !out.Released || out.Discarded {
			t.Fatalf("outcome=%+v", out)
		}
		if !tryPopFree(t, probe, emp) {
			t.Fatal("barrier held after SQL error")
		}
	})

	t.Run("cancellation discards", func(t *testing.T) {
		emp := fresh()
		ctx, cancel := context.WithCancel(tctx(t))
		defer cancel()
		time.AfterFunc(200*time.Millisecond, cancel)
		out, err := Do(ctx, pool, emp, memberEnumerate(emp),
			func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT pg_sleep(30)`)
				return err
			})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context canceled", err)
		}
		if out.Released || !out.Discarded {
			t.Fatalf("outcome=%+v, want discard", out)
		}
	})

	t.Run("deadline expiry discards", func(t *testing.T) {
		emp := fresh()
		ctx, cancel := context.WithTimeout(tctx(t), 200*time.Millisecond)
		defer cancel()
		out, err := Do(ctx, pool, emp, memberEnumerate(emp),
			func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT pg_sleep(30)`)
				return err
			})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v, want deadline exceeded", err)
		}
		if out.Released || !out.Discarded {
			t.Fatalf("outcome=%+v, want discard", out)
		}
	})

	t.Run("panic propagates after cleanup", func(t *testing.T) {
		emp := fresh()
		func() {
			defer func() {
				if p := recover(); p != "boom" {
					t.Fatalf("recovered %v, want boom", p)
				}
			}()
			_, _ = Do(tctx(t), pool, emp, memberEnumerate(emp),
				func(context.Context, pgx.Tx) error { panic("boom") })
		}()
		if !tryPopFree(t, probe, emp) {
			t.Fatal("barrier held after panic")
		}
	})

	t.Run("unlock failure discards", func(t *testing.T) {
		emp := fresh()
		started := make(chan string, 1)
		proceed := make(chan struct{})
		type res struct {
			out Outcome
			err error
		}
		done := make(chan res, 1)
		go func() {
			o, e := Do(tctx(t), pool, emp, memberEnumerate(emp),
				func(ctx context.Context, tx pgx.Tx) error {
					var pid int32
					if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
						return err
					}
					started <- fmt.Sprint(pid)
					select {
					case <-proceed:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(30 * time.Second):
						return errors.New("proceed timeout")
					}
				})
			done <- res{o, e}
		}()
		var pid string
		select {
		case pid = <-started:
		case <-time.After(30 * time.Second):
			t.Fatal("work never started")
		}
		// Simulate hold-table inconsistency out of band: the
		// unlock finds no row and reports failure.
		su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
		if _, err := su.Exec(tctx(t),
			`DELETE FROM exp_barrier_hold WHERE backend_pid = $1::int`, pid); err != nil {
			t.Fatalf("corrupt holds: %v", err)
		}
		close(proceed)
		var r res
		select {
		case r = <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("Do never returned")
		}
		if r.err != nil {
			t.Fatalf("work failed: %v", r.err)
		}
		if r.out.Released || !r.out.Discarded {
			t.Fatalf("outcome=%+v, want discard despite work success", r.out)
		}
		// Discard closed the backend, so PostgreSQL dropped the
		// still-held session lock with it.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if tryPopFree(t, probe, emp) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("session lock survived the discard")
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	t.Run("clean admission returns the connection", func(t *testing.T) {
		emp := fresh()
		out, err := Do(tctx(t), pool, emp, memberEnumerate(emp),
			func(context.Context, pgx.Tx) error { return nil })
		if err != nil || !out.Released {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		out2, err := Do(tctx(t), pool, emp, memberEnumerate(emp),
			func(context.Context, pgx.Tx) error { return nil })
		if err != nil || !out2.Released {
			t.Fatalf("reuse out=%+v err=%v", out2, err)
		}
	})
}
