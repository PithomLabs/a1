package payroll

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/audit"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// holdRace inverts snapRace: the WRITER holds a barrier in an open
// transaction while capture (or validation) starts behind it. The
// capture provably blocks (pg_locks NOT-granted), the writer commits,
// and the capture must then include the committed change.
//
// Red-first: capture fixes its REPEATABLE READ snapshot at the
// run-row SELECT, before acquiring any barrier, so every inclusion
// test below currently FAILS with the writer's change missing.
// Post-Gate-1 the same tests prove inclusion. Do not weaken them.
type holdRace struct {
	emp, period, run string
	app, admin       *pgxpool.Pool
	wconn            *pgx.Conn
	wtx              pgx.Tx
	capPool          *pgxpool.Pool
	capPids          []int32
	done             chan error
	snapID           string
	verdict          *Validation
}

func beginHoldRace(t *testing.T) *holdRace {
	t.Helper()
	return beginHoldRaceSpan(t, "2026-08-01", "2026-08-31")
}

func beginHoldRaceSpan(t *testing.T, start, end string) *holdRace {
	t.Helper()
	app, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetupSpan(t, app, start, end)
	period := mustPeriod(t, app, emp)
	dsn := dbutil.RaceDSN(t, app)
	wconn := dbutil.AppConn(t, dsn)
	wtx, err := wconn.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	capPool, capPids := dbutil.WaiterPool(t, dsn)
	return &holdRace{emp: emp, period: period, run: run, app: app, admin: admin,
		wconn: wconn, wtx: wtx, capPool: capPool, capPids: capPids, done: make(chan error, 1)}
}

// beginHoldRaceAdmin is beginHoldRace with a privileged writer: pack
// activation rows are unreadable-or-writable by app rules that keep
// the mutation faithful (app cannot UPDATE activation).
func beginHoldRaceAdmin(t *testing.T) *holdRace {
	t.Helper()
	r := beginHoldRace(t)
	r.wtx.Rollback(ctx(t))
	r.wconn.Close(ctx(t))
	adsn := dbutil.RaceDSN(t, r.admin)
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := pgx.Connect(cctx, adsn)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	var who string
	if err := c.QueryRow(cctx, `SELECT current_user`).Scan(&who); err != nil || who == "app" {
		t.Fatalf("writer runs as %q, want the privileged role", who)
	}
	wtx, err := c.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	r.wconn, r.wtx = c, wtx
	return r
}

func (r *holdRace) hold(t *testing.T, lockSQL string, args ...any) {
	t.Helper()
	if _, err := r.wtx.Exec(ctx(t), lockSQL, args...); err != nil {
		t.Fatalf("hold barrier: %v", err)
	}
}

func (r *holdRace) startCapture(t *testing.T) {
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

func (r *holdRace) startValidation(t *testing.T) {
	t.Helper()
	go func() {
		v, err := ValidateRun(ctx(t), r.capPool, r.emp, r.run, 0, "test-op")
		if err == nil {
			r.verdict = v
			r.snapID = v.SnapshotID
		}
		r.done <- err
	}()
	dbutil.AwaitAdvisoryWait(t, r.app, r.capPids, r.done)
}

func (r *holdRace) mutate(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := r.wtx.Exec(ctx(t), sql, args...); err != nil {
		t.Fatalf("writer mutation: %v", err)
	}
}

func (r *holdRace) commit(t *testing.T) {
	t.Helper()
	if err := r.wtx.Commit(ctx(t)); err != nil {
		t.Fatalf("writer commit: %v", err)
	}
	dbutil.AwaitDone(t, r.done, "capture after writer commit")
}

func (r *holdRace) snapshot(t *testing.T) *SnapshotDetail {
	t.Helper()
	d, err := GetSnapshot(ctx(t), r.app, r.snapID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return d
}

func findSnapshotEmployee(d *SnapshotDetail, no string) *SnapshotEmployee {
	for i := range d.Employees {
		if d.Employees[i].EmployeeNo == no {
			return &d.Employees[i]
		}
	}
	return nil
}

const insertRaceEmployeeSQL = `INSERT INTO employee
	 (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
	 VALUES ($1, $2, $3, '9 Test Ave', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE) RETURNING id`

const insertRaceTermsSQL = `INSERT INTO employment_terms
	 (employer_id, employee_id, effective_from, job_title, job_duties, employment_category,
	  part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement, normal_hours_weekly,
	  working_days, daily_hours, rest_day, start_date, contract_type, place_of_work, salary_period)
	 VALUES ($1, $2, $3, 'Agent', 'Support', 'workman', true, 'manual-wages', 2600,
	  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')
	 RETURNING id`

// mkSubmittedEmployee builds a complete employee whose sheet is
// submitted but deliberately unapproved, so a racing approval is the
// only change under test. It returns the employee and sheet ids.
func mkSubmittedEmployee(t *testing.T, pool *pgxpool.Pool, emp, period string) (empID, sheetID string) {
	t.Helper()
	no := fmt.Sprintf("SUB%06d", time.Now().UnixNano()%1000000)
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
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, e.ID, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	var sheet string
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
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet, "test"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	return e.ID, sheet
}

func TestWriterFirstEmployeeCreation(t *testing.T) {
	r := beginHoldRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.hold(t, `SELECT acquire_payroll_population_lock($1::uuid)`, r.emp)
	r.startCapture(t)
	var racer string
	if err := r.wtx.QueryRow(ctx(t), insertRaceEmployeeSQL, r.emp, "RACE2", "RACE2").Scan(&racer); err != nil {
		t.Fatalf("writer employee: %v", err)
	}
	r.mutate(t, insertRaceTermsSQL, r.emp, racer, "2026-01-01")
	r.commit(t)
	if findSnapshotEmployee(r.snapshot(t), "RACE2") == nil {
		t.Fatal("RED: capture missed the writer-first employee (D1)")
	}
}

func TestWriterFirstTermsAdded(t *testing.T) {
	r := beginHoldRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	e2, err := hr.CreateEmployee(ctx(t), r.app, r.emp, hr.EmployeeInput{
		EmployeeNo: "RACE3", LegalName: "RACE3", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	r.hold(t, `SELECT acquire_payroll_population_lock($1::uuid)`, r.emp)
	r.startCapture(t)
	var termsID string
	if err := r.wtx.QueryRow(ctx(t), insertRaceTermsSQL, r.emp, e2.ID, "2026-01-01").Scan(&termsID); err != nil {
		t.Fatalf("writer terms: %v", err)
	}
	r.commit(t)
	got := findSnapshotEmployee(r.snapshot(t), "RACE3")
	if got == nil {
		t.Fatal("RED: capture missed the writer-first terms-holder (D1)")
	}
	found := false
	for _, id := range got.TermsIDs {
		if id == termsID {
			found = true
		}
	}
	if !found {
		t.Fatalf("RED: captured terms %v miss the committed row %s (D1)", got.TermsIDs, termsID)
	}
}

func TestWriterFirstTermination(t *testing.T) {
	// Span around tomorrow: the termination must be effective
	// after today's seeded status row yet inside the period.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	first := time.Date(tomorrow.Year(), tomorrow.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	r := beginHoldRaceSpan(t, first.Format("2006-01-02"), last.Format("2006-01-02"))
	e1, _ := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	eff := tomorrow.Format("2006-01-02")
	r.hold(t, `SELECT acquire_payroll_population_lock($1::uuid)`, r.emp)
	r.startCapture(t)
	r.mutate(t, `SELECT transition_employee_status($1::uuid, $2::uuid, 'terminated', $3::date, 'race', 'test-op')`,
		r.emp, e1, eff)
	r.commit(t)
	var histID string
	if err := r.app.QueryRow(ctx(t),
		`SELECT id FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 AND status = 'terminated'`,
		r.emp, e1).Scan(&histID); err != nil {
		t.Fatalf("history: %v", err)
	}
	got := findSnapshotEmployee(r.snapshot(t), snapshotNo(t, r.app, r.emp, e1))
	if got == nil {
		t.Fatal("capture missed the terminated employee entirely")
	}
	for _, id := range got.StatusIDs {
		if id == histID {
			return
		}
	}
	t.Fatalf("RED: captured statuses %v miss the committed termination %s (D1)", got.StatusIDs, histID)
}

func snapshotNo(t *testing.T, pool *pgxpool.Pool, emp, empID string) string {
	t.Helper()
	var no string
	if err := pool.QueryRow(ctx(t),
		`SELECT employee_no FROM employee WHERE employer_id = $1 AND id = $2`,
		emp, empID).Scan(&no); err != nil {
		t.Fatalf("employee no: %v", err)
	}
	return no
}

func TestWriterFirstCompensation(t *testing.T) {
	r := beginHoldRace(t)
	e1, termsID := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.hold(t, `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, r.emp, e1)
	r.startCapture(t)
	// Faithful writer: the same controlled function the service
	// uses (app cannot raw-INSERT compensation rows).
	r.mutate(t,
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, 'allowance', 'RACE',
		  50.00::numeric, '2026-01-01'::date, NULL, 'test-op', 'race allowance')`,
		r.emp, e1, termsID)
	r.commit(t)
	got := findSnapshotEmployee(r.snapshot(t), snapshotNo(t, r.app, r.emp, e1))
	if got == nil {
		t.Fatal("capture missed the employee entirely")
	}
	for _, s := range got.CompSegments {
		if s.Code == "RACE" {
			return
		}
	}
	t.Fatalf("RED: captured segments %+v miss the committed RACE line (D1)", got.CompSegments)
}

func TestWriterFirstApproval(t *testing.T) {
	r := beginHoldRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	e2, sheet := mkSubmittedEmployee(t, r.app, r.emp, r.period)
	r.hold(t, `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, r.emp, e2)
	r.startCapture(t)
	// Faithful approval: the exact UPDATE the service transition
	// issues, plus its audit row, inside the held transaction.
	r.mutate(t,
		`UPDATE timesheet SET status = 'approved', approved_by = 'test-op', approved_at = now()
		 WHERE employer_id = $1 AND id = $2 AND status = 'submitted'`,
		r.emp, sheet)
	if err := audit.Write(ctx(t), r.wtx, r.emp, "test-op", "timesheet", "timesheet.approved",
		"timesheet", &sheet, map[string]any{"status": "submitted"}, map[string]any{"status": "approved"}); err != nil {
		t.Fatalf("writer audit: %v", err)
	}
	r.commit(t)
	got := findSnapshotEmployee(r.snapshot(t), snapshotNo(t, r.app, r.emp, e2))
	if got == nil {
		t.Fatal("capture missed the employee entirely")
	}
	if got.ApprovedSheetID == nil || *got.ApprovedSheetID != sheet {
		t.Fatalf("RED: approved sheet %v misses the committed approval %s (D1)", got.ApprovedSheetID, sheet)
	}
}

func TestWriterFirstAttendanceEvidence(t *testing.T) {
	r := beginHoldRace(t)
	e1, _ := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.hold(t, `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, r.emp, e1)
	r.startCapture(t)
	// Evidence created after the approval is a genuine conflict —
	// but only if capture sees it.
	r.mutate(t,
		`INSERT INTO attendance_interval (id, employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-04 09:00:00+08', '2026-08-04 18:00:00+08',
		  'Asia/Singapore', '2026-08-04', 'work', 'clock', '{"clock_event_ids": ["race-ev-1"]}')`,
		r.emp, e1)
	r.commit(t)
	got := findSnapshotEmployee(r.snapshot(t), snapshotNo(t, r.app, r.emp, e1))
	if got == nil {
		t.Fatal("capture missed the employee entirely")
	}
	for _, b := range got.Blockers {
		if strings.Contains(b, "created after approval") {
			return
		}
	}
	t.Fatalf("RED: decision %q blockers %v miss the post-approval evidence (D1)", got.Decision, got.Blockers)
}

func TestWriterFirstSHGChange(t *testing.T) {
	r := beginHoldRace(t)
	e1, _ := mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.hold(t, `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, r.emp, e1)
	r.startCapture(t)
	var shgID string
	if err := r.wtx.QueryRow(ctx(t),
		`SELECT add_shg_instruction($1::uuid, $2::uuid, 'SINDA', 'pending', NULL, 'race declaration',
		  '2026-01-01'::date, NULL, NULL, NULL, NULL, 'test-op')`,
		r.emp, e1).Scan(&shgID); err != nil {
		t.Fatalf("writer shg: %v", err)
	}
	r.commit(t)
	got := findSnapshotEmployee(r.snapshot(t), snapshotNo(t, r.app, r.emp, e1))
	if got == nil {
		t.Fatal("capture missed the employee entirely")
	}
	for _, s := range got.SHGTips {
		if s.ID == shgID {
			return
		}
	}
	t.Fatalf("RED: captured SHG tips %+v miss the committed instruction %s (D1)", got.SHGTips, shgID)
}

func TestWriterFirstPackActivation(t *testing.T) {
	r := beginHoldRaceAdmin(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	pack := BasePacks[0]
	r.hold(t, `SELECT acquire_rule_pack_lock($1::uuid, $2)`, r.emp, pack)
	r.startCapture(t)
	// Visibility-only writer: the same activation row the function
	// would rewrite, changed while capture waits on the pack key.
	r.mutate(t,
		`UPDATE rule_pack_activation SET version = 'vRACE' WHERE employer_id = $1 AND pack = $2`,
		r.emp, pack)
	r.commit(t)
	for _, p := range r.snapshot(t).Packs {
		if p.Pack == pack {
			if p.Version == "vRACE" {
				return
			}
			t.Fatalf("RED: captured pin %v misses the committed vRACE (D1)", p)
		}
	}
	t.Fatalf("RED: pack %s missing from captured pins (D1)", pack)
}

func TestValidationWriterFirst(t *testing.T) {
	r := beginHoldRace(t)
	mkSnapEmployee(t, r.app, r.emp, r.period)
	implementPacks(t, r.admin, r.emp)
	r.hold(t, `SELECT acquire_payroll_population_lock($1::uuid)`, r.emp)
	r.startValidation(t)
	var racer string
	if err := r.wtx.QueryRow(ctx(t), insertRaceEmployeeSQL, r.emp, "RACE2", "RACE2").Scan(&racer); err != nil {
		t.Fatalf("writer employee: %v", err)
	}
	r.mutate(t, insertRaceTermsSQL, r.emp, racer, "2026-01-01")
	r.commit(t)
	if r.verdict == nil {
		t.Fatal("no validation verdict")
	}
	if findSnapshotEmployee(r.snapshot(t), "RACE2") == nil {
		t.Fatalf("RED: validation verdict valid=%v missed the writer-first employee (D1/T2)", r.verdict.Valid)
	}
}

// TestStaleSnapshotAttack demonstrates review6 §A against the current
// boundary: a REPEATABLE READ reader that establishes its snapshot
// BEFORE acquiring barriers never sees a writer that commits during
// the barrier wait — even with every barrier provably granted. The
// first assertion documents the vacuity (barriers granted, view
// stale); the second fails red. Post-Gate-1 this test takes its
// ticket-rejection form: the same invocation sequence must be
// refused instead of silently admitted.
func TestStaleSnapshotAttack(t *testing.T) {
	app, _ := testPoolAndAdmin(t)
	emp, _, _ := snapSetup(t, app)
	e1, _ := mkSnapEmployee(t, app, emp, mustPeriod(t, app, emp))
	dsn := dbutil.RaceDSN(t, app)
	reader := dbutil.AppConn(t, dsn)
	if _, err := reader.Exec(ctx(t), `BEGIN ISOLATION LEVEL REPEATABLE READ`); err != nil {
		t.Fatalf("begin reader: %v", err)
	}
	var before int
	if err := reader.QueryRow(ctx(t),
		`SELECT count(*) FROM employee WHERE employer_id = $1`, emp).Scan(&before); err != nil {
		t.Fatalf("establish snapshot: %v", err)
	}
	// The writer commits AFTER the snapshot is established but
	// BEFORE any barrier exists: nothing blocks it, and the
	// snapshot already cannot see it.
	writer := dbutil.AppConn(t, dsn)
	var racer string
	if err := writer.QueryRow(ctx(t), insertRaceEmployeeSQL, emp, "RACEA", "RACEA").Scan(&racer); err != nil {
		t.Fatalf("writer employee: %v", err)
	}
	if _, err := writer.Exec(ctx(t), insertRaceTermsSQL, emp, racer, "2026-01-01"); err != nil {
		t.Fatalf("writer terms: %v", err)
	}
	// Full barrier set, canonical keys, acquired AFTER the writer
	// committed. Every lock grants immediately — and changes
	// nothing about the fixed snapshot.
	if _, err := reader.Exec(ctx(t),
		`SELECT acquire_payroll_population_lock($1::uuid)`, emp); err != nil {
		t.Fatalf("reader pop lock: %v", err)
	}
	if _, err := reader.Exec(ctx(t),
		`SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, emp, e1); err != nil {
		t.Fatalf("reader input lock: %v", err)
	}
	for _, p := range BasePacks {
		if _, err := reader.Exec(ctx(t),
			`SELECT acquire_rule_pack_lock($1::uuid, $2)`, emp, p); err != nil {
			t.Fatalf("reader pack lock %s: %v", p, err)
		}
	}
	var granted int
	if err := reader.QueryRow(ctx(t),
		`SELECT count(*) FROM pg_locks
		 WHERE pid = pg_backend_pid() AND locktype = 'advisory' AND granted`).Scan(&granted); err != nil {
		t.Fatalf("pg_locks: %v", err)
	}
	if granted < 2+len(BasePacks) {
		t.Fatalf("barriers not granted: %d", granted)
	}
	var after int
	if err := reader.QueryRow(ctx(t),
		`SELECT count(*) FROM employee WHERE employer_id = $1`, emp).Scan(&after); err != nil {
		t.Fatalf("reread: %v", err)
	}
	reader.Exec(ctx(t), `ROLLBACK`)
	if after != before+1 {
		t.Fatalf("RED: %d barriers granted yet the committed writer is invisible (%d -> %d) (D1/§A)", granted, before, after)
	}
}

// TestSnapshotFabricationByApp demonstrates EF-E3-P0: ordinary app
// SQL can currently INSERT snapshot headers and employee rows with
// wholly invented content — no derivation, no barrier, no audit.
// Post-Gate-1 (INSERT revoked, DEFINER capture only) the same
// statements must fail with permission denied, and this test takes
// its denial form.
func TestSnapshotFabricationByApp(t *testing.T) {
	app, _ := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, app)
	period := mustPeriod(t, app, emp)
	e1, _ := mkSnapEmployee(t, app, emp, period)
	var snapID string
	err := app.QueryRow(ctx(t),
		`INSERT INTO payroll_snapshot (employer_id, payroll_run_id, pay_period_id, version, status,
		  schema_version, content_hash, calc_engine, jurisdiction, currency, captured_by)
		 VALUES ($1, $2, $3, 1, 'valid', 'm2/v1', 'invented', 'unimplemented', 'SG', 'SGD', 'attacker')
		 RETURNING id`,
		emp, run, period).Scan(&snapID)
	if err != nil {
		t.Fatalf("header insert failed unexpectedly: %v", err)
	}
	if _, err := app.Exec(ctx(t),
		`INSERT INTO payroll_snapshot_employee (snapshot_id, employee_id, employment_terms_ids, status_history_ids,
		  employee_basis_seq, approved_sheet_id, decision, blockers, inputs, row_hash)
		 VALUES ($1, $2, '{}', '{}', 0, NULL, 'included', '[]', '{}', 'invented')`,
		snapID, e1); err != nil {
		t.Fatalf("employee insert failed unexpectedly: %v", err)
	}
	var hash string
	if err := app.QueryRow(ctx(t),
		`SELECT content_hash FROM payroll_snapshot WHERE id = $1`, snapID).Scan(&hash); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	t.Fatalf("RED: app fabricated a readable valid snapshot with content hash %q (EF-E3)", hash)
}

// TestLockSubstitutionUndetectable demonstrates review6.1 §B: a
// session-level advisory lock that is released and re-acquired as a
// transaction-level lock on the same key leaves byte-identical
// pg_locks rows, so no ownership check can detect the swap. Post-fix
// (advisory EXECUTE revoked) this test takes its denial form: raw
// acquire/release must fail with permission denied.
func TestLockSubstitutionUndetectable(t *testing.T) {
	app, _ := testPoolAndAdmin(t)
	dsn := dbutil.RaceDSN(t, app)
	c := dbutil.AppConn(t, dsn)
	const key = 424242
	row := func() string {
		var s string
		// The connection holds exactly one advisory lock: no
		// key filter needed (bigint keys span several columns).
		if err := c.QueryRow(ctx(t),
			`SELECT locktype || '|' || mode || '|' || granted::text || '|' ||
			    fastpath::text || '|' || coalesce(transactionid::text, 'NULL') || '|' ||
			    coalesce(waitstart::text, 'NULL')
			 FROM pg_locks WHERE pid = pg_backend_pid() AND locktype = 'advisory'`).Scan(&s); err != nil {
			t.Fatalf("pg_locks row: %v", err)
		}
		return s
	}
	if _, err := c.Exec(ctx(t), `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatalf("session lock: %v", err)
	}
	sessionRow := row()
	if _, err := c.Exec(ctx(t), `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if _, err := c.Exec(ctx(t), `BEGIN`); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := c.Exec(ctx(t), `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		t.Fatalf("xact lock: %v", err)
	}
	xactRow := row()
	c.Exec(ctx(t), `ROLLBACK`)
	c.Exec(ctx(t), `SELECT pg_advisory_unlock_all()`)
	if sessionRow == xactRow {
		t.Fatalf("RED: session and transaction locks indistinguishable in pg_locks (%q) (§B)", sessionRow)
	}
}
