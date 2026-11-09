package payroll

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

// An exclusion redirects payroll liability, so it is audited with the
// run it applies to and the reason that justifies it.
func TestExclusionAudited(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	r, err := CreateRun(ctx(t), pool, emp, period, "", "", "run-op")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	id, err := AddExclusion(ctx(t), pool, emp, r.ID, "E002", "second job elsewhere", "op-1")
	if err != nil {
		t.Fatalf("exclude: %v", err)
	}
	actor, service, entity, eid, after := lastAudit(t, pool, emp, "payroll.exclude")
	if actor != "op-1" || service != "payroll" || entity != "payroll_run_exclusion" || eid != id {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	if !strings.Contains(after, "second job") || !strings.Contains(after, "E002") {
		t.Fatalf("after=%s want reason and employee", after)
	}
}

// Creating the pay run stamps who opened it, for which period, and as
// which kind. Timestamptz serializes with its offset, so the row's own
// occurred_at keeps the when.
func TestCreateRunAudited(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	r, err := CreateRun(ctx(t), pool, emp, period, "", "", "op-2")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	actor, service, entity, eid, after := lastAudit(t, pool, emp, "payroll.run_create")
	if actor != "op-2" || service != "payroll" || entity != "payroll_run" || eid != r.ID {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	if !strings.Contains(after, "regular") || !strings.Contains(after, period) {
		t.Fatalf("after=%s want kind and period", after)
	}
}
