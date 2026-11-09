package payroll

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// snapRace holds one capture-vs-writer orchestration: the capture runs
// on a waiter pool while the test holds every required pack lock, so
// the capture provably holds the population and employee locks while
// the writer attempts its change.
type snapRace struct {
	emp, period, run string
	app              *pgxpool.Pool
	admin            *pgxpool.Pool
	hold             *pgx.Conn
	holdTx           pgx.Tx
	capPool          *pgxpool.Pool
	capPids          []int32
	done             chan error
	snapID           string
}

func beginSnapRace(t *testing.T) *snapRace {
	t.Helper()
	return beginSnapRaceSpan(t, "2026-08-01", "2026-08-31")
}

func beginSnapRaceSpan(t *testing.T, start, end string) *snapRace {
	t.Helper()
	app, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetupSpan(t, app, start, end)
	period := mustPeriod(t, app, emp)
	dsn := dbutil.RaceDSN(t, app)
	// Hold every required pack lock: the capture blocks after taking
	// the population and employee locks, provably mid-flight.
	hold := dbutil.AppConn(t, dsn)
	htx, err := hold.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	for _, p := range BasePacks {
		if _, err := htx.Exec(ctx(t), `SELECT acquire_rule_pack_lock($1::uuid, $2)`, emp, p); err != nil {
			t.Fatalf("hold pack %s: %v", p, err)
		}
	}
	capPool, capPids := dbutil.WaiterPool(t, dsn)
	r := &snapRace{emp: emp, period: period, run: run, app: app, admin: admin,
		hold: hold, holdTx: htx, capPool: capPool, capPids: capPids, done: make(chan error, 1)}
	return r
}

func (r *snapRace) startCapture(t *testing.T) {
	t.Helper()
	go func() {
		snap, err := CaptureSnapshot(ctx(t), r.capPool, r.emp, r.run, "test-op")
		if err == nil {
			r.snapID = snap.ID
		}
		r.done <- err
	}()
	dbutil.AwaitAdvisoryWait(t, r.app, r.capPids, r.done)
}

func (r *snapRace) release(t *testing.T) {
	t.Helper()
	if err := r.holdTx.Commit(ctx(t)); err != nil {
		t.Fatalf("release hold: %v", err)
	}
	dbutil.AwaitDone(t, r.done, "capture after release")
}

// timeoutPool returns an app-role pool whose sessions fail fast on
// lock waits, so a blocked writer proves serialization instead of
// hanging the test.
func timeoutPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2s"
	p, err := pgxpool.NewWithConfig(ctx(t), cfg)
	if err != nil {
		t.Fatalf("timeout pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func (r *snapRace) drift(t *testing.T) []string {
	t.Helper()
	rep, err := DetectDrift(ctx(t), r.app, r.emp, r.snapID)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !rep.Drifted {
		t.Fatal("expected drift after the racing writer committed")
	}
	return rep.Reasons
}

func TestCaptureVsEmployeeCreation(t *testing.T) {
	r := beginSnapRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.startCapture(t)
	// New employment cannot enter the expected population mid-capture.
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := hr.CreateEmployee(ctx(t), wp, r.emp, hr.EmployeeInput{
		EmployeeNo: "RACE1", LegalName: "Racer", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err == nil {
		t.Fatal("employee creation slipped past the population barrier")
	}
	r.release(t)
	detail, err := GetSnapshot(ctx(t), r.app, r.snapID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Employees) != 1 {
		t.Fatalf("employees=%d want exactly the pre-capture population", len(detail.Employees))
	}
	if _, err := hr.CreateEmployee(ctx(t), r.app, r.emp, hr.EmployeeInput{
		EmployeeNo: "RACE1", LegalName: "Racer", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// The retry has terms? No: population counts terms-holders and
	// approved-sheet holders. Give the racer terms so the population
	// genuinely grows.
	var racer string
	if err := r.app.QueryRow(ctx(t), `SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'RACE1'`,
		r.emp).Scan(&racer); err != nil {
		t.Fatalf("racer: %v", err)
	}
	if _, err := hr.SetTerms(ctx(t), r.app, r.emp, racer, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err != nil {
		t.Fatalf("racer terms: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "population") {
		t.Fatalf("reasons=%v want population drift", reasons)
	}
}

func TestCaptureVsTermChange(t *testing.T) {
	r := beginSnapRace(t)
	eID, termsID := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	// Cap the allowance inside the first terms span, so the later
	// terms change is a pure terms change, not a comp-outlives error.
	comps, err := hr.ListCompensation(ctx(t), r.app, r.emp, termsID)
	if err != nil || len(comps) != 1 {
		t.Fatalf("comps=%+v err=%v", comps, err)
	}
	end := "2026-08-14"
	if _, err := hr.CorrectCompensation(ctx(t), r.app, r.emp, comps[0].ID, hr.CorrectionInput{
		AmountMonthly: "100.00", EffectiveFrom: "2026-01-01", EffectiveTo: &end, Reason: "cap",
	}, "test-op"); err != nil {
		t.Fatalf("cap: %v", err)
	}
	r.startCapture(t)
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := hr.SetTerms(ctx(t), wp, r.emp, eID, hr.TermsInput{
		EffectiveFrom: "2026-08-15", JobTitle: "Senior Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2800", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err == nil {
		t.Fatal("term change slipped past the barrier")
	}
	r.release(t)
	// The retry lands through direct SQL: the service's
	// approved-period guard would refuse a mid-period change against
	// the approved sheet, which is orthogonal to the barrier and
	// drift being proven. The close function and the INSERT trigger
	// both take the population lock, so these are honest barrier
	// participants.
	if _, err := r.admin.Exec(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-08-15'::date, 'op')`,
		r.emp, eID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := r.admin.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-08-15', 'Senior Agent', 'support', 'workman', true, 'test', 2800,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		r.emp, eID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "terms") {
		t.Fatalf("reasons=%v want terms drift", reasons)
	}
}

func TestCaptureVsTermination(t *testing.T) {
	// The race runs on a span around tomorrow: the termination must
	// be effective after today's seeded status row yet inside the
	// period, or it cannot drift the captured status section.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	first := time.Date(tomorrow.Year(), tomorrow.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	r := beginSnapRaceSpan(t, first.Format("2006-01-02"), last.Format("2006-01-02"))
	eID, _ := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	eff := tomorrow.Format("2006-01-02")
	r.startCapture(t)
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := hr.SetStatus(ctx(t), wp, r.emp, eID, "terminated", eff, "race", "test-op"); err == nil {
		t.Fatal("termination slipped past the barrier")
	}
	r.release(t)
	if _, err := hr.SetStatus(ctx(t), r.app, r.emp, eID, "terminated", eff, "race", "test-op"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "status") {
		t.Fatalf("reasons=%v want status drift", reasons)
	}
}

func TestCaptureVsCompensation(t *testing.T) {
	r := beginSnapRace(t)
	_, termsID := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.startCapture(t)
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := hr.AddCompensation(ctx(t), wp, r.emp, termsID, hr.CompensationInput{
		Kind: "allowance", Code: "RACE", AmountMonthly: "50.00",
		EffectiveFrom: "2026-01-01", Reason: "race",
	}, "test-op"); err == nil {
		t.Fatal("compensation insert slipped past the barrier")
	}
	r.release(t)
	if _, err := hr.AddCompensation(ctx(t), r.app, r.emp, termsID, hr.CompensationInput{
		Kind: "allowance", Code: "RACE", AmountMonthly: "50.00",
		EffectiveFrom: "2026-01-01", Reason: "race",
	}, "test-op"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "compensation") {
		t.Fatalf("reasons=%v want compensation drift", reasons)
	}
}

func TestCaptureVsApproval(t *testing.T) {
	r := beginSnapRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	// Second employee with a draft sheet: the racing approval must wait.
	no := fmt.Sprintf("SN%06d", time.Now().UnixNano()%1000000)
	e, err := hr.CreateEmployee(ctx(t), r.app, r.emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := hr.SetTerms(ctx(t), r.app, r.emp, e.ID, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err != nil {
		t.Fatalf("terms: %v", err)
	}
	var sheet string
	if err := r.app.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
		 VALUES ($1, $2, $3, 'draft') RETURNING id`, r.emp, e.ID, r.period).Scan(&sheet); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if _, err := r.app.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, r.emp, sheet, r.period); err != nil {
		t.Fatalf("line: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), r.app, r.emp, sheet, "test"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	implementPacks(t, r.admin, r.emp)
	r.startCapture(t)
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := timesheet.Approve(ctx(t), wp, r.emp, sheet, "test-op"); err == nil {
		t.Fatal("approval slipped past the barrier")
	}
	r.release(t)
	detail, err := GetSnapshot(ctx(t), r.app, r.snapID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, se := range detail.Employees {
		if se.EmployeeID == e.ID && se.Decision != "missing-timesheet" {
			t.Fatalf("draft employee decision=%s want missing-timesheet", se.Decision)
		}
	}
	if _, err := timesheet.Approve(ctx(t), r.app, r.emp, sheet, "test-op"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "timesheet") {
		t.Fatalf("reasons=%v want timesheet drift", reasons)
	}
}

func TestCaptureVsAttendanceEvidence(t *testing.T) {
	r := beginSnapRace(t)
	eID, _ := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.startCapture(t)
	// A racing clock event lands through the barrier trigger, which
	// takes the employee lock the capture holds.
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := wp.Exec(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in', 'race', 'evt-race-1')`,
		r.emp, eID); err == nil {
		t.Fatal("clock event slipped past the barrier")
	}
	r.release(t)
	if _, err := r.app.Exec(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in', 'race', 'evt-race-1')`,
		r.emp, eID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "attendance") {
		t.Fatalf("reasons=%v want attendance drift", reasons)
	}
}

func TestCaptureVsSHGChange(t *testing.T) {
	r := beginSnapRace(t)
	eID, _ := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	list, err := hr.ListSHG(ctx(t), r.app, r.emp, eID)
	if err != nil || len(list) != 1 {
		t.Fatalf("shg=%+v err=%v", list, err)
	}
	r.startCapture(t)
	wp := timeoutPool(t, dbutil.RaceDSN(t, r.app))
	if _, err := hr.CorrectSHG(ctx(t), wp, r.emp, list[0].ID, hr.SHGCorrectInput{
		Status: "active", SourceDeclaration: "race v2", EffectiveFrom: "2026-01-01",
	}, "test-op"); err == nil {
		t.Fatal("SHG correction slipped past the barrier")
	}
	r.release(t)
	if _, err := hr.CorrectSHG(ctx(t), r.app, r.emp, list[0].ID, hr.SHGCorrectInput{
		Status: "active", SourceDeclaration: "race v2", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "shg") {
		t.Fatalf("reasons=%v want shg drift", reasons)
	}
}

func TestCaptureVsPackSupersession(t *testing.T) {
	r := beginSnapRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	// Pin a reviewed version for a required pack so the racing writer
	// can genuinely supersede it after the capture.
	if _, err := r.admin.Exec(ctx(t),
		`INSERT INTO rule_pack (pack, jurisdiction, scope, reviewed_version, manifest_digest, spec_version, verification_status)
		 VALUES ('SG-SDL', 'SG', 'levies', 'v1', 'sha256:aaa', 'spec-1', 'GOLDENS_VERIFIED')
		 ON CONFLICT (pack) DO UPDATE SET reviewed_version = 'v1', manifest_digest = 'sha256:aaa',
		 spec_version = 'spec-1', verification_status = 'GOLDENS_VERIFIED'`); err != nil {
		t.Fatalf("pin: %v", err)
	}
	r.startCapture(t)
	// The racing activation needs the pack lock the capture holds.
	holdTx, err := r.admin.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer holdTx.Rollback(ctx(t))
	if _, err := holdTx.Exec(ctx(t), `SET LOCAL lock_timeout = '2s'`); err != nil {
		t.Fatalf("timeout: %v", err)
	}
	_, err = holdTx.Exec(ctx(t),
		`SELECT activate_rule_pack($1::uuid, 'SG-SDL', 'v1', 'SG', 'levies', '2026-01-01'::date, NULL,
		  'sha256:aaa', '{"sources":[]}'::jsonb, 'spec-1', 'r', 'GOLDENS_VERIFIED', 'op', 'race')`,
		r.emp)
	if err == nil {
		t.Fatal("pack activation slipped past the barrier")
	}
	// The behavioral proof: the activation did not land.
	var n int
	if err := r.app.QueryRow(ctx(t),
		`SELECT count(*) FROM rule_pack_activation_history WHERE employer_id = $1`, r.emp).Scan(&n); err != nil || n != 0 {
		t.Fatalf("history rows=%d err=%v want the activation blocked", n, err)
	}
	holdTx.Rollback(ctx(t))
	r.release(t)
	var id string
	if err := r.admin.QueryRow(ctx(t),
		`SELECT activate_rule_pack($1::uuid, 'SG-SDL', 'v1', 'SG', 'levies', '2026-01-01'::date, NULL,
		  'sha256:aaa', '{"sources":[]}'::jsonb, 'spec-1', 'r', 'GOLDENS_VERIFIED', 'op', 'race')::text`,
		r.emp).Scan(&id); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reasons := r.drift(t); !strings.Contains(strings.Join(reasons, "\n"), "pack") {
		t.Fatalf("reasons=%v want pack drift", reasons)
	}
}
