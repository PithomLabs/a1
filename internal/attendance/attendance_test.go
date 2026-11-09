package attendance

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	migrate := func(ctx context.Context, migDSN string) error {
		_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	}
	setup := dbutil.Setup(t, migrate)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := coredatabase.Open(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		pool.Close()
		t.Fatalf("suite pool runs as %q, want app role", who)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testAdmin opens the same test database as the schema owner, for
// fixtures that must set privileged state (e.g. IMPLEMENTABLE packs).
// Product assertions always read back through the app pool.
func testAdmin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	migrate := func(ctx context.Context, migDSN string) error {
		_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	}
	setup := dbutil.Setup(t, migrate)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := coredatabase.Open(ctx, setup.AdminDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(admin.Close)
	return admin
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func uniq(t *testing.T, s string) string {
	t.Helper()
	base := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	return fmt.Sprintf("att_%s_%d_%s", base, time.Now().UnixNano(), s)
}

func mkEmployer(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&id)
	if err != nil {
		t.Fatalf("mkEmployer: %v", err)
	}
	return id
}

// chain builds employer, employee, programme roster chain and a day template
// with a one-hour unpaid break starting 3h in. Returns employer, employeeNo.
func chain(t *testing.T, pool *pgxpool.Pool, tag, week string) (string, string) {
	t.Helper()
	return chainTpl(t, pool, tag, week, "DAY", "09:00", "18:00", false, []wfm.SegmentInput{
		{Kind: "work", StartOffsetMin: 0, DurationMin: 180, Paid: true},
		{Kind: "break_unpaid", StartOffsetMin: 180, DurationMin: 60, Paid: false},
		{Kind: "work", StartOffsetMin: 240, DurationMin: 300, Paid: true},
	})
}

// chainTpl is chain with a caller-chosen template.
func chainTpl(t *testing.T, pool *pgxpool.Pool, tag, week, tplCode, startLocal, endLocal string, cross bool, segs []wfm.SegmentInput, extraDates ...string) (string, string) {
	t.Helper()
	emp := mkEmployer(t, pool, uniq(t, tag))
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
		Code: tplCode, Name: tplCode, StartLocal: startLocal, EndLocal: endLocal,
		CrossMidnight: cross, Segments: segs,
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	r, err := wfm.CreateRoster(ctx(t), pool, emp, wfm.RosterInput{Programme: p.Code, WeekStarting: week})
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	for _, d := range append([]string{week}, extraDates...) {
		if _, err := wfm.Assign(ctx(t), pool, emp, r.ID, wfm.AssignmentInput{
			Employee: e.EmployeeNo, Date: d, Template: tpl.Code,
		}); err != nil {
			t.Fatalf("assign %s: %v", d, err)
		}
	}
	if _, err := wfm.Publish(ctx(t), pool, emp, r.ID, "test"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return emp, e.EmployeeNo
}

func TestImportPairReview(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,evt-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,evt-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,evt-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	t.Logf("exceptions: %+v", rep.Exceptions)
	if rep.EventsStored != 4 || rep.Duplicates != 0 {
		t.Fatalf("stored=%d dup=%d", rep.EventsStored, rep.Duplicates)
	}
	if rep.IntervalsBuilt != 3 {
		t.Fatalf("intervals=%d want 3 (work/break/work)", rep.IntervalsBuilt)
	}
	if len(rep.Exceptions) != 0 {
		t.Fatalf("exceptions=%+v", rep.Exceptions)
	}
	// Replay returns duplicates without new intervals.
	rep2, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep2.Duplicates != 4 || rep2.EventsStored != 0 || rep2.IntervalsBuilt != 0 {
		t.Fatalf("replay=%+v", rep2)
	}
	rev, err := ReviewAttendance(ctx(t), pool, emp, "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(rev.Records) != 1 {
		t.Fatalf("records=%d", len(rev.Records))
	}
	rec := rev.Records[0]
	if rec.ActualMin == nil || *rec.ActualMin != 540 {
		t.Fatalf("actual=%v want 540", rec.ActualMin)
	}
	if rec.PaidMin == nil || *rec.PaidMin != 480 {
		t.Fatalf("paid=%v want 480", rec.PaidMin)
	}
	if len(rev.UnpairedEvents) != 0 {
		t.Fatalf("unpaired=%d", len(rev.UnpairedEvents))
	}
}

func TestMissingPunchAndCorrection(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-9,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(rep.Exceptions) != 1 || rep.Exceptions[0].Code != ExcMissingPunch {
		t.Fatalf("exceptions=%+v", rep.Exceptions)
	}
	// The out arrives in a later delivery and pairs across imports.
	csv2 := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-10,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep2, err := Import(ctx(t), pool, emp, strings.NewReader(csv2), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import2: %v", err)
	}
	if rep2.IntervalsBuilt != 1 {
		t.Fatalf("intervals=%d want 1", rep2.IntervalsBuilt)
	}
	// Correct the interval end; the old row stays, the new row supersedes.
	var intervalID string
	err = pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND kind = 'work'`, emp).Scan(&intervalID)
	if err != nil {
		t.Fatalf("find interval: %v", err)
	}
	crep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{intervalID}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if len(crep.SegmentIDs) != 1 || crep.SegmentIDs[0] == intervalID {
		t.Fatal("expected new superseding interval")
	}
	var count int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1`, emp).Scan(&count); err != nil || count != 2 {
		t.Fatalf("intervals=%d err=%v", count, err)
	}
	rev, _ := ReviewAttendance(ctx(t), pool, emp, "2026-08-01", "2026-08-31")
	if rev.Records[0].ActualMin == nil || *rev.Records[0].ActualMin != 480 {
		t.Fatalf("actual=%v want 480 after correction", rev.Records[0].ActualMin)
	}
}

func TestMissingBreakEvidence(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-20,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,evt-21,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	found := false
	for _, e := range rep.Exceptions {
		if e.Code == ExcMissingBreak {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_break_evidence, got %+v", rep.Exceptions)
	}
}
