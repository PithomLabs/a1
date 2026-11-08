package attendance

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// Amendment A barrier proofs: an input mutation and the approval
// freezing its interpretation cannot cross unnoticed. Each pair runs
// three live scenarios on separate PostgreSQL connections, all as
// the app role:
//
//  1. Mutation holds pre-commit; the approval waiter provably blocks
//     on the shared lock (pg_locks, not a sleep), then commits after
//     release with the mutation ordered first and no conflict.
//  2. Approval holds pre-commit; the service mutation provably
//     blocks, then succeeds after release.
//  3. Divergence: the mutation transaction starts before approval
//     but commits after it. created_at (transaction-start) predates
//     approved_at — the exact D-RACE signature — and readiness must
//     still flag the conflict via the lock-serialized basis_seq.
//
// Every scenario uses its own employee: locks are per-employee, and
// one employee holds at most one original sheet per period.

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

func raceEmployee(t *testing.T, pool *pgxpool.Pool, emp, no string) string {
	t.Helper()
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	// Terms and SHG covering August: readiness skips employees
	// without them before evaluating conflicts.
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
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, no, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "race", EffectiveFrom: "2026-08-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	return e.ID
}

func raceEmployeeID(t *testing.T, pool *pgxpool.Pool, emp, no string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&id); err != nil {
		t.Fatalf("employee id: %v", err)
	}
	return id
}

func feedSQL(emp, ee, tag string) string {
	return fmt.Sprintf(`INSERT INTO clock_event
	 (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
	 VALUES ('%s', '%s', '2026-08-04T09:00:00+08:00', 'Asia/Singapore', 'in', 'vendor-x', '%s-in'),
	        ('%s', '%s', '2026-08-04T18:00:00+08:00', 'Asia/Singapore', 'out', 'vendor-x', '%s-out')`,
		emp, ee, tag, emp, ee, tag)
}

// raceLiveInterval stores a live clock-source interval citing two real
// events, returning its id. Days are caller-chosen (Aug/Sep 2026).
func raceLiveInterval(t *testing.T, pool *pgxpool.Pool, emp, ee, tag, day string) string {
	t.Helper()
	var inID, outID string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, $3, 'Asia/Singapore', 'in', 'vendor-x', $4) RETURNING id::text`,
		emp, ee, day+"T09:00:00+08:00", tag+"-in").Scan(&inID)
	if err != nil {
		t.Fatalf("in event: %v", err)
	}
	err = pool.QueryRow(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, $3, 'Asia/Singapore', 'out', 'vendor-x', $4) RETURNING id::text`,
		emp, ee, day+"T17:00:00+08:00", tag+"-out").Scan(&outID)
	if err != nil {
		t.Fatalf("out event: %v", err)
	}
	var iv string
	refs := fmt.Sprintf(`{"clock_event_ids": ["%s", "%s"]}`, inID, outID)
	err = pool.QueryRow(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, $3, $4, 'Asia/Singapore', $5::date, 'work', 'clock', $6::jsonb)
		 RETURNING id::text`,
		emp, ee, day+"T09:00:00+08:00", day+"T17:00:00+08:00", day, refs).Scan(&iv)
	if err != nil {
		t.Fatalf("interval: %v", err)
	}
	return iv
}

// raceCorrectionSQL replicates the service Correct writes (group row
// plus one same-bounds segment) as a single statement for held-txn
// scenarios. Regen and audit are service-side concerns; the barrier
// property under test is lock ordering plus basis sequencing.
func raceCorrectionSQL(emp, ee, iv, day string) string {
	return fmt.Sprintf(`WITH g AS (
	  INSERT INTO attendance_correction_group (employer_id, employee_id, reason, corrected_by)
	  VALUES ('%s', '%s', 'race', 'test') RETURNING id
	),
	l AS (
	  INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
	  SELECT '%s', g.id, '%s' FROM g RETURNING group_id
	)
	INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
	  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id)
	SELECT '%s', '%s', '%sT09:00:00+08:00', '%sT17:00:00+08:00',
	  'Asia/Singapore', '%s'::date, 'work', 'correction',
	  (SELECT jsonb_build_object('correction_id', '%s', 'predecessor_interval_id', '%s',
	    'correction_group_id', g.id::text) FROM g),
	  '%s', g.id FROM g`,
		emp, ee, emp, iv, emp, ee, day, day, day, iv, iv, iv)
}

func TestCorrectVsApprovalBarrier(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp, _ := chain(t, pool, "BARC", "2026-08-03")
	period := racePeriod(t, pool, emp, uniq(t, "PPC"))

	// Scenario 1: correction holds pre-commit; service approval
	// blocks, then commits with the segment ordered first.
	ee1 := raceEmployee(t, pool, emp, uniq(t, "C1"))
	iv1 := raceLiveInterval(t, pool, emp, ee1, uniq(t, "c1"), "2026-08-04")
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
	if _, err := htx.Exec(ctx(t), raceCorrectionSQL(emp, ee1, iv1, "2026-08-04")); err != nil {
		t.Fatalf("hold correction: %v", err)
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
	dbutil.AwaitDone(t, done, "approval after correction commit")
	var live bool
	if err := pool.QueryRow(ctx(t),
		`SELECT is_current FROM attendance_interval WHERE id = $1`, iv1).Scan(&live); err != nil || live {
		t.Fatalf("predecessor live=%v err=%v want superseded", live, err)
	}
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, c := range r.AttendanceConflicts {
		t.Fatalf("conflicts=%+v want none: segment committed first", c)
	}

	// Scenario 2: approval holds pre-commit; service Correct blocks,
	// then succeeds after rollback. The correction targets September
	// while the held approval covers August: corrections into a
	// submitted period are refused by design, and the lock is
	// per-employee, so the cross-period pair still proves the wait.
	ee2 := raceEmployee(t, pool, emp, uniq(t, "C2"))
	iv2 := raceLiveInterval(t, pool, emp, ee2, uniq(t, "c2"), "2026-09-04")
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
		_, err := Correct(ctx(t), wp2, emp, CorrectionInput{
			IntervalIDs: []string{iv2}, Start: "2026-09-04T09:00:00+08:00", End: "2026-09-04T17:00:00+08:00",
			Kind: "work", Reason: "barrier race",
		}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
		done2 <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids2, done2)
	if err := htx2.Rollback(ctx(t)); err != nil {
		t.Fatalf("rollback hold2: %v", err)
	}
	dbutil.AwaitDone(t, done2, "correct after approval rollback")
	var groups int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group WHERE employer_id = $1 AND employee_id = $2`,
		emp, ee2).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("groups=%d err=%v want the correction's group", groups, err)
	}

	// Scenario 3: correction transaction starts before approval but
	// commits after it. The segment's created_at predates
	// approved_at; readiness must still flag via basis_seq.
	ee3 := raceEmployee(t, pool, emp, uniq(t, "C3"))
	iv3 := raceLiveInterval(t, pool, emp, ee3, uniq(t, "c3"), "2026-08-05")
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
	if _, err := ltx.Exec(ctx(t), raceCorrectionSQL(emp, ee3, iv3, "2026-08-05")); err != nil {
		t.Fatalf("late correction: %v", err)
	}
	var diverged bool
	if err := ltx.QueryRow(ctx(t),
		`SELECT max(i.created_at) < (SELECT approved_at FROM timesheet WHERE id = $1)
		 FROM attendance_interval i WHERE i.employer_id = $2 AND i.employee_id = $3 AND i.source = 'correction'`,
		sheet3, emp, ee3).Scan(&diverged); err != nil || !diverged {
		t.Fatalf("diverged=%v err=%v want segment created_at predating approved_at", diverged, err)
	}
	if err := ltx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit late: %v", err)
	}
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess3: %v", err)
	}
	found := false
	for _, c := range r3.AttendanceConflicts {
		if strings.Contains(c.Reason, "created after approval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts=%+v want the late segment flagged", r3.AttendanceConflicts)
	}
}

func TestImportVsApprovalBarrier(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp, _ := chain(t, pool, "BAR", "2026-08-03")
	period := racePeriod(t, pool, emp, uniq(t, "PP"))

	// Scenario 1: import holds pre-commit; service approval blocks,
	// then commits with the evidence ordered first: no conflict.
	ee1 := raceEmployee(t, pool, emp, uniq(t, "E1"))
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
	if _, err := htx.Exec(ctx(t), feedSQL(emp, ee1, uniq(t, "s1"))); err != nil {
		t.Fatalf("hold events: %v", err)
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
	dbutil.AwaitDone(t, done, "approval after import commit")
	var evSeq, apSeq int64
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT max(basis_seq) FROM clock_event WHERE employer_id = $1 AND employee_id = $2`,
		emp, ee1).Scan(&evSeq); err != nil {
		t.Fatalf("event seq: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT status, basis_seq FROM timesheet WHERE id = $1`, sheet1).Scan(&status, &apSeq); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if status != "approved" || evSeq == 0 || apSeq == 0 || evSeq >= apSeq {
		t.Fatalf("status=%s evSeq=%d apSeq=%d want approved with evidence ordered first", status, evSeq, apSeq)
	}
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	for _, c := range r.AttendanceConflicts {
		t.Fatalf("conflicts=%+v want none: evidence committed first", c)
	}

	// Scenario 2: approval holds pre-commit; service import blocks,
	// then succeeds after rollback of the hold. The sheet is
	// submitted before the hold begins (Submit takes the lock too).
	// The employee has no roster, so the import lands unpaired with
	// exceptions — still a committed mutation under the lock.
	no2 := uniq(t, "E2")
	ee2 := raceEmployee(t, pool, emp, no2)
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
		csv := "source,source_event_id,employee_no,occurred_at,timezone,kind\n" +
			"vendor-x," + uniq(t, "s2i") + "," + no2 + ",2026-08-05T09:00:00+08:00,Asia/Singapore,in\n" +
			"vendor-x," + uniq(t, "s2o") + "," + no2 + ",2026-08-05T18:00:00+08:00,Asia/Singapore,out\n"
		_, err := Import(ctx(t), wp2, emp, strings.NewReader(csv), "test-op",
			timesheet.RegenerateDraft, timesheet.ChainTerminal)
		done2 <- err
	}()
	dbutil.AwaitAdvisoryWait(t, pool, pids2, done2)
	if err := htx2.Rollback(ctx(t)); err != nil {
		t.Fatalf("rollback hold2: %v", err)
	}
	dbutil.AwaitDone(t, done2, "import after approval rollback")
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event WHERE employer_id = $1 AND employee_id = $2`,
		emp, ee2).Scan(&n); err != nil || n != 2 {
		t.Fatalf("events=%d err=%v want the import's pair stored", n, err)
	}

	// Scenario 3: import transaction starts before approval but
	// commits after it. created_at predates approved_at (the D-RACE
	// signature); readiness must still flag via basis_seq.
	ee3 := raceEmployee(t, pool, emp, uniq(t, "E3"))
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
	if _, err := ltx.Exec(ctx(t), feedSQL(emp, ee3, uniq(t, "s3"))); err != nil {
		t.Fatalf("late events: %v", err)
	}
	var diverged bool
	if err := ltx.QueryRow(ctx(t),
		`SELECT max(e.created_at) < (SELECT approved_at FROM timesheet WHERE id = $1)
		 FROM clock_event e WHERE e.employer_id = $2 AND e.employee_id = $3`,
		sheet3, emp, ee3).Scan(&diverged); err != nil || !diverged {
		t.Fatalf("diverged=%v err=%v want created_at predating approved_at", diverged, err)
	}
	if err := ltx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit late: %v", err)
	}
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess3: %v", err)
	}
	found := false
	for _, c := range r3.AttendanceConflicts {
		if strings.Contains(c.Reason, "created after approval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts=%+v want the late evidence flagged", r3.AttendanceConflicts)
	}
}
