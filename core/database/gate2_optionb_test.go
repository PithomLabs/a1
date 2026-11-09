// Gate 2 Option B experiment (prompt18 Phases 3–4): READ COMMITTED +
// transaction-scoped employer gate + database-enforced writer
// participation + own-row phase marker. Disposable database only;
// never migrations/. Findings feed §10 Q3–Q10.
package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// gate2ExpSQL is the isolated Option B fixture: input tables (open and
// restricted), pack pins, snapshot + validation decision tables, the
// canonical gate, one writer-participation trigger function, DEFINER
// writers, and the capture/validate boundary.
const gate2ExpSQL = `
CREATE TABLE IF NOT EXISTS gate2_employer (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS gate2_input (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    employee_id uuid,
    kind text NOT NULL,
    payload text NOT NULL,
    version integer NOT NULL DEFAULT 1,
    supersedes_id uuid REFERENCES gate2_input (id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS gate2_restricted (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    employee_id uuid,
    kind text NOT NULL,
    payload text NOT NULL,
    version integer NOT NULL DEFAULT 1,
    supersedes_id uuid REFERENCES gate2_restricted (id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS gate2_pack (
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    pack text NOT NULL,
    version text NOT NULL,
    status text NOT NULL,
    PRIMARY KEY (employer_id, pack)
);
CREATE TABLE IF NOT EXISTS gate2_run (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'validated')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS gate2_snapshot (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    run_id uuid NOT NULL REFERENCES gate2_run (id),
    version integer NOT NULL,
    status text NOT NULL DEFAULT 'valid',
    content_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, version)
);
CREATE TABLE IF NOT EXISTS gate2_snapshot_child (
    snapshot_id uuid NOT NULL REFERENCES gate2_snapshot (id),
    input_id uuid NOT NULL,
    src text NOT NULL,
    kind text NOT NULL,
    employee_id uuid,
    payload text NOT NULL,
    version integer NOT NULL,
    supersedes_id uuid,
    payload_hash text NOT NULL,
    PRIMARY KEY (snapshot_id, input_id)
);
CREATE TABLE IF NOT EXISTS gate2_validation_attempt (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    snapshot_id uuid NOT NULL REFERENCES gate2_snapshot (id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS gate2_validation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    snapshot_id uuid NOT NULL REFERENCES gate2_snapshot (id),
    employer_id uuid NOT NULL REFERENCES gate2_employer (id),
    verdict text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE SEQUENCE IF NOT EXISTS gate2_snapshot_version_seq AS bigint START 1;

ALTER TABLE public.gate2_employer OWNER TO migration_admin;
ALTER TABLE public.gate2_input OWNER TO migration_admin;
ALTER TABLE public.gate2_restricted OWNER TO migration_admin;
ALTER TABLE public.gate2_pack OWNER TO migration_admin;
ALTER TABLE public.gate2_run OWNER TO migration_admin;
ALTER TABLE public.gate2_snapshot OWNER TO migration_admin;
ALTER TABLE public.gate2_snapshot_child OWNER TO migration_admin;
ALTER TABLE public.gate2_validation_attempt OWNER TO migration_admin;
ALTER TABLE public.gate2_validation OWNER TO migration_admin;
ALTER SEQUENCE public.gate2_snapshot_version_seq OWNER TO migration_admin;

-- Most permissive input path is fully open: the trigger is the
-- enforcement, not the grant. Decision tables are append-only.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.gate2_employer TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.gate2_input TO app;
REVOKE ALL ON TABLE public.gate2_restricted FROM app;
GRANT SELECT ON TABLE public.gate2_restricted TO app;
REVOKE ALL ON TABLE public.gate2_pack FROM app;
GRANT SELECT ON TABLE public.gate2_pack TO app;
REVOKE ALL ON TABLE public.gate2_run FROM app;
GRANT SELECT, INSERT ON TABLE public.gate2_run TO app;
REVOKE ALL ON TABLE public.gate2_snapshot FROM app;
GRANT SELECT, INSERT ON TABLE public.gate2_snapshot TO app;
REVOKE ALL ON TABLE public.gate2_snapshot_child FROM app;
GRANT SELECT, INSERT ON TABLE public.gate2_snapshot_child TO app;
REVOKE ALL ON TABLE public.gate2_validation_attempt FROM app;
GRANT SELECT, INSERT ON TABLE public.gate2_validation_attempt TO app;
REVOKE ALL ON TABLE public.gate2_validation FROM app;
GRANT SELECT, INSERT ON TABLE public.gate2_validation TO app;

-- The one canonical gate. INVOKER: triggers and DEFINER nests share
-- the same lock namespace whatever role holds the transaction.
CREATE OR REPLACE FUNCTION gate2_acquire_employer_gate(p_employer uuid)
RETURNS void LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF current_setting('transaction_isolation') IS DISTINCT FROM 'read committed' THEN
        RAISE EXCEPTION 'payroll gate requires READ COMMITTED, not %',
            current_setting('transaction_isolation');
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('gate2-payroll:' || p_employer::text, 0));
END;
$$;
ALTER FUNCTION public.gate2_acquire_employer_gate(uuid) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_acquire_employer_gate(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.gate2_acquire_employer_gate(uuid) TO app;

-- Writer participation + phase marker. Fail-fast marker check first
-- (own uncommitted decision row for this employer), then the gate.
-- UPDATE across employers locks both, sorted.
CREATE OR REPLACE FUNCTION gate2_input_gate()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_old uuid;
    v_new uuid;
    v_emp uuid;
BEGIN
    IF TG_TABLE_SCHEMA IS DISTINCT FROM 'public'
       OR TG_RELNAME NOT IN ('gate2_input', 'gate2_restricted', 'gate2_pack') THEN
        RAISE EXCEPTION 'gate2_input_gate on unexpected table %.%', TG_TABLE_SCHEMA, TG_RELNAME;
    END IF;
    IF TG_OP = 'INSERT' THEN
        v_new := NEW.employer_id;
    ELSIF TG_OP = 'DELETE' THEN
        v_old := OLD.employer_id;
    ELSE
        v_old := OLD.employer_id;
        v_new := NEW.employer_id;
    END IF;
    -- Same-txn membership: a decision row that is BOTH visible in
    -- this snapshot AND in-progress can only be our own uncommitted
    -- row (other backends' in-progress rows are invisible; committed
    -- rows are not in progress). Exact for top-level and
    -- subtransactions alike, with no xid casts or epoch games.
    -- Checked for both sides of the write: DELETE carries only OLD.
    FOR v_emp IN SELECT unnest(ARRAY[v_old, v_new]) LOOP
        IF v_emp IS NULL THEN CONTINUE; END IF;
        IF EXISTS (SELECT 1 FROM public.gate2_snapshot s
                   WHERE s.employer_id = v_emp
                     AND txid_status(s.xmin::text::bigint) = 'in progress') THEN
            RAISE EXCEPTION 'payroll input write after capture entered the protected phase';
        END IF;
        IF EXISTS (SELECT 1 FROM public.gate2_validation_attempt a
                   WHERE a.employer_id = v_emp
                     AND txid_status(a.xmin::text::bigint) = 'in progress') THEN
            RAISE EXCEPTION 'payroll input write after validation entered the protected phase';
        END IF;
    END LOOP;
    IF v_old IS NOT NULL AND v_new IS NOT NULL AND v_old IS DISTINCT FROM v_new THEN
        IF v_old < v_new THEN
            PERFORM public.gate2_acquire_employer_gate(v_old);
            PERFORM public.gate2_acquire_employer_gate(v_new);
        ELSE
            PERFORM public.gate2_acquire_employer_gate(v_new);
            PERFORM public.gate2_acquire_employer_gate(v_old);
        END IF;
    ELSIF v_new IS NOT NULL THEN
        PERFORM public.gate2_acquire_employer_gate(v_new);
    ELSE
        PERFORM public.gate2_acquire_employer_gate(v_old);
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
ALTER FUNCTION public.gate2_input_gate() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_input_gate() FROM PUBLIC;

DROP TRIGGER IF EXISTS gate2_input_participation ON public.gate2_input;
CREATE TRIGGER gate2_input_participation
    BEFORE INSERT OR UPDATE OR DELETE ON public.gate2_input
    FOR EACH ROW EXECUTE FUNCTION public.gate2_input_gate();
DROP TRIGGER IF EXISTS gate2_input_participation ON public.gate2_restricted;
CREATE TRIGGER gate2_input_participation
    BEFORE INSERT OR UPDATE OR DELETE ON public.gate2_restricted
    FOR EACH ROW EXECUTE FUNCTION public.gate2_input_gate();
DROP TRIGGER IF EXISTS gate2_input_participation ON public.gate2_pack;
CREATE TRIGGER gate2_input_participation
    BEFORE INSERT OR UPDATE OR DELETE ON public.gate2_pack
    FOR EACH ROW EXECUTE FUNCTION public.gate2_input_gate();

-- Run lifecycle: draft -> validated one way.
CREATE OR REPLACE FUNCTION gate2_run_lifecycle()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (OLD.status <> 'draft' OR NEW.status <> 'validated') THEN
        RAISE EXCEPTION 'run transition % -> % refused', OLD.status, NEW.status;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payroll runs are never deleted';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
ALTER FUNCTION public.gate2_run_lifecycle() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_run_lifecycle() FROM PUBLIC;
DROP TRIGGER IF EXISTS gate2_run_lifecycle ON public.gate2_run;
CREATE TRIGGER gate2_run_lifecycle
    BEFORE UPDATE OR DELETE ON public.gate2_run
    FOR EACH ROW EXECUTE FUNCTION public.gate2_run_lifecycle();

-- DEFINER writers for the restricted paths.
CREATE OR REPLACE FUNCTION gate2_add_input(p_employer uuid, p_employee uuid, p_kind text, p_payload text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_id uuid;
BEGIN
    INSERT INTO public.gate2_restricted (employer_id, employee_id, kind, payload)
    VALUES (p_employer, p_employee, p_kind, p_payload) RETURNING id INTO v_id;
    RETURN v_id;
END;
$$;
ALTER FUNCTION public.gate2_add_input(uuid, uuid, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_add_input(uuid, uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.gate2_add_input(uuid, uuid, text, text) TO app;

CREATE OR REPLACE FUNCTION gate2_activate_pack(p_employer uuid, p_pack text, p_version text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    INSERT INTO public.gate2_pack (employer_id, pack, version, status)
    VALUES (p_employer, p_pack, p_version, 'active')
    ON CONFLICT (employer_id, pack) DO UPDATE
    SET version = EXCLUDED.version, status = 'active';
END;
$$;
ALTER FUNCTION public.gate2_activate_pack(uuid, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_activate_pack(uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.gate2_activate_pack(uuid, text, text) TO app;
`

// gate2CaptureSQL is the capture/validate decision boundary: the
// claim verifier (proof-checks, never re-solves), capture (gate,
// hash recomputation over supplied bytes, snapshot persist), and
// split/one-txn validation (attempt marker, re-read from snapshot
// rows, verdict, run transition — all or nothing).
const gate2CaptureSQL = `
CREATE TABLE IF NOT EXISTS gate2_snapshot_pack (
    snapshot_id uuid NOT NULL REFERENCES gate2_snapshot (id),
    pack text NOT NULL,
    version text NOT NULL,
    status text NOT NULL,
    PRIMARY KEY (snapshot_id, pack)
);
ALTER TABLE public.gate2_snapshot_pack OWNER TO migration_admin;
REVOKE ALL ON TABLE public.gate2_snapshot_pack FROM app;
GRANT SELECT, INSERT ON TABLE public.gate2_snapshot_pack TO app;

CREATE OR REPLACE FUNCTION gate2_verify_claims(p_employer uuid, p_claims jsonb)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    r RECORD;
    v_count integer;
BEGIN
    FOR r IN SELECT * FROM jsonb_to_recordset(p_claims -> 'inputs')
        AS (id uuid, src text, kind text, employee uuid, payload text, version integer, supersedes uuid) LOOP
        IF r.src = 'open' THEN
            PERFORM 1 FROM public.gate2_input t
            WHERE t.id = r.id AND t.employer_id = p_employer AND t.kind IS NOT DISTINCT FROM r.kind
              AND t.employee_id IS NOT DISTINCT FROM r.employee AND t.payload = r.payload
              AND t.version = r.version AND t.supersedes_id IS NOT DISTINCT FROM r.supersedes;
        ELSIF r.src = 'restricted' THEN
            PERFORM 1 FROM public.gate2_restricted t
            WHERE t.id = r.id AND t.employer_id = p_employer AND t.kind IS NOT DISTINCT FROM r.kind
              AND t.employee_id IS NOT DISTINCT FROM r.employee AND t.payload = r.payload
              AND t.version = r.version AND t.supersedes_id IS NOT DISTINCT FROM r.supersedes;
        ELSE
            RAISE EXCEPTION 'claim with unknown source %', r.src;
        END IF;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'claim mismatch or fabrication for input %', r.id;
        END IF;
        IF r.supersedes IS NOT NULL THEN
            IF r.src = 'open' THEN
                PERFORM 1 FROM public.gate2_input t
                WHERE t.id = r.supersedes AND t.employer_id = p_employer;
            ELSE
                PERFORM 1 FROM public.gate2_restricted t
                WHERE t.id = r.supersedes AND t.employer_id = p_employer;
            END IF;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'claim lineage broken for input %', r.id;
            END IF;
        END IF;
    END LOOP;
    SELECT count(*) INTO v_count FROM public.gate2_input t
    WHERE t.employer_id = p_employer AND NOT EXISTS (
        SELECT 1 FROM jsonb_to_recordset(p_claims -> 'inputs') AS (id uuid) WHERE id = t.id);
    IF v_count > 0 THEN RAISE EXCEPTION 'claims omit % open inputs', v_count; END IF;
    SELECT count(*) INTO v_count FROM public.gate2_restricted t
    WHERE t.employer_id = p_employer AND NOT EXISTS (
        SELECT 1 FROM jsonb_to_recordset(p_claims -> 'inputs') AS (id uuid) WHERE id = t.id);
    IF v_count > 0 THEN RAISE EXCEPTION 'claims omit % restricted inputs', v_count; END IF;
    FOR r IN SELECT * FROM jsonb_to_recordset(p_claims -> 'packs')
        AS (pack text, version text, status text) LOOP
        PERFORM 1 FROM public.gate2_pack p
        WHERE p.employer_id = p_employer AND p.pack = r.pack
          AND p.version = r.version AND p.status = r.status;
        IF NOT FOUND THEN RAISE EXCEPTION 'claim mismatch for pack %', r.pack; END IF;
    END LOOP;
    SELECT count(*) INTO v_count FROM public.gate2_pack p
    WHERE p.employer_id = p_employer AND NOT EXISTS (
        SELECT 1 FROM jsonb_to_recordset(p_claims -> 'packs') AS (pack text) WHERE pack = p.pack);
    IF v_count > 0 THEN RAISE EXCEPTION 'claims omit % packs', v_count; END IF;
END;
$$;
ALTER FUNCTION public.gate2_verify_claims(uuid, jsonb) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_verify_claims(uuid, jsonb) FROM PUBLIC;

CREATE OR REPLACE FUNCTION gate2_capture(p_employer uuid, p_run uuid, p_claims jsonb, p_canonical text, p_hash text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_id uuid;
    v_version integer;
    r RECORD;
BEGIN
    IF current_setting('transaction_isolation') IS DISTINCT FROM 'read committed' THEN
        RAISE EXCEPTION 'capture requires READ COMMITTED, not %',
            current_setting('transaction_isolation');
    END IF;
    PERFORM public.gate2_acquire_employer_gate(p_employer);
    PERFORM 1 FROM public.gate2_run
    WHERE id = p_run AND employer_id = p_employer AND status = 'draft' FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'run is not a draft for this employer'; END IF;
    IF encode(public.digest(convert_to(p_canonical, 'UTF8'), 'sha256'), 'hex') IS DISTINCT FROM p_hash THEN
        RAISE EXCEPTION 'content hash mismatch';
    END IF;
    PERFORM public.gate2_verify_claims(p_employer, p_claims);
    v_version := nextval('public.gate2_snapshot_version_seq');
    INSERT INTO public.gate2_snapshot (employer_id, run_id, version, content_hash)
    VALUES (p_employer, p_run, v_version, p_hash) RETURNING id INTO v_id;
    FOR r IN SELECT * FROM jsonb_to_recordset(p_claims -> 'inputs')
        AS (id uuid, src text, kind text, employee uuid, payload text, version integer, supersedes uuid) LOOP
        INSERT INTO public.gate2_snapshot_child (snapshot_id, input_id, src, kind, employee_id, payload, version, supersedes_id, payload_hash)
        VALUES (v_id, r.id, r.src, r.kind, r.employee, r.payload, r.version, r.supersedes,
                encode(public.digest(convert_to(r.payload, 'UTF8'), 'sha256'), 'hex'));
    END LOOP;
    FOR r IN SELECT * FROM jsonb_to_recordset(p_claims -> 'packs')
        AS (pack text, version text, status text) LOOP
        INSERT INTO public.gate2_snapshot_pack (snapshot_id, pack, version, status)
        VALUES (v_id, r.pack, r.version, r.status);
    END LOOP;
    RETURN v_id;
END;
$$;
ALTER FUNCTION public.gate2_capture(uuid, uuid, jsonb, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_capture(uuid, uuid, jsonb, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.gate2_capture(uuid, uuid, jsonb, text, text) TO app;

CREATE OR REPLACE FUNCTION gate2_validate(p_employer uuid, p_run uuid, p_snapshot uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_claims jsonb;
BEGIN
    IF current_setting('transaction_isolation') IS DISTINCT FROM 'read committed' THEN
        RAISE EXCEPTION 'validation requires READ COMMITTED, not %',
            current_setting('transaction_isolation');
    END IF;
    PERFORM public.gate2_acquire_employer_gate(p_employer);
    INSERT INTO public.gate2_validation_attempt (employer_id, snapshot_id)
    VALUES (p_employer, p_snapshot);
    -- Claims rebuilt from the frozen SNAPSHOT rows, never
    -- caller-supplied and never re-read from the live tables (that
    -- would compare current state to itself and pass stale).
    SELECT jsonb_build_object(
        'inputs', (SELECT coalesce(jsonb_agg(jsonb_build_object(
            'id', c.input_id,
            'src', c.src,
            'kind', c.kind,
            'employee', c.employee_id,
            'payload', c.payload,
            'version', c.version,
            'supersedes', c.supersedes_id)), '[]')
         FROM public.gate2_snapshot_child c
         WHERE c.snapshot_id = p_snapshot),
        'packs', (SELECT coalesce(jsonb_agg(jsonb_build_object(
            'pack', p.pack, 'version', p.version, 'status', p.status)), '[]')
         FROM public.gate2_snapshot_pack p WHERE p.snapshot_id = p_snapshot))
    INTO v_claims;
    PERFORM public.gate2_verify_claims(p_employer, v_claims);
    PERFORM 1 FROM public.gate2_snapshot
    WHERE id = p_snapshot AND employer_id = p_employer AND status = 'valid';
    IF NOT FOUND THEN RAISE EXCEPTION 'snapshot is not valid for this employer'; END IF;
    UPDATE public.gate2_run SET status = 'validated'
    WHERE id = p_run AND employer_id = p_employer AND status = 'draft';
    IF NOT FOUND THEN RAISE EXCEPTION 'run is not a draft for this employer'; END IF;
    INSERT INTO public.gate2_validation (snapshot_id, employer_id, verdict)
    VALUES (p_snapshot, p_employer, 'valid');
END;
$$;
ALTER FUNCTION public.gate2_validate(uuid, uuid, uuid) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION public.gate2_validate(uuid, uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.gate2_validate(uuid, uuid, uuid) TO app;
`

var (
	gate2bOnce sync.Once
	gate2bDB   dbutil.TestDB
	gate2bErr  error
)

// gate2bSetup provisions the shared Option B experiment database once per
// process: Gate 1 base objects (inert here) plus the Option B fixture.
// Tests use random employers, so no cross-test interference.
func gate2bSetup(t *testing.T) dbutil.TestDB {
	t.Helper()
	gate2bOnce.Do(func() {
		ctx, cancel := testCtx()
		defer cancel()
		dsn := dbutil.DSN(t)
		name := dbName(t, dsn) + "_gate2b"
		assertDisposableDrop(dsn, name)
		maint, err := pgx.Connect(ctx, dsn)
		if err != nil {
			gate2bErr = err
			return
		}
		if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			gate2bErr = err
			maint.Close(ctx)
			return
		}
		maint.Close(ctx)
		db, err := dbutil.SetupDB(ctx, dsn, name,
			func(ctx context.Context, migDSN string) error {
				_, err := MigrateUp(ctx, migDSN, coremigrations.FS)
				return err
			})
		if err != nil {
			gate2bErr = err
			return
		}
		mig, err := pgx.Connect(ctx, db.MigDSN)
		if err != nil {
			gate2bErr = err
			return
		}
		defer mig.Close(ctx)
		if _, err := mig.Exec(ctx, admissionExpSQL); err != nil {
			gate2bErr = err
			return
		}
		if _, err := mig.Exec(ctx, gate2ExpSQL); err != nil {
			gate2bErr = err
			return
		}
		if _, err := mig.Exec(ctx, gate2CaptureSQL); err != nil {
			gate2bErr = err
			return
		}
		gate2bDB = db
	})
	if gate2bErr != nil {
		t.Fatalf("optionb setup: %v", gate2bErr)
	}
	return gate2bDB
}

// gate2ClaimInput is one claimed input row. Empty Employee/Supersedes
// encode JSON null.
type gate2ClaimInput struct {
	ID         string
	Src        string // "open" | "restricted"
	Kind       string
	Employee   string
	Payload    string
	Version    int
	Supersedes string
}

type gate2ClaimPack struct {
	Pack    string
	Version string
	Status  string
}

// gate2Canonical renders the canonical byte form: fixed line order,
// inputs sorted by id, packs sorted by pack. No maps, no ambiguity.
func gate2Canonical(employer string, inputs []gate2ClaimInput, packs []gate2ClaimPack) string {
	ins := append([]gate2ClaimInput(nil), inputs...)
	sort.Slice(ins, func(i, j int) bool { return ins[i].ID < ins[j].ID })
	pks := append([]gate2ClaimPack(nil), packs...)
	sort.Slice(pks, func(i, j int) bool { return pks[i].Pack < pks[j].Pack })
	var b strings.Builder
	b.WriteString("employer:" + employer + "\n")
	for _, in := range ins {
		fmt.Fprintf(&b, "input:%s|%s|%s|%s|%s|%d|%s\n",
			in.ID, in.Src, in.Kind, in.Employee, in.Payload, in.Version, in.Supersedes)
	}
	for _, p := range pks {
		fmt.Fprintf(&b, "pack:%s|%s|%s\n", p.Pack, p.Version, p.Status)
	}
	return b.String()
}

func gate2Hash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// gate2ClaimsJSON renders the claims document the verifier checks
// field-by-field against the tables.
func gate2ClaimsJSON(inputs []gate2ClaimInput, packs []gate2ClaimPack) string {
	var b strings.Builder
	b.WriteString(`{"inputs":[`)
	for i, in := range inputs {
		if i > 0 {
			b.WriteString(",")
		}
		emp, sup := "null", "null"
		if in.Employee != "" {
			emp = `"` + in.Employee + `"`
		}
		if in.Supersedes != "" {
			sup = `"` + in.Supersedes + `"`
		}
		fmt.Fprintf(&b, `{"id":%q,"src":%q,"kind":%q,"employee":%s,"payload":%q,"version":%d,"supersedes":%s}`,
			in.ID, in.Src, in.Kind, emp, in.Payload, in.Version, sup)
	}
	b.WriteString(`],"packs":[`)
	for i, p := range packs {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"pack":%q,"version":%q,"status":%q}`, p.Pack, p.Version, p.Status)
	}
	b.WriteString(`]}`)
	return b.String()
}

func gate2Employer(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var id string
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := c.QueryRow(tctx(t),
		`INSERT INTO gate2_employer (code) VALUES ('G2'||$1) RETURNING id`, tag).Scan(&id); err != nil {
		t.Fatalf("employer: %v", err)
	}
	return id
}

func gate2Run(t *testing.T, c *pgx.Conn, emp string) string {
	t.Helper()
	var id string
	if err := c.QueryRow(tctx(t),
		`INSERT INTO gate2_run (employer_id) VALUES ($1::uuid) RETURNING id`, emp).Scan(&id); err != nil {
		t.Fatalf("run: %v", err)
	}
	return id
}

// gate2ReadClaimsE reads the authoritative input state for an employer
// and builds claims + canonical + hash exactly as the Go resolver
// would. Reads must happen post-gate; the verifier re-checks anyway.
// Error-returning core shared by the test-goroutine and background
// variants (t.Fatalf is illegal off the test goroutine).
func gate2ReadClaimsE(ctx context.Context, c *pgx.Conn, emp string) (claims, canonical, hash string, err error) {
	inputs, packs, err := gate2ReadStructsE(ctx, c, emp)
	if err != nil {
		return "", "", "", err
	}
	canonical = gate2Canonical(emp, inputs, packs)
	return gate2ClaimsJSON(inputs, packs), canonical, gate2Hash(canonical), nil
}

// gate2ReadStructsE returns the claim structs for tampering tests.
func gate2ReadStructsE(ctx context.Context, c *pgx.Conn, emp string) ([]gate2ClaimInput, []gate2ClaimPack, error) {
	var err error
	var inputs []gate2ClaimInput
	for _, src := range []struct {
		table string
		src   string
	}{{"gate2_input", "open"}, {"gate2_restricted", "restricted"}} {
		var rows pgx.Rows
		rows, err = c.Query(ctx,
			`SELECT id::text, kind, employee_id::text, payload, version, supersedes_id::text
			 FROM `+src.table+` WHERE employer_id = $1::uuid`, emp)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var in gate2ClaimInput
			var employee, supersedes *string
			if err := rows.Scan(&in.ID, &in.Kind, &employee, &in.Payload, &in.Version, &supersedes); err != nil {
				rows.Close()
				return nil, nil, err
			}
			in.Src = src.src
			if employee != nil {
				in.Employee = *employee
			}
			if supersedes != nil {
				in.Supersedes = *supersedes
			}
			inputs = append(inputs, in)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	var packs []gate2ClaimPack
	var prows pgx.Rows
	prows, err = c.Query(ctx,
		`SELECT pack, version, status FROM gate2_pack WHERE employer_id = $1::uuid`, emp)
	if err != nil {
		return nil, nil, err
	}
	for prows.Next() {
		var p gate2ClaimPack
		if err := prows.Scan(&p.Pack, &p.Version, &p.Status); err != nil {
			prows.Close()
			return nil, nil, err
		}
		packs = append(packs, p)
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, nil, err
	}
	return inputs, packs, nil
}

func gate2ReadClaims(t *testing.T, c *pgx.Conn, emp string) (claims, canonical, hash string) {
	t.Helper()
	claims, canonical, hash, err := gate2ReadClaimsE(tctx(t), c, emp)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	return claims, canonical, hash
}

func gate2Waiters(t *testing.T, su *pgx.Conn) int {
	t.Helper()
	var n int
	if err := su.QueryRow(tctx(t),
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&n); err != nil {
		t.Fatalf("waiters: %v", err)
	}
	return n
}

// Every writer class blocks on a held gate (visible waiter) and
// proceeds after release: direct I/U/D, DEFINER writers, upsert,
// multi-row, COPY, and MERGE.
func TestGate2B_WriterClassesParticipate(t *testing.T) {
	db := gate2bSetup(t)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	cases := []struct {
		name  string
		write func(ctx context.Context, w *pgx.Conn, emp string) error
		check func(t *testing.T, c *pgx.Conn, emp string)
	}{
		{"direct insert", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'w')`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'w'`, emp).Scan(&n); err != nil || n != 1 {
				t.Fatalf("inserted=%d err=%v", n, err)
			}
		}},
		{"direct update", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `UPDATE gate2_input SET payload = 'w-upd' WHERE employer_id = $1::uuid AND payload = 'seed'`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'w-upd'`, emp).Scan(&n); err != nil || n != 1 {
				t.Fatalf("updated=%d err=%v", n, err)
			}
		}},
		{"direct delete", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `DELETE FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'seed'`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'seed'`, emp).Scan(&n); err != nil || n != 0 {
				t.Fatalf("remaining=%d err=%v", n, err)
			}
		}},
		{"definer add", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `SELECT gate2_add_input($1::uuid, NULL::uuid, 'shg', 'w')`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_restricted WHERE employer_id = $1::uuid AND payload = 'w'`, emp).Scan(&n); err != nil || n != 1 {
				t.Fatalf("added=%d err=%v", n, err)
			}
		}},
		{"definer pack", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `SELECT gate2_activate_pack($1::uuid, 'P', 'v2')`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var v string
			if err := c.QueryRow(tctx(t), `SELECT version FROM gate2_pack WHERE employer_id = $1::uuid AND pack = 'P'`, emp).Scan(&v); err != nil || v != "v2" {
				t.Fatalf("pack=%s err=%v", v, err)
			}
		}},
		{"upsert", func(ctx context.Context, w *pgx.Conn, emp string) error {
			var id string
			if err := w.QueryRow(ctx,
				`SELECT id::text FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'seed' LIMIT 1`, emp).Scan(&id); err != nil {
				return err
			}
			_, err := w.Exec(ctx,
				`INSERT INTO gate2_input (id, employer_id, kind, payload) VALUES ($1::uuid, $2::uuid, 'comp', 'w-ups')
				 ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload`, id, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'w-ups'`, emp).Scan(&n); err != nil || n != 1 {
				t.Fatalf("upserted=%d err=%v", n, err)
			}
		}},
		{"multi-row", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx,
				`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'm1'), ($1::uuid, 'comp', 'm2'), ($1::uuid, 'comp', 'm3')`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload LIKE 'm%'`, emp).Scan(&n); err != nil || n != 3 {
				t.Fatalf("multi=%d err=%v", n, err)
			}
		}},
		{"copy", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.CopyFrom(ctx, pgx.Identifier{"gate2_input"},
				[]string{"employer_id", "kind", "payload"},
				pgx.CopyFromSlice(1, func(i int) ([]any, error) {
					return []any{emp, "comp", "copied"}, nil
				}))
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'copied'`, emp).Scan(&n); err != nil || n != 1 {
				t.Fatalf("copied=%d err=%v", n, err)
			}
		}},
		{"merge", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx,
				`MERGE INTO gate2_input t USING (VALUES ($1::uuid, 'comp', 'merged')) AS s(emp, kind, payload)
				 ON t.employer_id = s.emp AND t.payload = 'seed'
				 WHEN MATCHED THEN UPDATE SET payload = s.payload
				 WHEN NOT MATCHED THEN INSERT (employer_id, kind, payload) VALUES (s.emp, s.kind, s.payload)`, emp)
			return err
		}, func(t *testing.T, c *pgx.Conn, emp string) {
			var n int
			if err := c.QueryRow(tctx(t), `SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'merged'`, emp).Scan(&n); err != nil || n != 1 {
				t.Fatalf("merged=%d err=%v", n, err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := openConn(t, db.AppDSN)
			emp := gate2Employer(t, c)
			execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'seed')`, emp)
			execConn(t, c, `SELECT gate2_activate_pack($1::uuid, 'P', 'v1')`, emp)
			run := gate2Run(t, c, emp)
			// Capture-first: the writer waits; capture commits a
			// snapshot that excludes the not-yet-committed write;
			// the writer commits after.
			execConn(t, c, `BEGIN`)
			execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
			done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
				if _, err := w.Exec(ctx, `BEGIN`); err != nil {
					return err
				}
				if err := tc.write(ctx, w, emp); err != nil {
					return err
				}
				_, err := w.Exec(ctx, `COMMIT`)
				return err
			})
			gate2AwaitWaiters(t, su, 1, 5*time.Second)
			snap := gate2Capture(t, c, emp, run)
			execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
			execConn(t, c, `COMMIT`)
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("writer: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("writer did not complete after release")
			}
			tc.check(t, c, emp)
			// The committed snapshot froze the pre-writer state:
			// exactly the seed content, no writer effect.
			frozen := gate2SnapshotFingerprint(t, c, snap)
			if frozen != "seed|P:v1" {
				t.Fatalf("capture-first snapshot=%q, want seed-only", frozen)
			}
			// Writer-first: the same writer commits unopposed on a
			// fresh employer; capture then includes its effect.
			emp2 := gate2Employer(t, c)
			execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'seed')`, emp2)
			execConn(t, c, `SELECT gate2_activate_pack($1::uuid, 'P', 'v1')`, emp2)
			run2 := gate2Run(t, c, emp2)
			execConn(t, c, `BEGIN`)
			w2, err := pgx.Connect(tctx(t), db.AppDSN)
			if err != nil {
				t.Fatalf("writer: %v", err)
			}
			defer w2.Close(context.Background())
			if _, err := w2.Exec(tctx(t), `BEGIN`); err != nil {
				t.Fatalf("begin: %v", err)
			}
			if err := tc.write(tctx(t), w2, emp2); err != nil {
				t.Fatalf("writer: %v", err)
			}
			if _, err := w2.Exec(tctx(t), `COMMIT`); err != nil {
				t.Fatalf("commit: %v", err)
			}
			execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp2)
			snap2 := gate2Capture(t, c, emp2, run2)
			execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp2, run2, snap2)
			execConn(t, c, `COMMIT`)
			tc.check(t, c, emp2)
			included := gate2SnapshotFingerprint(t, c, snap2)
			if included == "seed|P:v1" {
				t.Fatal("writer-first snapshot missed the committed write")
			}
		})
	}
}

// gate2SnapshotFingerprint renders the FROZEN child payloads and pack
// pins of one snapshot for ordering assertions. Frozen copies only:
// joining back to the live tables would show post-capture drift.
func gate2SnapshotFingerprint(t *testing.T, c *pgx.Conn, snap string) string {
	t.Helper()
	var fp string
	if err := c.QueryRow(tctx(t),
		`SELECT coalesce((SELECT string_agg(payload, ',' ORDER BY payload)
		  FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid), '') || '|' ||
		 coalesce((SELECT string_agg(pack || ':' || version, ',' ORDER BY pack)
		  FROM gate2_snapshot_pack WHERE snapshot_id = $1::uuid), '')`, snap).Scan(&fp); err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}

// gate2Seed writes one open input, one restricted input (via the
// DEFINER writer), and one pack pin (via DEFINER) for an employer.
func gate2Seed(t *testing.T, c *pgx.Conn, emp string) {
	t.Helper()
	execConn(t, c,
		`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'base-100')`, emp)
	execConn(t, c,
		`SELECT gate2_add_input($1::uuid, NULL::uuid, 'shg', 'fund-cdac')`, emp)
	execConn(t, c,
		`SELECT gate2_activate_pack($1::uuid, 'SG-MOM-OT-001', 'v1')`, emp)
}

func gate2Capture(t *testing.T, c *pgx.Conn, emp, run string) string {
	t.Helper()
	claims, canonical, hash := gate2ReadClaims(t, c, emp)
	var snap string
	if err := c.QueryRow(tctx(t),
		`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`,
		emp, run, claims, canonical, hash).Scan(&snap); err != nil {
		t.Fatalf("capture: %v", err)
	}
	return snap
}

// Coverage is fail-closed: every input table carries the BEFORE ROW
// I/U/D gate trigger; decision tables are append-only for app;
// app holds no TRUNCATE, ownership, or role membership anywhere.
func TestGate2B_Coverage(t *testing.T) {
	db := gate2bSetup(t)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	for _, table := range []string{"gate2_input", "gate2_restricted", "gate2_pack"} {
		var n int
		if err := su.QueryRow(tctx(t),
			`SELECT count(*) FROM pg_trigger WHERE tgrelid = $1::regclass AND NOT tgisinternal`, table).Scan(&n); err != nil {
			t.Fatalf("triggers %s: %v", table, err)
		}
		if n != 1 {
			t.Fatalf("%s triggers=%d, want 1 participation trigger", table, n)
		}
		var events string
		if err := su.QueryRow(tctx(t),
			`SELECT string_agg(et, ',' ORDER BY et) FROM (
				SELECT unnest(ARRAY['I','U','D']) et) e
			 WHERE (SELECT tgtype & CASE et WHEN 'I' THEN 4 WHEN 'U' THEN 16 ELSE 8 END <> 0
			        FROM pg_trigger WHERE tgrelid = $1::regclass AND NOT tgisinternal)`, table).Scan(&events); err != nil {
			t.Fatalf("events %s: %v", table, err)
		}
		if events != "D,I,U" {
			t.Fatalf("%s trigger events=%s, want D,I,U", table, events)
		}
	}
	for _, table := range []string{"gate2_snapshot", "gate2_snapshot_child", "gate2_snapshot_pack",
		"gate2_validation_attempt", "gate2_validation", "gate2_run"} {
		var privs string
		if err := su.QueryRow(tctx(t),
			`SELECT string_agg(privilege_type, ',' ORDER BY privilege_type)
			 FROM information_schema.role_table_grants
			 WHERE grantee = 'app' AND table_name = $1`, table).Scan(&privs); err != nil {
			t.Fatalf("grants %s: %v", table, err)
		}
		if privs != "INSERT,SELECT" {
			t.Fatalf("%s app privs=%s, want INSERT,SELECT", table, privs)
		}
	}
	var trunc int
	if err := su.QueryRow(tctx(t),
		`SELECT count(*) FROM information_schema.role_table_grants
		 WHERE grantee = 'app' AND privilege_type = 'TRUNCATE'
		   AND table_name LIKE 'gate2\_%'`).Scan(&trunc); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if trunc != 0 {
		t.Fatalf("app holds TRUNCATE on %d fixture tables", trunc)
	}
	var casc int
	if err := su.QueryRow(tctx(t),
		`SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND confdeltype = 'c'
		  AND conrelid::regclass::text LIKE 'gate2\_%'`).Scan(&casc); err != nil {
		t.Fatalf("cascades: %v", err)
	}
	if casc != 0 {
		t.Fatalf("fixture has %d cascading FKs", casc)
	}
}

// One gated transaction: gate, read, capture, validate, commit. All
// persisted artifacts asserted; a second capture on the settled run
// is refused.
func TestGate2B_OneTransactionHappy(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	snap := gate2Capture(t, c, emp, run)
	execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
	execConn(t, c, `COMMIT`)
	var status, verdict, children, packs string
	if err := c.QueryRow(tctx(t),
		`SELECT (SELECT status FROM gate2_run WHERE id = $1::uuid),
		        (SELECT verdict FROM gate2_validation WHERE snapshot_id = $2::uuid),
		        (SELECT count(*)::text FROM gate2_snapshot_child WHERE snapshot_id = $2::uuid),
		        (SELECT count(*)::text FROM gate2_snapshot_pack WHERE snapshot_id = $2::uuid)`,
		run, snap).Scan(&status, &verdict, &children, &packs); err != nil {
		t.Fatalf("persisted: %v", err)
	}
	if status != "validated" || verdict != "valid" || children != "2" || packs != "1" {
		t.Fatalf("run=%s verdict=%s children=%s packs=%s", status, verdict, children, packs)
	}
	execConn(t, c, `BEGIN`)
	claims, canonical, hash := gate2ReadClaims(t, c, emp)
	_, err := c.Exec(tctx(t),
		`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`, emp, run, claims, canonical, hash)
	expectErr(t, err, "not a draft")
	execConn(t, c, `ROLLBACK`)
}

// Split path: capture commits, validation re-acquires the same gate
// in a new transaction and settles the run.
func TestGate2B_SplitHappy(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	snap := gate2Capture(t, c, emp, run)
	execConn(t, c, `COMMIT`)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
	execConn(t, c, `COMMIT`)
	var status string
	if err := c.QueryRow(tctx(t),
		`SELECT status FROM gate2_run WHERE id = $1::uuid`, run).Scan(&status); err != nil || status != "validated" {
		t.Fatalf("run=%s err=%v", status, err)
	}
}

// Gate, capture, and validate all reject non-RC isolation.
func TestGate2B_RejectsNonRC(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	_, err := c.Exec(tctx(t), `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	expectErr(t, err, "requires READ COMMITTED")
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	claims, canonical, hash := gate2ReadClaims(t, c, emp)
	_, err = c.Exec(tctx(t),
		`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`, emp, run, claims, canonical, hash)
	expectErr(t, err, "requires READ COMMITTED")
	execConn(t, c, `ROLLBACK`)
	execConn(t, c, `BEGIN ISOLATION LEVEL SERIALIZABLE`)
	_, err = c.Exec(tctx(t), `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, emp)
	expectErr(t, err, "requires READ COMMITTED")
	execConn(t, c, `ROLLBACK`)
}

// One gate, one key: a waiter arriving through any path (direct
// write, DEFINER nest, second capture) waits on the exact lock the
// holder owns — opaque (classid, objid) pairs compared equal. Two
// employers held at once carry different keys.
func TestGate2B_OneGateOneKey(t *testing.T) {
	db := gate2bSetup(t)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	paths := []struct {
		name  string
		write func(ctx context.Context, w *pgx.Conn, emp string) error
	}{
		{"direct", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'x')`, emp)
			return err
		}},
		{"definer", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `SELECT gate2_add_input($1::uuid, NULL::uuid, 'shg', 'x')`, emp)
			return err
		}},
		{"capture", func(ctx context.Context, w *pgx.Conn, emp string) error {
			_, err := w.Exec(ctx, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
			return err
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			c := openConn(t, db.AppDSN)
			emp := gate2Employer(t, c)
			execConn(t, c, `BEGIN`)
			execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
			hc, ho := gate2LockIdentity(t, su, backendPID(t, c), true)
			done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
				if _, err := w.Exec(ctx, `BEGIN`); err != nil {
					return err
				}
				if err := p.write(ctx, w, emp); err != nil {
					return err
				}
				_, err := w.Exec(ctx, `ROLLBACK`)
				return err
			})
			gate2AwaitWaiters(t, su, 1, 5*time.Second)
			var wc, wo string
			if err := su.QueryRow(tctx(t),
				`SELECT classid::text, objid::text FROM pg_locks
				 WHERE locktype = 'advisory' AND NOT granted`).Scan(&wc, &wo); err != nil {
				t.Fatalf("waiter identity: %v", err)
			}
			if wc != hc || wo != ho {
				t.Fatalf("waiter (%s,%s) != holder (%s,%s)", wc, wo, hc, ho)
			}
			execConn(t, c, `ROLLBACK`)
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("writer: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("writer did not complete after release")
			}
		})
	}
	t.Run("employers differ", func(t *testing.T) {
		a := openConn(t, db.AppDSN)
		b := openConn(t, db.AppDSN)
		empA := gate2Employer(t, a)
		empB := gate2Employer(t, a)
		execConn(t, a, `BEGIN`)
		execConn(t, a, `SELECT gate2_acquire_employer_gate($1::uuid)`, empA)
		execConn(t, b, `BEGIN`)
		execConn(t, b, `SELECT gate2_acquire_employer_gate($1::uuid)`, empB)
		ac, ao := gate2LockIdentity(t, su, backendPID(t, a), true)
		bc, bo := gate2LockIdentity(t, su, backendPID(t, b), true)
		if ac == bc && ao == bo {
			t.Fatal("distinct employers share a gate key")
		}
		if gate2Waiters(t, su) != 0 {
			t.Fatal("unrelated employers block each other")
		}
		execConn(t, a, `ROLLBACK`)
		execConn(t, b, `ROLLBACK`)
	})
}

// The §6.4 attack, exactly: after capture enters the protected phase
// in a transaction, every same-transaction input mutation is rejected
// — while pre-marker writes stay allowed and self-consistent.
func TestGate2B_SameTransactionAttack(t *testing.T) {
	db := gate2bSetup(t)

	t.Run("prompt sequence rejected", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		_ = gate2Capture(t, c, emp, run)
		_, err := c.Exec(tctx(t),
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'sneak')`, emp)
		expectErr(t, err, "protected phase")
		execConn(t, c, `ROLLBACK`)
		var n int
		if err := c.QueryRow(tctx(t),
			`SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'sneak'`, emp).Scan(&n); err != nil || n != 0 {
			t.Fatalf("sneaked=%d err=%v", n, err)
		}
	})

	t.Run("pre-marker write allowed and included", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'early')`, emp)
		snap := gate2Capture(t, c, emp, run)
		execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
		execConn(t, c, `COMMIT`)
		var n int
		if err := c.QueryRow(tctx(t),
			`SELECT count(*) FROM gate2_snapshot_child c JOIN gate2_input i ON i.id = c.input_id
			 WHERE c.snapshot_id = $1::uuid AND i.payload = 'early'`, snap).Scan(&n); err != nil || n != 1 {
			t.Fatalf("included=%d err=%v", n, err)
		}
	})

	t.Run("update delete definer rejected", func(t *testing.T) {
		for _, op := range []string{
			`UPDATE gate2_input SET payload = 'sneak' WHERE employer_id = $1::uuid`,
			`DELETE FROM gate2_input WHERE employer_id = $1::uuid`,
			`SELECT gate2_add_input($1::uuid, NULL::uuid, 'shg', 'sneak')`,
			`SELECT gate2_activate_pack($1::uuid, 'P', 'sneak')`,
		} {
			c := openConn(t, db.AppDSN)
			emp := gate2Employer(t, c)
			gate2Seed(t, c, emp)
			run := gate2Run(t, c, emp)
			execConn(t, c, `BEGIN`)
			execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
			_ = gate2Capture(t, c, emp, run)
			_, err := c.Exec(tctx(t), op, emp)
			expectErr(t, err, "protected phase")
			execConn(t, c, `ROLLBACK`)
		}
	})

	t.Run("attempt marker branch", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		snap := gate2Capture(t, c, emp, run)
		execConn(t, c, `COMMIT`)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		// Plant the attempt marker exactly as gate2_validate does,
		// then attempt the interleaved write the prompt prescribes.
		execConn(t, c, `INSERT INTO gate2_validation_attempt (employer_id, snapshot_id) VALUES ($1::uuid, $2::uuid)`, emp, snap)
		_, err := c.Exec(tctx(t),
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'sneak')`, emp)
		expectErr(t, err, "validation entered the protected phase")
		execConn(t, c, `ROLLBACK`)
		var status string
		if err := c.QueryRow(tctx(t),
			`SELECT status FROM gate2_run WHERE id = $1::uuid`, run).Scan(&status); err != nil || status != "draft" {
			t.Fatalf("run=%s err=%v, want draft (attempt rolled back)", status, err)
		}
	})

	t.Run("cross employer allowed", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		empA := gate2Employer(t, c)
		empB := gate2Employer(t, c)
		gate2Seed(t, c, empA)
		run := gate2Run(t, c, empA)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, empA)
		_ = gate2Capture(t, c, empA, run)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'other')`, empB)
		execConn(t, c, `ROLLBACK`)
	})

	t.Run("savepoint instant correctness", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		execConn(t, c, `SAVEPOINT pre`)
		_ = gate2Capture(t, c, emp, run)
		_, err := c.Exec(tctx(t),
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 's1')`, emp)
		expectErr(t, err, "protected phase")
		execConn(t, c, `ROLLBACK TO SAVEPOINT pre`)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 's2')`, emp)
		_ = gate2Capture(t, c, emp, run)
		_, err = c.Exec(tctx(t),
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 's3')`, emp)
		expectErr(t, err, "protected phase")
		execConn(t, c, `ROLLBACK`)
	})

	t.Run("committed snapshot blocks nothing new", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		snap := gate2Capture(t, c, emp, run)
		execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
		execConn(t, c, `COMMIT`)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'after')`, emp)
	})

	t.Run("marker unremovable by app", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		snap := gate2Capture(t, c, emp, run)
		_, err := c.Exec(tctx(t), `DELETE FROM gate2_snapshot WHERE id = $1::uuid`, snap)
		expectErr(t, err, "permission denied")
		execConn(t, c, `ROLLBACK`)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		_ = gate2Capture(t, c, emp, run)
		_, err = c.Exec(tctx(t), `UPDATE gate2_snapshot SET status = 'x' WHERE employer_id = $1::uuid`, emp)
		expectErr(t, err, "permission denied")
		execConn(t, c, `ROLLBACK`)
	})
}

// Same-employer captures serialize and both settle with distinct
// versions; the gate serializes, the sequence orders.
func TestGate2B_ConcurrentCaptures(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run1 := gate2Run(t, c, emp)
	run2 := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	mkCap := func(run string) <-chan error {
		return gate2Async(db.AppDSN, func(ctx context.Context, a *pgx.Conn) error {
			if _, err := a.Exec(ctx, `BEGIN`); err != nil {
				return err
			}
			if _, err := a.Exec(ctx, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp); err != nil {
				return err
			}
			claims, canonical, hash, err := gate2ReadClaimsE(ctx, a, emp)
			if err != nil {
				return err
			}
			var snap string
			if err := a.QueryRow(ctx,
				`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`,
				emp, run, claims, canonical, hash).Scan(&snap); err != nil {
				return err
			}
			if _, err := a.Exec(ctx, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap); err != nil {
				return err
			}
			_, err = a.Exec(ctx, `COMMIT`)
			return err
		})
	}
	d1, d2 := mkCap(run1), mkCap(run2)
	gate2AwaitWaiters(t, su, 2, 5*time.Second)
	execConn(t, c, `ROLLBACK`)
	for i, d := range []<-chan error{d1, d2} {
		select {
		case err := <-d:
			if err != nil {
				t.Fatalf("capture %d: %v", i, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("capture %d did not complete", i)
		}
	}
	var v1, v2, s1, s2 string
	if err := c.QueryRow(tctx(t),
		`SELECT (SELECT version::text FROM gate2_snapshot WHERE run_id = $1::uuid),
		        (SELECT version::text FROM gate2_snapshot WHERE run_id = $2::uuid),
		        (SELECT status FROM gate2_run WHERE id = $1::uuid),
		        (SELECT status FROM gate2_run WHERE id = $2::uuid)`,
		run1, run2).Scan(&v1, &v2, &s1, &s2); err != nil {
		t.Fatalf("versions: %v", err)
	}
	if v1 == v2 || s1 != "validated" || s2 != "validated" {
		t.Fatalf("v1=%s v2=%s s1=%s s2=%s", v1, v2, s1, s2)
	}
}

// Employer-changing UPDATE locks both gates (sorted): it blocks
// while either is held, and opposite swaps serialize deadlock-free.
func TestGate2B_EmployerChange(t *testing.T) {
	db := gate2bSetup(t)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	for _, holdFirst := range []bool{true, false} {
		c := openConn(t, db.AppDSN)
		empA := gate2Employer(t, c)
		empB := gate2Employer(t, c)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'move')`, empA)
		held := empA
		if !holdFirst {
			held = empB
		}
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, held)
		done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
			if _, err := w.Exec(ctx, `BEGIN`); err != nil {
				return err
			}
			if _, err := w.Exec(ctx,
				`UPDATE gate2_input SET employer_id = $1::uuid WHERE employer_id = $2::uuid AND payload = 'move'`, empB, empA); err != nil {
				return err
			}
			_, err := w.Exec(ctx, `COMMIT`)
			return err
		})
		gate2AwaitWaiters(t, su, 1, 5*time.Second)
		execConn(t, c, `ROLLBACK`)
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("holdFirst=%v: %v", holdFirst, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("swap did not complete after release")
		}
		var n int
		if err := c.QueryRow(tctx(t),
			`SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'move'`, empB).Scan(&n); err != nil || n != 1 {
			t.Fatalf("moved=%d err=%v", n, err)
		}
	}
	// Opposite concurrent swaps: sorted acquisition serializes
	// instead of deadlocking.
	c := openConn(t, db.AppDSN)
	empA := gate2Employer(t, c)
	empB := gate2Employer(t, c)
	execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'ab'), ($2::uuid, 'comp', 'ba')`, empA, empB)
	mkSwap := func(from, to, payload string) <-chan error {
		return gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
			if _, err := w.Exec(ctx, `BEGIN`); err != nil {
				return err
			}
			if _, err := w.Exec(ctx,
				`UPDATE gate2_input SET employer_id = $1::uuid WHERE employer_id = $2::uuid AND payload = $3`, to, from, payload); err != nil {
				return err
			}
			_, err := w.Exec(ctx, `COMMIT`)
			return err
		})
	}
	d1, d2 := mkSwap(empA, empB, "ab"), mkSwap(empB, empA, "ba")
	for i, d := range []<-chan error{d1, d2} {
		select {
		case err := <-d:
			if err != nil {
				t.Fatalf("swap %d (want no deadlock): %v", i, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("swap %d timed out", i)
		}
	}
}

// Authority negatives (§9): every tampered claim is rejected with no
// partial snapshot. Canonical bytes and hash are recomputed over the
// TAMPERED claims (except where noted), so each case simultaneously
// proves a matching hash never launders fabricated content.
func TestGate2B_AuthorityNegatives(t *testing.T) {
	db := gate2bSetup(t)
	type seed struct {
		emp, other, run string
		chainParent     string
	}
	setup := func(t *testing.T) (*pgx.Conn, seed) {
		c := openConn(t, db.AppDSN)
		var s seed
		s.emp = gate2Employer(t, c)
		s.other = gate2Employer(t, c)
		gate2Seed(t, c, s.emp)
		gate2Seed(t, c, s.other)
		execConn(t, c,
			`INSERT INTO gate2_input (employer_id, employee_id, kind, payload) VALUES ($1::uuid, $1::uuid, 'comp', 'chain-v1') RETURNING id`, s.emp)
		if err := c.QueryRow(tctx(t),
			`SELECT id::text FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'chain-v1'`, s.emp).Scan(&s.chainParent); err != nil {
			t.Fatalf("parent: %v", err)
		}
		execConn(t, c,
			`INSERT INTO gate2_input (employer_id, employee_id, kind, payload, supersedes_id)
			 VALUES ($1::uuid, $1::uuid, 'comp', 'chain-v2', $2::uuid)`, s.emp, s.chainParent)
		s.run = gate2Run(t, c, s.emp)
		return c, s
	}
	cases := []struct {
		name   string
		tamper func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack)
		rehash bool
		want   string
	}{
		{"omitted input", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			return ins[1:], pks
		}, true, "claims omit"},
		{"fabricated input", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			return append(ins, gate2ClaimInput{ID: s.other, Src: "open", Kind: "comp", Payload: "ghost", Version: 1}), pks
		}, true, "claim mismatch or fabrication"},
		{"wrong employee", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			for i := range ins {
				if ins[i].Payload == "chain-v1" {
					ins[i].Employee = s.other
				}
			}
			return ins, pks
		}, true, "claim mismatch or fabrication"},
		{"wrong payload", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			ins[0].Payload += "-tampered"
			return ins, pks
		}, true, "claim mismatch or fabrication"},
		{"wrong source", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			for i := range ins {
				if ins[i].Src == "open" {
					ins[i].Src = "restricted"
					break
				}
			}
			return ins, pks
		}, true, "claim mismatch or fabrication"},
		{"stale version", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			ins[0].Version = 999
			return ins, pks
		}, true, "claim mismatch or fabrication"},
		{"broken lineage", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			for i := range ins {
				if ins[i].Payload == "chain-v2" {
					ins[i].Supersedes = s.other
				}
			}
			return ins, pks
		}, true, "claim mismatch or fabrication"},
		{"cross-employer lineage", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			// The FK guarantees existence, so the lineage branch
			// guards employer scope: chain-v2's supersedes is
			// repointed at another employer's row (updated in the
			// table too, so the claim matches exactly and only the
			// lineage check can fire).
			return ins, pks
		}, true, "claim lineage broken"},
		{"wrong pack version", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			pks[0].Version = "evil"
			return ins, pks
		}, true, "claim mismatch for pack"},
		{"omitted pack", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			return ins, nil
		}, true, "claims omit 1 packs"},
		{"fabricated pack", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			return ins, append(pks, gate2ClaimPack{Pack: "GHOST", Version: "v9", Status: "active"})
		}, true, "claim mismatch for pack"},

		{"wrong hash", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			return ins, pks
		}, false, "content hash mismatch"},
		{"empty claims", func(s seed, ins []gate2ClaimInput, pks []gate2ClaimPack) ([]gate2ClaimInput, []gate2ClaimPack) {
			return nil, nil
		}, true, "claims omit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s := setup(t)
			if tc.name == "cross-employer lineage" {
				var foreign string
				if err := c.QueryRow(tctx(t),
					`SELECT id::text FROM gate2_input WHERE employer_id = $1::uuid LIMIT 1`, s.other).Scan(&foreign); err != nil {
					t.Fatalf("foreign: %v", err)
				}
				execConn(t, c, `UPDATE gate2_input SET supersedes_id = $1::uuid WHERE payload = 'chain-v2' AND employer_id = $2::uuid`, foreign, s.emp)
			}
			ins, pks, err := gate2ReadStructsE(tctx(t), c, s.emp)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			ins, pks = tc.tamper(s, ins, pks)
			claims := gate2ClaimsJSON(ins, pks)
			canonical := gate2Canonical(s.emp, ins, pks)
			hash := gate2Hash(canonical)
			if !tc.rehash {
				hash += "00"
			}
			execConn(t, c, `BEGIN`)
			_, err = c.Exec(tctx(t),
				`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`, s.emp, s.run, claims, canonical, hash)
			expectErr(t, err, tc.want)
			execConn(t, c, `ROLLBACK`)
			var n int
			if err := c.QueryRow(tctx(t),
				`SELECT count(*) FROM gate2_snapshot WHERE run_id = $1::uuid`, s.run).Scan(&n); err != nil || n != 0 {
				t.Fatalf("snapshots=%d err=%v, want 0", n, err)
			}
		})
	}
	t.Run("wrong employer", func(t *testing.T) {
		// Claims truthfully describing another employer's state,
		// submitted under this employer: every row fails the
		// employer scope.
		c, s := setup(t)
		ins, pks, err := gate2ReadStructsE(tctx(t), c, s.other)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		claims := gate2ClaimsJSON(ins, pks)
		canonical := gate2Canonical(s.emp, ins, pks)
		hash := gate2Hash(canonical)
		execConn(t, c, `BEGIN`)
		_, err = c.Exec(tctx(t),
			`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`, s.emp, s.run, claims, canonical, hash)
		expectErr(t, err, "claim mismatch or fabrication")
		execConn(t, c, `ROLLBACK`)
	})
	t.Run("forged hash over fabricated content", func(t *testing.T) {
		// The §9 key case: bytes and hash are mutually consistent
		// but describe content that was never authoritative.
		c, s := setup(t)
		ins, pks, err := gate2ReadStructsE(tctx(t), c, s.emp)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		ins[0].Payload = "attacker-choice"
		claims := gate2ClaimsJSON(ins, pks)
		canonical := gate2Canonical(s.emp, ins, pks)
		hash := gate2Hash(canonical) // consistent with the lie
		execConn(t, c, `BEGIN`)
		_, err = c.Exec(tctx(t),
			`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`, s.emp, s.run, claims, canonical, hash)
		expectErr(t, err, "claim mismatch or fabrication")
		execConn(t, c, `ROLLBACK`)
	})
	t.Run("planted snapshot cannot validate", func(t *testing.T) {
		// App may INSERT snapshot rows (planting = self-restriction
		// for inputs), but a planted snapshot with no verified
		// children can never validate.
		c, s := setup(t)
		var planted string
		if err := c.QueryRow(tctx(t),
			`INSERT INTO gate2_snapshot (employer_id, run_id, version, content_hash)
			 VALUES ($1::uuid, $2::uuid, 4242, 'planted') RETURNING id`,
			s.emp, s.run).Scan(&planted); err != nil {
			t.Fatalf("plant: %v", err)
		}
		execConn(t, c, `BEGIN`)
		_, err := c.Exec(tctx(t), `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, s.emp, s.run, planted)
		expectErr(t, err, "claims omit")
		execConn(t, c, `ROLLBACK`)
		var status string
		if err := c.QueryRow(tctx(t),
			`SELECT status FROM gate2_run WHERE id = $1::uuid`, s.run).Scan(&status); err != nil || status != "draft" {
			t.Fatalf("run=%s err=%v, want draft", status, err)
		}
	})
	t.Run("validated content immutable", func(t *testing.T) {
		c, s := setup(t)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, s.emp)
		snap := gate2Capture(t, c, s.emp, s.run)
		execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, s.emp, s.run, snap)
		execConn(t, c, `COMMIT`)
		for _, op := range []string{
			`UPDATE gate2_snapshot SET content_hash = 'x' WHERE id = $1::uuid`,
			`DELETE FROM gate2_snapshot WHERE id = $1::uuid`,
			`UPDATE gate2_snapshot_child SET kind = 'x' WHERE snapshot_id = $1::uuid`,
			`DELETE FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid`,
			`UPDATE gate2_snapshot_pack SET version = 'x' WHERE snapshot_id = $1::uuid`,
			`DELETE FROM gate2_snapshot_pack WHERE snapshot_id = $1::uuid`,
			`UPDATE gate2_validation SET verdict = 'x' WHERE snapshot_id = $1::uuid`,
			`DELETE FROM gate2_validation WHERE snapshot_id = $1::uuid`,
		} {
			_, err := c.Exec(tctx(t), op, snap)
			expectErr(t, err, "permission denied")
		}
		var hash string
		if err := c.QueryRow(tctx(t),
			`SELECT content_hash FROM gate2_snapshot WHERE id = $1::uuid`, snap).Scan(&hash); err != nil {
			t.Fatalf("hash: %v", err)
		}
		if hash == "x" || hash == "" {
			t.Fatalf("content changed to %q", hash)
		}
	})
}

// Direct SQL as app (§7.2): every bypass attempt is denied, and the
// triggers stay enforced after each denial.
func TestGate2B_DirectSQLBattery(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	for _, op := range []struct{ name, sql, want string }{
		{"truncate", `TRUNCATE gate2_input`, "permission denied"},
		{"disable trigger", `ALTER TABLE gate2_input DISABLE TRIGGER gate2_input_participation`, "must be owner"},
		{"drop trigger", `DROP TRIGGER gate2_input_participation ON gate2_input`, "must be owner"},
		{"change owner", `ALTER TABLE gate2_input OWNER TO app`, "must be owner"},
		{"set role", `SET ROLE migration_admin`, "permission denied"},
		{"replica mode", `SET session_replication_role = 'replica'`, "permission denied"},
		{"create table", `CREATE TABLE gate2_evil (id int)`, "permission denied"},
		{"direct restricted insert", `INSERT INTO gate2_restricted (employer_id, kind, payload) VALUES ($1::uuid, 'shg', 'x')`, "permission denied"},
	} {
		var err error
		if strings.Contains(op.sql, "$1") {
			_, err = c.Exec(tctx(t), op.sql, emp)
		} else {
			_, err = c.Exec(tctx(t), op.sql)
		}
		expectErr(t, err, op.want)
	}
	// Positive control: ordinary writes work and participate.
	execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'ok')`, emp)
	// Triggers still enforced after the failed replica-mode SET:
	// plant a marker, then the input write must be rejected.
	run := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	_ = gate2Capture(t, c, emp, run)
	_, err := c.Exec(tctx(t),
		`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'sneak')`, emp)
	expectErr(t, err, "protected phase")
	execConn(t, c, `ROLLBACK`)
}

// Split validation rejects anything that changed between capture and
// validation, with no partial audit: the re-read compares frozen
// snapshot rows against current tables.
func TestGate2B_SplitValidationRejectsStale(t *testing.T) {
	db := gate2bSetup(t)
	drift := []struct {
		name string
		op   func(t *testing.T, c *pgx.Conn, emp string)
		want string
	}{
		{"update", func(t *testing.T, c *pgx.Conn, emp string) {
			execConn(t, c, `UPDATE gate2_input SET payload = 'drifted' WHERE employer_id = $1::uuid`, emp)
		}, "claim mismatch or fabrication"},
		{"insert", func(t *testing.T, c *pgx.Conn, emp string) {
			execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'drifted')`, emp)
		}, "claims omit"},
		{"delete", func(t *testing.T, c *pgx.Conn, emp string) {
			execConn(t, c, `DELETE FROM gate2_input WHERE employer_id = $1::uuid`, emp)
		}, "claim mismatch or fabrication"},
		{"pack change", func(t *testing.T, c *pgx.Conn, emp string) {
			execConn(t, c, `SELECT gate2_activate_pack($1::uuid, 'SG-MOM-OT-001', 'v2')`, emp)
		}, "claim mismatch for pack"},
	}
	for _, d := range drift {
		t.Run(d.name, func(t *testing.T) {
			c := openConn(t, db.AppDSN)
			emp := gate2Employer(t, c)
			gate2Seed(t, c, emp)
			run := gate2Run(t, c, emp)
			execConn(t, c, `BEGIN`)
			execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
			snap := gate2Capture(t, c, emp, run)
			execConn(t, c, `COMMIT`)
			var before string
			if err := c.QueryRow(tctx(t),
				`SELECT string_agg(input_id::text || kind || payload_hash, ',' ORDER BY input_id::text)
				 FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid`, snap).Scan(&before); err != nil {
				t.Fatalf("frozen: %v", err)
			}
			d.op(t, c, emp)
			execConn(t, c, `BEGIN`)
			_, err := c.Exec(tctx(t), `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
			expectErr(t, err, d.want)
			execConn(t, c, `ROLLBACK`)
			var status string
			var attempts, verdicts int
			if err := c.QueryRow(tctx(t),
				`SELECT (SELECT status FROM gate2_run WHERE id = $1::uuid),
				        (SELECT count(*) FROM gate2_validation_attempt WHERE snapshot_id = $2::uuid),
				        (SELECT count(*) FROM gate2_validation WHERE snapshot_id = $2::uuid)`,
				run, snap).Scan(&status, &attempts, &verdicts); err != nil {
				t.Fatalf("state: %v", err)
			}
			if status != "draft" || attempts != 0 || verdicts != 0 {
				t.Fatalf("run=%s attempts=%d verdicts=%d, want draft/0/0", status, attempts, verdicts)
			}
			var after string
			if err := c.QueryRow(tctx(t),
				`SELECT string_agg(input_id::text || kind || payload_hash, ',' ORDER BY input_id::text)
				 FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid`, snap).Scan(&after); err != nil || after != before {
				t.Fatalf("snapshot bytes changed: %q vs %q err=%v", after, before, err)
			}
		})
	}
}

// Post-validation corrections never rewrite history: the validated
// snapshot's bytes are frozen while a new run captures the change.
func TestGate2B_PostValidationSuccession(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run1 := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	snap1 := gate2Capture(t, c, emp, run1)
	execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run1, snap1)
	execConn(t, c, `COMMIT`)
	var frozen string
	if err := c.QueryRow(tctx(t),
		`SELECT content_hash || (SELECT string_agg(input_id::text || payload, ',' ORDER BY input_id::text)
		 FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid) FROM gate2_snapshot WHERE id = $1::uuid`,
		snap1).Scan(&frozen); err != nil {
		t.Fatalf("frozen: %v", err)
	}
	execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'correction')`, emp)
	run2 := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	snap2 := gate2Capture(t, c, emp, run2)
	execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run2, snap2)
	execConn(t, c, `COMMIT`)
	var frozenAfter string
	var children2 int
	if err := c.QueryRow(tctx(t),
		`SELECT (SELECT content_hash || (SELECT string_agg(input_id::text || payload, ',' ORDER BY input_id::text)
		          FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid) FROM gate2_snapshot WHERE id = $1::uuid),
		        (SELECT count(*) FROM gate2_snapshot_child WHERE snapshot_id = $2::uuid)`,
		snap1, snap2).Scan(&frozenAfter, &children2); err != nil {
		t.Fatalf("succession: %v", err)
	}
	if frozenAfter != frozen || children2 != 3 {
		t.Fatalf("old bytes changed or new snapshot incomplete")
	}
}

// Failures roll back with no partial state, typed errors, and usable
// connections; termination releases the gate with nothing orphaned.
func TestGate2B_RollbackAndFailures(t *testing.T) {
	db := gate2bSetup(t)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))

	t.Run("capture failure leaves nothing", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		gate2Seed(t, c, emp)
		run := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		_, err := c.Exec(tctx(t),
			`SELECT gate2_capture($1::uuid, $2::uuid, '{"inputs":[],"packs":[]}'::jsonb, 'x', 'y')`, emp, run)
		expectErr(t, err, "content hash mismatch")
		execConn(t, c, `ROLLBACK`)
		var snaps, children, attempts, verdicts int
		if err := c.QueryRow(tctx(t),
			`SELECT (SELECT count(*) FROM gate2_snapshot WHERE run_id = $1::uuid),
			        (SELECT count(*) FROM gate2_snapshot_child c JOIN gate2_snapshot s ON s.id = c.snapshot_id WHERE s.run_id = $1::uuid),
			        (SELECT count(*) FROM gate2_validation_attempt WHERE employer_id = $2::uuid),
			        (SELECT count(*) FROM gate2_validation WHERE employer_id = $2::uuid)`,
			run, emp).Scan(&snaps, &children, &attempts, &verdicts); err != nil {
			t.Fatalf("state: %v", err)
		}
		if snaps+children+attempts+verdicts != 0 {
			t.Fatalf("partial state: %d/%d/%d/%d", snaps, children, attempts, verdicts)
		}
		// Gate released, marker gone: a fresh capture and a plain
		// writer both proceed.
		run2 := gate2Run(t, c, emp)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		snap := gate2Capture(t, c, emp, run2)
		execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run2, snap)
		execConn(t, c, `COMMIT`)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'after')`, emp)
	})

	t.Run("writer failure rolls back", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'doomed')`, emp)
		_, err := c.Exec(tctx(t), `SELECT 1/0`)
		expectErr(t, err, "division by zero")
		execConn(t, c, `ROLLBACK`)
		var n int
		if err := c.QueryRow(tctx(t),
			`SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'doomed'`, emp).Scan(&n); err != nil || n != 0 {
			t.Fatalf("doomed=%d err=%v", n, err)
		}
	})

	t.Run("cancel releases the wait, holder proceeds", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		wctx, cancel := context.WithCancel(context.Background())
		w, err := pgx.Connect(context.Background(), db.AppDSN)
		if err != nil {
			t.Fatalf("writer conn: %v", err)
		}
		if _, err := w.Exec(context.Background(), `BEGIN`); err != nil {
			t.Fatalf("begin: %v", err)
		}
		werr := make(chan error, 1)
		go func() {
			_, err := w.Exec(wctx,
				`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'x')`, emp)
			werr <- err
		}()
		gate2AwaitWaiters(t, su, 1, 5*time.Second)
		cancel()
		select {
		case err := <-werr:
			if err == nil {
				t.Fatal("canceled writer reported success")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("canceled writer never returned")
		}
		// Discard-on-cancel (pgx breaks the canceled connection;
		// pools must not reuse it — same posture as admission.go).
		w.Close(context.Background())
		deadline := time.Now().Add(5 * time.Second)
		for gate2Waiters(t, su) != 0 {
			if time.Now().After(deadline) {
				t.Fatal("waiter survived cancel")
			}
			time.Sleep(20 * time.Millisecond)
		}
		execConn(t, c, `COMMIT`)
		var n int
		if err := c.QueryRow(tctx(t),
			`SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'x'`, emp).Scan(&n); err != nil || n != 0 {
			t.Fatalf("canceled row persisted: %d err=%v", n, err)
		}
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'after')`, emp)
	})

	t.Run("statement and lock timeouts are typed", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		for _, tc := range []struct{ set, code string }{
			{`SET statement_timeout = '300ms'`, "57014"},
			{`SET lock_timeout = '300ms'`, "55P03"},
		} {
			w := openConn(t, db.AppDSN)
			execConn(t, w, tc.set)
			_, err := w.Exec(tctx(t), `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || string(pgErr.Code) != tc.code {
				t.Fatalf("%s: err=%v, want code %s", tc.set, err, tc.code)
			}
			execConn(t, w, `ROLLBACK`)
			var one int
			if err := w.QueryRow(tctx(t), `SELECT 1`).Scan(&one); err != nil {
				t.Fatalf("conn unusable after %s: %v", tc.set, err)
			}
		}
		execConn(t, c, `ROLLBACK`)
	})

	t.Run("termination releases gate, nothing orphans", func(t *testing.T) {
		c := openConn(t, db.AppDSN)
		emp := gate2Employer(t, c)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
		pid := backendPID(t, c)
		done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
			if _, err := w.Exec(ctx, `BEGIN`); err != nil {
				return err
			}
			if _, err := w.Exec(ctx,
				`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'survivor')`, emp); err != nil {
				return err
			}
			_, err := w.Exec(ctx, `COMMIT`)
			return err
		})
		gate2AwaitWaiters(t, su, 1, 5*time.Second)
		var terminated bool
		if err := su.QueryRow(tctx(t), `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
			t.Fatalf("terminate=%v err=%v", terminated, err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("waiter: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("waiter did not proceed after termination")
		}
		var n int
		if err := su.QueryRow(tctx(t),
			`SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid AND payload = 'survivor'`, emp).Scan(&n); err != nil || n != 1 {
			t.Fatalf("survivor=%d err=%v", n, err)
		}
		if n := gate2Waiters(t, su); n != 0 {
			t.Fatalf("waiters=%d after termination", n)
		}
	})

	t.Run("crafted multi-employer deadlock is typed and clean", func(t *testing.T) {
		// Single-employer discipline is deadlock-free by
		// construction (one gate per txn, gate-before-row). This
		// characterizes the out-of-MVP case: two txns incrementally
		// accumulating two gates in opposite order must deadlock
		// with a typed error and no partial state — never hang or
		// half-commit.
		c := openConn(t, db.AppDSN)
		empA := gate2Employer(t, c)
		empB := gate2Employer(t, c)
		execConn(t, c, `BEGIN`)
		execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 't1a')`, empA)
		ready := make(chan int32, 1)
		done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
			if _, err := w.Exec(ctx, `BEGIN`); err != nil {
				return err
			}
			if _, err := w.Exec(ctx,
				`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 't2b')`, empB); err != nil {
				return err
			}
			var pid int32
			if err := w.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			ready <- pid
			if _, err := w.Exec(ctx,
				`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 't2a')`, empA); err != nil {
				return err
			}
			_, err := w.Exec(ctx, `COMMIT`)
			return err
		})
		var t2pid int32
		select {
		case t2pid = <-ready:
		case <-time.After(10 * time.Second):
			t.Fatal("t2 never started")
		}
		// Wait until T2 blocks on gate A, so the cycle is certain.
		deadline := time.Now().Add(5 * time.Second)
		for {
			var n int
			if err := su.QueryRow(tctx(t),
				`SELECT count(*) FROM pg_locks WHERE pid = $1 AND locktype = 'advisory' AND NOT granted`, t2pid).Scan(&n); err != nil {
				t.Fatalf("waiter: %v", err)
			}
			if n == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("t2 never blocked on gate A")
			}
			time.Sleep(20 * time.Millisecond)
		}
		_, t1err := c.Exec(tctx(t),
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 't1b')`, empB)
		var t1pg *pgconn.PgError
		t1Dead := errors.As(t1err, &t1pg) && string(t1pg.Code) == "40P01"
		var t2err error
		select {
		case t2err = <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("t2 never returned")
		}
		var t2pg *pgconn.PgError
		t2Dead := errors.As(t2err, &t2pg) && string(t2pg.Code) == "40P01"
		if t1Dead == t2Dead {
			t.Fatalf("t1err=%v t2err=%v, want exactly one 40P01", t1err, t2err)
		}
		// Winner commits, loser rolls back (already aborted).
		if t1Dead {
			execConn(t, c, `ROLLBACK`)
		} else {
			execConn(t, c, `COMMIT`)
		}
		// Loser's first write fully rolled back; winner committed.
		var counts string
		if err := su.QueryRow(tctx(t),
			`SELECT string_agg(payload || ':' || count::text, ',' ORDER BY payload) FROM (
				SELECT payload, count(*) FROM gate2_input
				WHERE payload IN ('t1a', 't1b', 't2a', 't2b') GROUP BY payload) s`,
		).Scan(&counts); err != nil {
			t.Fatalf("counts: %v", err)
		}
		if counts != "t1a:1,t1b:1" && counts != "t2a:1,t2b:1" {
			t.Fatalf("payloads=%s, want exactly one intact pair", counts)
		}
	})
}

// Application-layer gate-first (§6.3): a writer that acquires the
// gate before its business reads never decides on stale pre-wait
// state — the derived write reflects the row committed ahead of it.
func TestGate2B_GoWriterGateFirst(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'ahead')`, emp)
	done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
		if _, err := w.Exec(ctx, `BEGIN`); err != nil {
			return err
		}
		// Gate BEFORE the business read.
		if _, err := w.Exec(ctx, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp); err != nil {
			return err
		}
		var n int
		if err := w.QueryRow(ctx,
			`SELECT count(*) FROM gate2_input WHERE employer_id = $1::uuid`, emp).Scan(&n); err != nil {
			return err
		}
		if _, err := w.Exec(ctx,
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', $2)`, emp, fmt.Sprintf("seen=%d", n)); err != nil {
			return err
		}
		_, err := w.Exec(ctx, `COMMIT`)
		return err
	})
	gate2AwaitWaiters(t, su, 1, 5*time.Second)
	execConn(t, c, `COMMIT`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writer: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writer did not complete")
	}
	// Seed open row + 'ahead' = 2 visible at the writer's read.
	var payload string
	if err := c.QueryRow(tctx(t),
		`SELECT payload FROM gate2_input WHERE employer_id = $1::uuid AND payload LIKE 'seen=%'`, emp).Scan(&payload); err != nil || payload != "seen=2" {
		t.Fatalf("payload=%s err=%v, want seen=2", payload, err)
	}
}

// gate2Async runs fn on a fresh app connection in the background,
// reporting its error. fn must not call t.Fatalf (wrong goroutine).
func gate2Async(dsn string, fn func(context.Context, *pgx.Conn) error) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			done <- err
			return
		}
		defer c.Close(context.Background())
		done <- fn(ctx, c)
	}()
	return done
}

func gate2AwaitWaiters(t *testing.T, su *pgx.Conn, want int, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if gate2Waiters(t, su) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d advisory waiters within %v", want, bound)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lockIdentity returns the opaque (classid, objid) pair identifying one
// backend's advisory lock. Tests compare pairs for equality; no key
// math is performed or needed.
func gate2LockIdentity(t *testing.T, su *pgx.Conn, pid int32, granted bool) (string, string) {
	t.Helper()
	var classid, objid string
	if err := su.QueryRow(tctx(t),
		`SELECT classid::text, objid::text FROM pg_locks
		 WHERE pid = $1 AND locktype = 'advisory' AND granted = $2`,
		pid, granted).Scan(&classid, &objid); err != nil {
		t.Fatalf("identity: %v", err)
	}
	return classid, objid
}

// Writer-first: the writer commits under the gate; capture waits,
// then includes the committed row.
func TestGate2B_WriterFirst(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'early')`, emp)
	done := gate2Async(db.AppDSN, func(ctx context.Context, a *pgx.Conn) error {
		if _, err := a.Exec(ctx, `BEGIN`); err != nil {
			return err
		}
		if _, err := a.Exec(ctx, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp); err != nil {
			return err
		}
		claims, canonical, hash, err := gate2ReadClaimsE(ctx, a, emp)
		if err != nil {
			return err
		}
		var snap string
		if err := a.QueryRow(ctx,
			`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`,
			emp, run, claims, canonical, hash).Scan(&snap); err != nil {
			return err
		}
		if _, err := a.Exec(ctx, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap); err != nil {
			return err
		}
		_, err = a.Exec(ctx, `COMMIT`)
		return err
	})
	gate2AwaitWaiters(t, su, 1, 5*time.Second)
	execConn(t, c, `COMMIT`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("capture did not complete after writer commit")
	}
	var children int
	if err := c.QueryRow(tctx(t),
		`SELECT count(*) FROM gate2_snapshot_child c JOIN gate2_snapshot s ON s.id = c.snapshot_id
		 WHERE s.run_id = $1::uuid`, run).Scan(&children); err != nil || children != 3 {
		t.Fatalf("children=%d err=%v, want 3 (early row included)", children, err)
	}
}

// Capture-first: capture holds the gate; the writer waits with a
// visible waiter row, then commits after capture.
func TestGate2B_CaptureFirst(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run := gate2Run(t, c, emp)
	execConn(t, c, `BEGIN`)
	execConn(t, c, `SELECT gate2_acquire_employer_gate($1::uuid)`, emp)
	done := gate2Async(db.AppDSN, func(ctx context.Context, w *pgx.Conn) error {
		if _, err := w.Exec(ctx, `BEGIN`); err != nil {
			return err
		}
		if _, err := w.Exec(ctx,
			`INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'late')`, emp); err != nil {
			return err
		}
		_, err := w.Exec(ctx, `COMMIT`)
		return err
	})
	gate2AwaitWaiters(t, su, 1, 5*time.Second)
	snap := gate2Capture(t, c, emp, run)
	execConn(t, c, `SELECT gate2_validate($1::uuid, $2::uuid, $3::uuid)`, emp, run, snap)
	execConn(t, c, `COMMIT`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writer: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writer did not complete after capture commit")
	}
	var children, late int
	if err := c.QueryRow(tctx(t),
		`SELECT (SELECT count(*) FROM gate2_snapshot_child WHERE snapshot_id = $1::uuid),
		        (SELECT count(*) FROM gate2_input WHERE employer_id = $2::uuid AND payload = 'late')`,
		snap, emp).Scan(&children, &late); err != nil {
		t.Fatalf("counts: %v", err)
	}
	if children != 2 || late != 1 {
		t.Fatalf("children=%d late=%d, want 2/1 (late row committed after capture, excluded from it)", children, late)
	}
}

// Pre-gate reads are advisory only: claims built before a writer
// commits are rejected for omission (the verifier re-reads post-gate),
// and no partial snapshot persists.
func TestGate2B_StaleClaimsRejected(t *testing.T) {
	db := gate2bSetup(t)
	c := openConn(t, db.AppDSN)
	w := openConn(t, db.AppDSN)
	emp := gate2Employer(t, c)
	gate2Seed(t, c, emp)
	run := gate2Run(t, c, emp)
	claims, canonical, hash := gate2ReadClaims(t, c, emp)
	execConn(t, w, `INSERT INTO gate2_input (employer_id, kind, payload) VALUES ($1::uuid, 'comp', 'late')`, emp)
	execConn(t, c, `BEGIN`)
	_, err := c.Exec(tctx(t),
		`SELECT gate2_capture($1::uuid, $2::uuid, $3::jsonb, $4, $5)`, emp, run, claims, canonical, hash)
	expectErr(t, err, "claims omit 1 open inputs")
	execConn(t, c, `ROLLBACK`)
	var n int
	if err := c.QueryRow(tctx(t),
		`SELECT count(*) FROM gate2_snapshot WHERE run_id = $1::uuid`, run).Scan(&n); err != nil || n != 0 {
		t.Fatalf("snapshots=%d err=%v, want 0 after rejection", n, err)
	}
}
