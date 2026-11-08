package wfm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/hr"
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
	return fmt.Sprintf("wfm_%s_%d_%s", base, time.Now().UnixNano(), s)
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

func mkEmployee(t *testing.T, pool *pgxpool.Pool, emp, no string) string {
	t.Helper()
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG", StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("mkEmployee: %v", err)
	}
	return e.ID
}

func mkSkill(t *testing.T, pool *pgxpool.Pool, code string) {
	t.Helper()
	_, err := pool.Exec(ctx(t),
		`INSERT INTO skill (code, name, kind) VALUES ($1, $1, 'language') ON CONFLICT (code) DO NOTHING`, code)
	if err != nil {
		t.Fatalf("mkSkill: %v", err)
	}
}

func TestBPOChain(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, err := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	p, err := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	if err != nil {
		t.Fatalf("programme: %v", err)
	}
	if p.Timezone != "Asia/Singapore" {
		t.Fatalf("timezone = %s", p.Timezone)
	}
	tm, err := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	e := mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	_ = e
	if _, err := ListClients(ctx(t), pool, emp); err != nil {
		t.Fatalf("list clients: %v", err)
	}
	if _, err := ListProgrammes(ctx(t), pool, emp); err != nil {
		t.Fatalf("list programmes: %v", err)
	}
	if _, err := ListTeams(ctx(t), pool, emp); err != nil {
		t.Fatalf("list teams: %v", err)
	}
}

func TestSkillGating(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	mkSkill(t, pool, "EN-SUPPORT")
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	mkEmployee(t, pool, emp, "E001")
	mkEmployee(t, pool, emp, "E002")
	if err := RequireSkills(ctx(t), pool, emp, p.Code, []SkillGrant{{Skill: "EN-SUPPORT", Level: 3}}, "test"); err != nil {
		t.Fatalf("require: %v", err)
	}
	if err := AssignSkills(ctx(t), pool, emp, "E001", "2026-01-01", []SkillGrant{{Skill: "EN-SUPPORT", Level: 3}}, "test"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := AssignSkills(ctx(t), pool, emp, "E002", "2026-01-01", []SkillGrant{{Skill: "EN-SUPPORT", Level: 2}}, "test"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test")
	AddMembership(ctx(t), pool, emp, tm.Code, "E002", "agent", "2026-01-01", "test")
	r, err := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	tpl, err := CreateTemplate(ctx(t), pool, emp, TemplateInput{
		Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []SegmentInput{{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true}},
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign skilled: %v", err)
	}
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E002", Date: "2026-08-04", Template: tpl.Code}); err == nil {
		t.Fatal("expected skill rejection for E002")
	}
}

func TestRosterPublishFreeze(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test")
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{
		Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00",
	})
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-04", RestDay: true}); err != nil {
		t.Fatalf("rest day: %v", err)
	}
	pub, err := Publish(ctx(t), pool, emp, r.ID, "test-op")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.Status != "published" || pub.LegalValidationStatus != "unavailable" {
		t.Fatalf("publish state = %+v", pub)
	}
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-05", RestDay: true}); err == nil {
		t.Fatal("expected frozen error assigning to published roster")
	}
	d, err := Show(ctx(t), pool, emp, r.ID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(d.Assignments) != 2 {
		t.Fatalf("assignments = %d, want 2", len(d.Assignments))
	}
}
