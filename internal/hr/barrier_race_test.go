package hr

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// Amendment A barrier proofs for HR mutations vs approval. Same three
// live scenarios as the attendance barrier tests (see
// internal/attendance/barrier_race_test.go): mutation holds and
// approval provably blocks; approval holds and the service mutation
// provably blocks; divergence (mutation starts first, commits
// second) is still detected. Every scenario uses its own employee.

func racePeriod(t *testing.T, pool *pgxpool.Pool, emp, code string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, $2, '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id::text`, emp, code).Scan(&id)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	// pay_code is global: ensure the REG code the race sheets cite.
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment)
		 VALUES ('REG', 'Regular', 'earning', 'OW') ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay code: %v", err)
	}
	return id
}

func raceDraftSheet(t *testing.T, pool *pgxpool.Pool, emp, ee, period string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
		 VALUES ($1, $2, $3, 'draft') RETURNING id::text`, emp, ee, period).Scan(&id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	// One payable line: readiness skips lineless sheets before ever
	// evaluating conflicts, which would make no-conflict assertions
	// vacuous.
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, id, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	return id
}

// raceSHG adds the August SHG instruction readiness requires before
// evaluating conflicts. Terms already exist per test (closed-first
// or open); lines come from raceDraftSheet.
func raceSHG(t *testing.T, pool *pgxpool.Pool, emp, no string) {
	t.Helper()
	amt := "fixed"
	if _, err := SetSHG(ctx(t), pool, emp, no, SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "race", EffectiveFrom: "2026-08-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
}

func raceEmployee(t *testing.T, pool *pgxpool.Pool, emp, no string) string {
	t.Helper()
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	return e.ID
}

// secondTermsSQL inserts a successor terms row directly, mirroring
// the service's column list. The caller must hold a non-overlapping
// predecessor (closed first terms) so the exclusion holds.
func secondTermsSQL(emp, ee, from string) string {
	return `INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
	  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
	  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
	  place_of_work, salary_period)
	 VALUES ('` + emp + `', '` + ee + `', '` + from + `', 'Agent', 'support', 'workman', true, 'test', 2600,
	  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`
}

func TestTermsVsApprovalBarrier(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	period := racePeriod(t, pool, emp, uniq(t, "PP"))

	closedFirst := func(t *testing.T, ee string) {
		t.Helper()
		in := termsInput("Sunday")
		to := "2026-05-31"
		in.EffectiveTo = &to
		if _, err := SetTerms(ctx(t), pool, emp, ee, in, "test-op"); err != nil {
			t.Fatalf("first terms: %v", err)
		}
	}

	// Scenario 1: terms change holds pre-commit; service approval
	// blocks, then commits with the terms ordered first.
	noT1 := uniq(t, "T1")
	ee1 := raceEmployee(t, pool, emp, noT1)
	closedFirst(t, ee1)
	raceSHG(t, pool, emp, noT1)
	sheet1 := raceDraftSheet(t, pool, emp, ee1, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet1, "test-op"); err != nil {
		t.Fatalf("submit1: %v", err)
	}
	hold := dbutil.AppConn(t, dsn)
	htx, err := hold.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	if _, err := htx.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee1); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if _, err := htx.Exec(ctx(t), secondTermsSQL(emp, ee1, "2026-06-01")); err != nil {
		t.Fatalf("hold terms: %v", err)
	}
	wp, pids := dbutil.WaiterPool(t, dsn)
	done := make(chan error, 1)
	go func() {
		_, err := timesheet.Approve(ctx(t), wp, emp, sheet1, "test-op")
		done <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids, done)
	if err := htx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
	dbutil.AwaitDone(t, done, "approval after terms commit")
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, c := range r.TermConflicts {
		t.Fatalf("conflicts=%+v want none: terms committed first", c)
	}

	// Scenario 2: approval holds pre-commit; service SetTerms
	// (future, non-intersecting) blocks, then succeeds.
	noT2 := uniq(t, "T2")
	ee2 := raceEmployee(t, pool, emp, noT2)
	closedFirst(t, ee2)
	raceSHG(t, pool, emp, noT2)
	sheet2 := raceDraftSheet(t, pool, emp, ee2, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet2, "test-op"); err != nil {
		t.Fatalf("submit2: %v", err)
	}
	hold2 := dbutil.AppConn(t, dsn)
	htx2, err := hold2.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold2: %v", err)
	}
	if _, err := htx2.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee2); err != nil {
		t.Fatalf("hold2 lock: %v", err)
	}
	if _, err := htx2.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now() WHERE id = $1`, sheet2); err != nil {
		t.Fatalf("hold2 approve: %v", err)
	}
	wp2, pids2 := dbutil.WaiterPool(t, dsn)
	done2 := make(chan error, 1)
	go func() {
		in := termsInput("Sunday")
		in.EffectiveFrom = "2027-01-01"
		_, err := SetTerms(ctx(t), wp2, emp, ee2, in, "test-op")
		done2 <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids2, done2)
	if err := htx2.Rollback(ctx(t)); err != nil {
		t.Fatalf("rollback hold2: %v", err)
	}
	dbutil.AwaitDone(t, done2, "set terms after approval rollback")
	terms, err := ListTerms(ctx(t), pool, emp, ee2)
	if err != nil || len(terms) != 2 {
		t.Fatalf("terms=%d err=%v want first plus successor", len(terms), err)
	}

	// Scenario 3: terms transaction starts before approval but
	// commits after it. created_at predates approved_at; readiness
	// must still flag via basis_seq. (The service would refuse this
	// range outright via its retro block; the raw path proves the
	// database-level detection underneath.)
	noT3 := uniq(t, "T3")
	ee3 := raceEmployee(t, pool, emp, noT3)
	closedFirst(t, ee3)
	raceSHG(t, pool, emp, noT3)
	late := dbutil.AppConn(t, dsn)
	ltx, err := late.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin late: %v", err)
	}
	sheet3 := raceDraftSheet(t, pool, emp, ee3, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet3, "test-op"); err != nil {
		t.Fatalf("submit3: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet3, "test-op"); err != nil {
		t.Fatalf("approve3: %v", err)
	}
	if _, err := ltx.Exec(ctx(t), secondTermsSQL(emp, ee3, "2026-06-01")); err != nil {
		t.Fatalf("late terms: %v", err)
	}
	var diverged bool
	if err := ltx.QueryRow(ctx(t),
		`SELECT max(t.created_at) < (SELECT approved_at FROM timesheet WHERE id = $1)
		 FROM employment_terms t WHERE t.employer_id = $2 AND t.employee_id = $3`,
		sheet3, emp, ee3).Scan(&diverged); err != nil || !diverged {
		t.Fatalf("diverged=%v err=%v want terms created_at predating approved_at", diverged, err)
	}
	if err := ltx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit late: %v", err)
	}
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess3: %v", err)
	}
	found := false
	for _, c := range r3.TermConflicts {
		if strings.Contains(c.Reason, "created after approval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts=%+v want the late terms flagged", r3.TermConflicts)
	}
}

func TestCompAddVsApprovalBarrier(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	period := racePeriod(t, pool, emp, uniq(t, "PP"))

	openTerms := func(t *testing.T, ee string) string {
		t.Helper()
		tm, err := SetTerms(ctx(t), pool, emp, ee, termsInput("Sunday"), "test-op")
		if err != nil {
			t.Fatalf("terms: %v", err)
		}
		return tm.ID
	}
	// The controlled write path: the same function the service calls.
	rawComp := func(ee, tm, code string) string {
		return `SELECT add_compensation('` + emp + `'::uuid, '` + ee + `'::uuid, '` + tm + `'::uuid,
		 'allowance', '` + code + `', 150, '2026-08-01'::date, NULL, 'test-op', 'race')`
	}

	// Scenario 1 (D-RACE, compensation wins): the insert holds
	// pre-commit; service approval blocks, then commits with the
	// line ordered first: explicit seq proof plus no conflict.
	noM1 := uniq(t, "M1")
	ee1 := raceEmployee(t, pool, emp, noM1)
	tm1 := openTerms(t, ee1)
	raceSHG(t, pool, emp, noM1)
	sheet1 := raceDraftSheet(t, pool, emp, ee1, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet1, "test-op"); err != nil {
		t.Fatalf("submit1: %v", err)
	}
	hold := dbutil.AppConn(t, dsn)
	htx, err := hold.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	if _, err := htx.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee1); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if _, err := htx.Exec(ctx(t), rawComp(ee1, tm1, "MEAL")); err != nil {
		t.Fatalf("hold comp: %v", err)
	}
	wp, pids := dbutil.WaiterPool(t, dsn)
	done := make(chan error, 1)
	go func() {
		_, err := timesheet.Approve(ctx(t), wp, emp, sheet1, "test-op")
		done <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids, done)
	if err := htx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
	dbutil.AwaitDone(t, done, "approval after comp commit")
	var lineSeq, apSeq int64
	if err := pool.QueryRow(ctx(t),
		`SELECT basis_seq FROM compensation_line WHERE employer_id = $1 AND terms_id = $2`,
		emp, tm1).Scan(&lineSeq); err != nil {
		t.Fatalf("line seq: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT basis_seq FROM timesheet WHERE id = $1`, sheet1).Scan(&apSeq); err != nil {
		t.Fatalf("sheet seq: %v", err)
	}
	if lineSeq == 0 || apSeq == 0 || lineSeq >= apSeq {
		t.Fatalf("lineSeq=%d apSeq=%d want the line ordered first", lineSeq, apSeq)
	}
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, c := range r.CompConflicts {
		t.Fatalf("conflicts=%+v want none: line committed first", c)
	}

	// Scenario 2: approval holds pre-commit; service
	// AddCompensation blocks, then succeeds after rollback.
	noM2 := uniq(t, "M2")
	ee2 := raceEmployee(t, pool, emp, noM2)
	tm2 := openTerms(t, ee2)
	raceSHG(t, pool, emp, noM2)
	sheet2 := raceDraftSheet(t, pool, emp, ee2, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet2, "test-op"); err != nil {
		t.Fatalf("submit2: %v", err)
	}
	hold2 := dbutil.AppConn(t, dsn)
	htx2, err := hold2.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold2: %v", err)
	}
	if _, err := htx2.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee2); err != nil {
		t.Fatalf("hold2 lock: %v", err)
	}
	if _, err := htx2.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now() WHERE id = $1`, sheet2); err != nil {
		t.Fatalf("hold2 approve: %v", err)
	}
	wp2, pids2 := dbutil.WaiterPool(t, dsn)
	done2 := make(chan error, 1)
	go func() {
		_, err := AddCompensation(ctx(t), wp2, emp, tm2, CompensationInput{
			Kind: "allowance", Code: "MEAL", AmountMonthly: "150", EffectiveFrom: "2026-08-01",
			Reason: "race",
		}, "test-op")
		done2 <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids2, done2)
	if err := htx2.Rollback(ctx(t)); err != nil {
		t.Fatalf("rollback hold2: %v", err)
	}
	dbutil.AwaitDone(t, done2, "add comp after approval rollback")
	lines, err := ListCompensation(ctx(t), pool, emp, tm2)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines=%d err=%v want the compensation line", len(lines), err)
	}

	// Scenario 3 (D-RACE, approval wins): the insert starts before
	// approval but commits after it. Normalization stamps created_at
	// at transaction start — predating approved_at — so the wall
	// clock alone would miss this; basis_seq must catch it.
	noM3 := uniq(t, "M3")
	ee3 := raceEmployee(t, pool, emp, noM3)
	tm3 := openTerms(t, ee3)
	raceSHG(t, pool, emp, noM3)
	late := dbutil.AppConn(t, dsn)
	ltx, err := late.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin late: %v", err)
	}
	sheet3 := raceDraftSheet(t, pool, emp, ee3, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet3, "test-op"); err != nil {
		t.Fatalf("submit3: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet3, "test-op"); err != nil {
		t.Fatalf("approve3: %v", err)
	}
	if _, err := ltx.Exec(ctx(t), rawComp(ee3, tm3, "MEAL")); err != nil {
		t.Fatalf("late comp: %v", err)
	}
	var diverged bool
	if err := ltx.QueryRow(ctx(t),
		`SELECT max(c.created_at) < (SELECT approved_at FROM timesheet WHERE id = $1)
		 FROM compensation_line c WHERE c.employer_id = $2 AND c.terms_id = $3`,
		sheet3, emp, tm3).Scan(&diverged); err != nil || !diverged {
		t.Fatalf("diverged=%v err=%v want line created_at predating approved_at", diverged, err)
	}
	if err := ltx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit late: %v", err)
	}
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess3: %v", err)
	}
	found := false
	for _, c := range r3.CompConflicts {
		if strings.Contains(c.Reason, "created after approval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts=%+v want the late line flagged", r3.CompConflicts)
	}
}

func TestStatusVsApprovalBarrier(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	period := racePeriod(t, pool, emp, uniq(t, "PP"))

	statusOf := func(t *testing.T, ee string) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx(t),
			`SELECT status FROM employee WHERE employer_id = $1 AND id = $2`,
			emp, ee).Scan(&s); err != nil {
			t.Fatalf("status: %v", err)
		}
		return s
	}

	// Scenario 1: status change holds pre-commit; service approval blocks.
	ee1 := raceEmployee(t, pool, emp, uniq(t, "S1"))
	sheet1 := raceDraftSheet(t, pool, emp, ee1, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet1, "test-op"); err != nil {
		t.Fatalf("submit1: %v", err)
	}
	hold := dbutil.AppConn(t, dsn)
	htx, err := hold.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	if _, err := htx.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'suspended', '2027-01-01', 'race', 'test')`,
		emp, ee1); err != nil {
		t.Fatalf("hold status: %v", err)
	}
	wp, pids := dbutil.WaiterPool(t, dsn)
	done := make(chan error, 1)
	go func() {
		_, err := timesheet.Approve(ctx(t), wp, emp, sheet1, "test-op")
		done <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids, done)
	if err := htx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
	dbutil.AwaitDone(t, done, "approval after status commit")
	if got := statusOf(t, ee1); got != "suspended" {
		t.Fatalf("status=%s want suspended", got)
	}

	// Scenario 2: approval holds pre-commit; service SetStatus blocks.
	ee2 := raceEmployee(t, pool, emp, uniq(t, "S2"))
	sheet2 := raceDraftSheet(t, pool, emp, ee2, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet2, "test-op"); err != nil {
		t.Fatalf("submit2: %v", err)
	}
	hold2 := dbutil.AppConn(t, dsn)
	htx2, err := hold2.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold2: %v", err)
	}
	if _, err := htx2.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee2); err != nil {
		t.Fatalf("hold2 lock: %v", err)
	}
	if _, err := htx2.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now() WHERE id = $1`, sheet2); err != nil {
		t.Fatalf("hold2 approve: %v", err)
	}
	wp2, pids2 := dbutil.WaiterPool(t, dsn)
	done2 := make(chan error, 1)
	go func() {
		_, err := SetStatus(ctx(t), wp2, emp, ee2, "suspended", "2027-01-01", "race", "test-op")
		done2 <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids2, done2)
	if err := htx2.Rollback(ctx(t)); err != nil {
		t.Fatalf("rollback hold2: %v", err)
	}
	dbutil.AwaitDone(t, done2, "set status after approval rollback")
	if got := statusOf(t, ee2); got != "suspended" {
		t.Fatalf("status=%s want suspended", got)
	}
}

func TestSHGVsApprovalBarrier(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	period := racePeriod(t, pool, emp, uniq(t, "PP"))

	// Mirrors the service SetSHG INSERT exactly.
	rawSHG := func(ee, fund string) string {
		return `INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, amount_or_basis,
		  source_declaration, effective_from, effective_to)
		 VALUES (gen_random_uuid(), '` + emp + `', '` + ee + `', '` + fund + `', 'active', 'fixed', 'race', '2026-08-01', NULL)`
	}

	// Scenario 1: SHG write holds pre-commit; approval blocks.
	ee1 := raceEmployee(t, pool, emp, uniq(t, "G1"))
	sheet1 := raceDraftSheet(t, pool, emp, ee1, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet1, "test-op"); err != nil {
		t.Fatalf("submit1: %v", err)
	}
	hold := dbutil.AppConn(t, dsn)
	htx, err := hold.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	if _, err := htx.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee1); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if _, err := htx.Exec(ctx(t), rawSHG(ee1, "CDAC")); err != nil {
		t.Fatalf("hold shg: %v", err)
	}
	wp, pids := dbutil.WaiterPool(t, dsn)
	done := make(chan error, 1)
	go func() {
		_, err := timesheet.Approve(ctx(t), wp, emp, sheet1, "test-op")
		done <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids, done)
	if err := htx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
	dbutil.AwaitDone(t, done, "approval after SHG commit")
	listed, err := ListSHG(ctx(t), pool, emp, ee1)
	if err != nil || len(listed) != 1 {
		t.Fatalf("shg=%d err=%v want the instruction", len(listed), err)
	}

	// Scenario 2: approval holds pre-commit; service SetSHG blocks.
	ee2 := raceEmployee(t, pool, emp, uniq(t, "G2"))
	sheet2 := raceDraftSheet(t, pool, emp, ee2, period)
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet2, "test-op"); err != nil {
		t.Fatalf("submit2: %v", err)
	}
	hold2 := dbutil.AppConn(t, dsn)
	htx2, err := hold2.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin hold2: %v", err)
	}
	if _, err := htx2.Exec(ctx(t), `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, ee2); err != nil {
		t.Fatalf("hold2 lock: %v", err)
	}
	if _, err := htx2.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now() WHERE id = $1`, sheet2); err != nil {
		t.Fatalf("hold2 approve: %v", err)
	}
	wp2, pids2 := dbutil.WaiterPool(t, dsn)
	done2 := make(chan error, 1)
	go func() {
		amt := "fixed"
		_, err := SetSHG(ctx(t), wp2, emp, ee2, SHGInput{
			Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
			SourceDeclaration: "race", EffectiveFrom: "2026-08-01",
		}, "test-op")
		done2 <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids2, done2)
	if err := htx2.Rollback(ctx(t)); err != nil {
		t.Fatalf("rollback hold2: %v", err)
	}
	dbutil.AwaitDone(t, done2, "set SHG after approval rollback")
	listed2, err := ListSHG(ctx(t), pool, emp, ee2)
	if err != nil || len(listed2) != 1 {
		t.Fatalf("shg=%d err=%v want the instruction", len(listed2), err)
	}
}
