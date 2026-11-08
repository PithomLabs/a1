package payroll

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
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
		t.Fatalf("open: %v", err)
	}
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		pool.Close()
		t.Fatalf("suite pool runs as %q, want app role", who)
	}
	t.Cleanup(pool.Close)
	return pool
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func setup(t *testing.T, pool *pgxpool.Pool) (emp, period string) {
	t.Helper()
	code := fmt.Sprintf("pay_%d", time.Now().UnixNano())
	err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&emp)
	if err != nil {
		t.Fatalf("employer: %v", err)
	}
	err = pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id`, emp).Scan(&period)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	mk := func(no string) string {
		var id string
		err := pool.QueryRow(ctx(t),
			`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
			 VALUES ($1, $2, $2, 'a', 'F', '1990-01-01', 'SG', 'active', '2026-01-01') RETURNING id`,
			emp, no).Scan(&id)
		if err != nil {
			t.Fatalf("employee: %v", err)
		}
		return id
	}
	mkTerms := func(empID string) {
		_, err := pool.Exec(ctx(t),
			`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
			  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
			  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
			  place_of_work, salary_period)
			 VALUES ($1, $2, '2026-01-01', 'Agent', 'support', 'workman', true, 'test', 2600,
			  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
			emp, empID)
		if err != nil {
			t.Fatalf("terms: %v", err)
		}
	}
	mkSHG := func(empID string) {
		_, err := pool.Exec(ctx(t),
			`INSERT INTO shg_instruction (employer_id, employee_id, fund, status, amount_or_basis, source_declaration, effective_from)
			 VALUES ($1, $2, 'CDAC', 'active', 'fixed', 'declared', '2026-01-01')`, emp, empID)
		if err != nil {
			t.Fatalf("shg: %v", err)
		}
	}
	mkSheet := func(empID, status string, hours float64) {
		var sheet string
		err := pool.QueryRow(ctx(t),
			`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
			 VALUES ($1, $2, $3, 'draft') RETURNING id`,
			emp, empID, period).Scan(&sheet)
		if err != nil {
			t.Fatalf("timesheet: %v", err)
		}
		if hours > 0 {
			if _, err := pool.Exec(ctx(t),
				`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
				 VALUES (gen_random_uuid(), $1, $2, 'REG', $3, ($3::numeric * 3600000000)::bigint, $4)`, emp, sheet, hours, period); err != nil {
				t.Fatalf("line: %v", err)
			}
		}
		if status == "approved" {
			if _, err := pool.Exec(ctx(t),
				`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now()
				 WHERE id = $1`, sheet); err != nil {
				t.Fatalf("approve: %v", err)
			}
		}
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay_code: %v", err)
	}
	complete := mk("E001")
	partial := mk("E002")
	// Complete employee: terms and SHG predate the approved sheet, which
	// carries payable lines.
	mkTerms(complete)
	mkSHG(complete)
	mkSheet(complete, "approved", 8)
	// Partial employee: approved sheet with lines, but no terms or SHG.
	mkSheet(partial, "approved", 8)
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES
		 ($1, 'SG-CPF-2026', 'v0', 'BLOCKED'), ($1, 'SG-SDL', 'v0', 'BLOCKED'),
		 ($1, 'SG-SHG', 'v0', 'BLOCKED'), ($1, 'SG-MOM-KET', 'v0', 'BLOCKED')`, emp); err != nil {
		t.Fatalf("activations: %v", err)
	}
	return emp, period
}

func TestAssessBlocked(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r.Ready {
		t.Fatal("expected not ready")
	}
	if r.Population != 2 || len(r.Included) != 1 || r.Included[0] != "E001" {
		t.Fatalf("population=%d included=%v", r.Population, r.Included)
	}
	if len(r.Expected) != 2 || len(r.WithApproved) != 2 {
		t.Fatalf("expected=%v with_approved=%v", r.Expected, r.WithApproved)
	}
	if len(r.MissingInfo) != 1 || r.MissingInfo[0].EmployeeNo != "E002" {
		t.Fatalf("missing=%+v", r.MissingInfo)
	}
	if !strings.Contains(r.MissingInfo[0].Reason, "terms") {
		t.Fatalf("reason=%s", r.MissingInfo[0].Reason)
	}
	if len(r.Blocked) != 4 {
		t.Fatalf("blocked=%+v", r.Blocked)
	}
	if len(r.Packs) != 4 {
		t.Fatalf("packs=%+v", r.Packs)
	}
}

func TestAssessWithExclusion(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	var run string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO payroll_run (employer_id, pay_period_id, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES ($1, $2, 'h', '{}', 'c', '{}') RETURNING id`, emp, period).Scan(&run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var e1 string
	err = pool.QueryRow(ctx(t), `SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`, emp).Scan(&e1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO payroll_run_exclusion (employer_id, payroll_run_id, employee_id, reason, actor)
		 VALUES ($1, $2, $3, 'test exclusion', 'op')`, emp, run, e1); err != nil {
		t.Fatalf("exclusion: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, &run)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.Excluded) != 1 || r.Excluded[0].EmployeeNo != "E001" {
		t.Fatalf("excluded=%+v", r.Excluded)
	}
	if r.RunStatus == nil {
		t.Fatal("expected run status to be reported")
	}
	if len(r.Included) != 0 || len(r.Blocked) != 1 {
		t.Fatalf("included=%v blocked=%v", r.Included, r.Blocked)
	}
	if r.Blocked[0].Pack != "(population)" {
		t.Fatalf("blocked=%+v", r.Blocked)
	}
	if r.Ready {
		t.Fatal("expected not ready with no included employees")
	}
}

func TestAssessUnknownRun(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	bogus := "00000000-0000-0000-0000-000000000000"
	if _, err := Assess(ctx(t), pool, emp, period, &bogus); err == nil {
		t.Fatal("expected NotFound for unknown run")
	}
}

func TestAssessRunWrongPeriod(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	var other string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP2', '2026-09-01', '2026-09-30', '2026-10-07', '2026-10-07', '2026-10-14')
		 RETURNING id`, emp).Scan(&other)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	var run string
	err = pool.QueryRow(ctx(t),
		`INSERT INTO payroll_run (employer_id, pay_period_id, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES ($1, $2, 'h', '{}', 'c', '{}') RETURNING id`, emp, other).Scan(&run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := Assess(ctx(t), pool, emp, period, &run); err == nil {
		t.Fatal("expected NotFound for run bound to another period")
	}
}

func TestAssessTermConflict(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	var e1 string
	err := pool.QueryRow(ctx(t), `SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`, emp).Scan(&e1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A raise recorded after the sheet was approved invalidates the
	// approval's view of the record. The predecessor closes through
	// the controlled function; the successor INSERT keeps its honest
	// now() stamp, which is what the conflict detector compares.
	if _, err := pool.Exec(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-08-15', 'test')`, emp, e1); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-08-15', 'Agent', 'support', 'workman', true, 'raise', 2800,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		emp, e1); err != nil {
		t.Fatalf("raise: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r.Ready {
		t.Fatal("expected not ready under term conflict")
	}
	if len(r.TermConflicts) != 1 || r.TermConflicts[0].EmployeeNo != "E001" {
		t.Fatalf("conflicts=%+v", r.TermConflicts)
	}
	if len(r.Included) != 0 {
		t.Fatalf("included=%v", r.Included)
	}
}

func TestAssessTerminatedWithApprovedSheet(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	var e1 string
	err := pool.QueryRow(ctx(t), `SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`, emp).Scan(&e1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// E001 leaves mid-period; their approved history must stay visible
	// to readiness instead of vanishing with their active status.
	if _, err := hr.SetStatus(ctx(t), pool, emp, e1, hr.StatusTerminated, "2026-08-20", "resigned", "test-op"); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.Expected) != 2 || len(r.WithApproved) != 2 {
		t.Fatalf("expected=%v with_approved=%v", r.Expected, r.WithApproved)
	}
	if len(r.Included) != 1 || r.Included[0] != "E001" {
		t.Fatalf("included=%v", r.Included)
	}
}

func TestAssessEmptyPopulation(t *testing.T) {
	pool := testPool(t)
	code := fmt.Sprintf("empty_%d", time.Now().UnixNano())
	var emp string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&emp)
	if err != nil {
		t.Fatalf("employer: %v", err)
	}
	var period string
	err = pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id`, emp).Scan(&period)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r.Ready {
		t.Fatal("expected not ready for an empty population")
	}
	if r.Population != 0 || r.Verdict != "not ready: empty population" {
		t.Fatalf("population=%d verdict=%q", r.Population, r.Verdict)
	}
}

func TestAssessReady(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	// Second employee becomes complete: terms and SHG are recorded,
	// then the setup-approved sheet is reissued so the effective
	// approval postdates the new terms and no term conflict arises.
	// (Backdating terms is impossible since C8 normalizes created_at;
	// the reissue flow is the governed way to move an approval.)
	var e2 string
	err := pool.QueryRow(ctx(t), `SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E002'`, emp).Scan(&e2)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'support', 'workman', true, 'test', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		emp, e2); err != nil {
		t.Fatalf("terms: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO shg_instruction (employer_id, employee_id, fund, status, amount_or_basis, source_declaration, effective_from)
		 VALUES ($1, $2, 'CDAC', 'active', 'fixed', 'declared', '2026-01-01')`, emp, e2); err != nil {
		t.Fatalf("shg: %v", err)
	}
	var v1 string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM timesheet WHERE employer_id = $1 AND employee_id = $2 AND supersedes_id IS NULL`,
		emp, e2).Scan(&v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, v2.ID, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE rule_pack_activation SET status = 'IMPLEMENTABLE' WHERE employer_id = $1`, emp); err != nil {
		t.Fatalf("activate: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !r.Ready {
		t.Fatalf("verdict=%s blocked=%+v missing=%+v conflicts=%+v", r.Verdict, r.Blocked, r.MissingInfo, r.TermConflicts)
	}
	if len(r.Included) != 2 {
		t.Fatalf("included=%v", r.Included)
	}
}
