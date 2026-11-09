// Gate 2 attack tests (prompt18 Phase 2): the postLin-issuance barrier
// question for Option A (XID-ticket + session barriers). These tests reuse
// the Gate 1 experimental mechanism (admissionExpSQL) on disposable
// databases and never modify it. Findings feed §10 Q1–Q2.
package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// gate2RedefineSQL is the Gate-1 privilege-request core: the three frozen
// INVOKER helpers redefined as DEFINER with byte-identical bodies. The
// revoked Gate-2 databases apply it so they test "Option A as proposed
// for production" rather than the known-broken intermediate state.
const gate2RedefineSQL = `
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
$$;`

// gate2Setup provisions a private drop-first database at head plus the Gate 1
// experimental SQL. revoked=true additionally applies the full advisory
// revoke and the DEFINER redefinition (the Gate-1 production proposal).
func gate2Setup(t *testing.T, suffix string, revoked bool) dbutil.TestDB {
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
	if revoked {
		if _, err := mig.Exec(tctx(t), gate2RedefineSQL); err != nil {
			t.Fatalf("redefine: %v", err)
		}
		su := openConn(t, dbutil.WithDB(dsn, name))
		if _, err := su.Exec(tctx(t), advisoryRevokeSQL); err != nil {
			t.Fatalf("revoke: %v", err)
		}
	}
	return db
}

// simpleConn opens a simple-protocol connection: every query avoids
// server-side prepared statements, so DISCARD ALL (which deallocates
// them) cannot break the driver. Server-side semantics are identical.
func simpleConn(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	ctx, cancel := testCtx()
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func backendPID(t *testing.T, c *pgx.Conn) int32 {
	t.Helper()
	var pid int32
	if err := c.QueryRow(tctx(t), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("pid: %v", err)
	}
	return pid
}

// locksHeldBy counts live advisory pg_locks rows (granted or waiting)
// for one backend. Inspection runs as superuser; the attacker never
// needs it.
func locksHeldBy(t *testing.T, su *pgx.Conn, pid int32) int {
	t.Helper()
	var n int
	if err := su.QueryRow(tctx(t),
		`SELECT count(*) FROM pg_locks WHERE pid = $1 AND locktype = 'advisory'`, pid).Scan(&n); err != nil {
		t.Fatalf("locks: %v", err)
	}
	return n
}

func holdRowsFor(t *testing.T, su *pgx.Conn, pid int32) int {
	t.Helper()
	var n int
	if err := su.QueryRow(tctx(t),
		`SELECT count(*) FROM exp_barrier_hold WHERE backend_pid = $1`, pid).Scan(&n); err != nil {
		t.Fatalf("holds: %v", err)
	}
	return n
}

func memberCount(t *testing.T, c *pgx.Conn, emp string) int {
	t.Helper()
	var n int
	if err := c.QueryRow(tctx(t),
		`SELECT count(*) FROM exp_membership WHERE employer_id = $1::uuid`, emp).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// writerCommits runs a participating writer (xact population lock,
// then insert, then commit) on its own backend and reports whether it
// completed within the bound. A physically held session barrier makes
// the xact lock wait; absent barriers let it commit promptly.
func writerCommits(t *testing.T, dsn, emp, member string, bound time.Duration) (bool, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bound+10*time.Second)
		defer cancel()
		w, err := pgx.Connect(ctx, dsn)
		if err != nil {
			done <- err
			return
		}
		defer w.Close(context.Background())
		if _, err := w.Exec(ctx, `BEGIN`); err != nil {
			done <- err
			return
		}
		if _, err := w.Exec(ctx, `SELECT acquire_payroll_population_lock($1::uuid)`, emp); err != nil {
			done <- err
			return
		}
		if _, err := w.Exec(ctx,
			`INSERT INTO exp_membership (employer_id, kind, member_id) VALUES ($1::uuid, 'employee', $2)`,
			emp, member); err != nil {
			done <- err
			return
		}
		done <- func() error {
			_, err := w.Exec(ctx, `COMMIT`)
			return err
		}()
	}()
	select {
	case err := <-done:
		return true, err
	case <-time.After(bound):
		return false, nil
	}
}

// Control: with barriers genuinely held, a participating writer's xact
// lock waits (visible waiter row); releasing lets it through. This
// proves the harness detects the protected state, so "writer
// committed promptly" in the attack tests means barriers were absent.
func TestGate2ControlBarriersBlockWriter(t *testing.T) {
	db := gate2Setup(t, "_gate2plain", false)
	a := openConn(t, db.AppDSN)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	emp := newEmpID(t, a)
	lockPop(t, a, emp)
	ticket := ticketForPop(t, a, emp)
	if locksHeldBy(t, su, backendPID(t, a)) != 1 {
		t.Fatal("want exactly the population session lock")
	}
	execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, a, `SELECT 1`)
	execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	ok, err := writerCommits(t, db.AppDSN, emp, "control-writer", 2*time.Second)
	if ok || err != nil {
		t.Fatalf("writer committed promptly against held barriers (ok=%v err=%v)", ok, err)
	}
	var waiters int
	if err := su.QueryRow(tctx(t),
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiters); err != nil {
		t.Fatalf("waiters: %v", err)
	}
	if waiters != 1 {
		t.Fatalf("waiters=%d, want 1 blocked writer", waiters)
	}
	execConn(t, a, `ROLLBACK`)
	execConn(t, a, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
	ok, err = writerCommits(t, db.AppDSN, emp, "control-writer-2", 5*time.Second)
	if !ok || err != nil {
		t.Fatalf("writer did not proceed after release (ok=%v err=%v)", ok, err)
	}
}

// Test A (prompt18 §5.1) on the unrevoked database: DISCARD ALL before
// BEGIN drops the physical locks while the hold rows survive; the
// wrapper reacquire is a silent no-op (row found → RETURN without
// locking), yet verification passes and a writer commits inside the
// capture window unprompted. A raw re-lock restores protection and is
// tested as the control branch.
func TestGate2APreBeginDiscard(t *testing.T) {
	db := gate2Setup(t, "_gate2plain", false)
	dsn := dbutil.DSN(t)
	su := openConn(t, dbutil.WithDB(dsn, db.Name))

	t.Run("wrapper reacquire silently unrestored, stale snapshot passes", func(t *testing.T) {
		a := simpleConn(t, db.AppDSN)
		pid := backendPID(t, a)
		emp := newEmpID(t, a)
		lockPop(t, a, emp)
		ticket := ticketForPop(t, a, emp)
		if locksHeldBy(t, su, pid) != 1 {
			t.Fatal("setup: want the session lock held")
		}
		execConn(t, a, `DISCARD ALL`)
		if n := locksHeldBy(t, su, pid); n != 0 {
			t.Fatalf("locks after DISCARD ALL = %d, want 0", n)
		}
		if n := holdRowsFor(t, su, pid); n != 1 {
			t.Fatalf("hold rows after DISCARD ALL = %d, want 1 (rows survive)", n)
		}
		// Permitted-path reacquire through the wrapper.
		lockPop(t, a, emp)
		if n := locksHeldBy(t, su, pid); n != 0 {
			t.Fatalf("locks after wrapper reacquire = %d, want 0 (silent no-op)", n)
		}
		if n := holdRowsFor(t, su, pid); n != 1 {
			t.Fatalf("hold rows after wrapper reacquire = %d, want 1", n)
		}
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, a, `SELECT 1`)
		// Verification passes with zero physical locks held.
		execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		before := memberCount(t, a, emp)
		ok, err := writerCommits(t, db.AppDSN, emp, "intruder", 5*time.Second)
		if !ok || err != nil {
			t.Fatalf("writer blocked despite absent barriers (ok=%v err=%v)", ok, err)
		}
		if n := memberCount(t, a, emp); n != before {
			t.Fatalf("snapshot saw the intruder: %d vs %d", n, before)
		}
		execConn(t, a, `COMMIT`)
		// Persisted state: the intruder committed strictly before
		// the admission committed, yet the admission's read set
		// missed it. Stale snapshot accepted.
		after := memberCount(t, a, emp)
		if after != before+1 {
			t.Fatalf("persisted members=%d, want %d (intruder missing?)", after, before+1)
		}
	})

	t.Run("raw reacquire restores protection", func(t *testing.T) {
		a := simpleConn(t, db.AppDSN)
		pid := backendPID(t, a)
		emp := newEmpID(t, a)
		lockPop(t, a, emp)
		ticket := ticketForPop(t, a, emp)
		execConn(t, a, `DISCARD ALL`)
		execConn(t, a,
			`SELECT pg_advisory_lock(hashtext('pay-population:' || $1::text))`, emp)
		if locksHeldBy(t, su, pid) != 1 {
			t.Fatal("raw reacquire did not restore the physical lock")
		}
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, a, `SELECT 1`)
		execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		ok, _ := writerCommits(t, db.AppDSN, emp, "blocked", 2*time.Second)
		if ok {
			t.Fatal("writer committed despite restored barriers")
		}
		execConn(t, a, `ROLLBACK`)
	})
}

// Test A under the Gate-1 revoke (+ DEFINER redefinition): DISCARD ALL
// is a session statement, not a revocable function, so it still drops
// the locks; the wrapper reacquire still no-ops; and the raw
// restoration path is now denied — the backend can no longer restore
// its barriers at all, yet verification still passes.
func TestGate2APreBeginDiscardRevoked(t *testing.T) {
	db := gate2Setup(t, "_gate2revoked", true)
	dsn := dbutil.DSN(t)
	su := openConn(t, dbutil.WithDB(dsn, db.Name))
	a := simpleConn(t, db.AppDSN)
	pid := backendPID(t, a)
	emp := newEmpID(t, a)
	lockPop(t, a, emp)
	ticket := ticketForPop(t, a, emp)
	execConn(t, a, `DISCARD ALL`)
	if n := locksHeldBy(t, su, pid); n != 0 {
		t.Fatalf("locks after DISCARD ALL = %d, want 0", n)
	}
	lockPop(t, a, emp)
	if n := locksHeldBy(t, su, pid); n != 0 {
		t.Fatalf("locks after wrapper reacquire = %d, want 0 (no-op)", n)
	}
	_, err := a.Exec(tctx(t),
		`SELECT pg_advisory_lock(hashtext('pay-population:' || $1::text))`, emp)
	expectErr(t, err, "permission denied")
	execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
	execConn(t, a, `SELECT 1`)
	execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
	before := memberCount(t, a, emp)
	ok, err := writerCommits(t, db.AppDSN, emp, "intruder", 5*time.Second)
	if !ok || err != nil {
		t.Fatalf("writer blocked despite absent barriers (ok=%v err=%v)", ok, err)
	}
	execConn(t, a, `COMMIT`)
	if after := memberCount(t, a, emp); after != before+1 {
		t.Fatalf("persisted members=%d, want %d", after, before+1)
	}
}

// The (int,int) advisory spelling uses a DISTINCT namespace from the
// bigint spelling: unlocking via the split halves does NOT release a
// bigint-taken lock. This removes the split spelling from Test B's
// release-path enumeration (fewer aliasing paths than feared).
func TestGate2AdvisoryNamespaceDistinct(t *testing.T) {
	db := gate2Setup(t, "_gate2plain", false)
	su := openConn(t, dbutil.WithDB(dbutil.DSN(t), db.Name))
	a := openConn(t, db.AppDSN)
	pid := backendPID(t, a)
	emp := newEmpID(t, a)
	lockPop(t, a, emp)
	if locksHeldBy(t, su, pid) != 1 {
		t.Fatal("setup: want the session lock held")
	}
	var released bool
	if err := a.QueryRow(tctx(t),
		`SELECT pg_advisory_unlock(((hashtext('pay-population:' || $1::text)) >> 32)::int,
		                           ((hashtext('pay-population:' || $1::text)) << 32 >> 32)::int)`,
		emp).Scan(&released); err != nil {
		t.Fatalf("split-spelling unlock: %v", err)
	}
	if released {
		t.Fatal("split-spelling unlock released a bigint lock: namespaces shared?")
	}
	if n := locksHeldBy(t, su, pid); n != 1 {
		t.Fatalf("locks after split-spelling unlock = %d, want 1 (untouched)", n)
	}
	execConn(t, a, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}

// Test B (prompt18 §5.2) on the unrevoked database: every app-permitted
// release path is attempted inside the open RR transaction. Raw unlock
// succeeds (physical gone, rows remain); DISCARD ALL in-txn is rejected
// by PostgreSQL itself; a writer commits while barriers are absent; the
// wrapper reacquire no-ops; re-verification fails closed (consumed).
// The extension shows the window the re-verify does not cover: release
// AFTER verification, during work, commits stale with no detection.
func TestGate2BPostSnapshotRelease(t *testing.T) {
	db := gate2Setup(t, "_gate2plain", false)
	dsn := dbutil.DSN(t)
	su := openConn(t, dbutil.WithDB(dsn, db.Name))

	t.Run("release paths enumerated, re-verify fails closed", func(t *testing.T) {
		a := openConn(t, db.AppDSN)
		pid := backendPID(t, a)
		emp := newEmpID(t, a)
		lockPop(t, a, emp)
		ticket := ticketForPop(t, a, emp)
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, a, `SELECT 1`)
		execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		// (i) raw bigint unlock: permitted, succeeds.
		var released bool
		if err := a.QueryRow(tctx(t),
			`SELECT pg_advisory_unlock(hashtext('pay-population:' || $1::text))`,
			emp).Scan(&released); err != nil || !released {
			t.Fatalf("raw unlock released=%v err=%v, want true/nil", released, err)
		}
		if n := locksHeldBy(t, su, pid); n != 0 {
			t.Fatalf("locks after raw unlock = %d, want 0", n)
		}
		if n := holdRowsFor(t, su, pid); n != 1 {
			t.Fatalf("hold rows after raw unlock = %d, want 1", n)
		}
		// (ii) writer commits while barriers are absent.
		ok, err := writerCommits(t, db.AppDSN, emp, "intruder", 5*time.Second)
		if !ok || err != nil {
			t.Fatalf("writer blocked despite absent barriers (ok=%v err=%v)", ok, err)
		}
		// (iii) wrapper reacquire no-ops on the surviving row.
		lockPop(t, a, emp)
		if n := locksHeldBy(t, su, pid); n != 0 {
			t.Fatalf("locks after wrapper reacquire = %d, want 0", n)
		}
		// (iv) re-verification fails closed: single-use consumed.
		_, err = a.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		expectErr(t, err, "already consumed")
		execConn(t, a, `ROLLBACK`)
		// (v) DISCARD ALL inside an open transaction: PostgreSQL
		// itself rejects it. Separate backend: the rejection aborts
		// the transaction it runs in.
		d := openConn(t, db.AppDSN)
		execConn(t, d, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, d, `SELECT 1`)
		_, err = d.Exec(tctx(t), `DISCARD ALL`)
		expectErr(t, err, "DISCARD ALL cannot run inside a transaction")
		execConn(t, d, `ROLLBACK`)
		// Rollback undoes the consume (single-use holds across
		// COMMITS; reuse after rollback is by design).
		var consumed bool
		if err := su.QueryRow(tctx(t),
			`SELECT consumed FROM exp_admission_ticket WHERE id = $1::uuid`, ticket).Scan(&consumed); err != nil {
			t.Fatalf("ticket state: %v", err)
		}
		if consumed {
			t.Fatal("consume survived rollback: single-use broken")
		}
	})

	t.Run("post-verify release commits stale undetected", func(t *testing.T) {
		a := openConn(t, db.AppDSN)
		emp := newEmpID(t, a)
		lockPop(t, a, emp)
		ticket := ticketForPop(t, a, emp)
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, a, `SELECT 1`)
		execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		before := memberCount(t, a, emp)
		// Release AFTER verification, during the work window: the
		// caller's own backend drops its physical barriers.
		execConn(t, a,
			`SELECT pg_advisory_unlock(hashtext('pay-population:' || $1::text))`, emp)
		ok, err := writerCommits(t, db.AppDSN, emp, "intruder", 5*time.Second)
		if !ok || err != nil {
			t.Fatalf("writer blocked despite absent barriers (ok=%v err=%v)", ok, err)
		}
		if n := memberCount(t, a, emp); n != before {
			t.Fatalf("snapshot saw the intruder: %d vs %d", n, before)
		}
		execConn(t, a, `COMMIT`)
		if after := memberCount(t, a, emp); after != before+1 {
			t.Fatalf("persisted members=%d, want %d", after, before+1)
		}
		// Cleanup notices too late: the wrapper unlock reports the
		// physical lock already gone (Do would discard the
		// connection — after committing the stale read set).
		var unlocked bool
		if err := a.QueryRow(tctx(t),
			`SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`,
			emp).Scan(&unlocked); err != nil {
			t.Fatalf("unlock: %v", err)
		}
		if unlocked {
			t.Fatal("unlock reported success despite the out-of-band release")
		}
	})
}

// Test B under the Gate-1 revoke: raw release paths are denied, the
// wrapper release deletes rows (fail-closed on re-verify), but the
// post-verify work window is unchanged — wrapper-unlock is a granted
// path, and nothing re-checks barriers between verification and
// commit.
func TestGate2BPostSnapshotReleaseRevoked(t *testing.T) {
	db := gate2Setup(t, "_gate2revoked", true)
	dsn := dbutil.DSN(t)
	su := openConn(t, dbutil.WithDB(dsn, db.Name))

	t.Run("raw release denied, wrapper release fails closed", func(t *testing.T) {
		a := openConn(t, db.AppDSN)
		pid := backendPID(t, a)
		emp := newEmpID(t, a)
		lockPop(t, a, emp)
		ticket := ticketForPop(t, a, emp)
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, a, `SELECT 1`)
		execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		// Each denied attempt runs under a savepoint: the 42501
		// aborts to the savepoint, keeping the RR snapshot alive
		// for the rest of the attack sequence.
		execConn(t, a, `SAVEPOINT deny1`)
		_, err := a.Exec(tctx(t),
			`SELECT pg_advisory_unlock(hashtext('pay-population:' || $1::text))`, emp)
		expectErr(t, err, "permission denied")
		execConn(t, a, `ROLLBACK TO SAVEPOINT deny1`)
		execConn(t, a, `SAVEPOINT deny2`)
		_, err = a.Exec(tctx(t), `SELECT pg_advisory_unlock_all()`)
		expectErr(t, err, "permission denied")
		execConn(t, a, `ROLLBACK TO SAVEPOINT deny2`)
		if n := locksHeldBy(t, su, pid); n != 1 {
			t.Fatalf("locks = %d, want 1 (all raw paths denied)", n)
		}
		// DISCARD ALL in-txn on a separate backend: the rejection
		// aborts its own transaction.
		d := openConn(t, db.AppDSN)
		execConn(t, d, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, d, `SELECT 1`)
		_, derr := d.Exec(tctx(t), `DISCARD ALL`)
		expectErr(t, derr, "DISCARD ALL cannot run inside a transaction")
		execConn(t, d, `ROLLBACK`)
		// The granted wrapper release works. The writer commits
		// while barriers are absent.
		execConn(t, a, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
		ok, err := writerCommits(t, db.AppDSN, emp, "intruder", 5*time.Second)
		if !ok || err != nil {
			t.Fatalf("writer blocked despite released barriers (ok=%v err=%v)", ok, err)
		}
		execConn(t, a, `COMMIT`)
		// Persisted state: the consume and the row deletion both
		// committed. A superuser view now sees zero hold rows.
		if n := holdRowsFor(t, su, pid); n != 0 {
			t.Fatalf("hold rows = %d, want 0 after committed release", n)
		}
		var consumed bool
		if err := su.QueryRow(tctx(t),
			`SELECT consumed FROM exp_admission_ticket WHERE id = $1::uuid`, ticket).Scan(&consumed); err != nil {
			t.Fatalf("ticket state: %v", err)
		}
		if !consumed {
			t.Fatal("ticket not persisted as consumed")
		}
		// The missing rows are proven through enforcement, not
		// table reads (app cannot SELECT the hold table): a fresh
		// issue fails on holds, and the old ticket fails consumed.
		_, err = a.Exec(tctx(t),
			`SELECT exp_issue_ticket($1::uuid, '{}'::uuid[], '{}'::text[])`, emp)
		expectErr(t, err, "population barrier not held")
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		_, err = a.Exec(tctx(t), `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		expectErr(t, err, "already consumed")
		execConn(t, a, `ROLLBACK`)
	})

	t.Run("post-verify window unchanged under revoke", func(t *testing.T) {
		a := openConn(t, db.AppDSN)
		emp := newEmpID(t, a)
		lockPop(t, a, emp)
		ticket := ticketForPop(t, a, emp)
		execConn(t, a, `BEGIN ISOLATION LEVEL REPEATABLE READ`)
		execConn(t, a, `SELECT 1`)
		execConn(t, a, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticket, emp)
		before := memberCount(t, a, emp)
		execConn(t, a, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
		ok, err := writerCommits(t, db.AppDSN, emp, "intruder", 5*time.Second)
		if !ok || err != nil {
			t.Fatalf("writer blocked despite released barriers (ok=%v err=%v)", ok, err)
		}
		execConn(t, a, `COMMIT`)
		if after := memberCount(t, a, emp); after != before+1 {
			t.Fatalf("persisted members=%d, want %d (stale not demonstrated)", after, before+1)
		}
	})
}

// Backend termination drops session locks with the backend while the
// hold rows orphan (GC is time-based, 1h): a fresh backend is
// unaffected, and the orphan rows age out rather than blocking.
func TestGate2BackendTerminationOrphans(t *testing.T) {
	db := gate2Setup(t, "_gate2plain", false)
	dsn := dbutil.DSN(t)
	su := openConn(t, dbutil.WithDB(dsn, db.Name))
	a := openConn(t, db.AppDSN)
	pid := backendPID(t, a)
	emp := newEmpID(t, a)
	lockPop(t, a, emp)
	_ = ticketForPop(t, a, emp)
	var terminated bool
	if err := su.QueryRow(tctx(t),
		`SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate=%v err=%v", terminated, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for locksHeldBy(t, su, pid) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("locks survived backend termination")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := holdRowsFor(t, su, pid); n != 1 {
		t.Fatalf("hold rows = %d, want 1 orphaned row", n)
	}
	b := openConn(t, db.AppDSN)
	lockPop(t, b, emp)
	execConn(t, b, `SELECT exp_admission_unlock('population', $1::uuid, NULL::uuid, NULL::text)`, emp)
}
