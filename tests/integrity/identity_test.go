package integrity

import (
	"testing"
)

// C5/C6: linked lines and sheets keep their identity. Moves and
// transplants are rejected; unlinked drafts stay fixable and status
// transitions keep working.

func TestLineMoveRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	e1 := mkEmployee(t, a, "E001")
	e2 := mkEmployee(t, a, "E002")
	period := mkPeriod(t, a, "PP")
	mkPayCode(t, "OT15")
	sheet1 := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, e1, period)
	sheet2 := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, e2, period)
	line := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet1, period)
	iv := mkInterval(t, a, e1, `{"clock_event_ids": ["e"]}`)
	linkLine(t, a, line, iv)
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet_line SET timesheet_id = $1 WHERE id = $2`, sheet2, line)
	expectSQLError(t, err, "P0001", "parent identity is frozen", "linked line moved to another sheet")
	var got string
	err = appPool.QueryRow(ctx(t),
		`SELECT timesheet_id::text FROM timesheet_line WHERE id = $1`, line).Scan(&got)
	expectOK(t, err, "read line parent")
	if got != sheet1 {
		t.Fatalf("line parent moved to %s, want %s", got, sheet1)
	}
}

func TestLineUnlinkedMoveAllowed(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	p1 := mkPeriod(t, a, "PP")
	p2 := mkPeriodSpan(t, a, "PP2", "2026-09-01", "2026-09-30")
	mkPayCode(t, "OT15")
	sheet1 := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, emp, p1)
	sheet2 := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, emp, p2)
	line := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet1, p1)
	// No interval links: re-parenting an empty draft line stays legal.
	exec(t, appPool,
		`UPDATE timesheet_line SET timesheet_id = $1, pay_period_id = $2 WHERE id = $3`,
		sheet2, p2, line)
	// Draft measure edits still work on linked lines.
	iv := mkInterval(t, a, emp, `{"clock_event_ids": ["e"]}`)
	linkLine(t, a, line, iv)
	exec(t, appPool, `UPDATE timesheet_line SET hours = 2 WHERE id = $1`, line)
}

func TestSheetIdentityFrozen(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	e1 := mkEmployee(t, a, "E001")
	e2 := mkEmployee(t, a, "E002")
	p1 := mkPeriod(t, a, "PP")
	p2 := mkPeriodSpan(t, a, "PP2", "2026-09-01", "2026-09-30")
	mkPayCode(t, "OT15")
	sheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, e1, p1)
	queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet, p1)
	for _, tc := range []struct{ col, val, what string }{
		{"employee_id", e2, "employee transplant"},
		{"pay_period_id", p2, "period transplant"},
	} {
		_, err := appPool.Exec(ctx(t),
			`UPDATE timesheet SET `+tc.col+` = $1 WHERE id = $2`, tc.val, sheet)
		expectSQLError(t, err, "P0001", "identity is frozen", tc.what)
	}
	// A status change bundled with a transplant is still a transplant.
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'x', approved_at = now(), employee_id = $1 WHERE id = $2`,
		e2, sheet)
	expectSQLError(t, err, "P0001", "identity is frozen", "approve bundled with transplant")
	requireStatus(t, "timesheet", sheet, "draft")
	// ...while a plain status transition on the same row succeeds.
	exec(t, appPool,
		`UPDATE timesheet SET status = 'submitted' WHERE id = $1`, sheet)
	requireStatus(t, "timesheet", sheet, "submitted")
}

func TestSheetIdentityFrozenDecided(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	e1 := mkEmployee(t, a, "E001")
	e2 := mkEmployee(t, a, "E002")
	period := mkPeriod(t, a, "PP")
	mkPayCode(t, "OT15")
	sheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, e1, period)
	queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet, period)
	exec(t, appPool,
		`UPDATE timesheet SET status = 'approved', approved_by = 'test', approved_at = now() WHERE id = $1`, sheet)
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet SET employee_id = $1 WHERE id = $2`, e2, sheet)
	expectSQLError(t, err, "P0001", "identity is frozen", "decided-sheet transplant")
	var got string
	err = appPool.QueryRow(ctx(t),
		`SELECT employee_id::text FROM timesheet WHERE id = $1`, sheet).Scan(&got)
	expectOK(t, err, "read sheet employee")
	if got != e1 {
		t.Fatalf("sheet employee moved to %s, want %s", got, e1)
	}
}
