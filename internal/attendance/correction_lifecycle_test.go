package attendance

import (
	"testing"

	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// A4 two-segment same-sheet lifecycle (§5): a cross-midnight
// correction lands as two current segments on one sheet chain, the
// reissued draft regenerates to the summed duration, readiness
// conflicts until the correction approves, and original evidence
// stays untouched.
func TestCorrectionTwoSegmentSheetLifecycle(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyChain(t, pool, "L2")
	importCSV(t, pool, emp, spanCSV(no, "l2-1", "l2-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	h1 := sheetHours(t, pool, emp, v1)
	orig := liveWorkID(t, pool, emp)
	var origStart, origEnd string
	if err := pool.QueryRow(ctx(t),
		`SELECT interval_start::text, interval_end::text FROM attendance_interval WHERE id = $1::uuid`,
		orig).Scan(&origStart, &origEnd); err != nil {
		t.Fatalf("orig bounds: %v", err)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	// Correct the day span into an overnight span: splitRun must
	// emit exactly two date-clean segments.
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T22:00:00+08:00", End: "2026-08-04T06:00:00+08:00",
		Kind: "work", Reason: "night shift fix",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("segments=%d want the cross-midnight pair", len(rep.SegmentIDs))
	}
	type seg struct {
		date    string
		us      int64
		live    bool
		supers  string
		groupID string
	}
	var segs []seg
	var total int64
	for _, id := range rep.SegmentIDs {
		var s seg
		err := pool.QueryRow(ctx(t),
			`SELECT local_date::text, EXTRACT(EPOCH FROM interval_end - interval_start)::bigint,
			 is_current, supersedes_id::text, correction_group_id::text
			 FROM attendance_interval WHERE id = $1::uuid`, id).
			Scan(&s.date, &s.us, &s.live, &s.supers, &s.groupID)
		if err != nil {
			t.Fatalf("segment %s: %v", id, err)
		}
		segs = append(segs, s)
		total += s.us
	}
	if segs[0].date != "2026-08-03" || segs[1].date != "2026-08-04" {
		t.Fatalf("dates=%v want the split pair", segs)
	}
	if total != 8*3600 {
		t.Fatalf("total=%d want 8h summed across both segments", total)
	}
	for _, s := range segs {
		if !s.live || s.supers != orig || s.groupID != rep.GroupID {
			t.Fatalf("seg=%+v want current, superseding orig, in group", s)
		}
	}
	var links int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group_predecessor WHERE group_id = $1::uuid`,
		rep.GroupID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("links=%d err=%v want the single predecessor binding", links, err)
	}
	var live bool
	var startNow, endNow string
	if err := pool.QueryRow(ctx(t),
		`SELECT is_current, interval_start::text, interval_end::text FROM attendance_interval WHERE id = $1::uuid`,
		orig).Scan(&live, &startNow, &endNow); err != nil || live {
		t.Fatalf("live=%v err=%v want orig historical", live, err)
	}
	if startNow != origStart || endNow != origEnd {
		t.Fatal("original evidence mutated by the correction")
	}
	// The open draft regenerated to the summed duration while the
	// approved predecessor still shows its own hours.
	if h := sheetHours(t, pool, emp, v2.ID); h != 8 {
		t.Fatalf("v2=%.1fh want the regenerated 8h", h)
	}
	if h := sheetHours(t, pool, emp, v1); h != h1 {
		t.Fatalf("v1=%.1fh want the untouched %.1fh", h, h1)
	}
	r2, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r2.Ready || len(r2.AttendanceConflicts) != 1 {
		t.Fatalf("r2=%+v want the conflict blocking readiness", r2)
	}
	approveSheet(t, pool, emp, v2.ID)
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !r3.Ready || len(r3.AttendanceConflicts) != 0 {
		t.Fatalf("r3=%+v want ready once the correction approves", r3)
	}
}
