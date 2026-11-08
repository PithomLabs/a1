package attendance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

func sameInstant(t *testing.T, dbText, want string) bool {
	t.Helper()
	parse := func(s string) time.Time {
		if v, err := time.Parse(time.RFC3339, s); err == nil {
			return v
		}
		v, err := time.Parse("2006-01-02 15:04:05-07", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}
	return parse(dbText).UnixMicro() == parse(want).UnixMicro()
}

func nightChain(t *testing.T, pool *pgxpool.Pool, tag, breakKind string, extraDates ...string) (string, string) {
	t.Helper()
	return chainTpl(t, pool, tag, "2026-08-03", "NIGHT", "22:00", "06:00", true, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 90, Paid: true},
		{Kind: breakKind, StartOffsetMin: 90, DurationMin: 60, Paid: breakKind == "break_paid"},
		{Kind: "work", StartOffsetMin: 150, DurationMin: 330, Paid: true},
	}, extraDates...)
}

type datedBreak struct {
	date, start, end string
}

// assertDatedBreaks verifies every live break interval sits within one
// local date with the exact expected bounds, shares the break's event
// evidence, and sums to the full break duration.
func assertDatedBreaks(t *testing.T, pool *pgxpool.Pool, emp string, want []datedBreak) {
	t.Helper()
	rows, err := pool.Query(ctx(t),
		`SELECT local_date::text, interval_start::text, interval_end::text,
		   source_refs -> 'clock_event_ids'
		 FROM attendance_interval WHERE employer_id = $1 AND is_current
		   AND kind IN ('break_paid', 'break_unpaid') ORDER BY interval_start`, emp)
	if err != nil {
		t.Fatalf("breaks: %v", err)
	}
	defer rows.Close()
	var got []datedBreak
	for rows.Next() {
		var d datedBreak
		var refs string
		if err := rows.Scan(&d.date, &d.start, &d.end, &refs); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, datedBreak{date: d.date, start: d.start, end: d.end})
		var ids []string
		if err := json.Unmarshal([]byte(refs), &ids); err != nil || len(ids) != 2 {
			t.Fatalf("piece %+v cites %q want both break events", d, refs)
		}
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	for i := range want {
		if got[i].date != want[i].date {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
		if !sameInstant(t, got[i].start, want[i].start) || !sameInstant(t, got[i].end, want[i].end) {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
	}
}

func TestMidnightBreakSplit(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_unpaid", "2026-08-04")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,mb-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,mb-2,` + no + `,2026-08-03T23:30:00+08:00,Asia/Singapore,break_start
vendor-x,mb-3,` + no + `,2026-08-04T00:30:00+08:00,Asia/Singapore,break_end
vendor-x,mb-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(rep.Exceptions) != 0 {
		t.Fatalf("exceptions=%+v", rep.Exceptions)
	}
	assertDatedBreaks(t, pool, emp, []datedBreak{
		{"2026-08-03", "2026-08-03T23:30:00+08:00", "2026-08-04T00:00:00+08:00"},
		{"2026-08-04", "2026-08-04T00:00:00+08:00", "2026-08-04T00:30:00+08:00"},
	})
}

func TestMidnightBreakExactBoundary(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_unpaid")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,eb-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,eb-2,` + no + `,2026-08-03T23:00:00+08:00,Asia/Singapore,break_start
vendor-x,eb-3,` + no + `,2026-08-04T00:00:00+08:00,Asia/Singapore,break_end
vendor-x,eb-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	// The midnight boundary instant belongs to exactly one piece: the
	// break ending at 00:00 stays whole on 08-03.
	assertDatedBreaks(t, pool, emp, []datedBreak{
		{"2026-08-03", "2026-08-03T23:00:00+08:00", "2026-08-04T00:00:00+08:00"},
	})
}

func TestMidnightBreakOneSecond(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_unpaid")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,os-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,os-2,` + no + `,2026-08-03T23:59:59+08:00,Asia/Singapore,break_start
vendor-x,os-3,` + no + `,2026-08-04T00:00:01+08:00,Asia/Singapore,break_end
vendor-x,os-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	assertDatedBreaks(t, pool, emp, []datedBreak{
		{"2026-08-03", "2026-08-03T23:59:59+08:00", "2026-08-04T00:00:00+08:00"},
		{"2026-08-04", "2026-08-04T00:00:00+08:00", "2026-08-04T00:00:01+08:00"},
	})
}

func TestMidnightBreakPaidKind(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_paid", "2026-08-04")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,pb-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,pb-2,` + no + `,2026-08-03T23:30:00+08:00,Asia/Singapore,break_start
vendor-x,pb-3,` + no + `,2026-08-04T00:30:00+08:00,Asia/Singapore,break_end
vendor-x,pb-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(rep.Exceptions) != 0 {
		t.Fatalf("exceptions=%+v", rep.Exceptions)
	}
	var kinds []string
	rows, err := pool.Query(ctx(t),
		`SELECT DISTINCT kind FROM attendance_interval WHERE employer_id = $1 AND is_current
		   AND kind IN ('break_paid', 'break_unpaid')`, emp)
	if err != nil {
		t.Fatalf("kinds: %v", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		kinds = append(kinds, k)
	}
	rows.Close()
	if len(kinds) != 1 || kinds[0] != "break_paid" {
		t.Fatalf("kinds=%v want only break_paid", kinds)
	}
	assertDatedBreaks(t, pool, emp, []datedBreak{
		{"2026-08-03", "2026-08-03T23:30:00+08:00", "2026-08-04T00:00:00+08:00"},
		{"2026-08-04", "2026-08-04T00:00:00+08:00", "2026-08-04T00:30:00+08:00"},
	})
}

func TestMidnightBreakDuplicate(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_unpaid")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,db-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,db-2,` + no + `,2026-08-03T23:30:00+08:00,Asia/Singapore,break_start
vendor-x,db-3,` + no + `,2026-08-04T00:30:00+08:00,Asia/Singapore,break_end
vendor-x,db-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("first: %v", err)
	}
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if rep.Duplicates != 4 || rep.IntervalsBuilt != 0 {
		t.Fatalf("dup=%d built=%d", rep.Duplicates, rep.IntervalsBuilt)
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current
		   AND kind IN ('break_paid', 'break_unpaid')`, emp).Scan(&n); err != nil || n != 2 {
		t.Fatalf("break pieces=%d err=%v want 2, no double-attach", n, err)
	}
}

func TestMidnightBreakMissingEnd(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_unpaid")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,me-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,me-2,` + no + `,2026-08-03T23:30:00+08:00,Asia/Singapore,break_start
vendor-x,me-3,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcDanglingBreak {
			found = true
		}
	}
	if !found {
		t.Fatalf("exceptions=%+v want dangling_break", rep.Exceptions)
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND is_current
		   AND kind IN ('break_paid', 'break_unpaid')`, emp).Scan(&n); err != nil || n != 0 {
		t.Fatalf("break pieces=%d err=%v want none", n, err)
	}
}

func TestMidnightBreakLateDelivery(t *testing.T) {
	pool := testPool(t)
	emp, no := nightChain(t, pool, "E", "break_unpaid")
	span := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ld-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,ld-2,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(span), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("span: %v", err)
	}
	late := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ld-3,` + no + `,2026-08-03T23:30:00+08:00,Asia/Singapore,break_start
vendor-x,ld-4,` + no + `,2026-08-04T00:30:00+08:00,Asia/Singapore,break_end
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(late), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("late: %v", err)
	}
	if rep.IntervalsBuilt == 0 {
		t.Fatalf("built=0 want the late break attached across both dated halves")
	}
	assertDatedBreaks(t, pool, emp, []datedBreak{
		{"2026-08-03", "2026-08-03T23:30:00+08:00", "2026-08-04T00:00:00+08:00"},
		{"2026-08-04", "2026-08-04T00:00:00+08:00", "2026-08-04T00:30:00+08:00"},
	})
	// Both break events are consumed: nothing dangles, nothing double-counts.
	var unpaired int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event e WHERE e.employer_id = $1
		   AND e.source_event_id IN ('ld-3', 'ld-4')
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)`,
		emp).Scan(&unpaired); err != nil || unpaired != 0 {
		t.Fatalf("unpaired=%d err=%v", unpaired, err)
	}
}

func TestBreakSpanningRosterBoundary(t *testing.T) {
	pool := testPool(t)
	// Monday night shift only: Tuesday has no assignment of its own.
	emp, no := nightChain(t, pool, "E", "break_unpaid")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,rb-1,` + no + `,2026-08-03T22:00:00+08:00,Asia/Singapore,in
vendor-x,rb-2,` + no + `,2026-08-03T23:30:00+08:00,Asia/Singapore,break_start
vendor-x,rb-3,` + no + `,2026-08-04T00:30:00+08:00,Asia/Singapore,break_end
vendor-x,rb-4,` + no + `,2026-08-04T06:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	// The Tuesday piece must not silently inherit Monday's assignment.
	var tueAid *string
	if err := pool.QueryRow(ctx(t),
		`SELECT roster_assignment_id FROM attendance_interval
		 WHERE employer_id = $1 AND is_current AND local_date = '2026-08-04'
		   AND kind IN ('break_paid', 'break_unpaid')`, emp).Scan(&tueAid); err != nil {
		t.Fatalf("tuesday piece: %v", err)
	}
	if tueAid != nil {
		t.Fatalf("tuesday aid=%s want NULL (no Tuesday assignment)", *tueAid)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcNoRosterLink {
			found = true
		}
	}
	if !found {
		t.Fatalf("exceptions=%+v want no_roster_link for the unassigned date", rep.Exceptions)
	}
}
