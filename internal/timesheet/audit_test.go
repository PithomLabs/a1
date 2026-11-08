package timesheet

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

func sheetID(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t), `SELECT id FROM timesheet WHERE employer_id = $1 ORDER BY created_at LIMIT 1`, emp).Scan(&id); err != nil {
		t.Fatalf("find sheet: %v", err)
	}
	return id
}

// Submission freezes the evidence set; the audit row names the operator
// who certified it.
func TestSubmitAudited(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "A")
	if _, err := Generate(ctx(t), pool, emp, period, "gen-op"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	id := sheetID(t, pool, emp)
	if _, err := Submit(ctx(t), pool, emp, id, "op-1"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	actor, service, entity, eid, after := lastAudit(t, pool, emp, "timesheet.submitted")
	if actor != "op-1" || service != "timesheet" || entity != "timesheet" || eid != id {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	if !strings.Contains(after, "submitted") {
		t.Fatalf("after=%s want the new status", after)
	}
}

func TestReissueAudited(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "A")
	if _, err := Generate(ctx(t), pool, emp, period, "gen-op"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	id := sheetID(t, pool, emp)
	if _, err := Submit(ctx(t), pool, emp, id, "B"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := Approve(ctx(t), pool, emp, id, "C"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	nt, err := Reissue(ctx(t), pool, emp, id, "op-2")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	actor, _, entity, eid, _ := lastAudit(t, pool, emp, "timesheet.reissue")
	if actor != "op-2" || entity != "timesheet" || eid != nt.ID {
		t.Fatalf("audit=%s/%s/%s", actor, entity, eid)
	}
}

// Generate stamps who/what/when at the moment of generation. Timestamptz
// serializes with its offset, so the generated_at provenance is readable
// off the row.
func TestGenerateAudited(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool, "A")
	gen, err := Generate(ctx(t), pool, emp, period, "op-3")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v", gen)
	}
	actor, service, entity, eid, after := lastAudit(t, pool, emp, "timesheet.generate")
	if actor != "op-3" || service != "timesheet" || entity != "pay_period" || eid != period {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	if !strings.Contains(after, "generated") {
		t.Fatalf("after=%s want the generation outcome", after)
	}
}
