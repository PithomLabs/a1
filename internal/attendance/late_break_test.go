package attendance

import (
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

// lateChain is a DAY roster with two scheduled breaks — lunch 12:00 and
// tea 15:00 — so the initial import pairs lunch (a clean approvable
// day) while the tea break arrives late. teaKind selects the tea
// window's paid treatment.
func lateChain(t *testing.T, pool *pgxpool.Pool, tag, teaKind string, extraDates ...string) (string, string) {
	t.Helper()
	return chainTpl(t, pool, tag, "2026-08-03", "DAY", "09:00", "18:00", false, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 180, Paid: true},
		{Kind: "break_unpaid", StartOffsetMin: 180, DurationMin: 60, Paid: false},
		{Kind: "work", StartOffsetMin: 240, DurationMin: 120, Paid: true},
		{Kind: teaKind, StartOffsetMin: 360, DurationMin: 30, Paid: teaKind == "break_paid"},
		{Kind: "work", StartOffsetMin: 390, DurationMin: 150, Paid: true},
	}, extraDates...)
}

func spanLunchCSV(no, pfx string) string {
	return `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,` + pfx + `-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,` + pfx + `-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,` + pfx + `-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,` + pfx + `-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
}

func teaCSV(no, pfx string) string {
	return breakCSV(no, pfx+`-5`, pfx+`-6`, "2026-08-03T15:00:00+08:00", "2026-08-03T15:30:00+08:00")
}

// readyBreakChain is lateChain with unpaid tea plus the terms, SHG
// instruction and pack activations readiness needs.
func readyBreakChain(t *testing.T, pool *pgxpool.Pool, tag string, extraDates ...string) (emp, no, period string) {
	t.Helper()
	emp, no = lateChain(t, pool, tag, "break_unpaid", extraDates...)
	var empID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&empID); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'support', 'workman', true, 'test', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		emp, empID); err != nil {
		t.Fatalf("terms: %v", err)
	}
	// Terms predate any approval the test performs by fixture order
	// (C8 normalizes created_at, so insertion order is the clock).
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, empID, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	// IMPLEMENTABLE packs are privileged state: the fixture sets them
	// as admin without testing activation itself.
	if _, err := testAdmin(t).Exec(ctx(t),
		`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES
		 ($1, 'SG-CPF-2026', 'v0', 'IMPLEMENTABLE'), ($1, 'SG-SDL', 'v0', 'IMPLEMENTABLE'),
		 ($1, 'SG-SHG', 'v0', 'IMPLEMENTABLE'), ($1, 'SG-MOM-KET', 'v0', 'IMPLEMENTABLE')`, emp); err != nil {
		t.Fatalf("activations: %v", err)
	}
	return emp, no, mkPeriod(t, pool, emp)
}

func breakCSV(no, startID, endID, startAt, endAt string) string {
	return `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,` + startID + `,` + no + `,` + startAt + `,Asia/Singapore,break_start
vendor-x,` + endID + `,` + no + `,` + endAt + `,Asia/Singapore,break_end
`
}

func openConflicts(t *testing.T, pool *pgxpool.Pool, emp, code string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_exception WHERE employer_id = $1 AND code = $2 AND NOT resolved`,
		emp, code).Scan(&n); err != nil {
		t.Fatalf("open %s: %v", code, err)
	}
	return n
}

// Clock events → Attendance → Generate → Approve → Late break import →
// Conflict → Reissue → Attach break → Regenerate → Approve correction:
// the held break never touches the approval, the correction draft
// receives it atomically, and readiness clears only on approval.
func TestLateBreakFullFlow(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyBreakChain(t, pool, "LF")
	rep0 := importCSV(t, pool, emp, spanLunchCSV(no, "lf"))
	if len(rep0.Exceptions) != 0 {
		t.Fatalf("exceptions=%+v want a clean day", rep0.Exceptions)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	r1, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil || !r1.Ready {
		t.Fatalf("r1=%+v err=%v want ready", r1, err)
	}
	before := snapshotSheet(t, pool, emp, v1)
	if before.hours != 8 {
		t.Fatalf("v1=%.1fh want 8", before.hours)
	}

	// The late tea break holds: nothing splits, the evidence waits
	// unpaired with an explicit blocking conflict.
	late := importCSV(t, pool, emp, teaCSV(no, "lf"))
	if late.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want the break held", late.IntervalsBuilt)
	}
	found := false
	for _, e := range late.Exceptions {
		if e.Code == ExcEvidenceConflict {
			found = true
		}
	}
	if !found {
		t.Fatalf("exceptions=%+v want evidence_conflict", late.Exceptions)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 1 {
		t.Fatalf("open conflicts=%d want 1", n)
	}
	var unpaired int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event e WHERE e.employer_id = $1 AND e.source_event_id IN ('lf-5', 'lf-6')
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)`,
		emp).Scan(&unpaired); err != nil || unpaired != 2 {
		t.Fatalf("unpaired=%d err=%v want both break events preserved unpaired", unpaired, err)
	}
	var cause []string
	if err := pool.QueryRow(ctx(t),
		`SELECT array_agg(eid::text) FROM attendance_exception ex, unnest(ex.event_ids) eid
		 WHERE ex.employer_id = $1 AND ex.code = 'evidence_conflict' AND NOT ex.resolved`,
		emp).Scan(&cause); err != nil || len(cause) != 2 {
		t.Fatalf("cause=%v err=%v want both break event IDs cited", cause, err)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1 under hold")
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM attendance_record WHERE employer_id = $1 AND business_date = '2026-08-03'`,
		emp).Scan(&status); err != nil || status != "exception" {
		t.Fatalf("day status=%q err=%v", status, err)
	}
	r2, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r2.Ready || len(r2.AttendanceConflicts) != 1 {
		t.Fatalf("r2=%+v want readiness blocked on the held evidence", r2)
	}

	// Reissue opens the correction draft; attach lands the break in it
	// with an atomic regeneration.
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 8 {
		t.Fatalf("v2=%.1fh want 8 before attach", h)
	}
	att, err := AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if att.IntervalsBuilt != 3 {
		t.Fatalf("built=%d want work/break/work", att.IntervalsBuilt)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 7.5 {
		t.Fatalf("v2=%.1fh want the regenerated 7.5h", h)
	}
	got, err := timesheet.Get(ctx(t), pool, emp, v2.ID)
	if err != nil {
		t.Fatalf("get v2: %v", err)
	}
	if len(got.Lines) != 1 || len(got.Lines[0].IntervalIDs) != 3 {
		t.Fatalf("v2 lines=%+v want one line over the three work pieces", got.Lines)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 0 {
		t.Fatalf("open conflicts=%d want resolved", n)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1 after attach")
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r3.Ready || len(r3.AttendanceConflicts) != 1 {
		t.Fatalf("r3=%+v want still blocked until the correction approves", r3)
	}
	approveSheet(t, pool, emp, v2.ID)
	r4, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !r4.Ready || len(r4.AttendanceConflicts) != 0 {
		t.Fatalf("r4=%+v want ready", r4)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1 final")
}

// A late break under a submitted-but-unapproved sheet holds too:
// submitted sheets decide first, then reissue, then attach.
func TestLateBreakSubmittedHold(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "LS", "break_unpaid")
	importCSV(t, pool, emp, spanLunchCSV(no, "ls"))
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	if _, err := timesheet.Submit(ctx(t), pool, emp, v1, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	late := importCSV(t, pool, emp, teaCSV(no, "ls"))
	if late.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want held", late.IntervalsBuilt)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 1 {
		t.Fatalf("open=%d", n)
	}
	// Attaching without an open draft changes nothing and duplicates
	// nothing, even across a redelivery of the same break.
	if _, err := AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("attach: %v", err)
	}
	again := importCSV(t, pool, emp, teaCSV(no, "ls"))
	if again.Duplicates != 2 || again.IntervalsBuilt != 0 {
		t.Fatalf("dup=%d built=%d", again.Duplicates, again.IntervalsBuilt)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 1 {
		t.Fatalf("open=%d want still exactly one conflict", n)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, v1, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	att, err := AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("attach after reissue: %v", err)
	}
	if att.IntervalsBuilt != 3 {
		t.Fatalf("built=%d", att.IntervalsBuilt)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 7.5 {
		t.Fatalf("v2=%.1fh", h)
	}
}

// When the correction draft already exists, a late break attaches on
// arrival and regenerates the draft: no conflict, no waiting.
func TestLateBreakExistingDraft(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "LD", "break_unpaid")
	importCSV(t, pool, emp, spanLunchCSV(no, "ld"))
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	late := importCSV(t, pool, emp, teaCSV(no, "ld"))
	if late.IntervalsBuilt != 3 {
		t.Fatalf("built=%d want immediate attach into the open draft", late.IntervalsBuilt)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 0 {
		t.Fatalf("open=%d want no conflict", n)
	}
	var total int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_exception WHERE employer_id = $1 AND code = 'evidence_conflict'`,
		emp).Scan(&total); err != nil || total != 0 {
		t.Fatalf("conflict rows=%d err=%v want none ever raised", total, err)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 7.5 {
		t.Fatalf("v2=%.1fh want regenerated on arrival", h)
	}
}

// Redelivering an attached late break changes nothing: duplicates,
// no new intervals, no double-attach.
func TestLateBreakDuplicate(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "LB", "break_unpaid")
	importCSV(t, pool, emp, spanLunchCSV(no, "lb"))
	first := importCSV(t, pool, emp, teaCSV(no, "lb"))
	if first.IntervalsBuilt != 3 {
		t.Fatalf("built=%d", first.IntervalsBuilt)
	}
	second := importCSV(t, pool, emp, teaCSV(no, "lb"))
	if second.Duplicates != 2 || second.IntervalsBuilt != 0 {
		t.Fatalf("dup=%d built=%d", second.Duplicates, second.IntervalsBuilt)
	}
	var pieces int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current
		   AND kind IN ('break_paid', 'break_unpaid')`, emp).Scan(&pieces); err != nil || pieces != 2 {
		t.Fatalf("pieces=%d err=%v want lunch and tea exactly once", pieces, err)
	}
}

// A held midnight-spanning break waits whole and attaches split: one
// conflict, then one dated piece per side once the draft opens.
func TestLateBreakMidnightFlow(t *testing.T) {
	pool := testPool(t)
	emp, no := chainTpl(t, pool, "LM", "2026-08-03", "NIGHT", "22:00", "06:00", true, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 90, Paid: true},
		{Kind: "break_unpaid", StartOffsetMin: 90, DurationMin: 60, Paid: false},
		{Kind: "work", StartOffsetMin: 150, DurationMin: 120, Paid: true},
		{Kind: "break_unpaid", StartOffsetMin: 270, DurationMin: 30, Paid: false},
		{Kind: "work", StartOffsetMin: 300, DurationMin: 180, Paid: true},
	}, "2026-08-04")
	initial := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,lm-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,lm-2,` + no + `,2026-08-04T02:30:00+08:00,Asia/Singapore,break_start
vendor-x,lm-3,` + no + `,2026-08-04T03:00:00+08:00,Asia/Singapore,break_end
vendor-x,lm-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	rep0 := importCSV(t, pool, emp, initial)
	if len(rep0.Exceptions) != 0 {
		t.Fatalf("exceptions=%+v want a clean night", rep0.Exceptions)
	}
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	before := snapshotSheet(t, pool, emp, v1)
	if before.hours != 7.5 {
		t.Fatalf("v1=%.1fh want 7.5", before.hours)
	}
	late := importCSV(t, pool, emp, breakCSV(no, "lm-5", "lm-6",
		"2026-08-03T23:30:00+08:00", "2026-08-04T00:30:00+08:00"))
	if late.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want held whole", late.IntervalsBuilt)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 1 {
		t.Fatalf("open=%d want one conflict for the whole break", n)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	att, err := AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if att.IntervalsBuilt != 4 {
		t.Fatalf("built=%d want work/break halves on both dates", att.IntervalsBuilt)
	}
	assertDatedBreaks(t, pool, emp, []datedBreak{
		{"2026-08-03", "2026-08-03T23:30:00+08:00", "2026-08-04T00:00:00+08:00"},
		{"2026-08-04", "2026-08-04T00:00:00+08:00", "2026-08-04T00:30:00+08:00"},
		{"2026-08-04", "2026-08-04T02:30:00+08:00", "2026-08-04T03:00:00+08:00"},
	})
	if h := sheetHours(t, pool, emp, v2.ID); h != 6.5 {
		t.Fatalf("v2=%.1fh want 6.5", h)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1")
}

// A paid late break keeps its kind through hold and attach, and the
// correction draft counts it as paid time.
func TestLateBreakPaidFlow(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "LP", "break_paid")
	importCSV(t, pool, emp, spanLunchCSV(no, "lp"))
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	before := snapshotSheet(t, pool, emp, v1)
	late := importCSV(t, pool, emp, teaCSV(no, "lp"))
	if late.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want held", late.IntervalsBuilt)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 8 {
		t.Fatalf("v2=%.1fh want 7.5 work + 0.5 paid break", h)
	}
	var kinds string
	if err := pool.QueryRow(ctx(t),
		`SELECT string_agg(DISTINCT i.kind, ',' ORDER BY i.kind) FROM timesheet_line_interval li
		 JOIN timesheet_line l ON l.employer_id = li.employer_id AND l.id = li.line_id
		 JOIN attendance_interval i ON i.employer_id = li.employer_id AND i.id = li.interval_id
		 WHERE li.employer_id = $1 AND l.timesheet_id = $2::uuid`,
		emp, v2.ID).Scan(&kinds); err != nil || kinds != "break_paid,work" {
		t.Fatalf("kinds=%q err=%v", kinds, err)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1")
}

// Two concurrent late-break attachments serialize on the employee
// lock and pair exactly once: the break lands on the draft a single
// time however the attaches interleave.
func TestConcurrentAttachPendingExactlyOnce(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "LA", "break_paid")
	importCSV(t, pool, emp, spanLunchCSV(no, "la"))
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	if late := importCSV(t, pool, emp, teaCSV(no, "la")); late.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want held", late.IntervalsBuilt)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	var wg sync.WaitGroup
	reps := make([]*AttachReport, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			reps[n], errs[n] = AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
		}(i)
	}
	wg.Wait()
	built := 0
	for n, err := range errs {
		if err != nil {
			t.Fatalf("attach %d: %v", n, err)
		}
		built += reps[n].IntervalsBuilt
	}
	// One attach builds the held tea break plus the two work pieces
	// it splits (3); the loser re-pairs against consumed events and
	// builds nothing.
	if built != 3 {
		t.Fatalf("intervals built = %d, want 3 exactly once", built)
	}
	var breaks int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval
		 WHERE employer_id = $1 AND kind = 'break_paid' AND is_current`,
		emp).Scan(&breaks); err != nil || breaks != 1 {
		t.Fatalf("breaks = %d, err = %v, want exactly one live break", breaks, err)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 8 {
		t.Fatalf("v2=%.1fh want 8", h)
	}
}

// An unclassifiable late break never attaches, even after reissue: no
// evidence conflict, no silent classification, the ambiguity stays open.
func TestLateBreakUnclassifiable(t *testing.T) {
	pool := testPool(t)
	emp, no := lateChain(t, pool, "LU", "break_unpaid")
	importCSV(t, pool, emp, spanLunchCSV(no, "lu"))
	period := mkPeriod(t, pool, emp)
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	late := importCSV(t, pool, emp, breakCSV(no, "lu-5", "lu-6",
		"2026-08-03T10:00:00+08:00", "2026-08-03T10:30:00+08:00"))
	if late.IntervalsBuilt != 0 {
		t.Fatalf("built=%d", late.IntervalsBuilt)
	}
	if n := openConflicts(t, pool, emp, ExcAmbiguousBreak); n != 1 {
		t.Fatalf("ambiguous=%d want 1", n)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 0 {
		t.Fatalf("conflicts=%d want none: nothing classifiable to hold", n)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	att, err := AttachPending(ctx(t), pool, emp, no, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if att.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want still nothing", att.IntervalsBuilt)
	}
	if n := openConflicts(t, pool, emp, ExcAmbiguousBreak); n != 1 {
		t.Fatalf("ambiguous=%d want still open", n)
	}
	if n := openConflicts(t, pool, emp, ExcEvidenceConflict); n != 0 {
		t.Fatalf("conflicts=%d", n)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 8 {
		t.Fatalf("v2=%.1fh", h)
	}
	var pieces int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current
		   AND kind IN ('break_paid', 'break_unpaid')`, emp).Scan(&pieces); err != nil || pieces != 1 {
		t.Fatalf("pieces=%d err=%v want lunch only", pieces, err)
	}
}
