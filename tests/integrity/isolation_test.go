package integrity

import (
	"testing"

	"github.com/PithomLabs/a1/internal/payroll"
)

// G1: two-employer isolation. Every test builds two employers and proves
// the boundary holds between them.

// A roster assignment may not mix rows from different employers: the
// composite foreign keys reject the write.
func TestCrossEmployerAssignmentRejected(t *testing.T) {
	a := mkEmployer(t, key(t, "A"), "UEN-"+key(t, "A"))
	b := mkEmployer(t, key(t, "B"), "UEN-"+key(t, "B"))
	empA := mkEmployee(t, a, "E001")
	empB := mkEmployee(t, b, "E001")
	_, prgA := mkClientProgramme(t, a, key(t, "A"))
	_, revA := mkRoster(t, a, prgA, "2026-08-03")
	tplB := mkTemplate(t, b, "DAY")

	_, err := appPool.Exec(ctx(t),
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, template_id, rest_day)
		 VALUES ($1, $2, $3, '2026-08-03', $4, false)`, a, revA, empA, tplB)
	expectSQLError(t, err, "23503", "roster_assignment_employer_id_template_id_fkey", "template from employer B in employer A assignment")

	_, err = appPool.Exec(ctx(t),
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, rest_day)
		 VALUES ($1, $2, $3, '2026-08-04', true)`, a, revA, empB)
	expectSQLError(t, err, "23503", "roster_assignment_employer_id_employee_id_fkey", "employee from employer B in employer A assignment")
}

// Employer codes are globally unique.
func TestEmployerCodeUnique(t *testing.T) {
	code := key(t, "dup")
	mkEmployer(t, code, "UEN-"+code+"-1")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, 'x', $2, 'y')`,
		code, "UEN-"+code+"-2")
	expectSQLError(t, err, "23505", "employer_code_key", "duplicate employer code")
}

// The same operational codes coexist under different employers.
func TestPerEmployerCodesCoexist(t *testing.T) {
	a := mkEmployer(t, key(t, "A"), "UEN-"+key(t, "A"))
	b := mkEmployer(t, key(t, "B"), "UEN-"+key(t, "B"))
	empA := mkEmployee(t, a, "SHARED001")
	empB := mkEmployee(t, b, "SHARED001")
	if empA == empB {
		t.Fatal("expected distinct employee rows")
	}
	tplA := mkTemplate(t, a, "SHAREDDAY")
	tplB := mkTemplate(t, b, "SHAREDDAY")
	if tplA == tplB {
		t.Fatal("expected distinct template rows")
	}
	// But the same code twice under one employer is rejected.
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, 'SHARED001', 'x', 'y', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE)`, a)
	expectSQLError(t, err, "23505", "employee_employer_id_employee_no_key", "duplicate employee_no in one employer")
}

// The UEN is unique per jurisdiction, not globally.
func TestUENScopedToJurisdiction(t *testing.T) {
	uen := "UEN-" + key(t, "shared")
	mkEmployer(t, key(t, "SG"), uen)
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO employer (code, name, uen, address, jurisdiction) VALUES ($1, 'x', $2, 'y', 'SG')`,
		key(t, "SG2"), uen)
	expectSQLError(t, err, "23505", "employer_jurisdiction_uen_key", "duplicate (jurisdiction, uen)")
	var id string
	err = appPool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address, jurisdiction) VALUES ($1, 'x', $2, 'y', 'MY') RETURNING id`,
		key(t, "MY"), uen).Scan(&id)
	expectOK(t, err, "same UEN under another jurisdiction")
}

// Vendor event IDs live in a per-employer namespace.
func TestPerEmployerEventNamespace(t *testing.T) {
	a := mkEmployer(t, key(t, "A"), "UEN-"+key(t, "A"))
	b := mkEmployer(t, key(t, "B"), "UEN-"+key(t, "B"))
	empA := mkEmployee(t, a, "E001")
	empB := mkEmployee(t, b, "E001")
	for _, tc := range []struct{ employer, employee string }{{a, empA}, {b, empB}} {
		_, err := appPool.Exec(ctx(t),
			`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
			 VALUES ($1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in', 'vendor-x', 'evt-1')`,
			tc.employer, tc.employee)
		expectOK(t, err, "same vendor event under two employers")
	}
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, '2026-08-03T09:01+08', 'Asia/Singapore', 'in', 'vendor-x', 'evt-1')`,
		a, empA)
	expectSQLError(t, err, "23505", "clock_event_employer_id_source_source_event_id_key", "duplicate vendor event in one employer")
}

// The activation row in the database feeds the approval predicate: a
// BLOCKED pack stops approval, IMPLEMENTABLE clears it, all-or-nothing.
func TestActivationFeedsApprovalPredicate(t *testing.T) {
	emp := mkEmployer(t, key(t), "UEN-"+key(t))
	empID := mkEmployee(t, emp, "E001")
	exec(t, appPool,
		`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES ($1, 'SG-CPF', 'v0.1', 'BLOCKED')`, emp)

	activations := loadActivations(t, emp)
	req := payroll.ApprovalRequest{
		Included:    []payroll.EmployeeRequirement{{EmployeeID: empID, RequiredPacks: []string{"SG-CPF"}}},
		Activations: activations,
	}
	if err := payroll.AuthorizeApproval(req); err == nil {
		t.Fatal("expected BLOCKED pack to stop approval")
	}
	exec(t, appPool,
		`UPDATE rule_pack_activation SET status = 'IMPLEMENTABLE', activated_at = now()
		 WHERE employer_id = $1 AND pack = 'SG-CPF'`, emp)
	req.Activations = loadActivations(t, emp)
	if err := payroll.AuthorizeApproval(req); err != nil {
		t.Fatalf("expected approval after IMPLEMENTABLE, got %v", err)
	}
}

func loadActivations(t *testing.T, employerID string) map[string]payroll.Activation {
	t.Helper()
	rows, err := appPool.Query(ctx(t),
		`SELECT pack, version, status FROM rule_pack_activation WHERE employer_id = $1`, employerID)
	expectOK(t, err, "load activations")
	defer rows.Close()
	out := map[string]payroll.Activation{}
	for rows.Next() {
		var a payroll.Activation
		if err := rows.Scan(&a.Pack, &a.Version, &a.Status); err != nil {
			t.Fatalf("scan activation: %v", err)
		}
		out[a.Pack] = a
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("activation rows: %v", err)
	}
	return out
}

// Neither runtime role may bypass the protection layer as a superuser.
func TestRolesAreNotSuperuser(t *testing.T) {
	for _, role := range []string{"migration_admin", "app"} {
		var isSuper, bypassRLS bool
		err := adminPool.QueryRow(ctx(t),
			`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = $1`, role).Scan(&isSuper, &bypassRLS)
		expectOK(t, err, "inspect role "+role)
		if isSuper || bypassRLS {
			t.Fatalf("role %s must not be superuser (super=%v bypassrls=%v)", role, isSuper, bypassRLS)
		}
	}
}

// Repair never mutates a published revision: publish, append v2, then
// reopen through the revise shape (published -> draft with the
// pointer advancing onto the existing v2); v1 stays byte-identical.
// (C7 forbids the old bare published -> draft reopen; the rejected
// shape is pinned by TestRosterUnpublishRejected.)
func TestRepairByNewRevision(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, prg := mkClientProgramme(t, a, key(t))
	roster, rev1 := mkRoster(t, a, prg, "2026-08-03")
	tpl := mkTemplate(t, a, "DAY")
	asg := mkAssignment(t, a, rev1, emp, tpl, "2026-08-03")

	exec(t, appPool, `UPDATE roster SET status = 'published', published_by = 'test' WHERE id = $1`, roster)
	requireStatus(t, "roster", roster, "published")
	_, err := appPool.Exec(ctx(t),
		`UPDATE roster_assignment SET status = 'edited' WHERE id = $1`, asg)
	expectSQLError(t, err, "P0001", "frozen", "edit assignment of published roster")

	// Append v2, then reopen onto it in one revise-shaped statement;
	// v1 becomes read-only history.
	rev2 := queryID(t, appPool,
		`INSERT INTO roster_revision (employer_id, roster_id, version, note) VALUES ($1, $2, 2, 'repair') RETURNING id`,
		a, roster)
	exec(t, appPool, `UPDATE roster SET status = 'draft', current_version = 2 WHERE id = $1`, roster)
	requireStatus(t, "roster", roster, "draft")
	asg2 := queryID(t, appPool,
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, template_id, rest_day)
		 VALUES ($1, $2, $3, '2026-08-03', $4, false) RETURNING id`,
		a, rev2, emp, tpl)
	_ = asg2

	_, err = appPool.Exec(ctx(t),
		`UPDATE roster_assignment SET status = 'edited' WHERE id = $1`, asg)
	expectSQLError(t, err, "P0001", "frozen", "edit superseded v1 assignment")

	var status string
	err = appPool.QueryRow(ctx(t),
		`SELECT status FROM roster_assignment WHERE id = $1`, asg).Scan(&status)
	expectOK(t, err, "read v1 assignment")
	if status != "scheduled" {
		t.Fatalf("v1 assignment mutated to %q, want scheduled", status)
	}
}
