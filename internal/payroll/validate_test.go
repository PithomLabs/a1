package payroll

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
)

func TestValidateHappyPath(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !v.Valid || v.SnapshotVersion != 1 || v.SnapshotID == "" {
		t.Fatalf("validation=%+v", v)
	}
	var status, inputsHash string
	if err := pool.QueryRow(ctx(t),
		`SELECT status, inputs_hash FROM payroll_run WHERE id = $1`, run).Scan(&status, &inputsHash); err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != "validated" {
		t.Fatalf("status=%s", status)
	}
	snap, err := GetSnapshot(ctx(t), pool, v.SnapshotID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inputsHash != snap.ContentHash {
		t.Fatalf("inputs_hash=%s want the snapshot content hash", inputsHash)
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM payroll_snapshot WHERE payroll_run_id = $1 AND status = 'valid'`, run).Scan(&n); err != nil || n != 1 {
		t.Fatalf("valid snapshots=%d err=%v want exactly one", n, err)
	}
	var action string
	if err := pool.QueryRow(ctx(t),
		`SELECT action FROM audit_event WHERE employer_id = $1 AND entity_id = $2 AND action = 'run.validate'`,
		emp, run).Scan(&action); err != nil {
		t.Fatalf("audit: %v", err)
	}
}

func TestValidateVersionLockout(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	// Capture v1 directly, without validating: offering any other
	// version must refuse without mutating run or snapshots.
	first, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := ValidateRun(ctx(t), pool, emp, run, 2, "test-op"); err == nil {
		t.Fatal("mismatched version accepted")
	} else if !strings.Contains(err.Error(), "version") {
		t.Fatalf("err=%v want version lockout", err)
	}
	var status string
	var versions int
	if err := pool.QueryRow(ctx(t), `SELECT status FROM payroll_run WHERE id = $1`, run).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v want draft untouched", status, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM payroll_snapshot WHERE payroll_run_id = $1`, run).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("snapshots=%d err=%v want no new version", versions, err)
	}
	// Offering the true version validates against the standing tip.
	v, err := ValidateRun(ctx(t), pool, emp, run, 1, "test-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !v.Valid || v.SnapshotID != first.ID {
		t.Fatalf("validation=%+v want the standing tip", v)
	}
}

func TestValidateBlockersThenFix(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, _, _ := mkDraftEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	// Draft sheet, no SHG: validation captures faithfully, then
	// refuses on the recorded blockers.
	v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if v.Valid || len(v.Blockers) == 0 {
		t.Fatalf("validation=%+v want blockers", v)
	}
	// Fix the inputs: add SHG, approve. The standing tip is now
	// stale, so re-offering v1 invalidates and recaptures instead of
	// validating.
	amt := "fixed"
	var no string
	if err := pool.QueryRow(ctx(t), `SELECT employee_no FROM employee WHERE id = $1`, eID).Scan(&no); err != nil {
		t.Fatalf("no: %v", err)
	}
	if _, err := hr.SetSHG(ctx(t), pool, emp, no, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	var sheet string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM timesheet WHERE employer_id = $1 AND employee_id = $2`, emp, eID).Scan(&sheet); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	approveEmp(t, pool, emp, sheet)
	stale, err := ValidateRun(ctx(t), pool, emp, run, 1, "test-op")
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if stale.Valid || !stale.Stale || stale.SnapshotVersion != 2 {
		t.Fatalf("validation=%+v want the stale verdict with v2", stale)
	}
	final, err := ValidateRun(ctx(t), pool, emp, run, 2, "test-op")
	if err != nil {
		t.Fatalf("validate v2: %v", err)
	}
	if !final.Valid {
		t.Fatalf("validation=%+v want valid", final)
	}
}

func TestValidateEmptyPopulation(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	implementPacks(t, admin, emp)
	v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if v.Valid {
		t.Fatalf("validation=%+v want vacuous refusal", v)
	}
	found := false
	for _, b := range v.Blockers {
		if strings.Contains(b, "empty") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blockers=%v want the empty-population reason", v.Blockers)
	}
	// Neither run nor snapshots advance on a vacuous validation.
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM payroll_snapshot WHERE payroll_run_id = $1`, run).Scan(&n); err != nil || n != 0 {
		t.Fatalf("snapshots=%d err=%v want none", n, err)
	}
}

func TestValidateDuplicateReplay(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	first, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	counts := inputCounts(t, pool, emp)
	second, err := ValidateRun(ctx(t), pool, emp, run, 1, "test-op")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Valid || second.SnapshotID != first.SnapshotID {
		t.Fatalf("replay=%+v want the identical validation", second)
	}
	// Validation reads; it never writes input records.
	after := inputCounts(t, pool, emp)
	if counts != after {
		t.Fatalf("input counts changed: %v -> %v", counts, after)
	}
	var versions int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM payroll_snapshot WHERE payroll_run_id = $1`, run).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("snapshots=%d err=%v want no duplicate version", versions, err)
	}
}

// inputCounts digests the input tables: validation must never move them.
func inputCounts(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	var out string
	for _, table := range []string{"employment_terms", "compensation_line", "timesheet", "shg_instruction",
		"attendance_interval", "clock_event", "employee_status_history", "rule_pack_activation"} {
		var n int
		if err := pool.QueryRow(ctx(t),
			`SELECT count(*) FROM `+table+` WHERE employer_id = $1`, emp).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out += fmt.Sprintf("%s:%d;", table, n)
	}
	return out
}

func TestValidateConcurrent(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	type result struct {
		v   *Validation
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op")
			results <- result{v, err}
		}()
	}
	// Deterministic set outcome: the winner validates; the loser
	// gets a stale verdict naming the winner's tip — never a silent
	// adoption of a version it never inspected. Either goroutine may
	// win.
	var winner, loser *Validation
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("racer: %v", r.err)
		}
		if r.v.Valid {
			winner = r.v
		} else {
			loser = r.v
		}
	}
	if winner == nil || loser == nil {
		t.Fatalf("want one valid and one stale verdict, got winner=%+v loser=%+v", winner, loser)
	}
	if !loser.Stale || loser.SnapshotVersion != winner.SnapshotVersion || loser.SnapshotID != winner.SnapshotID {
		t.Fatalf("loser verdict=%+v does not name winner tip=%+v", loser, winner)
	}
	var versions int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM payroll_snapshot WHERE payroll_run_id = $1`, run).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("snapshots=%d err=%v want exactly one version", versions, err)
	}
	// Re-offering the named tip is idempotent success.
	v, err := ValidateRun(ctx(t), pool, emp, run, loser.SnapshotVersion, "test-op")
	if err != nil || !v.Valid || v.SnapshotID != winner.SnapshotID {
		t.Fatalf("re-offer: v=%+v err=%v", v, err)
	}
}

func TestValidatePostValidationDrift(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, termsID := mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if _, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	comps, err := hr.ListCompensation(ctx(t), pool, emp, termsID)
	if err != nil {
		t.Fatalf("comps: %v", err)
	}
	if _, err := hr.CorrectCompensation(ctx(t), pool, emp, comps[0].ID, hr.CorrectionInput{
		AmountMonthly: "150.00", EffectiveFrom: "2026-01-01", Reason: "late raise",
	}, "test-op"); err != nil {
		t.Fatalf("correct: %v", err)
	}
	// The run stays validated; drift is detected, not auto-applied.
	var status string
	if err := pool.QueryRow(ctx(t), `SELECT status FROM payroll_run WHERE id = $1`, run).Scan(&status); err != nil || status != "validated" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	var tip string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM payroll_snapshot WHERE payroll_run_id = $1 AND status = 'valid'`, run).Scan(&tip); err != nil {
		t.Fatalf("tip: %v", err)
	}
	rep, err := DetectDrift(ctx(t), pool, emp, tip)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !rep.Drifted {
		t.Fatal("post-validation change undetected")
	}
	// The correction left a post-approval conflict on the v1 sheet;
	// the governed reissue (new draft, resubmit, reapprove) clears
	// it before revalidation can succeed.
	var v1 string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM timesheet WHERE employer_id = $1 AND employee_id = $2 AND supersedes_id IS NULL`,
		emp, eID).Scan(&v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	v2sheet, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, v2sheet.ID, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, v2sheet.ID, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, v2sheet.ID, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// Re-offering the stale tip revalidates explicitly.
	stale, err := ValidateRun(ctx(t), pool, emp, run, 1, "test-op")
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if stale.Valid || !stale.Stale || stale.SnapshotVersion != 2 {
		t.Fatalf("validation=%+v want the stale verdict", stale)
	}
	final, err := ValidateRun(ctx(t), pool, emp, run, 2, "test-op")
	if err != nil {
		t.Fatalf("validate v2: %v", err)
	}
	if !final.Valid {
		t.Fatalf("validation=%+v", final)
	}
}

func TestRunLifecycleTrigger(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if _, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// No silent calculated, approved or finalized — not even by
	// direct SQL, and validated never regresses.
	for _, to := range []string{"calculated", "approved", "finalized", "draft"} {
		if _, err := pool.Exec(ctx(t),
			`UPDATE payroll_run SET status = $1 WHERE id = $2`, to, run); err == nil {
			t.Fatalf("run transition to %s accepted", to)
		}
	}
	var status string
	if err := pool.QueryRow(ctx(t), `SELECT status FROM payroll_run WHERE id = $1`, run).Scan(&status); err != nil || status != "validated" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	// Runs are born draft; nothing inserts a pre-validated run.
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO payroll_run (employer_id, pay_period_id, kind, status, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES ($1, $2, 'regular', 'validated', 'x', '{}', 'none', '{}')`, emp, period); err == nil {
		t.Fatal("non-draft run INSERT accepted")
	}
}

// A run over blocked statutory packs stays blocked: validation
// refuses with pack blockers on the faithful tip, and only an
// explicit revalidation after the pack change can succeed.
func TestValidateBlockedPacks(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	// Real pack states: nothing IMPLEMENTABLE is manufactured here.
	v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if v.Valid || v.SnapshotVersion != 1 {
		t.Fatalf("validation=%+v want a blocked v1 verdict", v)
	}
	named := false
	for _, b := range v.Blockers {
		if strings.Contains(b, "SG-") {
			named = true
		}
	}
	if !named {
		t.Fatalf("blockers=%v want named statutory packs", v.Blockers)
	}
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM payroll_run WHERE id = $1`, run).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	detail, err := GetSnapshot(ctx(t), pool, v.SnapshotID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	blocked := false
	for _, p := range detail.Packs {
		if p.Status != Implementable {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("pins=%+v want recorded non-implementable packs", detail.Packs)
	}
	// The pack change is source drift: re-offer, then validate clean.
	implementPacks(t, admin, emp)
	stale, err := ValidateRun(ctx(t), pool, emp, run, 1, "test-op")
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if stale.Valid || !stale.Stale || stale.SnapshotVersion != 2 {
		t.Fatalf("validation=%+v want the stale verdict", stale)
	}
	final, err := ValidateRun(ctx(t), pool, emp, run, 2, "test-op")
	if err != nil || !final.Valid {
		t.Fatalf("validation=%+v err=%v", final, err)
	}
}
