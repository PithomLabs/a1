package wfm

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func lastAudit(t *testing.T, pool *pgxpool.Pool, emp, action string) (actor, service, entity, entityID, after string) {
	t.Helper()
	var eid, aft *string
	if err := pool.QueryRow(ctx(t),
		`SELECT actor, service, entity, entity_id::text, after_state::text
		 FROM audit_event WHERE employer_id = $1 AND action = $2
		 ORDER BY occurred_at DESC LIMIT 1`, emp, action).Scan(
		&actor, &service, &entity, &eid, &aft); err != nil {
		t.Fatalf("audit %s: %v", action, err)
	}
	if eid != nil {
		entityID = *eid
	}
	if aft != nil {
		after = *aft
	}
	return actor, service, entity, entityID, after
}

func TestMembershipAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	id, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "op-1")
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	actor, service, entity, eid, after := lastAudit(t, pool, emp, "membership.add")
	if actor != "op-1" || service != "wfm" || entity != "team_membership" || eid != id {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	if !strings.Contains(after, "E001") || !strings.Contains(after, "agent") {
		t.Fatalf("after=%s want member and role", after)
	}
}

func TestSkillsAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	mkSkill(t, pool, "EN-SUPPORT")
	mkEmployee(t, pool, emp, "E001")
	if err := AssignSkills(ctx(t), pool, emp, "E001", "2026-01-01",
		[]SkillGrant{{Skill: "EN-SUPPORT", Level: 3}}, "op-2"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	actor, service, entity, _, after := lastAudit(t, pool, emp, "skills.assign")
	if actor != "op-2" || service != "wfm" || entity != "employee_skill" {
		t.Fatalf("audit=%s/%s/%s", actor, service, entity)
	}
	if !strings.Contains(after, "EN-SUPPORT") {
		t.Fatalf("after=%s want the grant", after)
	}
}

func TestRequireSkillsAudited(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	mkSkill(t, pool, "EN-SUPPORT")
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	if err := RequireSkills(ctx(t), pool, emp, p.Code,
		[]SkillGrant{{Skill: "EN-SUPPORT", Level: 3}}, "op-2b"); err != nil {
		t.Fatalf("require: %v", err)
	}
	actor, service, entity, _, after := lastAudit(t, pool, emp, "skills.require")
	if actor != "op-2b" || service != "wfm" || entity != "programme_required_skill" {
		t.Fatalf("audit=%s/%s/%s", actor, service, entity)
	}
	if !strings.Contains(after, "EN-SUPPORT") || !strings.Contains(after, "PRG") {
		t.Fatalf("after=%s want programme and grant", after)
	}
}

func TestPublishAudited(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "PA", "2026-08-03")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, roster, "op-3"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	actor, _, entity, eid, after := lastAudit(t, pool, emp, "roster.publish")
	if actor != "op-3" || entity != "roster" || eid != roster {
		t.Fatalf("audit=%s/%s/%s", actor, entity, eid)
	}
	if after == "" {
		t.Fatalf("after empty")
	}
}

func TestReviseAudited(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "RA", "2026-08-03")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, roster, "op-3"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := Revise(ctx(t), pool, emp, roster, "fix", "op-4"); err != nil {
		t.Fatalf("revise: %v", err)
	}
	actor, _, entity, eid, after := lastAudit(t, pool, emp, "roster.revise")
	if actor != "op-4" || entity != "roster" || eid != roster {
		t.Fatalf("audit=%s/%s/%s", actor, entity, eid)
	}
	if !strings.Contains(after, "fix") {
		t.Fatalf("after=%s want the reason", after)
	}
}
