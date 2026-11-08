package attendance

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/timesheet"
)

func recordMicros(t *testing.T, pool *pgxpool.Pool, emp, date string) (actualUs, paidUs int64, actualMin, paidMin float64) {
	t.Helper()
	if err := pool.QueryRow(ctx(t),
		`SELECT actual_us, paid_us, actual_min, paid_min FROM attendance_record
		 WHERE employer_id = $1 AND business_date = $2::date`,
		emp, date).Scan(&actualUs, &paidUs, &actualMin, &paidMin); err != nil {
		t.Fatalf("record %s: %v", date, err)
	}
	return actualUs, paidUs, actualMin, paidMin
}

func lineMicros(t *testing.T, pool *pgxpool.Pool, emp, sheetID string) (us int64, hours float64) {
	t.Helper()
	got, err := timesheet.Get(ctx(t), pool, emp, sheetID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("lines=%+v want one", got.Lines)
	}
	return got.Lines[0].HoursUs, got.Lines[0].Hours
}

// A one-second work segment aggregates exactly: the microsecond totals
// are authoritative and the legacy decimal displays quantize explicitly
// in Go, never implicitly in the database.
func TestDurationOneSecond(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DU")
	importCSV(t, pool, emp, spanCSV(no, "du-1", "du-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T09:00:01+08:00",
		Kind: "work", Reason: "one second",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("correct: %v", err)
	}
	actualUs, paidUs, actualMin, paidMin := recordMicros(t, pool, emp, "2026-08-03")
	if actualUs != 1000000 || paidUs != 1000000 {
		t.Fatalf("us=%d/%d want 1000000/1000000", actualUs, paidUs)
	}
	if actualMin != 0.02 || paidMin != 0.02 {
		t.Fatalf("min=%.4f/%.4f want explicit 0.02/0.02", actualMin, paidMin)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, mkPeriod(t, pool, emp), "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	us, hours := lineMicros(t, pool, emp, gen.Generated[0].TimesheetID)
	if us != 1000000 || hours != 0.0 {
		t.Fatalf("line us=%d hours=%.4f want 1000000 and explicit 0.00", us, hours)
	}
}

// Fractional intervals sum in integer microseconds with no float drift:
// 1s + 2s + 3s is exactly 6,000,000µs, and mixed with whole hours the
// total stays exact while the display quantizes explicitly.
func TestDurationAggregationExact(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DA")
	importCSV(t, pool, emp, spanCSV(no, "da-1", "da-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T09:00:01+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "da-3", "da-4",
		"2026-08-03T10:00:00+08:00", "2026-08-03T10:00:02+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "da-5", "da-6",
		"2026-08-03T11:00:00+08:00", "2026-08-03T11:00:03+08:00"))
	actualUs, paidUs, _, _ := recordMicros(t, pool, emp, "2026-08-03")
	if actualUs != 6000000 || paidUs != 6000000 {
		t.Fatalf("us=%d/%d want 6000000/6000000", actualUs, paidUs)
	}
	importCSV(t, pool, emp, spanCSV(no, "da-7", "da-8",
		"2026-08-03T13:00:00+08:00", "2026-08-03T14:00:00+08:00"))
	actualUs, _, _, _ = recordMicros(t, pool, emp, "2026-08-03")
	if actualUs != 3606000000 {
		t.Fatalf("us=%d want 3606000000", actualUs)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, mkPeriod(t, pool, emp), "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	us, hours := lineMicros(t, pool, emp, gen.Generated[0].TimesheetID)
	if us != 3606000000 || hours != 1.0 {
		t.Fatalf("line us=%d hours=%.4f want 3606000000 and explicit 1.00", us, hours)
	}
}

// A one-second Tuesday piece aggregates onto its own date: cross-date
// splits keep per-date microsecond totals exact end to end.
func TestDurationSplitPiece(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DS2", "2026-08-04")
	importCSV(t, pool, emp, spanCSV(no, "ds-1", "ds-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T14:00:00+08:00", End: "2026-08-04T00:00:01+08:00",
		Kind: "work", Reason: "overnight",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("correct: %v", err)
	}
	monUs, _, _, _ := recordMicros(t, pool, emp, "2026-08-03")
	tueUs, tuePaid, _, _ := recordMicros(t, pool, emp, "2026-08-04")
	if monUs != 36000000000 || tueUs != 1000000 || tuePaid != 1000000 {
		t.Fatalf("mon=%d tue=%d/%d want 36000000000 and 1000000/1000000", monUs, tueUs, tuePaid)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, mkPeriod(t, pool, emp), "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	us, hours := lineMicros(t, pool, emp, gen.Generated[0].TimesheetID)
	if us != 36001000000 || hours != 10.0 {
		t.Fatalf("line us=%d hours=%.4f want 36001000000 and explicit 10.00", us, hours)
	}
}

// Paid and unpaid time aggregate separately in microseconds: a 30s paid
// break counts toward paid exactly, and actual covers every live minute.
func TestDurationPaidSplit(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "DP2", "break_paid")
	initial := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,dp-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,dp-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,dp-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,dp-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	importCSV(t, pool, emp, initial)
	importCSV(t, pool, emp, breakCSV(no, "dp-5", "dp-6",
		"2026-08-03T15:00:00+08:00", "2026-08-03T15:00:30+08:00"))
	actualUs, paidUs, _, _ := recordMicros(t, pool, emp, "2026-08-03")
	// Actual covers all 9h live; paid drops only the unpaid lunch hour
	// (the 30s paid tea substitutes work one for one).
	if actualUs != 32400000000 || paidUs != 28800000000 {
		t.Fatalf("us=%d/%d want 32400000000/28800000000", actualUs, paidUs)
	}
	var teaKind, teaStart, teaEnd string
	if err := pool.QueryRow(ctx(t),
		`SELECT kind, interval_start::text, interval_end::text FROM attendance_interval
		 WHERE employer_id = $1 AND is_current AND kind LIKE 'break%'
		   AND interval_start >= '2026-08-03T15:00:00+08:00'`,
		emp).Scan(&teaKind, &teaStart, &teaEnd); err != nil {
		t.Fatalf("tea piece: %v", err)
	}
	if teaKind != "break_paid" || !sameInstant(t, teaStart, "2026-08-03T15:00:00+08:00") ||
		!sameInstant(t, teaEnd, "2026-08-03T15:00:30+08:00") {
		t.Fatalf("tea=%s %s/%s want the 30s paid piece", teaKind, teaStart, teaEnd)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, mkPeriod(t, pool, emp), "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	us, hours := lineMicros(t, pool, emp, gen.Generated[0].TimesheetID)
	if us != 28800000000 || hours != 8.0 {
		t.Fatalf("line us=%d hours=%.4f want 28800000000 and explicit 8.00", us, hours)
	}
}
