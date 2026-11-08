package integrity

import (
	"testing"
)

// C4: the single-chain and scope guards protect UPDATE as well as
// INSERT. (Allocations join lines to intervals in a bipartite link
// with no self-reference, so there is no self-link shape to test;
// lineage cycles through supersedes are C3's trigger.)

func linkLine(t *testing.T, employer, line, interval string) {
	t.Helper()
	exec(t, appPool,
		`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
		employer, line, interval)
}

func linkTarget(t *testing.T, employer, interval, excludeLine string) string {
	t.Helper()
	var id string
	err := appPool.QueryRow(ctx(t),
		`SELECT line_id::text FROM timesheet_line_interval
		 WHERE employer_id = $1 AND interval_id = $2 AND line_id::text <> $3`,
		employer, interval, excludeLine).Scan(&id)
	expectOK(t, err, "read surviving link")
	return id
}

// Re-pointing an allocation to another employee's chain is rejected
// and the original link survives.
func TestChainUpdateCrossEmployeeRejected(t *testing.T) {
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
	line1 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet1, period)
	line2 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet2, period)
	iv := mkInterval(t, a, e1, `{"clock_event_ids": ["e"]}`)
	linkLine(t, a, line1, iv)
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet_line_interval SET line_id = $1 WHERE employer_id = $2 AND interval_id = $3`,
		line2, a, iv)
	expectSQLError(t, err, "P0001", "different employees", "chain UPDATE to another employee")
	_ = linkTarget(t, a, iv, line2)
}

// Re-pointing an allocation to another pay period's chain is rejected
// even for the same employee.
func TestChainUpdateCrossPeriodRejected(t *testing.T) {
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
	line1 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet1, p1)
	line2 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet2, p2)
	iv := mkInterval(t, a, emp, `{"clock_event_ids": ["e"]}`)
	linkLine(t, a, line1, iv)
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet_line_interval SET line_id = $1 WHERE employer_id = $2 AND interval_id = $3`,
		line2, a, iv)
	expectSQLError(t, err, "P0001", "another employee or period", "chain UPDATE to another period")
	_ = linkTarget(t, a, iv, line2)
}

// Swapping the interval endpoint to another employee's interval is
// rejected by the employee-match guard.
func TestChainUpdateIntervalEndpointRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	e1 := mkEmployee(t, a, "E001")
	e2 := mkEmployee(t, a, "E002")
	period := mkPeriod(t, a, "PP")
	mkPayCode(t, "OT15")
	sheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, e1, period)
	line := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet, period)
	iv1 := mkInterval(t, a, e1, `{"clock_event_ids": ["e1"]}`)
	iv2 := mkInterval(t, a, e2, `{"clock_event_ids": ["e2"]}`)
	linkLine(t, a, line, iv1)
	_, err := appPool.Exec(ctx(t),
		`UPDATE timesheet_line_interval SET interval_id = $1 WHERE employer_id = $2 AND line_id = $3`,
		iv2, a, line)
	expectSQLError(t, err, "P0001", "different employees", "chain UPDATE to another employee's interval")
	var got string
	err = appPool.QueryRow(ctx(t),
		`SELECT interval_id::text FROM timesheet_line_interval WHERE employer_id = $1 AND line_id = $2`,
		a, line).Scan(&got)
	expectOK(t, err, "read surviving endpoint")
	if got != iv1 {
		t.Fatalf("endpoint moved to %s, want the original %s", got, iv1)
	}
}

// Moving an allocation between two lines of the same sheet chain
// stays legal: same employee, same period, still one chain.
func TestChainUpdateSameChainAllowed(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	period := mkPeriod(t, a, "PP")
	mkPayCode(t, "OT15")
	sheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, emp, period)
	line1 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet, period)
	line2 := queryID(t, appPool,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, 'OT15', 1, 3600000000, $3) RETURNING id`,
		a, sheet, period)
	iv := mkInterval(t, a, emp, `{"clock_event_ids": ["e"]}`)
	linkLine(t, a, line1, iv)
	exec(t, appPool,
		`UPDATE timesheet_line_interval SET line_id = $1 WHERE employer_id = $2 AND interval_id = $3`,
		line2, a, iv)
	_ = linkTarget(t, a, iv, line1)
}
