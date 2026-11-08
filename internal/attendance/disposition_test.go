package attendance

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

// secondEmployee adds E002 under the same employer for cross-employee
// mismatch cases.
func secondEmployee(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: "E002", LegalName: "E002", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("second employee: %v", err)
	}
	return e.EmployeeNo
}

// dayChain is a single plain work-day roster: one 09:00-18:00 work
// segment, no planned breaks, so duplicate-span tests pair cleanly.
func dayChain(t *testing.T, pool *pgxpool.Pool, tag string, extraDates ...string) (string, string) {
	t.Helper()
	return chainTpl(t, pool, tag, "2026-08-03", "DAY", "09:00", "18:00", false, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true},
	}, extraDates...)
}

func importCSV(t *testing.T, pool *pgxpool.Pool, emp, csv string) *Report {
	t.Helper()
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return rep
}

func eventID(t *testing.T, pool *pgxpool.Pool, emp, sourceEventID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM clock_event WHERE employer_id = $1 AND source_event_id = $2`,
		emp, sourceEventID).Scan(&id); err != nil {
		t.Fatalf("event %s: %v", sourceEventID, err)
	}
	return id
}

func mkPeriod(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay code: %v", err)
	}
	var id string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP-0826', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id::text`, emp).Scan(&id); err != nil {
		t.Fatalf("period: %v", err)
	}
	return id
}

func spanCSV(no, inID, outID, inAt, outAt string) string {
	return `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,` + inID + `,` + no + `,` + inAt + `,Asia/Singapore,in
vendor-x,` + outID + `,` + no + `,` + outAt + `,Asia/Singapore,out
`
}

func openException(t *testing.T, pool *pgxpool.Pool, emp, code string) (count int, resolved bool) {
	t.Helper()
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*), bool_and(resolved) FROM attendance_exception
		 WHERE employer_id = $1 AND code = $2`, emp, code).Scan(&count, &resolved); err != nil {
		t.Fatalf("exception %s: %v", code, err)
	}
	return count, resolved
}

// A duplicate vendor delivery under fresh source IDs raises
// overlapping_span and blocks generation; confirming both endpoints as
// duplicates of the original events unblocks exactly one allocation.
func TestDispositionDuplicateClearsBlock(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DD")
	rep1 := importCSV(t, pool, emp, spanCSV(no, "dd-1", "dd-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	if rep1.IntervalsBuilt != 1 {
		t.Fatalf("built=%d want 1", rep1.IntervalsBuilt)
	}
	rep2 := importCSV(t, pool, emp, spanCSV(no, "dd-3", "dd-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	if rep2.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want 0 (duplicate must not double-pair)", rep2.IntervalsBuilt)
	}
	found := false
	for _, e := range rep2.Exceptions {
		if e.Code == ExcOverlappingSpan {
			found = true
		}
	}
	if !found {
		t.Fatalf("exceptions=%+v want overlapping_span", rep2.Exceptions)
	}
	period := mkPeriod(t, pool, emp)
	gen1, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate before disposition: %v", err)
	}
	if len(gen1.Generated) != 0 || len(gen1.Skipped) != 1 {
		t.Fatalf("gen=%+v want the employee skipped while the duplicate is unresolved", gen1)
	}

	dupIn, dupOut := eventID(t, pool, emp, "dd-3"), eventID(t, pool, emp, "dd-4")
	origIn, origOut := eventID(t, pool, emp, "dd-1"), eventID(t, pool, emp, "dd-2")
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: dupIn, CanonicalID: origIn, Reason: "vendor resend under fresh IDs",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("disposition in: %v", err)
	}
	drep, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: dupOut, CanonicalID: origOut, Reason: "vendor resend under fresh IDs",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("disposition out: %v", err)
	}
	if drep.AlreadyRecorded {
		t.Fatalf("second endpoint must be a fresh disposition")
	}
	// The exception is retained but resolved; the disposition records
	// the operator decision with reason and attribution.
	if n, resolved := openException(t, pool, emp, ExcOverlappingSpan); n != 1 || !resolved {
		t.Fatalf("overlapping_span rows=%d resolved=%v want 1 retained resolved row", n, resolved)
	}
	var reason, by string
	if err := pool.QueryRow(ctx(t),
		`SELECT reason, decided_by FROM clock_event_disposition WHERE event_id = $1::uuid`,
		dupIn).Scan(&reason, &by); err != nil || reason == "" || by != "operator-1" {
		t.Fatalf("disposition row reason=%q by=%q err=%v", reason, by, err)
	}
	var audits int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'clock_event.disposition'`,
		emp).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows=%d err=%v want 2", audits, err)
	}

	gen2, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate after disposition: %v", err)
	}
	if len(gen2.Generated) != 1 || len(gen2.Skipped) != 0 {
		t.Fatalf("gen=%+v want exactly one sheet", gen2)
	}
	got, err := timesheet.Get(ctx(t), pool, emp, gen2.Generated[0].TimesheetID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].Hours != 9 || len(got.Lines[0].IntervalIDs) != 1 {
		t.Fatalf("line=%+v want one 9h line over one interval", got.Lines)
	}
	sub, err := timesheet.Submit(ctx(t), pool, emp, got.ID, "test-op")
	if err != nil || sub.Status != "submitted" {
		t.Fatalf("submit=%+v err=%v", sub, err)
	}
	app, err := timesheet.Approve(ctx(t), pool, emp, got.ID, "test-op")
	if err != nil || app.Status != "approved" {
		t.Fatalf("approve=%+v err=%v", app, err)
	}
}

// A half-confirmed duplicate still blocks: the unsettled endpoint
// keeps the exception open and generation skipped.
func TestDispositionPartialKeepsBlock(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DP")
	importCSV(t, pool, emp, spanCSV(no, "dp-1", "dp-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "dp-3", "dp-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	period := mkPeriod(t, pool, emp)
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "dp-3"), CanonicalID: eventID(t, pool, emp, "dp-1"),
		Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("disposition: %v", err)
	}
	if n, resolved := openException(t, pool, emp, ExcOverlappingSpan); n != 1 || resolved {
		t.Fatalf("rows=%d resolved=%v want the exception still open", n, resolved)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 0 || len(gen.Skipped) != 1 {
		t.Fatalf("gen=%+v want still skipped with one endpoint unsettled", gen)
	}
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "dp-4"), CanonicalID: eventID(t, pool, emp, "dp-2"),
		Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("disposition out: %v", err)
	}
	gen2, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate after full disposition: %v", err)
	}
	if len(gen2.Generated) != 1 {
		t.Fatalf("gen=%+v want one sheet once both endpoints settle", gen2)
	}
}

// A genuinely overlapping second span is not a duplicate: disposition
// is rejected and the overlap keeps blocking generation.
func TestDispositionRejectsGenuineOverlap(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DG")
	importCSV(t, pool, emp, spanCSV(no, "dg-1", "dg-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "dg-3", "dg-4",
		"2026-08-03T10:00:00+08:00", "2026-08-03T19:00:00+08:00"))
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "dg-3"), CanonicalID: eventID(t, pool, emp, "dg-1"),
		Reason: "looks like a resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatalf("disposition of a different-instant event must fail")
	}
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "dg-4"), CanonicalID: eventID(t, pool, emp, "dg-2"),
		Reason: "looks like a resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatalf("disposition of a different-instant event must fail")
	}
	if n, resolved := openException(t, pool, emp, ExcOverlappingSpan); n != 1 || resolved {
		t.Fatalf("rows=%d resolved=%v want the overlap still open", n, resolved)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, mkPeriod(t, pool, emp), "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 0 || len(gen.Skipped) != 1 {
		t.Fatalf("gen=%+v want the genuine overlap still blocking", gen)
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event_disposition WHERE employer_id = $1`, emp).Scan(&n); err != nil || n != 0 {
		t.Fatalf("disposition rows=%d err=%v want none", n, err)
	}
}

// Repeating the same disposition is a no-op success, not an error and
// not a second row.
func TestDispositionIdempotent(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DI")
	importCSV(t, pool, emp, spanCSV(no, "di-1", "di-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "di-3", "di-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	in := DispositionInput{
		EventID: eventID(t, pool, emp, "di-3"), CanonicalID: eventID(t, pool, emp, "di-1"),
		Reason: "resend",
	}
	first, err := DispositionDuplicate(ctx(t), pool, emp, in, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil || first.AlreadyRecorded {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := DispositionDuplicate(ctx(t), pool, emp, in, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil || !second.AlreadyRecorded {
		t.Fatalf("second=%+v err=%v want idempotent success", second, err)
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event_disposition WHERE event_id = $1::uuid`,
		in.EventID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows=%d err=%v want exactly one", n, err)
	}
}

// The same event cannot be confirmed as a duplicate of two different
// canonical events: the second decision conflicts and the first stands.
func TestDispositionConflict(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DC")
	importCSV(t, pool, emp, spanCSV(no, "dc-1", "dc-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "dc-3", "dc-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	// A second identical delivery gives the event a rival canonical.
	importCSV(t, pool, emp, spanCSV(no, "dc-5", "dc-6",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	dup := eventID(t, pool, emp, "dc-3")
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: dup, CanonicalID: eventID(t, pool, emp, "dc-1"), Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: dup, CanonicalID: eventID(t, pool, emp, "dc-5"), Reason: "changed mind",
	}, "operator-2", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatalf("rival canonical must conflict")
	}
	var canon string
	if err := pool.QueryRow(ctx(t),
		`SELECT duplicate_of_event_id::text FROM clock_event_disposition WHERE event_id = $1::uuid`,
		dup).Scan(&canon); err != nil || canon != eventID(t, pool, emp, "dc-1") {
		t.Fatalf("canon=%s err=%v want the first decision standing", canon, err)
	}
}

func TestDispositionRejectsMismatch(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DM")
	importCSV(t, pool, emp, spanCSV(no, "dm-1", "dm-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "dm-3", "dm-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	other := secondEmployee(t, pool, emp)
	importCSV(t, pool, emp, spanCSV(other, "dm-5", "dm-6",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	cases := map[string]DispositionInput{
		"cross employee": {
			EventID: eventID(t, pool, emp, "dm-5"), CanonicalID: eventID(t, pool, emp, "dm-1"),
			Reason: "x",
		},
		"cross kind": {
			EventID: eventID(t, pool, emp, "dm-3"), CanonicalID: eventID(t, pool, emp, "dm-2"),
			Reason: "x",
		},
		"self reference": {
			EventID: eventID(t, pool, emp, "dm-3"), CanonicalID: eventID(t, pool, emp, "dm-3"),
			Reason: "x",
		},
		"empty reason": {
			EventID: eventID(t, pool, emp, "dm-3"), CanonicalID: eventID(t, pool, emp, "dm-1"),
		},
	}
	for name, in := range cases {
		if _, err := DispositionDuplicate(ctx(t), pool, emp, in, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
			t.Fatalf("%s: must fail", name)
		}
	}
	// Chain: dm-3 confirmed as a duplicate of dm-1 cannot then serve
	// as the canonical event of another duplicate.
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "dm-3"), CanonicalID: eventID(t, pool, emp, "dm-1"),
		Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "dm-4"), CanonicalID: eventID(t, pool, emp, "dm-3"),
		Reason: "chain",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatalf("canonical-is-duplicate: must fail")
	}
}

// An event already allocated into an interval cannot be confirmed as
// a duplicate; corrections own that path.
func TestDispositionRejectsConsumedEvent(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DS")
	importCSV(t, pool, emp, spanCSV(no, "ds-1", "ds-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "ds-3", "ds-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	// The consumed original cannot be the duplicate, even against the
	// still-unpaired redelivery as canonical.
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "ds-1"), CanonicalID: eventID(t, pool, emp, "ds-3"),
		Reason: "backwards",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatalf("consumed event must not be dispositionable")
	}
	// The forward direction stays valid: the unpaired redelivery is
	// the duplicate of the consumed original.
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "ds-3"), CanonicalID: eventID(t, pool, emp, "ds-1"),
		Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("forward: %v", err)
	}
}

// The database trigger enforces the matching rule even for writers
// that bypass the service: same employee, kind and exact instant, and
// a canonical event that is not itself a duplicate.
func TestDispositionTriggerRejectsBypass(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DB")
	importCSV(t, pool, emp, spanCSV(no, "db-1", "db-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "db-3", "db-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	other := secondEmployee(t, pool, emp)
	importCSV(t, pool, emp, spanCSV(other, "db-5", "db-6",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	direct := func(dup, canon string) error {
		_, err := pool.Exec(ctx(t),
			`INSERT INTO clock_event_disposition (employer_id, event_id, disposition, duplicate_of_event_id, reason, decided_by)
			 VALUES ($1, $2::uuid, 'duplicate', $3::uuid, 'bypass', 'test')`,
			emp, dup, canon)
		return err
	}
	if err := direct(eventID(t, pool, emp, "db-3"), eventID(t, pool, emp, "db-1")); err != nil {
		t.Fatalf("valid direct insert: %v", err)
	}
	for name, pair := range map[string][2]string{
		"cross kind":   {eventID(t, pool, emp, "db-4"), eventID(t, pool, emp, "db-1")},
		"cross person": {eventID(t, pool, emp, "db-5"), eventID(t, pool, emp, "db-1")},
		"self":         {eventID(t, pool, emp, "db-4"), eventID(t, pool, emp, "db-4")},
		"chained":      {eventID(t, pool, emp, "db-4"), eventID(t, pool, emp, "db-3")},
	} {
		if err := direct(pair[0], pair[1]); err == nil {
			t.Fatalf("%s: trigger must reject", name)
		}
	}
	// History is insert-only for the app role: no updates, no deletes.
	if _, err := pool.Exec(ctx(t),
		`UPDATE clock_event_disposition SET reason = 'rewritten' WHERE employer_id = $1`, emp); err == nil {
		t.Fatalf("UPDATE disposition must be revoked from app")
	}
	if _, err := pool.Exec(ctx(t),
		`DELETE FROM clock_event_disposition WHERE employer_id = $1`, emp); err == nil {
		t.Fatalf("DELETE disposition must be revoked from app")
	}
}
