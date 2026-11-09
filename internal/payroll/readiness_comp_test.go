package payroll

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// mkCompFixture builds a complete employee with January terms and a
// draft sheet carrying one REG line. The caller approves explicitly to
// control the approval timestamp deterministically.
func mkCompFixture(t *testing.T, pool *pgxpool.Pool, emp, period string) (empID, termsID, sheet string) {
	t.Helper()
	no := fmt.Sprintf("KC%06d", time.Now().UnixNano()%1000000)
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	tm, err := hr.SetTerms(ctx(t), pool, emp, e.ID, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	// No backdate: terms keep their honest now() stamp; approvals
	// below sort after them in real time (C8 normalizes created_at).
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, e.ID, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
		 VALUES ($1, $2, $3, 'draft') RETURNING id`,
		emp, e.ID, period).Scan(&sheet); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, sheet, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	return e.ID, tm.ID, sheet
}

func approveAt(t *testing.T, pool *pgxpool.Pool, sheet, when string) {
	t.Helper()
	if _, err := pool.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = `+when+`
		 WHERE id = $1`, sheet); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

func compNo(t *testing.T, pool *pgxpool.Pool, emp, id string) string {
	t.Helper()
	var no string
	if err := pool.QueryRow(ctx(t),
		`SELECT employee_no FROM employee WHERE employer_id = $1 AND id = $2`, emp, id).Scan(&no); err != nil {
		t.Fatalf("no: %v", err)
	}
	return no
}

// Compensation added before the approval is covered by it: no conflict.
func TestAssessCompBeforeApproval(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1, terms, sheet := mkCompFixture(t, pool, emp, period)
	if _, err := hr.AddCompensation(ctx(t), pool, emp, terms, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100", EffectiveFrom: "2026-08-01",
		Reason: "shift work",
	}, "test-op"); err != nil {
		t.Fatalf("add: %v", err)
	}
	var audit int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'compensation.add'`,
		emp).Scan(&audit); err != nil || audit != 1 {
		t.Fatalf("audits=%d err=%v want the audited add", audit, err)
	}
	var created string
	if err := pool.QueryRow(ctx(t),
		`SELECT max(created_at)::text FROM compensation_line WHERE employer_id = $1`, emp).Scan(&created); err != nil {
		t.Fatalf("created: %v", err)
	}
	approveAt(t, pool, sheet, `'`+created+`'::timestamptz + interval '1 hour'`)
	_ = e1
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, c := range r.CompConflicts {
		if c.EmployeeNo == compNo(t, pool, emp, e1) {
			t.Fatalf("conflicts=%+v want none", r.CompConflicts)
		}
	}
}

// A fixed allowance added retroactively after approval is an explicit
// readiness conflict; approving a correction version clears it.
func TestAssessCompRetroConflict(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1, terms, sheet := mkCompFixture(t, pool, emp, period)
	no := compNo(t, pool, emp, e1)
	approveAt(t, pool, sheet, `now()`)
	if _, err := hr.AddCompensation(ctx(t), pool, emp, terms, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100", EffectiveFrom: "2026-08-01",
		Reason: "shift work",
	}, "test-op"); err != nil {
		t.Fatalf("retro add: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.CompConflicts) != 1 || r.CompConflicts[0].EmployeeNo != no {
		t.Fatalf("conflicts=%+v want the retro allowance", r.CompConflicts)
	}
	for _, in := range r.Included {
		if in == no {
			t.Fatalf("included=%v want %s excluded under conflict", r.Included, no)
		}
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
		t.Fatalf("conflicts=%+v want cleared by the correction approval", r2.CompConflicts)
	}
}

// Compensation effective only in a future period never conflicts with
// this period's approval.
func TestAssessCompFuturePeriod(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	_, terms, sheet := mkCompFixture(t, pool, emp, period)
	approveAt(t, pool, sheet, `now()`)
	if _, err := hr.AddCompensation(ctx(t), pool, emp, terms, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100", EffectiveFrom: "2026-09-01",
		Reason: "shift work",
	}, "test-op"); err != nil {
		t.Fatalf("add: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.CompConflicts) != 0 {
		t.Fatalf("conflicts=%+v want none", r.CompConflicts)
	}
}

// The backdated-insert dodge is closed twice: raw INSERT is revoked,
// so no fabricated created_at can reach the table at all.
func TestAssessCompCreatedAtBypass(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1, terms, sheet := mkCompFixture(t, pool, emp, period)
	no := compNo(t, pool, emp, e1)
	approveAt(t, pool, sheet, `now()`)
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO compensation_line (employer_id, terms_id, kind, code, amount_monthly, effective_from, created_at)
		 VALUES ($1, $2, 'allowance', 'SHIFT', 100, '2026-08-01', '2020-01-01T00:00:00Z')`,
		emp, terms); err == nil {
		t.Fatal("raw compensation INSERT must be revoked from app")
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, c := range r.CompConflicts {
		if c.EmployeeNo == no {
			t.Fatalf("conflicts=%+v want none: nothing was inserted", r.CompConflicts)
		}
	}
}
