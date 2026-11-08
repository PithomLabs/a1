package integrity

import (
	"testing"
)

// G2: temporal freezing. Drafts stay mutable; decided, published and
// approved rows reject edits; insert-only history rejects updates and
// deletes outright.

func mkTimesheetChain(t *testing.T, tag string) (employer, employee, period, sheet, line, interval string) {
	t.Helper()
	employer = mkEmployer(t, key(t, tag), "UEN-"+key(t, tag))
	employee = mkEmployee(t, employer, "E001")
	period = mkPeriod(t, employer, "PP")
	mkPayCode(t, "OT15")
	sheet = queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		employer, employee, period)
	line = queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 2, 7200000000, $3) RETURNING id`,
		employer, sheet, period)
	interval = mkInterval(t, employer, employee, `{"clock_event_ids": ["evt-1"]}`)
	return employer, employee, period, sheet, line, interval
}

// Re-parenting into a frozen parent is rejected (SYN-P1-1): UPDATE must
// validate the NEW parent as well as the OLD one.
func TestFreezeReparenting(t *testing.T) {
	emp := mkEmployer(t, key(t), "UEN-"+key(t))
	empA := mkEmployee(t, emp, "E001")
	period := mkPeriod(t, emp, "PP")
	mkPayCode(t, "OT15")
	decidedSheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		emp, empA, period)
	// The draft is a correction version of the decided sheet: same
	// employee and period, so the re-parenting below exercises the
	// freeze guard rather than the single-chain guard (which owns
	// cross-chain moves and is pinned separately).
	draftSheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, supersedes_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		emp, empA, period, decidedSheet)
	draftLine := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		emp, draftSheet, period)
	decidedLine := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		emp, decidedSheet, period)
	exec(t, appPool,
		`UPDATE timesheet SET status = 'approved', approved_by = 'test', approved_at = now() WHERE id = $1`,
		decidedSheet)

	// Move a draft line under the approved sheet: NEW parent frozen.
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet_line SET timesheet_id = $1 WHERE id = $2`, decidedSheet, draftLine)
	expectSQLError(t, err, "P0001", "frozen", "line re-parented into approved sheet")

	// Move a draft allocation under a line of the approved sheet.
	iv := mkInterval(t, emp, empA, `{"clock_event_ids": ["evt-rp"]}`)
	exec(t, appPool,
		`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
		emp, draftLine, iv)
	_, err = appPool.Exec(ctx(t),
		`UPDATE timesheet_line_interval SET line_id = $1 WHERE employer_id = $2 AND line_id = $3`,
		decidedLine, emp, draftLine)
	expectSQLError(t, err, "P0001", "frozen", "allocation re-parented into approved sheet")

	// Roster assignment moved from a draft revision into a published one.
	_, prg := mkClientProgramme(t, emp, key(t))
	rosterP, revP := mkRoster(t, emp, prg, "2026-08-03")
	rosterD, revD := mkRoster(t, emp, prg, "2026-08-10")
	tpl := mkTemplate(t, emp, "DAY")
	asg := mkAssignment(t, emp, revD, empA, tpl, "2026-08-10")
	exec(t, appPool, `UPDATE roster SET status = 'published', published_by = 'test' WHERE id = $1`, rosterP)
	_ = rosterD
	_, err = appPool.Exec(ctx(t),
		`UPDATE roster_assignment SET revision_id = $1 WHERE id = $2`, revP, asg)
	expectSQLError(t, err, "P0001", "frozen", "assignment re-parented into published revision")

	// Payroll line moved from a draft run into an approved run.
	runA := mkRun(t, emp, period)
	runB := mkRun(t, emp, period)
	line := queryID(t, appPool,
		`INSERT INTO payroll_line (employer_id, run_id, employee_id, component, pay_code, amount, cpf_treatment, work_period, payable_date, contribution_period, source_refs)
		 VALUES ($1, $2, $3, 'basic', 'OT15', 100, 'OW', '2026-08', '2026-09-07', '2026-08', $4::jsonb) RETURNING id`,
		emp, runB, empA, lineRefs(t))
	exec(t, appPool, `UPDATE payroll_run SET status = 'approved', approved_by = 'test' WHERE id = $1`, runA)
	_, err = appPool.Exec(ctx(t),
		`UPDATE payroll_line SET run_id = $1 WHERE id = $2`, runA, line)
	expectSQLError(t, err, "P0001", "frozen", "payroll line re-parented into approved run")
}

// Timesheet, lines and allocations are editable in draft and frozen once
// the sheet is approved or rejected.
func TestTimesheetFreezeMatrix(t *testing.T) {
	for _, decided := range []string{"approved", "rejected"} {
		t.Run(decided, func(t *testing.T) {
			emp, _, _, sheet, line, interval := mkTimesheetChain(t, decided)

			// Draft: everything mutable.
			_, err := appPool.Exec(ctx(t),
				`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
				emp, line, interval)
			expectOK(t, err, "draft: link interval")
			exec(t, appPool, `UPDATE timesheet_line SET hours = 3 WHERE id = $1`, line)
			exec(t, appPool, `DELETE FROM timesheet_line_interval WHERE employer_id = $1 AND line_id = $2`, emp, line)

			// Decide the sheet; the whole chain freezes.
			exec(t, appPool,
				`UPDATE timesheet SET status = $1, approved_by = 'test', approved_at = now() WHERE id = $2`,
				decided, sheet)
			requireStatus(t, "timesheet", sheet, decided)

			_, err = appPool.Exec(ctx(t), `UPDATE timesheet SET status = 'draft' WHERE id = $1`, sheet)
			expectSQLError(t, err, "P0001", "frozen", decided+": reopen sheet")
			_, err = appPool.Exec(ctx(t), `DELETE FROM timesheet WHERE id = $1`, sheet)
			expectSQLError(t, err, "P0001", "frozen", decided+": delete sheet")
			_, err = appPool.Exec(ctx(t), `UPDATE timesheet_line SET hours = 4 WHERE id = $1`, line)
			expectSQLError(t, err, "P0001", "frozen", decided+": edit line")
			_, err = appPool.Exec(ctx(t), `DELETE FROM timesheet_line WHERE id = $1`, line)
			expectSQLError(t, err, "P0001", "frozen", decided+": delete line")
			_, err = appPool.Exec(ctx(t),
				`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
				 SELECT employer_id, $1, 'OT15', 1, 3600000000, pay_period_id FROM timesheet WHERE id = $1`, sheet)
			expectSQLError(t, err, "P0001", "frozen", decided+": add line")
			_, err = appPool.Exec(ctx(t),
				`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
				emp, line, interval)
			expectSQLError(t, err, "P0001", "frozen", decided+": link interval")
		})
	}
}

// Assignments are editable only on the current revision of a draft roster.
func TestRosterDraftMatrix(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, prg := mkClientProgramme(t, a, key(t))
	roster, rev := mkRoster(t, a, prg, "2026-08-03")
	tpl := mkTemplate(t, a, "DAY")
	asg := mkAssignment(t, a, rev, emp, tpl, "2026-08-03")

	exec(t, appPool, `UPDATE roster_assignment SET status = 'confirmed' WHERE id = $1`, asg)
	exec(t, appPool, `DELETE FROM roster_assignment WHERE id = $1`, asg)
	asg = mkAssignment(t, a, rev, emp, tpl, "2026-08-04")

	exec(t, appPool, `UPDATE roster SET status = 'published', published_by = 'test' WHERE id = $1`, roster)
	requireStatus(t, "roster", roster, "published")
	_, err := appPool.Exec(ctx(t),
		`UPDATE roster_assignment SET status = 'edited' WHERE id = $1`, asg)
	expectSQLError(t, err, "P0001", "frozen", "published: update assignment")
	_, err = appPool.Exec(ctx(t), `DELETE FROM roster_assignment WHERE id = $1`, asg)
	expectSQLError(t, err, "P0001", "frozen", "published: delete assignment")
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, template_id, rest_day)
		 VALUES ($1, $2, $3, '2026-08-05', $4, false)`, a, rev, emp, tpl)
	expectSQLError(t, err, "P0001", "frozen", "published: insert assignment")
}

// Runs and lines freeze on approval; exclusions exist only for drafts.
func TestRunFreezeMatrix(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	period := mkPeriod(t, a, "PP")
	mkPayCode(t, "BASIC")
	run := mkRun(t, a, period)

	line := queryID(t, appPool,
		`INSERT INTO payroll_line (employer_id, run_id, employee_id, component, pay_code, amount, cpf_treatment, work_period, payable_date, contribution_period, source_refs)
		 VALUES ($1, $2, $3, 'basic', 'BASIC', 2600, 'OW', '2026-08', '2026-09-07', '2026-08', $4::jsonb) RETURNING id`,
		a, run, emp, lineRefs(t))
	exec(t, appPool, `UPDATE payroll_line SET amount = 2601 WHERE id = $1`, line)
	excl := queryID(t, appPool,
		`INSERT INTO payroll_run_exclusion (employer_id, payroll_run_id, employee_id, reason, actor)
		 VALUES ($1, $2, $3, 'r', 'op') RETURNING id`,
		a, run, emp)
	exec(t, appPool, `UPDATE payroll_run_exclusion SET reason = 'r2' WHERE id = $1`, excl)
	exec(t, appPool, `DELETE FROM payroll_run_exclusion WHERE id = $1`, excl)

	excl2 := queryID(t, appPool,
		`INSERT INTO payroll_run_exclusion (employer_id, payroll_run_id, employee_id, reason, actor)
		 VALUES ($1, $2, $3, 'early', 'op') RETURNING id`,
		a, run, emp)
	exec(t, appPool, `UPDATE payroll_run SET status = 'validated' WHERE id = $1`, run)
	requireStatus(t, "payroll_run", run, "validated")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO payroll_run_exclusion (employer_id, payroll_run_id, employee_id, reason, actor)
		 VALUES ($1, $2, $3, 'late', 'op')`, a, run, emp)
	expectSQLError(t, err, "P0001", "draft", "validated: insert exclusion")
	_, err = appPool.Exec(ctx(t), `UPDATE payroll_run_exclusion SET reason = 'late-edit' WHERE id = $1`, excl2)
	expectSQLError(t, err, "P0001", "draft", "validated: update exclusion")
	_, err = appPool.Exec(ctx(t), `DELETE FROM payroll_run_exclusion WHERE id = $1`, excl2)
	expectSQLError(t, err, "P0001", "draft", "validated: delete exclusion")
	// Lines stay mutable while validation can still fail the run.
	exec(t, appPool, `UPDATE payroll_line SET amount = 2602 WHERE id = $1`, line)

	exec(t, appPool, `UPDATE payroll_run SET status = 'approved', approved_by = 'test' WHERE id = $1`, run)
	requireStatus(t, "payroll_run", run, "approved")
	_, err = appPool.Exec(ctx(t), `UPDATE payroll_run SET status = 'draft' WHERE id = $1`, run)
	expectSQLError(t, err, "P0001", "frozen", "approved: reopen run")
	_, err = appPool.Exec(ctx(t), `DELETE FROM payroll_run WHERE id = $1`, run)
	expectSQLError(t, err, "P0001", "frozen", "approved: delete run")
	_, err = appPool.Exec(ctx(t), `UPDATE payroll_line SET amount = 1 WHERE id = $1`, line)
	expectSQLError(t, err, "P0001", "frozen", "approved: edit line")
	_, err = appPool.Exec(ctx(t), `DELETE FROM payroll_line WHERE id = $1`, line)
	expectSQLError(t, err, "P0001", "frozen", "approved: delete line")

	// One approved regular run per period.
	run2 := mkRun(t, a, period)
	_, err = appPool.Exec(ctx(t),
		`UPDATE payroll_run SET status = 'approved', approved_by = 'test' WHERE id = $1`, run2)
	expectSQLError(t, err, "23505", "payroll_run_one_approved_regular", "second approved regular run")
}

// Decided leave requests are immutable; requested ones stay editable.
func TestLeaveFreeze(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	leave := queryID(t, appPool,
		`INSERT INTO leave_request (employer_id, employee_id, type, from_date, to_date, days)
		 VALUES ($1, $2, 'annual', '2026-09-01', '2026-09-02', 2) RETURNING id`,
		a, emp)
	exec(t, appPool, `UPDATE leave_request SET days = 1, to_date = '2026-09-01' WHERE id = $1`, leave)
	exec(t, appPool,
		`UPDATE leave_request SET status = 'approved', approved_by = 'test', approved_at = now() WHERE id = $1`, leave)
	requireStatus(t, "leave_request", leave, "approved")
	_, err := appPool.Exec(ctx(t), `UPDATE leave_request SET days = 2 WHERE id = $1`, leave)
	expectSQLError(t, err, "P0001", "frozen", "approved: edit leave")
	_, err = appPool.Exec(ctx(t), `DELETE FROM leave_request WHERE id = $1`, leave)
	expectSQLError(t, err, "P0001", "frozen", "approved: delete leave")
}

// History tables accept inserts and reject updates and deletes for the
// app role. Each case builds its own parent chain.
func TestInsertOnlyTables(t *testing.T) {
	t.Run("clock_event", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		emp := mkEmployee(t, a, "E001")
		id := queryID(t, appPool,
			`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
			 VALUES ($1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in', 's', 'e1') RETURNING id`,
			a, emp)
		_, err := appPool.Exec(ctx(t), `UPDATE clock_event SET kind = 'out' WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "clock_event update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM clock_event WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "clock_event delete")
	})

	t.Run("attendance_interval", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		emp := mkEmployee(t, a, "E001")
		id := mkInterval(t, a, emp, `{"clock_event_ids": ["evt-1"]}`)
		_, err := appPool.Exec(ctx(t), `UPDATE attendance_interval SET kind = 'break_paid' WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "attendance_interval update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM attendance_interval WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "attendance_interval delete")
	})

	t.Run("attendance_correction", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		emp := mkEmployee(t, a, "E001")
		rec := mkRecord(t, a, emp, "2026-08-03")
		id := queryID(t, appPool,
			`INSERT INTO attendance_correction (employer_id, attendance_id, field, old_value, new_value, reason, corrected_by)
			 VALUES ($1, $2, 'actual_min', '480', '470', 'r', 'op') RETURNING id`,
			a, rec)
		_, err := appPool.Exec(ctx(t), `UPDATE attendance_correction SET reason = 'x' WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "attendance_correction update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM attendance_correction WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "attendance_correction delete")
	})

	t.Run("roster_revision", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		_, prg := mkClientProgramme(t, a, key(t))
		_, rev := mkRoster(t, a, prg, "2026-08-03")
		_, err := appPool.Exec(ctx(t), `UPDATE roster_revision SET note = 'x' WHERE id = $1`, rev)
		expectSQLError(t, err, "42501", "permission denied", "roster_revision update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM roster_revision WHERE id = $1`, rev)
		expectSQLError(t, err, "42501", "permission denied", "roster_revision delete")
	})

	t.Run("ket_record", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		emp := mkEmployee(t, a, "E001")
		terms := mkTerms(t, a, emp, "2026-01-01")
		id := queryID(t, appPool,
			`INSERT INTO ket_record (employer_id, terms_id, payload) VALUES ($1, $2, '{}') RETURNING id`,
			a, terms)
		_, err := appPool.Exec(ctx(t), `UPDATE ket_record SET payload = '{"x":1}' WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "ket_record update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM ket_record WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "ket_record delete")
	})

	t.Run("payslip", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		emp := mkEmployee(t, a, "E001")
		period := mkPeriod(t, a, "PP")
		run := mkRun(t, a, period)
		id := queryID(t, appPool,
			`INSERT INTO payslip (employer_id, run_id, employee_id, payload) VALUES ($1, $2, $3, '{}') RETURNING id`,
			a, run, emp)
		_, err := appPool.Exec(ctx(t), `UPDATE payslip SET payload = '{"x":1}' WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "payslip update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM payslip WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "payslip delete")
	})

	t.Run("audit_event", func(t *testing.T) {
		a := mkEmployer(t, key(t), "UEN-"+key(t))
		id := queryID(t, appPool,
			`INSERT INTO audit_event (employer_id, actor, service, action, entity) VALUES ($1, 'op', 'svc', 'act', 'ent') RETURNING id`,
			a)
		_, err := appPool.Exec(ctx(t), `UPDATE audit_event SET actor = 'x' WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "audit_event update")
		_, err = appPool.Exec(ctx(t), `DELETE FROM audit_event WHERE id = $1`, id)
		expectSQLError(t, err, "42501", "permission denied", "audit_event delete")
	})
}

// The five logical triggers of database-design §3 exist under their spec
// names (shared across their tables). This guards doc/code agreement.
func TestSpecTriggerNamesExist(t *testing.T) {
	want := map[string][]string{
		"trg_timesheet_frozen":         {"timesheet", "timesheet_line", "timesheet_line_interval"},
		"trg_roster_published_frozen":  {"roster_assignment"},
		"trg_payroll_run_locked":       {"payroll_run", "payroll_line"},
		"trg_payroll_exclusion_locked": {"payroll_run_exclusion"},
		"trg_leave_decided_frozen":     {"leave_request"},
	}
	for trg, tables := range want {
		for _, tbl := range tables {
			var exists bool
			err := appPool.QueryRow(ctx(t),
				`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE NOT tgisinternal AND tgname = $1 AND tgrelid = $2::regclass)`,
				trg, tbl).Scan(&exists)
			expectOK(t, err, "check trigger "+trg+" on "+tbl)
			if !exists {
				t.Fatalf("spec trigger %s missing on %s", trg, tbl)
			}
		}
	}
}

// Effective-dated rows may not overlap on their keys.
func TestEffectiveDatingNoOverlap(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")

	mkTerms(t, a, emp, "2026-01-01")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-06-01', 'Agent', 'Support', 'workman', true, 'b', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')`,
		a, emp)
	expectSQLError(t, err, "23P01", "employment_terms_no_overlap", "overlapping terms")

	exec(t, appPool,
		`INSERT INTO leave_request (employer_id, employee_id, type, from_date, to_date, days)
		 VALUES ($1, $2, 'annual', '2026-09-01', '2026-09-03', 3)`, a, emp)
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO leave_request (employer_id, employee_id, type, from_date, to_date, days)
		 VALUES ($1, $2, 'sick', '2026-09-02', '2026-09-04', 3)`, a, emp)
	expectSQLError(t, err, "23P01", "leave_request_no_overlap", "overlapping leave")

	_, prg := mkClientProgramme(t, a, key(t))
	team := queryID(t, appPool,
		`INSERT INTO team (employer_id, programme_id, code, name) VALUES ($1, $2, 'T1', 'T1') RETURNING id`,
		a, prg)
	exec(t, appPool,
		`INSERT INTO team_membership (employer_id, team_id, employee_id, role, effective_from)
		 VALUES ($1, $2, $3, 'agent', '2026-01-01')`, a, team, emp)
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO team_membership (employer_id, team_id, employee_id, role, effective_from)
		 VALUES ($1, $2, $3, 'lead', '2026-06-01')`, a, team, emp)
	expectSQLError(t, err, "23P01", "team_membership_no_overlap", "overlapping membership")
}
