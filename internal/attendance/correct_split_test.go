package attendance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

func mkPeriodCode(t *testing.T, pool *pgxpool.Pool, emp, code, start, end string) string {
	t.Helper()
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay code: %v", err)
	}
	var id string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, $2, $3::date, $4::date, '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id::text`, emp, code, start, end).Scan(&id); err != nil {
		t.Fatalf("period %s: %v", code, err)
	}
	return id
}

func sheetHours(t *testing.T, pool *pgxpool.Pool, emp, sheetID string) float64 {
	t.Helper()
	got, err := timesheet.Get(ctx(t), pool, emp, sheetID)
	if err != nil {
		t.Fatalf("get %s: %v", sheetID, err)
	}
	var total float64
	for _, l := range got.Lines {
		total += l.Hours
	}
	return total
}

func liveWorkID(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND kind = 'work' AND is_current
		 ORDER BY interval_start LIMIT 1`, emp).Scan(&id); err != nil {
		t.Fatalf("live work: %v", err)
	}
	return id
}

func groupSegments(t *testing.T, pool *pgxpool.Pool, groupID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx(t),
		`SELECT id::text FROM attendance_interval WHERE correction_group_id = $1::uuid ORDER BY interval_start`, groupID)
	if err != nil {
		t.Fatalf("segments: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// A same-date correction is a one-segment group: the successor keeps
// the original's shift identity and source events, cites the group,
// and the original flips non-current exactly once.
func TestCorrectSameDateGroup(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CG")
	importCSV(t, pool, emp, spanCSV(no, "cg-1", "cg-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 1 || rep.SegmentIDs[0] == orig || rep.GroupID == "" {
		t.Fatalf("rep=%+v want one fresh segment in a group", rep)
	}
	var pred, reason, by string
	if err := pool.QueryRow(ctx(t),
		`SELECT l.predecessor_interval_id::text, g.reason, g.corrected_by
		 FROM attendance_correction_group g
		 JOIN attendance_correction_group_predecessor l ON l.group_id = g.id
		 WHERE g.id = $1::uuid`, rep.GroupID).Scan(&pred, &reason, &by); err != nil {
		t.Fatalf("group: %v", err)
	}
	if pred != orig || reason != "early close" || by != "test-op" {
		t.Fatalf("group pred=%s reason=%q by=%q", pred, reason, by)
	}
	if segs := groupSegments(t, pool, rep.GroupID); len(segs) != 1 || segs[0] != rep.SegmentIDs[0] {
		t.Fatalf("segments=%v want the single successor", segs)
	}
	var refs, shift, origShift string
	var live bool
	if err := pool.QueryRow(ctx(t),
		`SELECT source_refs::text, shift_identity::text, is_current FROM attendance_interval
		 WHERE employer_id = $1 AND id = $2::uuid`, emp, rep.SegmentIDs[0]).Scan(&refs, &shift, &live); err != nil {
		t.Fatalf("successor: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT shift_identity::text, is_current FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, orig).Scan(&origShift, &live); err != nil || live {
		t.Fatalf("orig shift=%s live=%v err=%v want superseded", origShift, live, err)
	}
	if shift != origShift {
		t.Fatalf("shift=%s want original %s", shift, origShift)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(refs), &decoded); err != nil {
		t.Fatalf("refs %q: %v", refs, err)
	}
	if _, dup := decoded["clock_event_ids"]; dup {
		t.Fatalf("refs=%v want correction lineage only, never duplicated clock events", decoded)
	}
	if decoded["correction_group_id"] != rep.GroupID || decoded["predecessor_interval_id"] != orig {
		t.Fatalf("refs=%v want group lineage", decoded)
	}
	// The predecessor row is untouched: the original clock events stay
	// cited exactly where the import left them.
	var origRefs string
	if err := pool.QueryRow(ctx(t),
		`SELECT source_refs::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, orig).Scan(&origRefs); err != nil {
		t.Fatalf("orig refs: %v", err)
	}
	var origDecoded map[string]any
	if err := json.Unmarshal([]byte(origRefs), &origDecoded); err != nil {
		t.Fatalf("orig refs %q: %v", origRefs, err)
	}
	ids, _ := origDecoded["clock_event_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("orig refs=%v want the original clock events preserved", origDecoded)
	}
	var audits int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction WHERE employer_id = $1 AND new_value = $2`,
		emp, rep.SegmentIDs[0]).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows=%d err=%v want one on the day record", audits, err)
	}
}

// New bounds crossing Singapore midnight split on the same canonical
// boundary the import uses: one segment per business date, each with
// its own roster assignment, both day records re-derived, and audit
// evidence on both dates.
func TestCorrectMidnightCrossing(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CC", "2026-08-04")
	importCSV(t, pool, emp, spanCSV(no, "cc-1", "cc-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T14:00:00+08:00", End: "2026-08-04T02:00:00+08:00",
		Kind: "work", Reason: "night overrun",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("rep=%+v want two dated segments", rep)
	}
	type seg struct {
		date, start, end string
		aid              *string
	}
	var got []seg
	for _, id := range rep.SegmentIDs {
		var s seg
		if err := pool.QueryRow(ctx(t),
			`SELECT local_date::text, interval_start::text, interval_end::text, roster_assignment_id::text
			 FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
			emp, id).Scan(&s.date, &s.start, &s.end, &s.aid); err != nil {
			t.Fatalf("segment %s: %v", id, err)
		}
		got = append(got, s)
	}
	if got[0].date != "2026-08-03" || !sameInstant(t, got[0].start, "2026-08-03T14:00:00+08:00") ||
		!sameInstant(t, got[0].end, "2026-08-04T00:00:00+08:00") {
		t.Fatalf("first=%+v want Mon 14:00-24:00", got[0])
	}
	if got[1].date != "2026-08-04" || !sameInstant(t, got[1].start, "2026-08-04T00:00:00+08:00") ||
		!sameInstant(t, got[1].end, "2026-08-04T02:00:00+08:00") {
		t.Fatalf("second=%+v want Tue 00:00-02:00", got[1])
	}
	if got[0].aid == nil || got[1].aid == nil || *got[0].aid == *got[1].aid {
		t.Fatalf("aids %+v/%+v want distinct per-date assignments", got[0].aid, got[1].aid)
	}
	var monAid, tueAid string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM roster_assignment WHERE employer_id = $1 AND business_date = '2026-08-03'`,
		emp).Scan(&monAid); err != nil {
		t.Fatalf("mon assignment: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM roster_assignment WHERE employer_id = $1 AND business_date = '2026-08-04'`,
		emp).Scan(&tueAid); err != nil {
		t.Fatalf("tue assignment: %v", err)
	}
	if *got[0].aid != monAid || *got[1].aid != tueAid {
		t.Fatalf("segment aids %s/%s want %s/%s", *got[0].aid, *got[1].aid, monAid, tueAid)
	}
	var monActual, tueActual float64
	if err := pool.QueryRow(ctx(t),
		`SELECT actual_min FROM attendance_record WHERE employer_id = $1 AND business_date = '2026-08-03'`,
		emp).Scan(&monActual); err != nil || monActual != 600 {
		t.Fatalf("mon actual=%v err=%v want 600", monActual, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT actual_min FROM attendance_record WHERE employer_id = $1 AND business_date = '2026-08-04'`,
		emp).Scan(&tueActual); err != nil || tueActual != 120 {
		t.Fatalf("tue actual=%v err=%v want 120", tueActual, err)
	}
	// Both dates carry audit evidence: one row per affected day record.
	var audits int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction WHERE employer_id = $1 AND old_value = $2`,
		emp, orig).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows=%d err=%v want one per affected date", audits, err)
	}
	var after string
	if err := pool.QueryRow(ctx(t),
		`SELECT after_state::text FROM audit_event WHERE employer_id = $1 AND action = 'interval.correct'
		 ORDER BY occurred_at DESC LIMIT 1`, emp).Scan(&after); err != nil {
		t.Fatalf("audit event: %v", err)
	}
	if !strings.Contains(after, rep.GroupID) || !strings.Contains(after, "2026-08-03") ||
		!strings.Contains(after, "2026-08-04") {
		t.Fatalf("after=%s want group and both dates", after)
	}
}

func TestCorrectMidnightExactly(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CE")
	importCSV(t, pool, emp, spanCSV(no, "ce-1", "ce-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T14:00:00+08:00", End: "2026-08-04T00:00:00+08:00",
		Kind: "work", Reason: "to midnight",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 1 {
		t.Fatalf("rep=%+v want a single segment ending at midnight", rep)
	}
	var date string
	if err := pool.QueryRow(ctx(t),
		`SELECT local_date::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, rep.SegmentIDs[0]).Scan(&date); err != nil || date != "2026-08-03" {
		t.Fatalf("date=%s err=%v want 2026-08-03", date, err)
	}
}

func TestCorrectSecondOverMidnight(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CS", "2026-08-04")
	importCSV(t, pool, emp, spanCSV(no, "cs-1", "cs-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T14:00:00+08:00", End: "2026-08-04T00:00:01+08:00",
		Kind: "work", Reason: "one second over",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("rep=%+v want two segments", rep)
	}
	var start, end string
	if err := pool.QueryRow(ctx(t),
		`SELECT interval_start::text, interval_end::text FROM attendance_interval
		 WHERE employer_id = $1 AND id = $2::uuid`, emp, rep.SegmentIDs[1]).Scan(&start, &end); err != nil {
		t.Fatalf("second: %v", err)
	}
	if !sameInstant(t, start, "2026-08-04T00:00:00+08:00") || !sameInstant(t, end, "2026-08-04T00:00:01+08:00") {
		t.Fatalf("second=%s/%s want the 1s Tuesday piece", start, end)
	}
}

// Moving a segment onto a date without its own assignment must not
// inherit the old date's roster link: the segment stays NULL with an
// explicit open exception.
func TestCorrectNewDateUnassigned(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CU")
	importCSV(t, pool, emp, spanCSV(no, "cu-1", "cu-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	var monAid string
	if err := pool.QueryRow(ctx(t),
		`SELECT roster_assignment_id::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, orig).Scan(&monAid); err != nil {
		t.Fatalf("orig aid: %v", err)
	}
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T14:00:00+08:00", End: "2026-08-04T02:00:00+08:00",
		Kind: "work", Reason: "night overrun, no Tuesday roster",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("rep=%+v want two segments", rep)
	}
	var aid0 *string
	var aid1 *string
	if err := pool.QueryRow(ctx(t),
		`SELECT roster_assignment_id::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, rep.SegmentIDs[0]).Scan(&aid0); err != nil || aid0 == nil || *aid0 != monAid {
		t.Fatalf("monday aid=%v err=%v want %s", aid0, err, monAid)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT roster_assignment_id::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, rep.SegmentIDs[1]).Scan(&aid1); err != nil {
		t.Fatalf("tuesday aid: %v", err)
	}
	if aid1 != nil {
		t.Fatalf("tuesday aid=%s want NULL, never Monday's link", *aid1)
	}
	var open int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_exception WHERE employer_id = $1 AND code = 'no_roster_link'
		 AND business_date = '2026-08-04' AND NOT resolved`, emp).Scan(&open); err != nil || open != 1 {
		t.Fatalf("open=%d err=%v want the explicit Tuesday exception", open, err)
	}
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM attendance_record WHERE employer_id = $1 AND business_date = '2026-08-04'`,
		emp).Scan(&status); err != nil || status != "exception" {
		t.Fatalf("tue status=%q err=%v want exception", status, err)
	}
}

// New bounds overlapping another live interval reject before anything
// persists: no group, no segments, the original stays live.
func TestCorrectOverlapRejected(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CO")
	importCSV(t, pool, emp, spanCSV(no, "co-1", "co-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T12:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "co-3", "co-4",
		"2026-08-03T13:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	var first string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND is_current
		 ORDER BY interval_start LIMIT 1`, emp).Scan(&first); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{first}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T14:00:00+08:00",
		Kind: "work", Reason: "overlap",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err == nil {
		t.Fatalf("overlapping correction must fail")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeConflict {
		t.Fatalf("err=%v want conflict", err)
	}
	var groups, segs int
	var live bool
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group WHERE employer_id = $1`, emp).Scan(&groups); err != nil || groups != 0 {
		t.Fatalf("groups=%d err=%v want none", groups, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND source = 'correction'`,
		emp).Scan(&segs); err != nil || segs != 0 {
		t.Fatalf("segments=%d err=%v want none", segs, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT is_current FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, first).Scan(&live); err != nil || !live {
		t.Fatalf("orig live=%v err=%v", live, err)
	}
}

// A mid-correction failure rolls everything back: the group row never
// survives without its segments, and the original stays live.
func TestCorrectRegenFailureAtomic(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CA")
	importCSV(t, pool, emp, spanCSV(no, "ca-1", "ca-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	orig := liveWorkID(t, pool, emp)
	var broken RegenerateFunc = func(ctx context.Context, tx pgx.Tx, employerID, sheetID string) error {
		return errors.New("regen exploded")
	}
	linkedDraft := gen.Generated[0].TimesheetID
	regenOnly := func(context.Context, pgx.Tx, string, string, []string) ([]string, error) {
		return []string{linkedDraft}, nil
	}
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "boom",
	}, "test-op", broken, regenOnly); err == nil {
		t.Fatalf("broken regen must fail the correction")
	}
	var groups, segs int
	var live bool
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group WHERE employer_id = $1`, emp).Scan(&groups); err != nil || groups != 0 {
		t.Fatalf("groups=%d err=%v want none", groups, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND source = 'correction'`,
		emp).Scan(&segs); err != nil || segs != 0 {
		t.Fatalf("segments=%d err=%v want none", segs, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT is_current FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, orig).Scan(&live); err != nil || !live {
		t.Fatalf("orig live=%v err=%v", live, err)
	}
}

// The database rejects a second correction group on the same original
// even for writers that bypass the service.
func TestCorrectDuplicateGroupRejected(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CD")
	importCSV(t, pool, emp, spanCSV(no, "cd-1", "cd-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "first",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T16:00:00+08:00",
		Kind: "work", Reason: "second",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err == nil {
		t.Fatalf("second correction of a superseded interval must fail")
	}
	var employeeID string
	if err := pool.QueryRow(ctx(t),
		`SELECT employee_id FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, orig).Scan(&employeeID); err != nil {
		t.Fatalf("employee: %v", err)
	}
	tx, err := pool.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx(t))
	var second string
	if err := tx.QueryRow(ctx(t),
		`INSERT INTO attendance_correction_group (employer_id, employee_id, reason, corrected_by)
		 VALUES ($1, $2, 'bypass', 'test') RETURNING id::text`,
		emp, employeeID).Scan(&second); err != nil {
		t.Fatalf("second group: %v", err)
	}
	_, err = tx.Exec(ctx(t),
		`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
		 VALUES ($1, $2, $3)`, emp, second, orig)
	if err == nil || !strings.Contains(err.Error(), "predecessor_interval_id") {
		t.Fatalf("err=%v want the link-table single-successor violation", err)
	}
}

// Group membership is employer/employee consistent at the database
// level: segments cannot smuggle another employee's time into a group,
// and correction rows cannot float without a group.
func TestCorrectGroupMemberConsistency(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CM")
	importCSV(t, pool, emp, spanCSV(no, "cm-1", "cm-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "seed",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	other := secondEmployee(t, pool, emp)
	var otherID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, other).Scan(&otherID); err != nil {
		t.Fatalf("other: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, correction_group_id, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T09:00:00+08:00', '2026-08-03T10:00:00+08:00', 'Asia/Singapore', '2026-08-03', 'work', 'correction', $3::uuid, $4::uuid)`,
		emp, otherID, rep.GroupID, orig); err == nil {
		t.Fatalf("cross-employee segment must violate group membership")
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, supersedes_id)
		 VALUES ($1, (SELECT employee_id FROM attendance_interval WHERE id = $2::uuid), '2026-08-03T09:00:00+08:00', '2026-08-03T10:00:00+08:00', 'Asia/Singapore', '2026-08-03', 'work', 'correction', $2::uuid)`,
		emp, orig); err == nil {
		t.Fatalf("groupless correction segment must violate the presence check")
	}
}

// A correction crossing a pay-period boundary splits into segments
// that stay in their own periods: the old period's draft regenerates
// without the moved hours and the new period picks them up.
func TestCorrectPayPeriodBoundary(t *testing.T) {
	pool := testPool(t)
	emp, no := chainTpl(t, pool, "CP", "2026-08-31", "DAY", "09:00", "18:00", false, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true},
	}, "2026-09-01")
	importCSV(t, pool, emp, spanCSV(no, "cp-1", "cp-2",
		"2026-08-31T09:00:00+08:00", "2026-08-31T18:00:00+08:00"))
	aug := mkPeriodCode(t, pool, emp, "PP-AUG", "2026-08-01", "2026-08-31")
	sep := mkPeriodCode(t, pool, emp, "PP-SEP", "2026-09-01", "2026-09-30")
	genAug, err := timesheet.Generate(ctx(t), pool, emp, aug, "test-op")
	if err != nil || len(genAug.Generated) != 1 {
		t.Fatalf("gen aug=%+v err=%v", genAug, err)
	}
	augSheet := genAug.Generated[0].TimesheetID
	genSep, err := timesheet.Generate(ctx(t), pool, emp, sep, "test-op")
	if err != nil || len(genSep.Generated) != 0 {
		t.Fatalf("gen sep=%+v err=%v want skipped while empty", genSep, err)
	}
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-31T14:00:00+08:00", End: "2026-09-01T02:00:00+08:00",
		Kind: "work", Reason: "overnight overrun",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("rep=%+v want August and September segments", rep)
	}
	if h := sheetHours(t, pool, emp, augSheet); h != 10 {
		t.Fatalf("aug sheet=%.1fh want 10 after the move", h)
	}
	genSep2, err := timesheet.Generate(ctx(t), pool, emp, sep, "test-op")
	if err != nil || len(genSep2.Generated) != 1 {
		t.Fatalf("gen sep=%+v err=%v", genSep2, err)
	}
	if h := sheetHours(t, pool, emp, genSep2.Generated[0].TimesheetID); h != 2 {
		t.Fatalf("sep sheet=%.1fh want the moved 2h", h)
	}
	// Each segment's date falls in the period whose sheet carries it.
	var augDate, sepDate string
	if err := pool.QueryRow(ctx(t),
		`SELECT local_date::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, rep.SegmentIDs[0]).Scan(&augDate); err != nil || augDate != "2026-08-31" {
		t.Fatalf("first=%s err=%v", augDate, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT local_date::text FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, rep.SegmentIDs[1]).Scan(&sepDate); err != nil || sepDate != "2026-09-01" {
		t.Fatalf("second=%s err=%v", sepDate, err)
	}
}

// Moving hours into a pay period with a decided sheet is rejected:
// the operator reissues that sheet first, then the correction lands
// in the open correction draft while the decided original is untouched.
func TestCorrectDecidedPeriodGuard(t *testing.T) {
	pool := testPool(t)
	emp, no := chainTpl(t, pool, "CG2", "2026-08-31", "DAY", "09:00", "18:00", false, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true},
	}, "2026-09-01")
	importCSV(t, pool, emp, spanCSV(no, "cg-1", "cg-2",
		"2026-08-31T09:00:00+08:00", "2026-08-31T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "cg-3", "cg-4",
		"2026-09-01T09:00:00+08:00", "2026-09-01T18:00:00+08:00"))
	aug := mkPeriodCode(t, pool, emp, "PP-AUG", "2026-08-01", "2026-08-31")
	sep := mkPeriodCode(t, pool, emp, "PP-SEP", "2026-09-01", "2026-09-30")
	genAug, err := timesheet.Generate(ctx(t), pool, emp, aug, "test-op")
	if err != nil || len(genAug.Generated) != 1 {
		t.Fatalf("gen aug=%+v err=%v", genAug, err)
	}
	genSep, err := timesheet.Generate(ctx(t), pool, emp, sep, "test-op")
	if err != nil || len(genSep.Generated) != 1 {
		t.Fatalf("gen sep=%+v err=%v", genSep, err)
	}
	sepSheet := genSep.Generated[0].TimesheetID
	if _, err := timesheet.Submit(ctx(t), pool, emp, sepSheet, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sepSheet, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	var orig string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND is_current
		 AND local_date = '2026-08-31'`, emp).Scan(&orig); err != nil {
		t.Fatalf("aug interval: %v", err)
	}
	move := CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-09-01T19:00:00+08:00", End: "2026-09-01T21:00:00+08:00",
		Kind: "work", Reason: "wrong day stamped",
	}
	if _, err := Correct(ctx(t), pool, emp, move, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err == nil {
		t.Fatalf("move into an approved period must fail")
	} else {
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeInvalidState || !strings.Contains(ae.Message, "reissue") {
			t.Fatalf("err=%v want invalid_state directing to reissue", err)
		}
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, sepSheet, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	rep, err := Correct(ctx(t), pool, emp, move, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct after reissue: %v", err)
	}
	if len(rep.SegmentIDs) != 1 {
		t.Fatalf("rep=%+v want one September segment", rep)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 11 {
		t.Fatalf("v2=%.1fh want 9+2", h)
	}
	if h := sheetHours(t, pool, emp, sepSheet); h != 9 {
		t.Fatalf("v1=%.1fh want the approved 9h untouched", h)
	}
	if h := sheetHours(t, pool, emp, genAug.Generated[0].TimesheetID); h != 0 {
		t.Fatalf("aug=%.1fh want emptied by the move", h)
	}
}

// Corrected breaks stay classification-governed: a break inside the
// scheduled window corrects cleanly, one outside it rejects.
func TestCorrectBreakClassification(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "CB", "2026-08-03")
	importCSV(t, pool, emp, spanCSV(no, "cb-1", "cb-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T12:00:00+08:00", End: "2026-08-03T13:00:00+08:00",
		Kind: "break_unpaid", Reason: "missed punch-out for lunch",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("scheduled break: %v", err)
	}
	var kind string
	if err := pool.QueryRow(ctx(t),
		`SELECT kind FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
		emp, rep.SegmentIDs[0]).Scan(&kind); err != nil || kind != "break_unpaid" {
		t.Fatalf("kind=%s err=%v", kind, err)
	}
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{rep.SegmentIDs[0]}, Start: "2026-08-03T15:00:00+08:00", End: "2026-08-03T16:00:00+08:00",
		Kind: "break_unpaid", Reason: "off-window",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err == nil {
		t.Fatalf("off-window break must fail classification")
	} else if !strings.Contains(err.Error(), "unclassifiable") {
		t.Fatalf("err=%v want unclassifiable", err)
	}
}

// A corrected break spanning midnight splits like the import path:
// classified once, one dated piece per side, kind preserved.
func TestCorrectBreakAcrossMidnight(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "CM", "break_unpaid", "2026-08-04")
	importCSV(t, pool, emp, spanCSV(no, "cm-1", "cm-2",
		"2026-08-03T22:00:00+08:00", "2026-08-04T06:00:00+08:00"))
	var monHalf, tueHalf string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND is_current
		 AND local_date = '2026-08-03'`, emp).Scan(&monHalf); err != nil {
		t.Fatalf("mon half: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND is_current
		 AND local_date = '2026-08-04'`, emp).Scan(&tueHalf); err != nil {
		t.Fatalf("tue half: %v", err)
	}
	// Shrink the Tuesday work half so the corrected break has room.
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{tueHalf}, Start: "2026-08-04T00:30:00+08:00", End: "2026-08-04T06:00:00+08:00",
		Kind: "work", Reason: "late start",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{monHalf}, Start: "2026-08-03T23:30:00+08:00", End: "2026-08-04T00:15:00+08:00",
		Kind: "break_unpaid", Reason: "midnight meal",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("rep=%+v want two break pieces", rep)
	}
	for i, want := range []struct{ date, start, end, kind string }{
		{"2026-08-03", "2026-08-03T23:30:00+08:00", "2026-08-04T00:00:00+08:00", "break_unpaid"},
		{"2026-08-04", "2026-08-04T00:00:00+08:00", "2026-08-04T00:15:00+08:00", "break_unpaid"},
	} {
		var date, start, end, kind string
		var aid *string
		if err := pool.QueryRow(ctx(t),
			`SELECT local_date::text, interval_start::text, interval_end::text, kind, roster_assignment_id::text
			 FROM attendance_interval WHERE employer_id = $1 AND id = $2::uuid`,
			emp, rep.SegmentIDs[i]).Scan(&date, &start, &end, &kind, &aid); err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
		if date != want.date || kind != want.kind || !sameInstant(t, start, want.start) || !sameInstant(t, end, want.end) {
			t.Fatalf("piece %d=%s %s/%s %s", i, date, start, end, kind)
		}
		if aid == nil {
			t.Fatalf("piece %d on %s lost its roster link", i, date)
		}
	}
}
