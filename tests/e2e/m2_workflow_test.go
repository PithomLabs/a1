// Package e2e proves the Milestone 2 end-to-end workflow (§10F) on the
// synthetic Singapore profile: seeded employer to validated-or-blocked
// run, with snapshot identity, drift and preservation proved on
// persisted state.
package e2e

import (
	"strings"
	"testing"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/seed"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// TestM2Workflow runs the fifteen §10F steps on the seeded MERIDIAN
// profile. Required statutory packs stay BLOCKED, so the run must
// remain blocked: the test proves the refusal, the snapshot identity
// across runs, and preservation under source drift. It never
// manufactures a validation by enabling unverified packs.
//
// The test needs a fresh e2e database: the seed employer is fixed,
// and a second seeding would rewrite governed history. The battery
// recycles test databases first; a dirty database fails loudly here,
// never skips.
func TestM2Workflow(t *testing.T) {
	pool := appPool
	var seeded bool
	if err := pool.QueryRow(ctx(t),
		`SELECT EXISTS (SELECT 1 FROM employer WHERE code = 'MERIDIAN')`).Scan(&seeded); err != nil {
		t.Fatalf("seed check: %v", err)
	}
	if seeded {
		t.Fatal("e2e database is dirty: MERIDIAN already seeded; drop the e2e database and re-run")
	}

	// Steps 1-7: bootstrap employer, employees, monthly terms,
	// roster, attendance evidence and the pay period.
	if _, err := seed.Demo(ctx(t), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var emp, period string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employer WHERE code = 'MERIDIAN'`).Scan(&emp); err != nil {
		t.Fatalf("employer: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM pay_period WHERE employer_id = $1 AND code = 'PP-2026-08'`, emp).Scan(&period); err != nil {
		t.Fatalf("period: %v", err)
	}
	e1, err := hr.GetEmployee(ctx(t), pool, emp, "DEMO-001")
	if err != nil {
		t.Fatalf("DEMO-001: %v", err)
	}

	// Step 4: record compensation for the included employee.
	var termsID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employment_terms WHERE employer_id = $1 AND employee_id = $2`,
		emp, e1.ID).Scan(&termsID); err != nil {
		t.Fatalf("terms: %v", err)
	}
	compID, err := hr.AddCompensation(ctx(t), pool, emp, termsID, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
		EffectiveFrom: "2026-01-01", Reason: "e2e shift allowance",
	}, "e2e-op")
	if err != nil {
		t.Fatalf("compensation: %v", err)
	}

	// Step 9: the seeded SHG instruction stands, and a governed
	// pre-approval succession replaces its August coverage.
	instructions, err := hr.ListSHG(ctx(t), pool, emp, "DEMO-001")
	if err != nil || len(instructions) != 1 {
		t.Fatalf("seeded shg: %v %+v", err, instructions)
	}
	amt := "fixed"
	succ, err := hr.CorrectSHG(ctx(t), pool, emp, instructions[0].ID, hr.SHGCorrectInput{
		Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "e2e August declaration", EffectiveFrom: "2026-08-01",
	}, "e2e-op")
	if err != nil {
		t.Fatalf("shg succession: %v", err)
	}
	if succ.SupersedesID == nil || *succ.SupersedesID != instructions[0].ID {
		t.Fatalf("successor=%+v does not link the seeded instruction", succ)
	}

	// Step 8: generate and approve the clean employee's timesheet.
	// DEMO-002's missing punch keeps it out of generation; both
	// incomplete employees are excluded below with reasons.
	gen, err := timesheet.Generate(ctx(t), pool, emp, "PP-2026-08", "e2e-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var sheet string
	for _, g := range gen.Generated {
		if g.EmployeeNo == "DEMO-001" {
			sheet = g.TimesheetID
		}
	}
	if sheet == "" {
		t.Fatalf("generation=%+v want DEMO-001 generated", gen)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet, "e2e-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet, "e2e-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Step 10: required packs keep their actual verified states —
	// BLOCKED — and OT-001 stays unpinned and unimplemented.
	for _, p := range []string{"SG-CPF-2026", "SG-SDL", "SG-SHG", "SG-MOM-KET"} {
		var status string
		if err := pool.QueryRow(ctx(t),
			`SELECT status FROM rule_pack_activation WHERE employer_id = $1 AND pack = $2`,
			emp, p).Scan(&status); err != nil || status != "BLOCKED" {
			t.Fatalf("pack %s status=%s err=%v", p, status, err)
		}
	}
	// The registry itself is unreadable to app (pinned separately
	// in the activation suite); the employer-meaningful assertion
	// is that no IMPLEMENTABLE row exists for OT-001.
	var otImpl int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM rule_pack_activation
		 WHERE employer_id = $1 AND pack = 'SG-MOM-OT-001' AND status = 'IMPLEMENTABLE'`,
		emp).Scan(&otImpl); err != nil || otImpl != 0 {
		t.Fatalf("OT-001 implementable rows=%d err=%v", otImpl, err)
	}

	// Step 11: create the run; exclude the incomplete employees.
	run, err := payroll.CreateRun(ctx(t), pool, emp, period, "regular", "", "e2e-op")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, no := range []string{"DEMO-002", "DEMO-003"} {
		if _, err := payroll.AddExclusion(ctx(t), pool, emp, run.ID, no, "e2e: incomplete evidence", "e2e-op"); err != nil {
			t.Fatalf("exclude %s: %v", no, err)
		}
	}

	// Step 12: readiness names the pack blockers.
	rep, err := payroll.Assess(ctx(t), pool, emp, period, &run.ID)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if rep.Ready || len(rep.Blocked) == 0 {
		t.Fatalf("readiness=%+v want pack blockers", rep)
	}

	// Step 13: capture the faithful snapshot.
	snap, err := payroll.CaptureSnapshot(ctx(t), pool, emp, run.ID, "e2e-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.Version != 1 || snap.ContentHash == "" {
		t.Fatalf("snapshot=%+v", snap)
	}

	// Step 14: snapshot identity and content hash. A second draft
	// run over the same inputs captures the identical hash: same
	// substantive inputs, same rule versions, same hash.
	detail, err := payroll.GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	decisions := map[string]string{}
	for _, e := range detail.Employees {
		decisions[e.EmployeeNo] = e.Decision
	}
	if decisions["DEMO-001"] != "included" || decisions["DEMO-002"] != "excluded" || decisions["DEMO-003"] != "excluded" {
		t.Fatalf("decisions=%v", decisions)
	}
	if len(detail.Packs) == 0 {
		t.Fatal("no pack pins recorded")
	}
	runB, err := payroll.CreateRun(ctx(t), pool, emp, period, "regular", "", "e2e-op")
	if err != nil {
		t.Fatalf("runB: %v", err)
	}
	for _, no := range []string{"DEMO-002", "DEMO-003"} {
		if _, err := payroll.AddExclusion(ctx(t), pool, emp, runB.ID, no, "e2e: incomplete evidence", "e2e-op"); err != nil {
			t.Fatalf("excludeB %s: %v", no, err)
		}
	}
	snapB, err := payroll.CaptureSnapshot(ctx(t), pool, emp, runB.ID, "e2e-op")
	if err != nil {
		t.Fatalf("captureB: %v", err)
	}
	if snapB.ContentHash != snap.ContentHash {
		t.Fatalf("hashA=%s hashB=%s want identical content hashes", snap.ContentHash, snapB.ContentHash)
	}

	// The blocked packs block the run: offering the captured v1
	// refuses with pack blockers, and the run stays draft. No
	// unverified pack is enabled to manufacture a success.
	v, err := payroll.ValidateRun(ctx(t), pool, emp, run.ID, 1, "e2e-op")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if v.Valid {
		t.Fatalf("validation=%+v want refusal over blocked packs", v)
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
		`SELECT status FROM payroll_run WHERE id = $1`, run.ID).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v", status, err)
	}

	// Step 15: a post-approval source change drifts the tip, never
	// rewrites it. The original row keeps its hash; invalidation
	// appends history; revalidation is explicit.
	if _, err := hr.CorrectCompensation(ctx(t), pool, emp, compID, hr.CorrectionInput{
		AmountMonthly: "150.00", EffectiveFrom: "2026-01-01", Reason: "e2e late raise",
	}, "e2e-op"); err != nil {
		t.Fatalf("correct comp: %v", err)
	}
	drift, err := payroll.DetectDrift(ctx(t), pool, emp, snap.ID)
	if err != nil || !drift.Drifted {
		t.Fatalf("drift=%+v err=%v want drift detected", drift, err)
	}
	var hashNow, statusNow string
	if err := pool.QueryRow(ctx(t),
		`SELECT content_hash, status FROM payroll_snapshot WHERE id = $1`, snap.ID).Scan(&hashNow, &statusNow); err != nil {
		t.Fatalf("re-read tip: %v", err)
	}
	if hashNow != snap.ContentHash {
		t.Fatalf("tip hash=%s want preserved %s", hashNow, snap.ContentHash)
	}
	if err := payroll.InvalidateSnapshot(ctx(t), pool, snap.ID, "e2e: post-approval correction", "e2e-op"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	// No tip stands after the explicit invalidation, so the
	// revalidation offers 0: v2 records the post-approval
	// conflict, and the run stays draft until a governed reissue
	// clears it.
	second, err := payroll.ValidateRun(ctx(t), pool, emp, run.ID, 0, "e2e-op")
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if second.Valid || second.SnapshotVersion != 2 {
		t.Fatalf("validation=%+v want the blocked v2 verdict", second)
	}
	conflict := false
	for _, b := range second.Blockers {
		if strings.Contains(b, "correction; reissue required") {
			conflict = true
		}
	}
	if !conflict {
		t.Fatalf("blockers=%v want the post-approval conflict", second.Blockers)
	}
	final, err := payroll.ValidateRun(ctx(t), pool, emp, run.ID, 2, "e2e-op")
	if err != nil {
		t.Fatalf("validate v2: %v", err)
	}
	if final.Valid {
		t.Fatalf("validation=%+v want conflict refusal", final)
	}
	var v1hash, v1status string
	if err := pool.QueryRow(ctx(t),
		`SELECT content_hash, status FROM payroll_snapshot WHERE payroll_run_id = $1 AND version = 1`,
		run.ID).Scan(&v1hash, &v1status); err != nil {
		t.Fatalf("v1 history: %v", err)
	}
	if v1hash != snap.ContentHash || v1status != "invalidated" {
		t.Fatalf("v1 hash=%s status=%s want preserved content, invalidated status", v1hash, v1status)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM payroll_run WHERE id = $1`, run.ID).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%s err=%v", status, err)
	}
}
