package attendance

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

func TestUnpairedEventsTimezone(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	// Same instant class, different declared zones: 00:30 SGT on 08-04 is
	// 16:30 UTC on 08-03, and 17:00 UTC on 08-03 is 01:00 SGT on 08-04.
	// Both attribute to the employment date 08-04: vendor zones resolve
	// instants, never payroll dates.
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,tz-1,` + no + `,2026-08-04T00:30:00+08:00,Asia/Singapore,in
vendor-x,tz-2,` + no + `,2026-08-03T17:00:00Z,UTC,in
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	rev, err := ReviewAttendance(ctx(t), pool, emp, "2026-08-04", "2026-08-04")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(rev.UnpairedEvents) != 2 {
		t.Fatalf("08-04 unpaired = %+v, want both events", rev.UnpairedEvents)
	}
	rev3, err := ReviewAttendance(ctx(t), pool, emp, "2026-08-03", "2026-08-03")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(rev3.UnpairedEvents) != 0 {
		t.Fatalf("08-03 unpaired = %+v, want none", rev3.UnpairedEvents)
	}
}

func TestCrossMidnightSplit(t *testing.T) {
	pool := testPool(t)
	emp, no := chainTpl(t, pool, "E", "2026-08-03", "NIGHT", "22:00", "06:00", true, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 240, Paid: true},
		{Kind: "break_unpaid", StartOffsetMin: 240, DurationMin: 60, Paid: false},
		{Kind: "work", StartOffsetMin: 300, DurationMin: 180, Paid: true},
	}, "2026-08-04")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,cm-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,cm-2,` + no + `,2026-08-04T02:00:00+08:00,Asia/Singapore,break_start
vendor-x,cm-3,` + no + `,2026-08-04T03:00:00+08:00,Asia/Singapore,break_end
vendor-x,cm-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(rep.Exceptions) != 0 {
		t.Fatalf("exceptions=%+v", rep.Exceptions)
	}
	// 22:00-24:00 work, 00:00-02:00 work, 02:00-03:00 break, 03:00-06:00 work.
	if rep.IntervalsBuilt != 4 {
		t.Fatalf("intervals=%d want 4 split runs", rep.IntervalsBuilt)
	}
	var dates []string
	rows, err := pool.Query(ctx(t),
		`SELECT DISTINCT local_date::text FROM attendance_interval WHERE employer_id = $1 ORDER BY 1`, emp)
	if err != nil {
		t.Fatalf("query dates: %v", err)
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		dates = append(dates, d)
	}
	rows.Close()
	if len(dates) != 2 || dates[0] != "2026-08-03" || dates[1] != "2026-08-04" {
		t.Fatalf("dates=%v want both calendar dates", dates)
	}
}

func TestLateBreakAttach(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	span := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,lb-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,lb-2,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(span), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt != 1 {
		t.Fatalf("intervals=%d want 1 whole-span work", rep.IntervalsBuilt)
	}
	late := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,lb-3,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,lb-4,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
`
	rep2, err := Import(ctx(t), pool, emp, strings.NewReader(late), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("late import: %v", err)
	}
	if rep2.IntervalsBuilt != 3 {
		t.Fatalf("intervals=%d want 3 split successors", rep2.IntervalsBuilt)
	}
	var live int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current`, emp).Scan(&live); err != nil || live != 3 {
		t.Fatalf("live=%d err=%v", live, err)
	}
	var kinds string
	if err := pool.QueryRow(ctx(t),
		`SELECT string_agg(kind, ',' ORDER BY interval_start) FROM attendance_interval WHERE employer_id = $1 AND is_current`, emp).Scan(&kinds); err != nil {
		t.Fatalf("kinds: %v", err)
	}
	if kinds != "work,break_unpaid,work" {
		t.Fatalf("kinds=%s", kinds)
	}
	var open int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_exception WHERE employer_id = $1 AND NOT resolved`, emp).Scan(&open); err != nil || open != 0 {
		t.Fatalf("open exceptions=%d err=%v", open, err)
	}
}

func TestConflictingRedelivery(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	one := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,cf-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(one), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	// Same source event ID, different instant: conflict, original kept.
	two := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,cf-1,` + no + `,2026-08-03T09:30:00+08:00,Asia/Singapore,in
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(two), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcConflictingRedeliver {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected conflicting_redelivery, got %+v", rep.Exceptions)
	}
	var at string
	if err := pool.QueryRow(ctx(t),
		`SELECT occurred_at::text FROM clock_event WHERE employer_id = $1 AND source_event_id = 'cf-1'`, emp).Scan(&at); err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if !strings.Contains(at, "09:00:00") {
		t.Fatalf("stored event overwritten: %s", at)
	}
}

func TestConcurrentCorrectionsSingleSuccessor(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,cc-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,cc-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,cc-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,cc-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	var intervalID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND kind = 'work' AND is_current ORDER BY interval_start LIMIT 1`, emp).Scan(&intervalID); err != nil {
		t.Fatalf("find interval: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			end := "2026-08-03T11:30:00+08:00"
			if i == 1 {
				end = "2026-08-03T11:45:00+08:00"
			}
			_, errs[i] = Correct(ctx(t), pool, emp, CorrectionInput{
				IntervalIDs: []string{intervalID}, Start: "2026-08-03T09:00:00+08:00", End: end,
				Kind: "work", Reason: "race",
			}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
		}(i)
	}
	wg.Wait()
	failed := 0
	for _, err := range errs {
		if err != nil {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("want exactly one winner, errs=%v", errs)
	}
	var successors int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE supersedes_id = $1::uuid`, intervalID).Scan(&successors); err != nil || successors != 1 {
		t.Fatalf("successors=%d err=%v", successors, err)
	}
	var groups int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group_predecessor WHERE predecessor_interval_id = $1::uuid`,
		intervalID).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("groups=%d err=%v want exactly one correction group", groups, err)
	}
}

func TestCorrectRejectsDecidedSheet(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,da-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,da-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,da-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,da-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')`, emp); err != nil {
		t.Fatalf("period: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay code: %v", err)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, "PP", "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 1 {
		t.Fatalf("generated=%+v skipped=%+v", gen.Generated, gen.Skipped)
	}
	sheet := gen.Generated[0].TimesheetID
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	var intervalID string
	if err := pool.QueryRow(ctx(t),
		`SELECT i.id FROM attendance_interval i
		 JOIN timesheet_line_interval li ON li.employer_id = i.employer_id AND li.interval_id = i.id
		 WHERE i.employer_id = $1 AND i.kind = 'work' ORDER BY i.interval_start LIMIT 1`, emp).Scan(&intervalID); err != nil {
		t.Fatalf("find linked interval: %v", err)
	}
	_, err = Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{intervalID}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T11:00:00+08:00",
		Kind: "work", Reason: "too late",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err == nil {
		t.Fatal("correction of approved-sheet interval succeeded")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeInvalidState {
		t.Fatalf("err=%v want invalid_state", err)
	}
	if !strings.Contains(ae.Message, "reissue") {
		t.Fatalf("err=%v must direct the operator to reissue", err)
	}
}

func TestSpanTooLong(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	// 33-hour candidate span: exceeds the 24h ceiling, both ends unpaired.
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,sg-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,sg-2,` + no + `,2026-08-04T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt != 0 {
		t.Fatalf("intervals=%d want 0", rep.IntervalsBuilt)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcSpanTooLong {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected span_too_long, got %+v", rep.Exceptions)
	}
	// The missing punches arrive later: both halves pair and the breach
	// resolves because every cited event is consumed.
	fix := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,sg-5,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
vendor-x,sg-6,` + no + `,2026-08-04T09:00:00+08:00,Asia/Singapore,in
`
	rep2, err := Import(ctx(t), pool, emp, strings.NewReader(fix), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import2: %v", err)
	}
	if rep2.IntervalsBuilt != 2 {
		t.Fatalf("intervals=%d want 2 recovered spans", rep2.IntervalsBuilt)
	}
	if rep2.ExceptionsResolved == 0 {
		t.Fatalf("resolved=%d want >0", rep2.ExceptionsResolved)
	}
}

func TestSpanDateMismatch(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	// 23-hour cross-date span within the ceiling but outside the DAY
	// shift (in-date is rostered, template is not cross-midnight).
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,dm-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,dm-2,` + no + `,2026-08-04T08:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt != 0 {
		t.Fatalf("intervals=%d want 0", rep.IntervalsBuilt)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcSpanDateMismatch {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected span_date_mismatch, got %+v", rep.Exceptions)
	}
}

func TestNoRosterLinkFlagged(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	// Span on an unrostered date: stored, flagged, never silently reviewed.
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,nr-1,` + no + `,2026-08-05T09:00:00+08:00,Asia/Singapore,in
vendor-x,nr-2,` + no + `,2026-08-05T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt != 1 {
		t.Fatalf("intervals=%d want 1 stored span", rep.IntervalsBuilt)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcNoRosterLink {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected no_roster_link, got %+v", rep.Exceptions)
	}
	rev, err := ReviewAttendance(ctx(t), pool, emp, "2026-08-05", "2026-08-05")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(rev.Records) != 1 || rev.Records[0].Status != "exception" {
		t.Fatalf("records=%+v want one exception day", rev.Records)
	}
}

func TestDuplicateSpanFreshIDs(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	span := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ds-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,ds-2,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(span), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Same clock span resent under fresh vendor IDs: the import must
	// succeed, pair nothing twice, and say so explicitly.
	resend := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ds-3,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,ds-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(resend), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("resend import: %v", err)
	}
	if rep.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want 0 on resend", rep.IntervalsBuilt)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcOverlappingSpan {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected overlapping_span, got %+v", rep.Exceptions)
	}
	var live int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current`, emp).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live=%d err=%v want exactly the original", live, err)
	}
	// The overlap exception persists and stays discoverable on review.
	rev, err := ReviewAttendance(ctx(t), pool, emp, "2026-08-03", "2026-08-03")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	found = false
	for _, e := range rev.Exceptions {
		if e.Code == ExcOverlappingSpan && !e.Resolved {
			found = true
		}
	}
	if !found {
		t.Fatalf("persisted exceptions=%+v", rev.Exceptions)
	}
}

func TestCorrectOnExceptionDay(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ce-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,ce-2,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
vendor-x,ce-3,` + no + `,2026-08-03T20:00:00+08:00,Asia/Singapore,in
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	var interval string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND is_current`, emp).Scan(&interval); err != nil {
		t.Fatalf("resolve interval: %v", err)
	}
	// An unresolved exception elsewhere on the day does not block a
	// surgical correction, and the correction must not clear it.
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{interval}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 1 || rep.SegmentIDs[0] == interval {
		t.Fatalf("rep=%+v", rep)
	}
	var open int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_exception
		 WHERE employer_id = $1 AND business_date = '2026-08-03' AND NOT resolved`, emp).Scan(&open); err != nil || open == 0 {
		t.Fatalf("open=%d err=%v want the missing punch still open", open, err)
	}
}

func TestMonthlyExcessIsComplianceOnly(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: "E001", LegalName: "E001", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	c, _ := wfm.CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := wfm.CreateProgramme(ctx(t), pool, emp, wfm.ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := wfm.CreateTeam(ctx(t), pool, emp, wfm.TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	if _, err := wfm.AddMembership(ctx(t), pool, emp, tm.Code, e.EmployeeNo, "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	tpl, err := wfm.CreateTemplate(ctx(t), pool, emp, wfm.TemplateInput{
		Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []wfm.SegmentInput{{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true}},
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	weeks := []string{"2026-08-03", "2026-08-10", "2026-08-17"}
	var days []string
	for _, w := range weeks {
		r, err := wfm.CreateRoster(ctx(t), pool, emp, wfm.RosterInput{Programme: p.Code, WeekStarting: w})
		if err != nil {
			t.Fatalf("roster: %v", err)
		}
		base, _ := time.Parse("2006-01-02", w)
		for d := 0; d < 5; d++ {
			day := base.AddDate(0, 0, d).Format("2006-01-02")
			days = append(days, day)
			if _, err := wfm.Assign(ctx(t), pool, emp, r.ID, wfm.AssignmentInput{
				Employee: e.EmployeeNo, Date: day, Template: tpl.Code,
			}); err != nil {
				t.Fatalf("assign %s: %v", day, err)
			}
		}
		if _, err := wfm.Publish(ctx(t), pool, emp, r.ID, "test"); err != nil {
			t.Fatalf("publish %s: %v", w, err)
		}
	}
	// 14-hour days against a 9-hour template: 5h excess a day, 75h for
	// the month, past the 72h statutory signal.
	var sb strings.Builder
	sb.WriteString("source,source_event_id,employee_no,occurred_at,timezone,kind\n")
	for i, day := range days {
		fmt.Fprintf(&sb, "vendor-x,ot-%d-a,E001,%sT09:00:00+08:00,Asia/Singapore,in\n", i, day)
		fmt.Fprintf(&sb, "vendor-x,ot-%d-b,E001,%sT23:00:00+08:00,Asia/Singapore,out\n", i, day)
	}
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(sb.String()), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt != 15 {
		t.Fatalf("built=%d want 15", rep.IntervalsBuilt)
	}
	rev, err := ReviewAttendance(ctx(t), pool, emp, "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	flagged := false
	for _, m := range rev.MonthlyExcess {
		if m.Month == "2026-08" && m.NeedsReview {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("excess=%+v want August flagged past 72h", rev.MonthlyExcess)
	}
	// The flag must not touch the hours: generation carries all 210.
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay code: %v", err)
	}
	var period string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP-0826', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id`, emp).Scan(&period); err != nil {
		t.Fatalf("period: %v", err)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 1 {
		t.Fatalf("generated=%+v skipped=%+v", gen.Generated, gen.Skipped)
	}
	got, err := timesheet.Get(ctx(t), pool, emp, gen.Generated[0].TimesheetID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].Hours != 210 {
		t.Fatalf("lines=%+v want one 210h REG line", got.Lines)
	}
}

func TestCorrectCrossDateResolvesAssignment(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: "E001", LegalName: "E001", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	c, _ := wfm.CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := wfm.CreateProgramme(ctx(t), pool, emp, wfm.ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := wfm.CreateTeam(ctx(t), pool, emp, wfm.TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	if _, err := wfm.AddMembership(ctx(t), pool, emp, tm.Code, e.EmployeeNo, "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	tpl, err := wfm.CreateTemplate(ctx(t), pool, emp, wfm.TemplateInput{
		Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []wfm.SegmentInput{{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true}},
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	r, err := wfm.CreateRoster(ctx(t), pool, emp, wfm.RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	asgMon, err := wfm.Assign(ctx(t), pool, emp, r.ID, wfm.AssignmentInput{
		Employee: e.EmployeeNo, Date: "2026-08-03", Template: tpl.Code,
	})
	if err != nil {
		t.Fatalf("assign mon: %v", err)
	}
	asgTue, err := wfm.Assign(ctx(t), pool, emp, r.ID, wfm.AssignmentInput{
		Employee: e.EmployeeNo, Date: "2026-08-04", Template: tpl.Code,
	})
	if err != nil {
		t.Fatalf("assign tue: %v", err)
	}
	if _, err := wfm.Publish(ctx(t), pool, emp, r.ID, "test"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,xd-1,E001,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,xd-2,E001,2026-08-03T23:30:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	var orig, origAid string
	if err := pool.QueryRow(ctx(t),
		`SELECT id, roster_assignment_id FROM attendance_interval WHERE employer_id = $1 AND is_current`,
		emp).Scan(&orig, &origAid); err != nil {
		t.Fatalf("resolve interval: %v", err)
	}
	if origAid != asgMon.ID {
		t.Fatalf("original links %s, want Monday assignment %s", origAid, asgMon.ID)
	}
	// Move the span onto Tuesday: the successor must resolve Tuesday's
	// assignment, both day records re-derive (Tuesday's is created
	// fresh by the move), and both records carry an audit row.
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-04T00:30:00+08:00", End: "2026-08-04T02:00:00+08:00",
		Kind: "work", Reason: "wrong date stamped",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(rep.SegmentIDs) != 1 {
		t.Fatalf("rep=%+v want one Tuesday segment", rep)
	}
	newID := rep.SegmentIDs[0]
	var newAid, newDate string
	if err := pool.QueryRow(ctx(t),
		`SELECT roster_assignment_id, local_date::text FROM attendance_interval WHERE employer_id = $1 AND id = $2`,
		emp, newID).Scan(&newAid, &newDate); err != nil {
		t.Fatalf("successor: %v", err)
	}
	if newDate != "2026-08-04" || newAid != asgTue.ID {
		t.Fatalf("successor date=%s assignment=%s, want 2026-08-04/%s", newDate, newAid, asgTue.ID)
	}
	var rows int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction WHERE employer_id = $1 AND new_value = $2`,
		emp, newID).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("correction rows=%d err=%v want one audit row per affected day record", rows, err)
	}
	var monActual, tueActual float64
	var monAid, tueAid string
	if err := pool.QueryRow(ctx(t),
		`SELECT actual_min, roster_assignment_id FROM attendance_record WHERE employer_id = $1 AND business_date = '2026-08-03'`,
		emp).Scan(&monActual, &monAid); err != nil {
		t.Fatalf("monday record: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT actual_min, roster_assignment_id FROM attendance_record WHERE employer_id = $1 AND business_date = '2026-08-04'`,
		emp).Scan(&tueActual, &tueAid); err != nil {
		t.Fatalf("tuesday record: %v", err)
	}
	if monActual != 0 || tueActual != 90 {
		t.Fatalf("actual mon=%.0f tue=%.0f want 0/90", monActual, tueActual)
	}
	if monAid != asgMon.ID || tueAid != asgTue.ID {
		t.Fatalf("record assignments mon=%s tue=%s", monAid, tueAid)
	}
}

func TestConcurrentImportsExactlyOnce(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ci-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,ci-2,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent import: %v", err)
		}
	}
	var events, live int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event WHERE employer_id = $1`, emp).Scan(&events); err != nil || events != 2 {
		t.Fatalf("events=%d err=%v want exactly-once storage", events, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current`, emp).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live=%d err=%v want one paired span", live, err)
	}
}

// Replaying the same import recovers without losing or duplicating
// evidence: the second delivery stores nothing new and pairs nothing
// twice. (Import runs in one transaction, so a failure between event
// insertion and pairing rolls back whole; the replay then sees the
// pre-import state exactly.)
func TestImportReplayIdempotent(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ri-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,ri-2,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	first, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.IntervalsBuilt != 1 {
		t.Fatalf("first built=%d want 1", first.IntervalsBuilt)
	}
	second, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.IntervalsBuilt != 0 {
		t.Fatalf("replay built=%d want 0", second.IntervalsBuilt)
	}
	var events, live int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event WHERE employer_id = $1`, emp).Scan(&events); err != nil || events != 2 {
		t.Fatalf("events=%d err=%v want exactly-once storage", events, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current`, emp).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live=%d err=%v want one paired span", live, err)
	}
}
