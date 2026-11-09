package payroll

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Correction runs form a controlled successor chain, never a second
// unrelated regular run: creation requires the link exactly for
// corrections, validation requires a validated target, chains extend
// linearly, and forks are rejected.
func TestCorrectionRunRelationship(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op"); err != nil || !v.Valid {
		t.Fatalf("original: v=%+v err=%v", v, err)
	}
	// Kind/link coherence at creation.
	if _, err := CreateRun(ctx(t), pool, emp, period, "correction", "", "test-op"); err == nil {
		t.Fatal("unlinked correction accepted")
	}
	if _, err := CreateRun(ctx(t), pool, emp, period, "regular", run, "test-op"); err == nil {
		t.Fatal("linked regular accepted")
	}
	if _, err := CreateRun(ctx(t), pool, emp, period, "correction", "00000000-0000-0000-0000-000000000000", "test-op"); err == nil {
		t.Fatal("correction off a nonexistent run accepted")
	}
	// The link stays inside one employer and period.
	otherPeriod := mkPeriod(t, pool, emp, "PP2", "2026-09-01", "2026-09-30")
	other, err := CreateRun(ctx(t), pool, emp, otherPeriod, "regular", "", "test-op")
	if err != nil {
		t.Fatalf("other-period run: %v", err)
	}
	if _, err := CreateRun(ctx(t), pool, emp, period, "correction", other.ID, "test-op"); err == nil {
		t.Fatal("cross-period correction accepted")
	}
	// A correction off a draft original cannot validate first. A
	// fresh employer isolates the ordering check from the
	// open-ended August fixtures above.
	emp2, period2, _ := snapSetup(t, pool)
	mkSnapEmployee(t, pool, emp2, period2)
	implementPacks(t, admin, emp2)
	other2, err := CreateRun(ctx(t), pool, emp2, period2, "regular", "", "test-op")
	if err != nil {
		t.Fatalf("other2: %v", err)
	}
	early, err := CreateRun(ctx(t), pool, emp2, period2, "correction", other2.ID, "test-op")
	if err != nil {
		t.Fatalf("early correction: %v", err)
	}
	if v, err := ValidateRun(ctx(t), pool, emp2, early.ID, 0, "test-op"); err == nil {
		t.Fatalf("correction validated ahead of its draft original: v=%+v", v)
	} else if !strings.Contains(err.Error(), "before its original") {
		t.Fatalf("err=%v want the original-first refusal", err)
	}
	// Linked correction validates against the validated original.
	c, err := CreateRun(ctx(t), pool, emp, period, "correction", run, "test-op")
	if err != nil {
		t.Fatalf("correction: %v", err)
	}
	if c.CorrectsRunID != run {
		t.Fatalf("correction=%+v does not record its original", c)
	}
	if v, err := ValidateRun(ctx(t), pool, emp, c.ID, 0, "test-op"); err != nil || !v.Valid {
		t.Fatalf("correction validation: v=%+v err=%v", v, err)
	}
	// Chains extend linearly off the latest validated run.
	c2, err := CreateRun(ctx(t), pool, emp, period, "correction", c.ID, "test-op")
	if err != nil {
		t.Fatalf("chained correction: %v", err)
	}
	if v, err := ValidateRun(ctx(t), pool, emp, c2.ID, 0, "test-op"); err != nil || !v.Valid {
		t.Fatalf("chained validation: v=%+v err=%v", v, err)
	}
	// A fork off the already-succeeded original is rejected and
	// stays draft without a standing tip.
	fork, err := CreateRun(ctx(t), pool, emp, period, "correction", run, "test-op")
	if err != nil {
		t.Fatalf("fork creation: %v", err)
	}
	if _, err := ValidateRun(ctx(t), pool, emp, fork.ID, 0, "test-op"); err == nil {
		t.Fatal("forked correction validated")
	}
	var status string
	var tips int
	row := pool.QueryRow(ctx(t), `SELECT status FROM payroll_run WHERE id = $1`, fork.ID)
	if err := row.Scan(&status); err != nil || status != "draft" {
		t.Fatalf("fork status=%s err=%v", status, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM payroll_snapshot WHERE payroll_run_id = $1`, fork.ID).Scan(&tips); err != nil || tips != 0 {
		t.Fatalf("fork tips=%d err=%v want none (failed validation rolls back)", tips, err)
	}
}

// Only one validated regular run stands per employer and period; a
// second regular run cannot validate, by service or by direct SQL.
func TestMultipleValidatedRegularPrevention(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op"); err != nil || !v.Valid {
		t.Fatalf("first: v=%+v err=%v", v, err)
	}
	second, err := CreateRun(ctx(t), pool, emp, period, "regular", "", "test-op")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	_, err = ValidateRun(ctx(t), pool, emp, second.ID, 0, "test-op")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "already") {
		t.Fatalf("second validation err=%v want an already-stands refusal", err)
	}
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM payroll_run WHERE id = $1`, second.ID).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("second status=%s err=%v", status, err)
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE payroll_run SET status = 'validated' WHERE id = $1`, second.ID); err == nil {
		t.Fatal("direct SQL validated a second regular run")
	}
}

// Draft-to-validated by direct SQL requires exactly one valid
// snapshot tip: a status flip without captured inputs is refused.
func TestDirectValidateWithoutSnapshotRejected(t *testing.T) {
	pool := testPool(t)
	emp, _, run := snapSetup(t, pool)
	if _, err := pool.Exec(ctx(t),
		`UPDATE payroll_run SET status = 'validated' WHERE id = $1`, run); err == nil {
		t.Fatal("direct SQL validated a run with no snapshot")
	} else if !strings.Contains(strings.ToLower(err.Error()), "snapshot") {
		t.Fatalf("err=%v want a snapshot refusal", err)
	}
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM payroll_run WHERE id = $1`, run).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	_ = emp
}

// A status flip over a blocked tip is refused even though the tip
// exists: direct SQL cannot bypass the blockers assessment.
func TestDirectValidateBlockedTipRejected(t *testing.T) {
	pool, _ := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	// Real pack states: blocked, never IMPLEMENTABLE here.
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.Version != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE payroll_run SET status = 'validated' WHERE id = $1`, run); err == nil {
		t.Fatal("direct SQL validated a run over a blocked tip")
	} else if !strings.Contains(err.Error(), "IMPLEMENTABLE") {
		t.Fatalf("err=%v want a pack-blocker refusal", err)
	}
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM payroll_run WHERE id = $1`, run).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v", status, err)
	}
}

// Run identity freezes once a snapshot exists: employer, period,
// kind and the correction link can no longer move. Before any
// snapshot, kind/link coherence still holds.
func TestRunIdentityFrozenAfterSnapshot(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op"); err != nil || !v.Valid {
		t.Fatalf("validate: v=%+v err=%v", v, err)
	}
	for _, q := range []string{
		`UPDATE payroll_run SET kind = 'correction' WHERE id = $1`,
		`UPDATE payroll_run SET pay_period_id = gen_random_uuid() WHERE id = $1`,
		`UPDATE payroll_run SET corrects_run_id = gen_random_uuid() WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx(t), q, run); err == nil {
			t.Fatalf("identity change accepted after snapshot: %s", q)
		}
	}
	var kind string
	var link *string
	if err := pool.QueryRow(ctx(t),
		`SELECT kind, corrects_run_id::text FROM payroll_run WHERE id = $1`, run).Scan(&kind, &link); err != nil || kind != "regular" || link != nil {
		t.Fatalf("kind=%s link=%v err=%v", kind, link, err)
	}
	// Pre-snapshot control: a bare draft cannot shed its kind/link
	// coherence either.
	bare, err := CreateRun(ctx(t), pool, emp, period, "regular", "", "test-op")
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE payroll_run SET kind = 'correction' WHERE id = $1`, bare.ID); err == nil {
		t.Fatal("linkless correction accepted by direct SQL")
	}
}

func mkPeriod(t *testing.T, pool *pgxpool.Pool, emp, code, start, end string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, $2, $3::date, $4::date, ($4::date + 7)::date, ($4::date + 7)::date, ($4::date + 14)::date)
		 RETURNING id`, emp, code, start, end).Scan(&id); err != nil {
		t.Fatalf("period %s: %v", code, err)
	}
	return id
}
