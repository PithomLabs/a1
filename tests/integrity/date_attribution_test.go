package integrity

import (
	"testing"
)

// A3 business-date attribution (§14A): local_date is pinned to the
// employment-local start date by trigger, the zone maps from the
// employer's jurisdiction, and the vendor-supplied row timezone is
// never consulted. All as the app role with re-reads.

func mkIntervalFull(t *testing.T, employerID, employeeID, tz, date, start, end string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, $3, $4, $5, $6::date, 'work', 'clock', '{"clock_event_ids": ["e1"]}')
		 RETURNING id`,
		employerID, employeeID, start, end, tz, date)
}

func TestLocalDateSplitPositive(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	// The canonical cross-midnight split: each piece carries its own
	// employment-local date, including the exact-midnight boundary.
	mkIntervalFull(t, a, emp, "Asia/Singapore", "2026-08-03",
		"2026-08-03T22:00+08", "2026-08-04T00:00+08")
	mkIntervalFull(t, a, emp, "Asia/Singapore", "2026-08-04",
		"2026-08-04T00:00+08", "2026-08-04T06:00+08")
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1`, a).Scan(&n)
	expectOK(t, err, "count split")
	if n != 2 {
		t.Fatalf("intervals=%d want the committed pair", n)
	}
}

func TestLocalDateUnsplitRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-03T22:00+08', '2026-08-04T06:00+08', 'Asia/Singapore',
		  '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e1"]}')`,
		a, emp)
	expectSQLError(t, err, "P0001", "neither same-date nor exact employment-local midnight", "unsplit span")
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1`, a).Scan(&n)
	expectOK(t, err, "count residue")
	if n != 0 {
		t.Fatalf("intervals=%d want zero residue", n)
	}
}

func TestLocalDateMisdatedRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T17:00+08', 'Asia/Singapore',
		  '2026-08-04', 'work', 'clock', '{"clock_event_ids": ["e1"]}')`,
		a, emp)
	expectSQLError(t, err, "P0001", "does not match employment-local start date", "misdated row")
}

func TestLocalDateMidnightBoundary(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	// End exactly at local midnight: allowed with the start date.
	mkIntervalFull(t, a, emp, "Asia/Singapore", "2026-08-03",
		"2026-08-03T22:00+08", "2026-08-04T00:00+08")
	// One second past midnight: a second date is touched, so the
	// single-date row is rejected.
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-05T22:00+08', '2026-08-06T00:00:01+08', 'Asia/Singapore',
		  $3, 'work', 'clock', '{"clock_event_ids": ["e2"]}')`,
		a, emp, "2026-08-05")
	expectSQLError(t, err, "P0001", "neither same-date nor exact employment-local midnight", "second past midnight")
	// Exact-midnight end claimed under the next date: the start-date
	// rule rejects it first.
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-07T22:00+08', '2026-08-08T00:00+08', 'Asia/Singapore',
		  '2026-08-08', 'work', 'clock', '{"clock_event_ids": ["e3"]}')`,
		a, emp)
	expectSQLError(t, err, "P0001", "does not match employment-local start date", "midnight claimed next-date")
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1`, a).Scan(&n)
	expectOK(t, err, "count residue")
	if n != 1 {
		t.Fatalf("intervals=%d want only the exact-midnight row", n)
	}
}

func TestLocalDateVendorTimezoneIgnored(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	// 00:30+08 is Aug 4 in Singapore but Aug 3 in New York. Claiming
	// the vendor-zone date must fail: only employment zone governs.
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-04T00:30+08', '2026-08-04T01:30+08', 'America/New_York',
		  '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e1"]}')`,
		a, emp)
	expectSQLError(t, err, "P0001", "does not match employment-local start date", "vendor-zone date")
	// Control: the employment-zone date commits whatever the row
	// timezone claims.
	mkIntervalFull(t, a, emp, "America/New_York", "2026-08-04",
		"2026-08-04T00:30+08", "2026-08-04T01:30+08")
}

func TestLocalDateUnknownJurisdiction(t *testing.T) {
	b := queryID(t, appPool,
		`INSERT INTO employer (code, name, uen, address, jurisdiction)
		 VALUES ($1, $2, $3, 'x', 'XX') RETURNING id`,
		key(t), key(t)+" Corp", "UEN-"+key(t))
	emp := mkEmployee(t, b, "E001")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T17:00+08', 'Asia/Singapore',
		  '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e1"]}')`,
		b, emp)
	expectSQLError(t, err, "P0001", "unknown employment jurisdiction", "unguessed zone")
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1`, b).Scan(&n)
	expectOK(t, err, "count residue")
	if n != 0 {
		t.Fatalf("intervals=%d want zero residue", n)
	}
}
