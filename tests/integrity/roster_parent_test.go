package integrity

import (
	"testing"
)

// C7: the roster parent row cannot silently demote published history.
// (Assignment edits and cross-revision reparenting stay covered by
// the freeze suite; these cases pin the parent pointer and stamps.)

func rosterState(t *testing.T, id string) (status string, version int, by *string) {
	t.Helper()
	err := appPool.QueryRow(ctx(t),
		`SELECT status, current_version, published_by FROM roster WHERE id = $1`,
		id).Scan(&status, &version, &by)
	expectOK(t, err, "read roster state")
	return status, version, by
}

func TestRosterUnpublishRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, prg := mkClientProgramme(t, a, key(t))
	roster, rev := mkRoster(t, a, prg, "2026-08-03")
	tpl := mkTemplate(t, a, "DAY")
	asg := mkAssignment(t, a, rev, emp, tpl, "2026-08-03")
	exec(t, appPool,
		`UPDATE roster SET status = 'published', published_at = now(), published_by = 'op1' WHERE id = $1`, roster)
	_, err := appPool.Exec(ctx(t), `UPDATE roster SET status = 'draft' WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "cannot reopen", "published roster reverted to draft")
	st, ver, _ := rosterState(t, roster)
	if st != "published" || ver != 1 {
		t.Fatalf("roster state = %s/v%d, want published/v1", st, ver)
	}
	_, err = appPool.Exec(ctx(t),
		`UPDATE roster_assignment SET status = 'edited' WHERE id = $1`, asg)
	expectSQLError(t, err, "P0001", "frozen", "assignment edit after failed unpublish")
}

func TestRosterPointerRollbackRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	_, prg := mkClientProgramme(t, a, key(t))
	roster, _ := mkRoster(t, a, prg, "2026-08-03")
	exec(t, appPool, `UPDATE roster SET status = 'published', published_by = 'op' WHERE id = $1`, roster)
	exec(t, appPool,
		`INSERT INTO roster_revision (employer_id, roster_id, version, note) VALUES ($1, $2, 2, 'r')`,
		a, roster)
	exec(t, appPool, `UPDATE roster SET status = 'draft', current_version = 2 WHERE id = $1`, roster)
	_, err := appPool.Exec(ctx(t), `UPDATE roster SET current_version = 1 WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "cannot move backwards", "pointer rollback to v1")
	_, err = appPool.Exec(ctx(t), `UPDATE roster SET current_version = 5 WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "cannot move the revision pointer on a draft", "pointer jump on draft")
	_, ver, _ := rosterState(t, roster)
	if ver != 2 {
		t.Fatalf("pointer = v%d, want v2", ver)
	}
}

func TestRosterReopenToMissingRevisionRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	_, prg := mkClientProgramme(t, a, key(t))
	roster, _ := mkRoster(t, a, prg, "2026-08-03")
	exec(t, appPool, `UPDATE roster SET status = 'published', published_by = 'op' WHERE id = $1`, roster)
	_, err := appPool.Exec(ctx(t),
		`UPDATE roster SET status = 'draft', current_version = 2 WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "missing revision", "reopen without the new revision row")
	st, ver, _ := rosterState(t, roster)
	if st != "published" || ver != 1 {
		t.Fatalf("roster state = %s/v%d, want published/v1", st, ver)
	}
}

// The legitimate revise shape keeps working end to end: publish,
// append v2, reopen onto it, edit v2, republish with fresh stamps,
// while v1 stays frozen history.
func TestRosterReviseShapePositive(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, prg := mkClientProgramme(t, a, key(t))
	roster, rev1 := mkRoster(t, a, prg, "2026-08-03")
	tpl := mkTemplate(t, a, "DAY")
	asg1 := mkAssignment(t, a, rev1, emp, tpl, "2026-08-03")
	exec(t, appPool,
		`UPDATE roster SET status = 'published', published_at = now(), published_by = 'op1' WHERE id = $1`, roster)
	rev2 := queryID(t, appPool,
		`INSERT INTO roster_revision (employer_id, roster_id, version, note) VALUES ($1, $2, 2, 'fix') RETURNING id`,
		a, roster)
	exec(t, appPool, `UPDATE roster SET status = 'draft', current_version = 2 WHERE id = $1`, roster)
	st, ver, by := rosterState(t, roster)
	if st != "draft" || ver != 2 || by == nil || *by != "op1" {
		t.Fatalf("state = %s/v%d/%v, want draft/v2/op1", st, ver, by)
	}
	asg2 := mkAssignment(t, a, rev2, emp, tpl, "2026-08-04")
	exec(t, appPool, `UPDATE roster_assignment SET status = 'confirmed' WHERE id = $1`, asg2)
	_, err := appPool.Exec(ctx(t),
		`UPDATE roster_assignment SET status = 'edited' WHERE id = $1`, asg1)
	expectSQLError(t, err, "P0001", "frozen", "v1 edit after revise")
	exec(t, appPool,
		`UPDATE roster SET status = 'published', published_at = now(), published_by = 'op2' WHERE id = $1`, roster)
	st, ver, by = rosterState(t, roster)
	if st != "published" || ver != 2 || by == nil || *by != "op2" {
		t.Fatalf("republish state = %s/v%d/%v, want published/v2/op2", st, ver, by)
	}
}

func TestRosterStampsImmutable(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	_, prg := mkClientProgramme(t, a, key(t))
	roster, _ := mkRoster(t, a, prg, "2026-08-03")
	exec(t, appPool,
		`UPDATE roster SET status = 'published', published_at = now(), published_by = 'op1' WHERE id = $1`, roster)
	_, err := appPool.Exec(ctx(t),
		`UPDATE roster SET published_by = 'op2' WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "parent row is frozen", "rewrite publisher on published roster")
	exec(t, appPool,
		`INSERT INTO roster_revision (employer_id, roster_id, version, note) VALUES ($1, $2, 2, 'r')`,
		a, roster)
	exec(t, appPool, `UPDATE roster SET status = 'draft', current_version = 2 WHERE id = $1`, roster)
	_, err = appPool.Exec(ctx(t),
		`UPDATE roster SET published_by = 'op2' WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "publisher is immutable", "rewrite publisher on draft")
	_, _, by := rosterState(t, roster)
	if by == nil || *by != "op1" {
		t.Fatalf("publisher = %v, want op1", by)
	}
}

func TestRosterIdentityImmutable(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	_, prg := mkClientProgramme(t, a, key(t))
	roster, _ := mkRoster(t, a, prg, "2026-08-03")
	_, err := appPool.Exec(ctx(t),
		`UPDATE roster SET week_starting = '2026-08-10' WHERE id = $1`, roster)
	expectSQLError(t, err, "P0001", "identity", "roster week transplant")
}
