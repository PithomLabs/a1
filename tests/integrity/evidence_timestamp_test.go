package integrity

import (
	"strings"
	"testing"
	"time"
)

// Stage D evidence timestamps (§14D): record-creation metadata cannot
// be backdated, while genuine event times stay honest. All inserts
// run as the app role with persisted-state re-reads.

func createdYear(t *testing.T, table, id string) int {
	t.Helper()
	var year int
	err := appPool.QueryRow(ctx(t),
		`SELECT EXTRACT(YEAR FROM created_at)::int FROM `+table+` WHERE id = $1::uuid`,
		id).Scan(&year)
	expectOK(t, err, "read created_at from "+table)
	return year
}

// A caller-supplied backdated created_at on a clock event normalizes
// to now; the event instant and source offset survive verbatim.
func TestClockEventCreatedAtNormalized(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := queryID(t, appPool,
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind,
		  source, source_event_id, created_at)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-03T09:00:00+08:00', 'Asia/Singapore', 'in',
		  'vendor-x', $3, '2020-01-01T00:00:00Z')
		 RETURNING id`,
		a, emp, key(t))
	if got := createdYear(t, "clock_event", id); got != time.Now().Year() {
		t.Fatalf("created year=%d want normalization to now", got)
	}
	var at, tz, src string
	err := appPool.QueryRow(ctx(t),
		`SELECT occurred_at::text, timezone, source_event_id FROM clock_event WHERE id = $1::uuid`,
		id).Scan(&at, &tz, &src)
	expectOK(t, err, "read event time")
	if tz != "Asia/Singapore" || src != key(t) {
		t.Fatalf("event=(%s %s %s) want the preserved source facts", at, tz, src)
	}
}

// Same rule for attendance intervals: creation metadata normalizes,
// business facts (bounds, employment date) do not move.
func TestIntervalCreatedAtNormalized(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, created_at)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T18:00+08', 'Asia/Singapore',
		  '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e"]}', '2020-01-01T00:00:00Z')
		 RETURNING id`,
		a, emp)
	if got := createdYear(t, "attendance_interval", id); got != time.Now().Year() {
		t.Fatalf("created year=%d want normalization to now", got)
	}
	var start, end, date string
	err := appPool.QueryRow(ctx(t),
		`SELECT interval_start::text, interval_end::text, local_date::text
		 FROM attendance_interval WHERE id = $1::uuid`, id).Scan(&start, &end, &date)
	expectOK(t, err, "read interval bounds")
	if date != "2026-08-03" {
		t.Fatalf("local_date=%s want the employment date pinned", date)
	}
}

// Correction-group segments are evidence too: their created_at
// normalizes the same way.
func TestCorrectionSegmentCreatedAtNormalized(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, pred)
	var seg string
	err = tx.QueryRow(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id, created_at)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T17:00+08', 'Asia/Singapore',
		  '2026-08-03', 'work', 'correction', $3::jsonb, $4, $5, '2020-01-01T00:00:00Z')
		 RETURNING id::text`,
		a, emp, segRefs(g, pred), pred, g).Scan(&seg)
	expectOK(t, err, "insert backdated segment")
	expectOK(t, tx.Commit(ctx(t)), "commit group")
	if got := createdYear(t, "attendance_interval", seg); got != time.Now().Year() {
		t.Fatalf("created year=%d want normalization to now", got)
	}
}

// Trigger-owned fields reject caller control: basis_seq is assigned
// by the barrier, never supplied or rewritten.
func TestBasisSeqCallerControlRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind,
		  source, source_event_id, basis_seq)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in',
		  'vendor-x', $3, 5)`,
		a, emp, key(t))
	expectSQLError(t, err, "P0001", "omit it", "supplied basis_seq")
	id := queryID(t, appPool,
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind,
		  source, source_event_id)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in',
		  'vendor-x', $3)
		 RETURNING id`,
		a, emp, key(t, "ok"))
	_, err = appPool.Exec(ctx(t),
		`UPDATE clock_event SET basis_seq = 7 WHERE id = $1::uuid`, id)
	expectSQLError(t, err, "42501", "", "rewritten basis_seq")
	var seq int64
	err = appPool.QueryRow(ctx(t),
		`SELECT basis_seq FROM clock_event WHERE id = $1::uuid`, id).Scan(&seq)
	expectOK(t, err, "read assigned seq")
	if seq <= 0 {
		t.Fatalf("seq=%d want the barrier-assigned version", seq)
	}
}

// H4 backstop: direct SQL cannot store an unknown programme
// timezone either; the probe rejects it at write time.
func TestProgrammeTimezoneProbeRejectsUnknown(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	client, _ := mkClientProgramme(t, a, key(t))
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO programme (employer_id, client_id, code, name, timezone)
		 VALUES ($1, $2, 'BAD', 'Bad', 'Mars/Olympus')`,
		a, client)
	if err == nil {
		t.Fatal("expected an unknown timezone to fail")
	}
	if !strings.Contains(err.Error(), "not recognized") {
		t.Fatalf("err=%v want the zone probe to name it", err)
	}
	id := queryID(t, appPool,
		`INSERT INTO programme (employer_id, client_id, code, name, timezone)
		 VALUES ($1, $2, 'OK', 'Ok', 'Pacific/Auckland') RETURNING id`,
		a, client)
	var tz string
	err = appPool.QueryRow(ctx(t),
		`SELECT timezone FROM programme WHERE id = $1::uuid`, id).Scan(&tz)
	expectOK(t, err, "read timezone")
	if tz != "Pacific/Auckland" {
		t.Fatalf("timezone=%s want the accepted zone", tz)
	}
}

// A genuinely late-arriving source event stays representable: its
// old event time persists exactly, with an honest creation stamp.
func TestLateArrivingSourceEventRepresentable(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := queryID(t, appPool,
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind,
		  source, source_event_id)
		 VALUES (gen_random_uuid(), $1, $2, '2025-01-15T09:00:00+08:00', 'Asia/Singapore', 'in',
		  'vendor-x', $3)
		 RETURNING id`,
		a, emp, key(t))
	if got := createdYear(t, "clock_event", id); got != time.Now().Year() {
		t.Fatalf("created year=%d want an honest now stamp", got)
	}
	var at string
	err := appPool.QueryRow(ctx(t),
		`SELECT occurred_at::text FROM clock_event WHERE id = $1::uuid`, id).Scan(&at)
	expectOK(t, err, "read occurred_at")
	if !strings.Contains(at, "2025-01-15") {
		t.Fatalf("occurred_at=%s want the old event time preserved", at)
	}
}
