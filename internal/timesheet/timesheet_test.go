package timesheet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/attendance"
	"github.com/PithomLabs/a1/internal/hr"
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

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func uniq(t *testing.T, s string) string {
	t.Helper()
	base := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	return fmt.Sprintf("ts_%s_%d_%s", base, time.Now().UnixNano(), s)
}

func setup(t *testing.T, pool *pgxpool.Pool, tag string) (emp, period string) {
	t.Helper()
	code := uniq(t, tag)
	var empID string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&empID)
	if err != nil {
		t.Fatalf("employer: %v", err)
	}
	e, err := hr.CreateEmployee(ctx(t), pool, empID, hr.EmployeeInput{
		EmployeeNo: "E001", LegalName: "E001", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	c, _ := wfm.CreateClient(ctx(t), pool, empID, "CLI", "Client")
	p, _ := wfm.CreateProgramme(ctx(t), pool, empID, wfm.ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := wfm.CreateTeam(ctx(t), pool, empID, wfm.TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	wfm.AddMembership(ctx(t), pool, empID, tm.Code, e.EmployeeNo, "agent", "2026-01-01", "test")
	tpl, _ := wfm.CreateTemplate(ctx(t), pool, empID, wfm.TemplateInput{
		Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []wfm.SegmentInput{{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true}},
	})
	r, _ := wfm.CreateRoster(ctx(t), pool, empID, wfm.RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	wfm.Assign(ctx(t), pool, empID, r.ID, wfm.AssignmentInput{Employee: e.EmployeeNo, Date: "2026-08-03", Template: tpl.Code})
	wfm.Publish(ctx(t), pool, empID, r.ID, "test")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-ts1,E001,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,evt-ts2,E001,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	if _, err := attendance.Import(ctx(t), pool, empID, strings.NewReader(csv), "test", RegenerateDraft, ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	_, err = pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`)
	if err != nil {
		t.Fatalf("pay code: %v", err)
	}
	var periodID string
	err = pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP-0826', '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')
		 RETURNING id`, empID).Scan(&periodID)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	return empID, periodID
}

func TestGenerateSubmitApprove(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "E")
	gen, err := Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 1 || len(gen.Skipped) != 0 {
		t.Fatalf("gen=%+v", gen)
	}
	var sheetID string
	err = pool.QueryRow(ctx(t), `SELECT id FROM timesheet WHERE employer_id = $1`, emp).Scan(&sheetID)
	if err != nil {
		t.Fatalf("find sheet: %v", err)
	}
	got, err := Get(ctx(t), pool, emp, sheetID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].PayCode != RegularPayCode {
		t.Fatalf("lines=%+v", got.Lines)
	}
	if got.Lines[0].Hours != 9 || len(got.Lines[0].IntervalIDs) != 1 {
		t.Fatalf("line=%+v", got.Lines[0])
	}
	sub, err := Submit(ctx(t), pool, emp, sheetID, "test-op")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if sub.Status != "submitted" {
		t.Fatalf("status=%s", sub.Status)
	}
	app, err := Approve(ctx(t), pool, emp, sheetID, "test-op")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if app.Status != "approved" {
		t.Fatalf("status=%s", app.Status)
	}
	// Idempotent duplicate approval returns the same state.
	app2, err := Approve(ctx(t), pool, emp, sheetID, "test-op")
	if err != nil || app2.Status != "approved" {
		t.Fatalf("re-approve err=%v", err)
	}
	// Regeneration skips the existing sheet.
	gen2, err := Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(gen2.Generated) != 0 || len(gen2.Skipped) != 1 {
		t.Fatalf("gen2=%+v", gen2)
	}
}

func TestSkipOnUnpairedEvents(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "E")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-bad,E001,2026-08-04T09:00:00+08:00,Asia/Singapore,in
`
	if _, err := attendance.Import(ctx(t), pool, emp, strings.NewReader(csv), "test", RegenerateDraft, ChainTerminal); err != nil {
		t.Fatalf("import: %v", err)
	}
	gen, err := Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 0 || len(gen.Skipped) != 1 {
		t.Fatalf("gen=%+v", gen)
	}
	if !strings.Contains(gen.Skipped[0].Reason, "unpaired") {
		t.Fatalf("reason=%s", gen.Skipped[0].Reason)
	}
}

// The effective approval is the deepest approval down the chain: a
// rejected correction in the middle does not split the line, the
// later approval supersedes the earlier one by position.
func TestEffectiveApprovedFollowsChain(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "AMB")
	var empID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`,
		emp).Scan(&empID); err != nil {
		t.Fatalf("employee: %v", err)
	}
	var v1 string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status, approved_by, approved_at)
		 VALUES ($1, $2, $3, 'approved', 'op', now()) RETURNING id`,
		emp, empID, period).Scan(&v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	var v2 string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status, supersedes_id)
		 VALUES ($1, $2, $3, 'rejected', $4) RETURNING id`,
		emp, empID, period, v1).Scan(&v2); err != nil {
		t.Fatalf("v2: %v", err)
	}
	var v3 string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status, approved_by, approved_at, supersedes_id)
		 VALUES ($1, $2, $3, 'approved', 'op', now(), $4) RETURNING id`,
		emp, empID, period, v2).Scan(&v3); err != nil {
		t.Fatalf("v3: %v", err)
	}
	got, err := EffectiveApproved(ctx(t), pool, emp, empID, period)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got != v3 {
		t.Fatalf("effective = %s, want the deepest approval %s", got, v3)
	}
}

// A chain link pointing outside the employee's scope is refused,
// never silently followed into another employee's approvals.
func TestEffectiveApprovedCrossScopeRefused(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "AMB")
	var e1, e2 string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`,
		emp).Scan(&e1); err != nil {
		t.Fatalf("e1: %v", err)
	}
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: "E002", LegalName: "E002", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("e2: %v", err)
	}
	e2 = e.ID
	var v1 string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status, approved_by, approved_at)
		 VALUES ($1, $2, $3, 'approved', 'op', now()) RETURNING id`,
		emp, e1, period).Scan(&v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status, approved_by, approved_at, supersedes_id)
		 VALUES ($1, $2, $3, 'approved', 'op', now(), $4)`,
		emp, e2, period, v1); err != nil {
		t.Fatalf("rogue link: %v", err)
	}
	if _, err := EffectiveApproved(ctx(t), pool, emp, e1, period); err == nil {
		t.Fatal("cross-employee chain link silently followed")
	} else if apperr.AsCode(err) != apperr.CodeInvalidState || !strings.Contains(err.Error(), "leaves its employee or period") {
		t.Fatalf("err=%v want invalid_state scope refusal", err)
	}
}
