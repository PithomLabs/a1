package payroll

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

func testPoolAndAdmin(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	migrate := func(ctx context.Context, migDSN string) error {
		_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	}
	setup := dbutil.Setup(t, migrate)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := coredatabase.Open(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	t.Cleanup(pool.Close)
	admin, err := coredatabase.Open(ctx, setup.AdminDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(admin.Close)
	return pool, admin
}

// mkSHGEmployee builds an employee with January terms and a draft sheet
// carrying one REG line, but no SHG instruction: the caller sets the SHG
// state under test.
func mkSHGEmployee(t *testing.T, pool *pgxpool.Pool, emp, period string) (empID, sheet string) {
	t.Helper()
	no := fmt.Sprintf("SH%06d", time.Now().UnixNano()%1000000)
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := hr.SetTerms(ctx(t), pool, emp, e.ID, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err != nil {
		t.Fatalf("terms: %v", err)
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
	return e.ID, sheet
}

func approveSheet(t *testing.T, pool *pgxpool.Pool, emp, sheet string) {
	t.Helper()
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet, "test"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

func setSHG(t *testing.T, pool *pgxpool.Pool, emp, empID string, in hr.SHGInput) *hr.SHGInstruction {
	t.Helper()
	var no string
	if err := pool.QueryRow(ctx(t), `SELECT employee_no FROM employee WHERE id = $1`, empID).Scan(&no); err != nil {
		t.Fatalf("no: %v", err)
	}
	s, err := hr.SetSHG(ctx(t), pool, emp, no, in, "test-op")
	if err != nil {
		t.Fatalf("set shg: %v", err)
	}
	return s
}

// TestAssessSHGMatrix covers the §10B readiness row: each SHG state maps
// to a machine-readable blocker or to covered.
func TestAssessSHGMatrix(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, pool *pgxpool.Pool, emp, empID string)
		code  string // "" means covered
	}{
		{"missing", func(t *testing.T, pool *pgxpool.Pool, emp, empID string) {},
			"SHG_INSTRUCTION_MISSING"},
		{"pending", func(t *testing.T, pool *pgxpool.Pool, emp, empID string) {
			setSHG(t, pool, emp, empID, hr.SHGInput{Fund: "CDAC", Status: "pending",
				SourceDeclaration: "form", EffectiveFrom: "2026-01-01"})
		}, "SHG_PENDING"},
		{"active", func(t *testing.T, pool *pgxpool.Pool, emp, empID string) {
			setSHG(t, pool, emp, empID, hr.SHGInput{Fund: "CDAC", Status: "active",
				SourceDeclaration: "form", EffectiveFrom: "2026-01-01"})
		}, ""},
		{"opt_out", func(t *testing.T, pool *pgxpool.Pool, emp, empID string) {
			setSHG(t, pool, emp, empID, hr.SHGInput{Fund: "CDAC", Status: "opt_out",
				SourceDeclaration: "opt-out request", EffectiveFrom: "2026-01-01"})
		}, "SHG_OPT_OUT"},
		{"verified_none", func(t *testing.T, pool *pgxpool.Pool, emp, empID string) {
			at, by, basis := "2026-08-02T10:00:00Z", "verifier-1", "form on file"
			setSHG(t, pool, emp, empID, hr.SHGInput{Fund: "CDAC", Status: "verified_none",
				SourceDeclaration: "form", EffectiveFrom: "2026-01-01",
				VerifiedAt: &at, VerifiedBy: &by, VerificationBasis: &basis})
		}, ""},
		{"superseded_pending_live_active", func(t *testing.T, pool *pgxpool.Pool, emp, empID string) {
			p := setSHG(t, pool, emp, empID, hr.SHGInput{Fund: "CDAC", Status: "pending",
				SourceDeclaration: "form", EffectiveFrom: "2026-01-01"})
			if _, err := hr.CorrectSHG(ctx(t), pool, emp, p.ID, hr.SHGCorrectInput{
				Status: "active", SourceDeclaration: "confirmed",
				EffectiveFrom: "2026-01-01",
			}, "test-op"); err != nil {
				t.Fatalf("correct: %v", err)
			}
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testPool(t)
			emp, period := setup(t, pool)
			empID, sheet := mkSHGEmployee(t, pool, emp, period)
			c.setup(t, pool, emp, empID)
			approveSheet(t, pool, emp, sheet)
			r, err := Assess(ctx(t), pool, emp, period, nil)
			if err != nil {
				t.Fatalf("assess: %v", err)
			}
			// The shared fixture's E002 and BLOCKED packs always block
			// overall readiness; scope assertions to this case's employee.
			no := compNo(t, pool, emp, empID)
			mine := func() (missing, conflict bool, reason string) {
				for _, m := range r.MissingInfo {
					if m.EmployeeNo == no {
						missing = true
						reason = m.Reason
					}
				}
				for _, s := range r.SHGConflicts {
					if s.EmployeeNo == no {
						conflict = true
					}
				}
				return missing, conflict, reason
			}
			missing, conflict, reason := mine()
			if c.code == "" {
				if missing || conflict {
					t.Fatalf("missing=%v conflict=%v reason=%q want covered",
						missing, conflict, reason)
				}
				return
			}
			if !missing || conflict {
				t.Fatalf("missing=%v conflict=%v want blocker %s", missing, conflict, c.code)
			}
			if !strings.HasPrefix(reason, c.code) {
				t.Fatalf("reason=%q want prefix %s", reason, c.code)
			}
		})
	}
}

// TestAssessSHGCorrupt proves a verified_none row with an incomplete
// triple blocks with SHG_CORRUPT. The row cannot be built through the
// service (service and CHECK agree), so the test relaxes the CHECK as
// admin, inserts, assesses, then restores.
func TestAssessSHGCorrupt(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, period := setup(t, pool)
	empID, sheet := mkSHGEmployee(t, pool, emp, period)
	c := ctx(t)
	restore := func() {
		_, _ = admin.Exec(context.Background(), `DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_verified_consistency') THEN
    ALTER TABLE shg_instruction ADD CONSTRAINT shg_verified_consistency CHECK (
      (status = 'verified_none' AND verified_at IS NOT NULL AND verified_by IS NOT NULL AND verification_basis IS NOT NULL)
      OR (status IN ('active', 'opt_out', 'pending') AND verified_at IS NULL AND verified_by IS NULL AND verification_basis IS NULL)
    );
  END IF;
END $$;`)
	}
	// Restore even on failure: the database is shared, and a dropped
	// CHECK left behind would poison every later run.
	t.Cleanup(restore)
	if _, err := admin.Exec(c, `ALTER TABLE shg_instruction DROP CONSTRAINT shg_verified_consistency`); err != nil {
		t.Fatalf("drop check: %v", err)
	}
	var corruptID string
	err := admin.QueryRow(c,
		`INSERT INTO shg_instruction (employer_id, employee_id, fund, status, source_declaration, effective_from, verified_by)
		 VALUES ($1, $2, 'CDAC', 'verified_none', 'direct', '2026-01-01', 'op')
		 RETURNING id`, emp, empID).Scan(&corruptID)
	if err != nil {
		t.Fatalf("insert corrupt: %v", err)
	}
	approveSheet(t, pool, emp, sheet)
	r, err := Assess(c, pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	no := compNo(t, pool, emp, empID)
	found := false
	for _, m := range r.MissingInfo {
		if m.EmployeeNo == no && strings.HasPrefix(m.Reason, "SHG_CORRUPT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing=%+v want SHG_CORRUPT for %s", r.MissingInfo, no)
	}
	if _, err := admin.Exec(c, `DELETE FROM shg_instruction WHERE id = $1`, corruptID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := admin.Exec(c, `ALTER TABLE shg_instruction ADD CONSTRAINT shg_verified_consistency CHECK (
	  (status = 'verified_none' AND verified_at IS NOT NULL AND verified_by IS NOT NULL AND verification_basis IS NOT NULL)
	  OR (status IN ('active', 'opt_out', 'pending') AND verified_at IS NULL AND verified_by IS NULL AND verification_basis IS NULL)
	)`); err != nil {
		t.Fatalf("restore check: %v", err)
	}
}

// TestAssessSHGPostApprovalCorrection proves an SHG instruction created
// after the sheet approval forces reissue via SHGConflicts.
func TestAssessSHGPostApprovalCorrection(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	empID, sheet := mkSHGEmployee(t, pool, emp, period)
	orig := setSHG(t, pool, emp, empID, hr.SHGInput{Fund: "CDAC", Status: "active",
		SourceDeclaration: "form", EffectiveFrom: "2026-01-01"})
	approveSheet(t, pool, emp, sheet)
	no := compNo(t, pool, emp, empID)
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, m := range r.MissingInfo {
		if m.EmployeeNo == no {
			t.Fatalf("missing=%+v want employee covered before correction", m)
		}
	}
	// Correct to a still-covering status: the employee stays covered
	// but the approval predates the correction, so the conflict leg
	// (not the coverage leg) must fire.
	if _, err := hr.CorrectSHG(ctx(t), pool, emp, orig.ID, hr.SHGCorrectInput{
		Status: "active", SourceDeclaration: "form v2",
		EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("correct: %v", err)
	}
	r, err = Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, m := range r.MissingInfo {
		if m.EmployeeNo == no {
			t.Fatalf("missing=%+v want coverage preserved after correction", m)
		}
	}
	if len(r.SHGConflicts) != 1 || r.SHGConflicts[0].EmployeeNo != no {
		t.Fatalf("conflicts=%+v want one SHG conflict for %s", r.SHGConflicts, no)
	}
	if !strings.Contains(r.Verdict, "shg conflicts") {
		t.Fatalf("verdict=%q want shg conflicts counted", r.Verdict)
	}
}
