package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PithomLabs/a1/internal/attendance"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

// TestDuplicateFeedToApprovedTimesheet is the B1 operator journey:
// duplicate feed with fresh IDs → inspect the exception → confirm the
// duplicate → generate the timesheet → verify one valid allocation.
// Every step runs through the same service calls the CLI and REST
// layer serve, against real PostgreSQL as the app role.
func TestDuplicateFeedToApprovedTimesheet(t *testing.T) {
	pool := appPool
	tag := fmt.Sprintf("e2e_dup_%d", time.Now().UnixNano())

	var emp string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		tag, tag, "UEN-"+tag).Scan(&emp); err != nil {
		t.Fatalf("employer: %v", err)
	}
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
	if _, err := wfm.Assign(ctx(t), pool, emp, r.ID, wfm.AssignmentInput{
		Employee: e.EmployeeNo, Date: "2026-08-03", Template: tpl.Code,
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := wfm.Publish(ctx(t), pool, emp, r.ID, "test"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	feed := func(inID, outID string) string {
		return `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,` + inID + `,E001,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,` + outID + `,E001,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	}
	if _, err := attendance.Import(ctx(t), pool, emp, strings.NewReader(feed("e2e-1", "e2e-2")), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("original feed: %v", err)
	}
	dup, err := attendance.Import(ctx(t), pool, emp, strings.NewReader(feed("e2e-3", "e2e-4")), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("duplicate feed: %v", err)
	}
	if dup.IntervalsBuilt != 0 {
		t.Fatalf("built=%d want 0 (duplicate must not double-pair)", dup.IntervalsBuilt)
	}

	// Inspect: the review names the overlapping exception and the exact
	// unpaired event IDs the operator must decide on.
	rev, err := attendance.ReviewAttendance(ctx(t), pool, emp, "2026-08-03", "2026-08-03")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	var overlap *attendance.PersistedException
	for i := range rev.Exceptions {
		if rev.Exceptions[i].Code == "overlapping_span" && !rev.Exceptions[i].Resolved {
			overlap = &rev.Exceptions[i]
		}
	}
	if overlap == nil {
		t.Fatalf("review exceptions=%+v want an open overlapping_span", rev.Exceptions)
	}
	bySource := map[string]attendance.UnpairedEvent{}
	for _, u := range rev.UnpairedEvents {
		bySource[u.SourceEventID] = u
	}
	dupIn, okIn := bySource["e2e-3"]
	dupOut, okOut := bySource["e2e-4"]
	if !okIn || !okOut {
		t.Fatalf("unpaired=%+v want the duplicate endpoints with exact event IDs", rev.UnpairedEvents)
	}
	var origIn, origOut string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM clock_event WHERE employer_id = $1 AND source_event_id = 'e2e-1'`,
		emp).Scan(&origIn); err != nil {
		t.Fatalf("original in: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM clock_event WHERE employer_id = $1 AND source_event_id = 'e2e-2'`,
		emp).Scan(&origOut); err != nil {
		t.Fatalf("original out: %v", err)
	}

	// Confirm: both duplicate endpoints against their canonical events.
	for _, d := range []attendance.DispositionInput{
		{EventID: dupIn.ID, CanonicalID: origIn, Reason: "vendor resend under fresh IDs"},
		{EventID: dupOut.ID, CanonicalID: origOut, Reason: "vendor resend under fresh IDs"},
	} {
		if _, err := attendance.DispositionDuplicate(ctx(t), pool, emp, d, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
			t.Fatalf("disposition %+v: %v", d, err)
		}
	}

	// Generate: one sheet, one regular line, one allocated interval.
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay code: %v", err)
	}
	var period string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP-0826', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id::text`, emp).Scan(&period); err != nil {
		t.Fatalf("period: %v", err)
	}
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 1 || len(gen.Skipped) != 0 {
		t.Fatalf("gen=%+v want exactly one sheet", gen)
	}
	got, err := timesheet.Get(ctx(t), pool, emp, gen.Generated[0].TimesheetID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].PayCode != timesheet.RegularPayCode ||
		got.Lines[0].Hours != 9 || len(got.Lines[0].IntervalIDs) != 1 {
		t.Fatalf("lines=%+v want one 9h regular line over one interval", got.Lines)
	}
	// The raw duplicate events are preserved, excluded, and settled:
	// nothing unpaired remains and the exception history is retained.
	rev2, err := attendance.ReviewAttendance(ctx(t), pool, emp, "2026-08-03", "2026-08-03")
	if err != nil {
		t.Fatalf("re-review: %v", err)
	}
	if len(rev2.UnpairedEvents) != 0 {
		t.Fatalf("unpaired=%+v want none left", rev2.UnpairedEvents)
	}
	var raw, settled int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*), count(*) FILTER (WHERE d.event_id IS NOT NULL)
		 FROM clock_event e LEFT JOIN clock_event_disposition d ON d.event_id = e.id
		 WHERE e.employer_id = $1 AND e.source_event_id IN ('e2e-3', 'e2e-4')`,
		emp).Scan(&raw, &settled); err != nil || raw != 2 || settled != 2 {
		t.Fatalf("raw=%d settled=%d err=%v want both preserved and dispositioned", raw, settled, err)
	}
}
