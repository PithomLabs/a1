package payroll

import (
	"strings"
	"testing"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// E5 allow-and-flag: correcting a line that feeds an approved period
// is allowed, but readiness carries an explicit correction conflict
// until the downstream sheet reissues and approves.
func TestAssessCompCorrectionApprovedPeriod(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1, terms, sheet := mkCompFixture(t, pool, emp, period)
	no := compNo(t, pool, emp, e1)
	orig, err := hr.AddCompensation(ctx(t), pool, emp, terms, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
		EffectiveFrom: "2026-08-01", Reason: "night shift",
	}, "test-op")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	approveAt(t, pool, sheet, `now()`)
	if _, err := hr.CorrectCompensation(ctx(t), pool, emp, orig, hr.CorrectionInput{
		AmountMonthly: "120.00", EffectiveFrom: "2026-08-01", Reason: "rate fix",
	}, "test-op"); err != nil {
		t.Fatalf("correct: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.CompConflicts) != 1 || r.CompConflicts[0].EmployeeNo != no {
		t.Fatalf("conflicts=%+v want the flagged correction", r.CompConflicts)
	}
	if !strings.Contains(r.CompConflicts[0].Reason, "created after approval") ||
		!strings.Contains(r.CompConflicts[0].Reason, "correction") {
		t.Fatalf("reason=%q want the explicit correction state", r.CompConflicts[0].Reason)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, sheet, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	r2, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r2.CompConflicts) != 0 {
		t.Fatalf("conflicts=%+v want cleared by the reissue approval", r2.CompConflicts)
	}
}
