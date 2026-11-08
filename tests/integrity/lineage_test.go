package integrity

import (
	"fmt"
	"testing"
)

// G3: lineage. Every interval carries exactly one source; every payroll
// line carries mandatory provenance keys; runs pin their inputs, rule
// versions, calculation version and rounding conventions.

// An interval cites exactly one source: clock events, one correction, or
// one recorded assumption. (The source label matching the cited evidence
// is a service-level rule the payroll slice will own.)
func TestIntervalExactlyOneSource(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")

	valid := []string{
		`{"clock_event_ids": ["evt-1", "evt-2"]}`,
		`{"correction_id": "00000000-0000-0000-0000-000000000001"}`,
		`{"assumption": "rostered hours, no clock feed"}`,
	}
	spans := [][2]string{
		{"2026-08-03T09:00+08", "2026-08-03T10:00+08"},
		{"2026-08-03T10:00+08", "2026-08-03T11:00+08"},
		{"2026-08-03T11:00+08", "2026-08-03T12:00+08"},
	}
	for i, refs := range valid {
		mkIntervalSpan(t, a, emp, refs, spans[i][0], spans[i][1])
	}

	bad := []string{
		`{}`,
		`{"clock_event_ids": []}`,
		`{"clock_event_ids": ["evt-1"], "correction_id": "00000000-0000-0000-0000-000000000001"}`,
		`{"correction_id": "00000000-0000-0000-0000-000000000001", "assumption": "x"}`,
	}
	// Invalid shapes must reach the CHECK, not the overlap exclusion:
	// each uses a span disjoint from every live row.
	for i, refs := range bad {
		start := 13 + i
		_, err := appPool.Exec(ctx(t),
			`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs)
			 VALUES ($1, $2, $3, $4, 'Asia/Singapore', '2026-08-03', 'work', 'clock', $5::jsonb)`,
			a, emp,
			formatHour("2026-08-03", start), formatHour("2026-08-03", start+1), refs)
		expectSQLError(t, err, "23514", "attendance_interval_source_refs_check", "interval refs "+refs)
	}
}

func formatHour(date string, h int) string {
	return fmt.Sprintf("%sT%02d:00+08", date, h)
}

// A payroll line must cite its timesheet lines, the terms row, the rule
// versions and the calculation that produced it.
func TestPayrollLineageMandatory(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	period := mkPeriod(t, a, "PP")
	mkPayCode(t, "BASIC")
	run := mkRun(t, a, period)

	_, err := appPool.Exec(ctx(t),
		`INSERT INTO payroll_line (employer_id, run_id, employee_id, component, pay_code, amount, cpf_treatment, work_period, payable_date, contribution_period, source_refs)
		 VALUES ($1, $2, $3, 'basic', 'BASIC', 2600, 'OW', '2026-08', '2026-09-07', '2026-08', '{"timesheet_line_ids": []}'::jsonb)`,
		a, run, emp)
	expectSQLError(t, err, "23514", "payroll_line_source_refs_check", "line with partial lineage")

	id := queryID(t, appPool,
		`INSERT INTO payroll_line (employer_id, run_id, employee_id, component, pay_code, amount, cpf_treatment, work_period, payable_date, contribution_period, source_refs)
		 VALUES ($1, $2, $3, 'basic', 'BASIC', 2600, 'OW', '2026-08', '2026-09-07', '2026-08', $4::jsonb) RETURNING id`,
		a, run, emp, lineRefs(t))
	if id == "" {
		t.Fatal("expected line id")
	}
}

// An interval's employee and its roster assignment's employee must match,
// even within one employer: the composite foreign key rejects the link.
func TestAttendanceAssignmentEmployeeMatch(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp1 := mkEmployee(t, a, "E001")
	emp2 := mkEmployee(t, a, "E002")
	_, prg := mkClientProgramme(t, a, key(t))
	_, rev := mkRoster(t, a, prg, "2026-08-03")
	tpl := mkTemplate(t, a, "DAY")
	asg1 := mkAssignment(t, a, rev, emp1, tpl, "2026-08-03")

	// emp2's interval may not point at emp1's assignment: same employer,
	// wrong employee.
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, roster_assignment_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, $3, '2026-08-03T09:00+08', '2026-08-03T18:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e"]}')`,
		a, emp2, asg1)
	expectSQLError(t, err, "23503", "attendance_interval_employer_id_employee_id_roster_assignm_fkey", "interval emp2 -> assignment emp1")

	// Same rule on the daily record.
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO attendance_record (employer_id, employee_id, business_date, roster_assignment_id, actual_min, actual_us)
		 VALUES ($1, $2, '2026-08-03', $3, 480, 28800000000)`,
		a, emp2, asg1)
	expectSQLError(t, err, "23503", "attendance_record_employer_id_employee_id_roster_assignmen_fkey", "record emp2 -> assignment emp1")

	// And the matching link succeeds.
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, roster_assignment_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, $3, '2026-08-03T09:00+08', '2026-08-03T18:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e"]}')`,
		a, emp1, asg1)
	expectOK(t, err, "interval emp1 -> assignment emp1")
}

// Lineage may not cross employers: lines, allocations and assignments
// follow their rows' employer.
func TestLineageFollowsEmployee(t *testing.T) {
	a := mkEmployer(t, key(t, "A"), "UEN-"+key(t, "A"))
	b := mkEmployer(t, key(t, "B"), "UEN-"+key(t, "B"))
	empA := mkEmployee(t, a, "E001")
	_ = mkEmployee(t, b, "E001")
	periodA := mkPeriod(t, a, "PP")
	periodB := mkPeriod(t, b, "PP")
	mkPayCode(t, "OT15")

	sheetB := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id)
		 SELECT $1, id, $2 FROM employee WHERE employer_id = $1 AND employee_no = 'E001' RETURNING id`,
		b, periodB)
	_ = sheetB

	// A line under employer A may not point at employer B's sheet.
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, (SELECT id FROM timesheet WHERE employer_id = $2 LIMIT 1), 'OT15', 1, 3600000000, $3)`,
		a, b, periodA)
	expectSQLError(t, err, "23503", "timesheet_line_employer_id_timesheet_id_fkey", "line A -> sheet B")

	// An allocation may not join employer A's line to employer B's interval.
	// The employee-match trigger rejects it before FK validation; the
	// composite FKs remain as defense in depth.
	sheetA := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, empA, periodA)
	lineA := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheetA, periodA)
	empB := queryID(t, appPool,
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`, b)
	intervalB := mkInterval(t, b, empB, `{"clock_event_ids": ["evt-9"]}`)
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
		a, lineA, intervalB)
	expectSQLError(t, err, "P0001", "different employees", "allocation A -> interval B")

	// Same employer, different employees: also rejected by the trigger.
	empA2 := mkEmployee(t, a, "E002")
	intervalA2 := mkIntervalSpan(t, a, empA2, `{"clock_event_ids": ["evt-10"]}`,
		"2026-08-03T09:00+08", "2026-08-03T10:00+08")
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
		a, lineA, intervalA2)
	expectSQLError(t, err, "P0001", "different employees", "allocation empA -> interval empA2")

	// One interval feeds at most one sheet chain: the same employee's
	// non-overlapping second period may not re-link the interval.
	periodA2 := mkPeriodSpan(t, a, "PP2", "2026-09-01", "2026-09-30")
	sheetA2 := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, empA, periodA2)
	lineA2 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheetA2, periodA2)
	ivOwn := mkIntervalSpan(t, a, empA, `{"clock_event_ids": ["evt-11"]}`,
		"2026-08-03T13:00+08", "2026-08-03T14:00+08")
	exec(t, appPool,
		`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
		a, lineA, ivOwn)
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
		a, lineA2, ivOwn)
	expectSQLError(t, err, "P0001", "another employee or period", "cross-period double allocation")
}

// A run pins its inputs hash, rule versions, calculation version and
// rounding conventions; none may be omitted.
func TestRunVersionsMandatory(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	period := mkPeriod(t, a, "PP")

	_, err := appPool.Exec(ctx(t),
		`INSERT INTO payroll_run (employer_id, pay_period_id, inputs_hash, calc_version, rounding_conventions)
		 VALUES ($1, $2, 'h', 'c', '{}')`, a, period)
	expectSQLError(t, err, "23502", "rule_versions", "run without rule_versions")

	_, err = appPool.Exec(ctx(t),
		`INSERT INTO payroll_run (employer_id, pay_period_id, inputs_hash, rule_versions, rounding_conventions)
		 VALUES ($1, $2, 'h', '{}', '{}')`, a, period)
	expectSQLError(t, err, "23502", "calc_version", "run without calc_version")

	_, err = appPool.Exec(ctx(t),
		`INSERT INTO payroll_run (employer_id, pay_period_id, rule_versions, calc_version, rounding_conventions)
		 VALUES ($1, $2, '{}', 'c', '{}')`, a, period)
	expectSQLError(t, err, "23502", "inputs_hash", "run without inputs_hash")

	mkRun(t, a, period)
}

// The end-to-end reproducibility equation (same immutable inputs + same
// rule versions + same calculation version = same payroll output) is
// specified in database-design §8 and executes with the payroll
// calculation slice, which owns the calculator. This gate proves its
// foundation: frozen runs, pinned versions, mandatory lineage.
func TestReproducibilityDeferred(t *testing.T) {
	t.Skip("deferred by design to the payroll slice; contract in database-design §8")
}
