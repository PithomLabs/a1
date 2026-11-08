package hr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
	"github.com/jackc/pgx/v5"
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

func uniq(t *testing.T, s string) string {
	t.Helper()
	base := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	return fmt.Sprintf("%s_%d_%s", base, time.Now().UnixNano(), s)
}

func mkEmployer(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&id)
	if err != nil {
		t.Fatalf("mkEmployer: %v", err)
	}
	return id
}

func TestCreateAndGetEmployee(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "E001", LegalName: "Test Person", Address: "addr",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.Status != StatusActive {
		t.Fatalf("status = %s, want active", e.Status)
	}
	got, err := GetEmployee(ctx(t), pool, emp, "E001")
	if err != nil {
		t.Fatalf("get by no: %v", err)
	}
	if got.ID != e.ID {
		t.Fatal("get by no returned wrong employee")
	}
	if _, err := GetEmployee(ctx(t), pool, emp, e.ID); err != nil {
		t.Fatalf("get by id: %v", err)
	}
}

func TestDuplicateEmployeeNo(t *testing.T) {
	pool := testPool(t)
	a := mkEmployer(t, pool, uniq(t, "A"))
	b := mkEmployer(t, pool, uniq(t, "B"))
	in := EmployeeInput{EmployeeNo: "DUP", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG"}
	if _, err := CreateEmployee(ctx(t), pool, a, in, "test"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := CreateEmployee(ctx(t), pool, a, in, "test"); err == nil {
		t.Fatal("expected conflict for duplicate employee_no")
	}
	if _, err := CreateEmployee(ctx(t), pool, b, in, "test"); err != nil {
		t.Fatalf("same employee_no in another employer must succeed: %v", err)
	}
	if _, err := GetEmployee(ctx(t), pool, b, "DUP"); err != nil {
		t.Fatalf("cross-employer scoping broken: %v", err)
	}
	other, _ := GetEmployee(ctx(t), pool, a, "DUP")
	if _, err := GetEmployee(ctx(t), pool, b, other.ID); err == nil {
		t.Fatal("expected not_found for other employer's employee ID")
	}
}

func TestSetStatus(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "S1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
		StatusEffectiveDate: "2026-07-01",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := SetStatus(ctx(t), pool, emp, e.ID, StatusSuspended, "2026-08-01", "test suspension", "test-op")
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if updated.Status != StatusSuspended {
		t.Fatalf("status = %s", updated.Status)
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM employee_status_history WHERE employer_id = $1 AND employee_id = $2`,
		emp, e.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("history rows = %d, err = %v", n, err)
	}
	var closed string
	if err := pool.QueryRow(ctx(t),
		`SELECT effective_to::text FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 AND status = 'active'`,
		emp, e.ID).Scan(&closed); err != nil || closed != "2026-07-31" {
		t.Fatalf("prior close = %q, err = %v", closed, err)
	}
	if _, err := SetStatus(ctx(t), pool, emp, e.ID, StatusActive, "2026-08-02", "", "test-op"); err == nil {
		t.Fatal("expected validation error for missing reason")
	}
	// Transition matrix: suspended can terminate; terminated is final.
	if _, err := SetStatus(ctx(t), pool, emp, e.ID, StatusTerminated, "2026-08-03", "fired", "test-op"); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if _, err := SetStatus(ctx(t), pool, emp, e.ID, StatusActive, "2026-08-04", "rehire", "test-op"); err == nil {
		t.Fatal("expected rejection of terminated -> active")
	}
	if _, err := SetStatus(ctx(t), pool, emp, e.ID, StatusTerminated, "2026-08-05", "again", "test-op"); err == nil {
		t.Fatal("expected rejection of same-status change")
	}
	var audits int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'employee.status'`,
		emp).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows = %d, err = %v", audits, err)
	}
}

func TestTermsOverlap(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "T1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	in := TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}
	first, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op")
	if err != nil {
		t.Fatalf("set terms: %v", err)
	}
	if first.MonthlyBasic != "2600" {
		t.Fatalf("monthly_basic = %q want the exact input", first.MonthlyBasic)
	}
	// Successor closes the predecessor: two rows, no gap, no overlap.
	in.EffectiveFrom = "2026-06-01"
	if _, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op"); err != nil {
		t.Fatalf("successor terms: %v", err)
	}
	terms, err := ListTerms(ctx(t), pool, emp, e.ID)
	if err != nil || len(terms) != 2 {
		t.Fatalf("list terms = %d, err = %v", len(terms), err)
	}
	// Backdated rows are rejected before they can overlap closed history.
	in.EffectiveFrom = "2026-03-01"
	end := "2026-04-01"
	in.EffectiveTo = &end
	if _, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op"); err == nil {
		t.Fatal("expected conflict for overlapping terms")
	}
	// Money must be exact: floats and 3dp are rejected at the boundary.
	in.EffectiveFrom = "2027-01-01"
	in.EffectiveTo = nil
	in.MonthlyBasic = "2600.005"
	if _, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op"); err == nil {
		t.Fatal("expected validation error for 3dp money")
	}
}

func TestCompensationExact(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "C1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op")
	if err != nil {
		t.Fatalf("set terms: %v", err)
	}
	if _, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "199.99", EffectiveFrom: "2026-01-01",
		Reason: "shift work",
	}, "test-op"); err != nil {
		t.Fatalf("add compensation: %v", err)
	}
	if _, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
		Kind: "allowance", Code: "BAD", AmountMonthly: "199.999", EffectiveFrom: "2026-01-01",
		Reason: "shift work",
	}, "test-op"); err == nil {
		t.Fatal("expected validation error for 3dp money")
	}
	lines, err := ListCompensation(ctx(t), pool, emp, tm.ID)
	if err != nil || len(lines) != 1 || lines[0].AmountMonthly != "199.99" {
		t.Fatalf("compensation=%+v err=%v", lines, err)
	}
}

type auditRow struct {
	actor, service, action, entity string
	entityID                       *string
	before, after                  string
}

func lastAudit(t *testing.T, pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, emp, action string) auditRow {
	t.Helper()
	var r auditRow
	var before, after *string
	if err := pool.QueryRow(ctx(t),
		`SELECT actor, service, action, entity, entity_id::text, before_state::text, after_state::text
		 FROM audit_event WHERE employer_id = $1 AND action = $2
		 ORDER BY occurred_at DESC LIMIT 1`, emp, action).Scan(
		&r.actor, &r.service, &r.action, &r.entity, &r.entityID, &before, &after); err != nil {
		t.Fatalf("audit %s: %v", action, err)
	}
	if before != nil {
		r.before = *before
	}
	if after != nil {
		r.after = *after
	}
	return r
}

// Employee creation persists its operator attribution atomically with
// the employee and its initial status history.
func TestEmployeeCreateAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "A1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "op-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	r := lastAudit(t, pool, emp, "employee.create")
	if r.actor != "op-1" || r.service != "hr" || r.entity != "employee" ||
		r.entityID == nil || *r.entityID != e.ID {
		t.Fatalf("audit=%+v", r)
	}
	if !strings.Contains(r.after, "A1") {
		t.Fatalf("after=%s want the employee reference", r.after)
	}
}

func TestTermsAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "A2", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "op-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "op-2")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	r := lastAudit(t, pool, emp, "terms.set")
	if r.actor != "op-2" || r.entity != "employment_terms" ||
		r.entityID == nil || *r.entityID != tm.ID {
		t.Fatalf("audit=%+v", r)
	}
	if !strings.Contains(r.after, "2600") {
		t.Fatalf("after=%s want the terms content", r.after)
	}
}

func TestStatusAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "A3", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "op-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := SetStatus(ctx(t), pool, emp, e.ID, StatusSuspended, "2026-02-01", "probe", "op-3"); err != nil {
		t.Fatalf("status: %v", err)
	}
	r := lastAudit(t, pool, emp, "employee.status")
	if r.actor != "op-3" || r.entity != "employee" ||
		r.entityID == nil || *r.entityID != e.ID {
		t.Fatalf("audit=%+v", r)
	}
	if !strings.Contains(r.before, "active") || !strings.Contains(r.after, "suspended") {
		t.Fatalf("before=%s after=%s want the transition", r.before, r.after)
	}
}

// The SHG record is a declaration, not a verified deduction: the audit
// row pins the source declaration the decision is based on. (A verified
// flag does not exist in this model; SHG lifecycle/verify is out of
// scope per prompt11 section 8.)
func TestSHGAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "S"))
	if _, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "S1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err != nil {
		t.Fatalf("employee: %v", err)
	}
	amt := "fixed"
	s, err := SetSHG(ctx(t), pool, emp, "S1", SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "employee form 2026-07", EffectiveFrom: "2026-08-01",
	}, "op-4")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	r := lastAudit(t, pool, emp, "shg.set")
	if r.actor != "op-4" || r.entity != "shg_instruction" ||
		r.entityID == nil || *r.entityID != s.ID {
		t.Fatalf("audit=%+v", r)
	}
	if !strings.Contains(r.after, "employee form 2026-07") {
		t.Fatalf("after=%s want the source declaration", r.after)
	}
}

func termsInput(rest string) TermsInput {
	return TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: rest,
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}
}

// Rest days are canonical full weekday names: anything else is rejected
// at the service boundary, never stored to silently bypass roster checks.
func TestRestDayCanonical(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "R1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i, day := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		in := termsInput(day)
		in.EffectiveFrom = fmt.Sprintf("2026-%02d-01", i+1)
		if _, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op"); err != nil {
			t.Fatalf("rest %s: %v", day, err)
		}
	}
	for _, bad := range []string{"", "Funday", "sun", "SUNDAY", "Sun", "Sunday "} {
		in := termsInput(bad)
		in.EffectiveFrom = "2027-01-01"
		if _, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op"); err == nil {
			t.Fatalf("rest %q must be rejected", bad)
		}
	}
}

// Compensation effective dates must sit inside their parent terms:
// nothing starts before the terms begin or outlives their end.
func TestCompensationTermsContainment(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "K1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	// Close the January terms at end of June with a successor.
	in := termsInput("Sunday")
	in.EffectiveFrom = "2026-07-01"
	if _, err := SetTerms(ctx(t), pool, emp, e.ID, in, "test-op"); err != nil {
		t.Fatalf("successor: %v", err)
	}
	add := func(code, from string, to *string) error {
		_, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
			Kind: "allowance", Code: code, AmountMonthly: "100", EffectiveFrom: from, EffectiveTo: to,
			Reason: "containment probe",
		}, "test-op")
		return err
	}
	if err := add("SHIFT", "2026-03-01", nil); err == nil {
		t.Fatalf("open-ended comp under closed terms must fail")
	}
	end := "2026-09-01"
	if err := add("SHIFT", "2026-03-01", &end); err == nil {
		t.Fatalf("comp outliving its terms must fail")
	}
	if err := add("SHIFT", "2025-12-01", nil); err == nil {
		t.Fatalf("comp predating its terms must fail")
	}
	okEnd := "2026-06-30"
	if err := add("SHIFT", "2026-03-01", &okEnd); err != nil {
		t.Fatalf("contained comp: %v", err)
	}
	if err := add("MEAL", "2026-01-01", &okEnd); err != nil {
		t.Fatalf("boundary-start comp: %v", err)
	}
}

// Compensation history is insert-only for the app role: existing rows
// cannot be rewritten or removed through the runtime role.
func TestCompensationHistoryImmutable(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "K2", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	if _, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100", EffectiveFrom: "2026-01-01",
		Reason: "shift work",
	}, "test-op"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE compensation_line SET amount_monthly = 1 WHERE employer_id = $1`, emp); err == nil {
		t.Fatalf("UPDATE compensation must be revoked from app")
	}
	if _, err := pool.Exec(ctx(t),
		`DELETE FROM compensation_line WHERE employer_id = $1`, emp); err == nil {
		t.Fatalf("DELETE compensation must be revoked from app")
	}
	var amount string
	if err := pool.QueryRow(ctx(t),
		`SELECT amount_monthly::text FROM compensation_line WHERE employer_id = $1`, emp).Scan(&amount); err != nil || amount != "100" {
		t.Fatalf("amount=%s err=%v want untouched", amount, err)
	}
}

// The app role cannot control compensation created_at: backdating the
// timestamp to dodge conflict detection is normalized to now.
func TestCompensationCreatedAtNormalized(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "K3", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, 'allowance', 'SHIFT', 100,
		  '2026-01-01'::date, NULL, 'test-op', 'shift work')`,
		emp, e.ID, tm.ID); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var recent bool
	if err := pool.QueryRow(ctx(t),
		`SELECT created_at > now() - interval '1 minute' FROM compensation_line
		 WHERE employer_id = $1 AND code = 'SHIFT'`, emp).Scan(&recent); err != nil || !recent {
		t.Fatalf("recent=%v err=%v want created_at normalized to now", recent, err)
	}
}

// The database CHECK enforces canonical rest days even for writers
// that bypass the service.
func TestRestDayCheckRejectsBypass(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "R2", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'support', 'workman', true, 'test', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Funday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		emp, e.ID); err == nil {
		t.Fatalf("direct Funday insert must violate the rest-day check")
	}
}

// Two concurrent successors for one open terms row admit exactly one
// winner: the controlled close serializes on the open row and the
// exclusion constraint rejects the overlapping loser.
func TestConcurrentTermsSuccessorSingleWinner(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "R1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "test-op"); err != nil {
		t.Fatalf("terms: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			in := termsInput("Sunday")
			in.EffectiveFrom = "2026-07-01"
			in.MonthlyBasic = "2800"
			_, errs[n] = SetTerms(ctx(t), pool, emp, e.ID, in, "test-op")
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			t.Fatalf("loser error = %v, want a coded error", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly one successor", wins)
	}
	rows, err := pool.Query(ctx(t),
		`SELECT effective_from::text, effective_to::text, monthly_basic::text FROM employment_terms
		 WHERE employer_id = $1 AND employee_id = $2 ORDER BY effective_from`, emp, e.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	defer rows.Close()
	type span struct{ from, to, basic string }
	var spans []span
	for rows.Next() {
		var s span
		var to *string
		if err := rows.Scan(&s.from, &to, &s.basic); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if to != nil {
			s.to = *to
		}
		spans = append(spans, s)
	}
	if len(spans) != 2 || spans[0].from != "2026-01-01" || spans[0].to != "2026-06-30" ||
		spans[1].from != "2026-07-01" || spans[1].to != "" || spans[1].basic != "2800" {
		t.Fatalf("spans = %+v, want contiguous Jan/Jul history", spans)
	}
}

// Two concurrent transitions to the same status admit exactly one
// winner; the employee row and the history stay mutually consistent.
func TestConcurrentStatusTransitionSingleWinner(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "R2", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dates := []string{"2026-03-01", "2026-04-01"}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = SetStatus(ctx(t), pool, emp, e.ID, StatusSuspended, dates[n], "race", "test-op")
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || (ae.Code != apperr.CodeInvalidState && ae.Code != apperr.CodeFrozen) {
			t.Fatalf("loser error = %v, want invalid_state or frozen", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly one transition", wins)
	}
	var status, from string
	if err := pool.QueryRow(ctx(t),
		`SELECT status, status_effective_date::text FROM employee WHERE id = $1`, e.ID).Scan(&status, &from); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if status != StatusSuspended || (from != dates[0] && from != dates[1]) {
		t.Fatalf("employee = %s/%s, want suspended on a raced date", status, from)
	}
	var open string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 AND effective_to IS NULL`, emp, e.ID).Scan(&open); err != nil {
		t.Fatalf("open history: %v", err)
	}
	if open != status {
		t.Fatalf("open history = %s, employee = %s", open, status)
	}
}
