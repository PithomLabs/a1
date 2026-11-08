package attendance

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

// Stage B group-atomic re-correction (§5): a multi-segment group
// re-corrects as one logical unit or not at all. Partial sets fail
// with a machine-readable missing-sibling error; the database
// backstops the service on every path.

type twoSegGroup struct {
	emp, no, period string
	orig            string
	group           string
	segA, segB      string
}

// buildTwoSegGroup imports a day span and corrects it into an
// overnight pair: segA on 2026-08-03, segB on 2026-08-04.
func buildTwoSegGroup(t *testing.T, pool *pgxpool.Pool, tag string, extraDates ...string) twoSegGroup {
	t.Helper()
	emp, no, period := readyChain(t, pool, tag, extraDates...)
	importCSV(t, pool, emp, spanCSV(no, tag+"-1", tag+"-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T22:00:00+08:00", End: "2026-08-04T06:00:00+08:00",
		Kind: "work", Reason: "night shift",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("segments=%d want the overnight pair", len(rep.SegmentIDs))
	}
	return twoSegGroup{emp, no, period, orig, rep.GroupID, rep.SegmentIDs[0], rep.SegmentIDs[1]}
}

func groupLinkCount(t *testing.T, pool *pgxpool.Pool, groupID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group_predecessor WHERE group_id = $1::uuid`,
		groupID).Scan(&n); err != nil {
		t.Fatalf("links: %v", err)
	}
	return n
}

func groupCountFor(t *testing.T, pool *pgxpool.Pool, emp string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group WHERE employer_id = $1`,
		emp).Scan(&n); err != nil {
		t.Fatalf("groups: %v", err)
	}
	return n
}

func isCurrentID(t *testing.T, pool *pgxpool.Pool, id string) bool {
	t.Helper()
	var live bool
	if err := pool.QueryRow(ctx(t),
		`SELECT is_current FROM attendance_interval WHERE id = $1::uuid`, id).Scan(&live); err != nil {
		t.Fatalf("liveness %s: %v", id, err)
	}
	return live
}

// Re-correcting one segment of a two-segment group is rejected: the
// error names the group, the required set, and the missing sibling.
func TestRecorrectPartialSegmentRejected(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RP")
	_, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segA}, Start: "2026-08-03T22:00:00+08:00", End: "2026-08-04T06:00:00+08:00",
		Kind: "work", Reason: "partial attempt",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err == nil {
		t.Fatal("expected the partial re-correction to fail")
	}
	if apperr.AsCode(err) != apperr.CodeValidation {
		t.Fatalf("code=%s want validation", apperr.AsCode(err))
	}
	for _, want := range []string{g.group, g.segA, g.segB, "missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%q want it to name %s", err, want)
		}
	}
	if n := groupCountFor(t, pool, g.emp); n != 1 {
		t.Fatalf("groups=%d want only the original", n)
	}
	if !isCurrentID(t, pool, g.segA) || !isCurrentID(t, pool, g.segB) {
		t.Fatal("siblings flipped without a committed successor")
	}
}

// Re-correcting the complete group succeeds: one successor group,
// both predecessors bound, both old members historical.
func TestRecorrectWholeGroupSucceeds(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RW")
	rep, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segA, g.segB}, Start: "2026-08-03T21:00:00+08:00", End: "2026-08-04T07:00:00+08:00",
		Kind: "work", Reason: "shift widened",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("re-correct: %v", err)
	}
	if rep.GroupID == g.group {
		t.Fatal("successor must be a new group")
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("segments=%d want the re-split pair", len(rep.SegmentIDs))
	}
	if n := groupLinkCount(t, pool, rep.GroupID); n != 2 {
		t.Fatalf("links=%d want both predecessors bound", n)
	}
	if isCurrentID(t, pool, g.segA) || isCurrentID(t, pool, g.segB) {
		t.Fatal("old members must be historical")
	}
	for _, id := range rep.SegmentIDs {
		if !isCurrentID(t, pool, id) {
			t.Fatalf("successor %s is not current", id)
		}
	}
}

// A same-date group holds one segment: re-correcting that singleton
// works exactly like a first correction, narrowing allowed.
func TestRecorrectSameDateGroup(t *testing.T) {
	pool := testPool(t)
	emp, no, _ := readyChain(t, pool, "RS")
	importCSV(t, pool, emp, spanCSV(no, "rs-1", "rs-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 1 {
		t.Fatalf("segments=%d want the same-date singleton", len(rep.SegmentIDs))
	}
	rep2, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{rep.SegmentIDs[0]}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T16:00:00+08:00",
		Kind: "work", Reason: "earlier close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("re-correct: %v", err)
	}
	if len(rep2.SegmentIDs) != 1 || rep2.GroupID == rep.GroupID {
		t.Fatalf("rep=%+v want a fresh singleton successor", rep2)
	}
	if isCurrentID(t, pool, rep.SegmentIDs[0]) {
		t.Fatal("first successor must be historical")
	}
}

// A cross-midnight group re-corrects whole: the successor re-splits
// on the same date boundary.
func TestRecorrectCrossMidnightGroup(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RM")
	rep, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segB, g.segA}, Start: "2026-08-03T20:00:00+08:00", End: "2026-08-04T08:00:00+08:00",
		Kind: "work", Reason: "shift moved",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("re-correct: %v", err)
	}
	var dates []string
	for _, id := range rep.SegmentIDs {
		var d string
		if err := pool.QueryRow(ctx(t),
			`SELECT local_date::text FROM attendance_interval WHERE id = $1::uuid`, id).Scan(&d); err != nil {
			t.Fatalf("date: %v", err)
		}
		dates = append(dates, d)
	}
	if len(dates) != 2 || dates[0] != "2026-08-03" || dates[1] != "2026-08-04" {
		t.Fatalf("dates=%v want the re-split pair", dates)
	}
}

// A re-correction may move the unit across a pay-period boundary:
// both drafts regenerate from the effective segments.
// addRosterWeek publishes assignments for dates outside the chain's
// home week (assignments validate against their roster week).
func addRosterWeek(t *testing.T, pool *pgxpool.Pool, emp, no, week string, dates ...string) {
	t.Helper()
	r, err := wfm.CreateRoster(ctx(t), pool, emp, wfm.RosterInput{Programme: "PRG", WeekStarting: week})
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	for _, d := range dates {
		if _, err := wfm.Assign(ctx(t), pool, emp, r.ID, wfm.AssignmentInput{
			Employee: no, Date: d, Template: "DAY",
		}); err != nil {
			t.Fatalf("assign %s: %v", d, err)
		}
	}
	if _, err := wfm.Publish(ctx(t), pool, emp, r.ID, "test"); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestRecorrectAcrossPayPeriodBoundary(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RB", "2026-08-04")
	addRosterWeek(t, pool, g.emp, g.no, "2026-08-31", "2026-08-31", "2026-09-01")
	gen, err := timesheet.Generate(ctx(t), pool, g.emp, g.period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	augSheet := gen.Generated[0].TimesheetID
	if h := sheetHours(t, pool, g.emp, augSheet); h != 8 {
		t.Fatalf("aug=%.1fh want the overnight 8h first", h)
	}
	rep, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segA, g.segB}, Start: "2026-08-31T22:00:00+08:00", End: "2026-09-01T06:00:00+08:00",
		Kind: "work", Reason: "month-end move",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("re-correct: %v", err)
	}
	if len(rep.SegmentIDs) != 2 {
		t.Fatalf("segments=%d want the boundary pair", len(rep.SegmentIDs))
	}
	// The August draft now carries only the Aug-31 sliver; the Sep-01
	// segment waits for the September generation.
	if h := sheetHours(t, pool, g.emp, augSheet); h != 2 {
		t.Fatalf("aug=%.1fh want the regenerated 2h sliver", h)
	}
	var sepPeriod string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP-0926', '2026-09-01', '2026-09-30', '2026-10-07', '2026-10-07', '2026-10-14')
		 RETURNING id::text`, g.emp).Scan(&sepPeriod); err != nil {
		t.Fatalf("sep period: %v", err)
	}
	sep, err := timesheet.Generate(ctx(t), pool, g.emp, sepPeriod, "test-op")
	if err != nil || len(sep.Generated) != 1 {
		t.Fatalf("sep gen=%+v err=%v", sep, err)
	}
	if h := sheetHours(t, pool, g.emp, sep.Generated[0].TimesheetID); h != 6 {
		t.Fatalf("sep=%.1fh want the 6h segment", h)
	}
}

// Concurrent whole-group re-corrections admit exactly one successor
// group; the loser finds the siblings superseded and leaves nothing.
func TestConcurrentWholeGroupRecorrection(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RC")
	type outcome struct {
		rep *CorrectionReport
		err error
	}
	done := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			rep, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
				IntervalIDs: []string{g.segA, g.segB}, Start: "2026-08-03T21:00:00+08:00", End: "2026-08-04T07:00:00+08:00",
				Kind: "work", Reason: "race",
			}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
			done <- outcome{rep, err}
		}()
	}
	var wins int
	for i := 0; i < 2; i++ {
		o := <-done
		if o.err == nil {
			wins++
			continue
		}
		if apperr.AsCode(o.err) != apperr.CodeInvalidState {
			t.Fatalf("loser code=%s want invalid_state", apperr.AsCode(o.err))
		}
	}
	if wins != 1 {
		t.Fatalf("wins=%d want exactly one successor", wins)
	}
	if n := groupCountFor(t, pool, g.emp); n != 2 {
		t.Fatalf("groups=%d want original plus one successor", n)
	}
}

// A regeneration failure rolls the whole re-correction back: zero
// partial successor groups, siblings still current.
func TestRecorrectRegenFailureAtomic(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RF", "2026-08-04")
	gen, err := timesheet.Generate(ctx(t), pool, g.emp, g.period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	failRegen := func(ctx context.Context, tx pgx.Tx, employerID, sheetID string) error {
		return apperr.New(apperr.CodeInternal, "injected regen failure")
	}
	_, err = Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segA, g.segB}, Start: "2026-08-03T21:00:00+08:00", End: "2026-08-04T07:00:00+08:00",
		Kind: "work", Reason: "doomed",
	}, "test-op", failRegen, timesheet.CorrectionDrafts)
	if err == nil {
		t.Fatal("expected the injected failure to abort the correction")
	}
	if n := groupCountFor(t, pool, g.emp); n != 1 {
		t.Fatalf("groups=%d want only the original", n)
	}
	if !isCurrentID(t, pool, g.segA) || !isCurrentID(t, pool, g.segB) {
		t.Fatal("siblings flipped without a committed successor")
	}
	var segs int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND source = 'correction'`,
		g.emp).Scan(&segs); err != nil || segs != 2 {
		t.Fatalf("segments=%d err=%v want only the first pair", segs, err)
	}
}

// Timesheet generation after a re-correction links the effective
// group only: superseded segments feed no line.
func TestGenerateUsesEffectiveGroupOnly(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RG", "2026-08-04")
	rep, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segA, g.segB}, Start: "2026-08-03T21:00:00+08:00", End: "2026-08-04T07:00:00+08:00",
		Kind: "work", Reason: "shift widened",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("re-correct: %v", err)
	}
	gen, err := timesheet.Generate(ctx(t), pool, g.emp, g.period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	rows, err := pool.Query(ctx(t),
		`SELECT DISTINCT i.id::text FROM timesheet_line_interval li
		 JOIN timesheet_line l ON l.id = li.line_id
		 JOIN attendance_interval i ON i.id = li.interval_id
		 WHERE l.employer_id = $1 AND l.timesheet_id = $2`,
		g.emp, gen.Generated[0].TimesheetID)
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	defer rows.Close()
	linked := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		linked[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("links: %v", err)
	}
	for _, id := range rep.SegmentIDs {
		if !linked[id] {
			t.Fatalf("linked=%v want successor %s", linked, id)
		}
	}
	for _, id := range []string{g.orig, g.segA, g.segB} {
		if linked[id] {
			t.Fatalf("linked=%v want history %s excluded", linked, id)
		}
	}
}

// Re-correction never rewrites history: the original row is
// byte-identical and the first-generation segments change only in
// their liveness flip.
func TestRecorrectHistoriesUnchanged(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RH")
	snap := func(id string) map[string]any {
		t.Helper()
		cols := []string{"employer_id", "employee_id", "roster_assignment_id", "interval_start",
			"interval_end", "timezone", "local_date", "kind", "source", "source_refs",
			"shift_identity", "supersedes_id", "correction_group_id", "created_at"}
		m := map[string]any{}
		for _, c := range cols {
			var v *string
			if err := pool.QueryRow(ctx(t),
				`SELECT (`+c+`)::text FROM attendance_interval WHERE id = $1::uuid`,
				id).Scan(&v); err != nil {
				t.Fatalf("snap %s.%s: %v", id, c, err)
			}
			if v != nil {
				m[c] = *v
			}
		}
		return m
	}
	eq := func(a, b map[string]any) bool {
		if len(a) != len(b) {
			return false
		}
		for k, v := range a {
			if b[k] != v {
				return false
			}
		}
		return true
	}
	beforeOrig, beforeA, beforeB := snap(g.orig), snap(g.segA), snap(g.segB)
	if _, err := Correct(ctx(t), pool, g.emp, CorrectionInput{
		IntervalIDs: []string{g.segA, g.segB}, Start: "2026-08-03T21:00:00+08:00", End: "2026-08-04T07:00:00+08:00",
		Kind: "work", Reason: "shift widened",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("re-correct: %v", err)
	}
	if got := snap(g.orig); !eq(got, beforeOrig) {
		t.Fatalf("original mutated: %+v was %+v", got, beforeOrig)
	}
	if got := snap(g.segA); !eq(got, beforeA) {
		t.Fatalf("segA mutated: %+v was %+v", got, beforeA)
	}
	if got := snap(g.segB); !eq(got, beforeB) {
		t.Fatalf("segB mutated: %+v was %+v", got, beforeB)
	}
	if isCurrentID(t, pool, g.segA) || isCurrentID(t, pool, g.segB) {
		t.Fatal("old members must be historical")
	}
}

// ListIntervals exposes the correction group each segment belongs
// to, so operators resolve the unit without SQL.
func TestListIntervalsExposesGroupID(t *testing.T) {
	pool := testPool(t)
	g := buildTwoSegGroup(t, pool, "RL")
	got, err := ListIntervals(ctx(t), pool, g.emp, "", "2026-08-03", "2026-08-04")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[string]IntervalDetail{}
	for _, d := range got {
		byID[d.ID] = d
	}
	for _, id := range []string{g.segA, g.segB} {
		d, ok := byID[id]
		if !ok {
			t.Fatalf("segment %s missing from the listing", id)
		}
		if d.CorrectionGroupID == nil || *d.CorrectionGroupID != g.group {
			t.Fatalf("segment %s group=%v want %s", id, d.CorrectionGroupID, g.group)
		}
	}
	if d, ok := byID[g.orig]; !ok {
		t.Fatal("original missing from the listing")
	} else if d.CorrectionGroupID != nil {
		t.Fatalf("original group=%v want nil", *d.CorrectionGroupID)
	}
}
