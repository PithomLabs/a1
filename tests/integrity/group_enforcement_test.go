package integrity

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stage A enforcement matrix (§14A): correction-group lineage,
// atomicity and business-date attribution, all as the app role with
// persisted-state re-reads after every rejection. Deferred checks
// fire at COMMIT, so negative cases below commit explicitly and
// assert the commit error plus the absence of residue. Scope
// mismatches fail faster, at the 00010 member-FK and supersede
// guards; those cases assert the fail-fast error instead.

func mkGroupTx(t *testing.T, tx pgx.Tx, employerID, employeeID string) string {
	t.Helper()
	var id string
	err := tx.QueryRow(ctx(t),
		`INSERT INTO attendance_correction_group (employer_id, employee_id, reason, corrected_by)
		 VALUES ($1, $2, 'matrix', 'test') RETURNING id::text`,
		employerID, employeeID).Scan(&id)
	expectOK(t, err, "insert group")
	return id
}

func mkLinkTx(t *testing.T, tx pgx.Tx, employerID, groupID, pred string) {
	t.Helper()
	_, err := tx.Exec(ctx(t),
		`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
		 VALUES ($1, $2, $3)`,
		employerID, groupID, pred)
	expectOK(t, err, "insert link")
}

func segRefs(groupID, pred string) string {
	return fmt.Sprintf(`{"correction_id": "%s", "predecessor_interval_id": "%s", "correction_group_id": "%s"}`,
		pred, pred, groupID)
}

func mkSegmentTx(t *testing.T, tx pgx.Tx, employerID, employeeID, groupID, pred, start, end, date, kind string) string {
	t.Helper()
	var id string
	err := tx.QueryRow(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id)
		 VALUES ($1, $2, $3, $4, 'Asia/Singapore', $5::date, $6, 'correction', $7::jsonb, $8, $9)
		 RETURNING id::text`,
		employerID, employeeID, start, end, date, kind, segRefs(groupID, pred), pred, groupID).Scan(&id)
	expectOK(t, err, "insert segment")
	return id
}

func groupCount(t *testing.T, employerID string) int {
	t.Helper()
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group WHERE employer_id = $1`, employerID).Scan(&n)
	expectOK(t, err, "count groups")
	return n
}

func TestGroupZeroSegmentsRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, pred)
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "0 segments", "group without segments")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
	if !isCurrent(t, pred) {
		t.Fatal("predecessor flipped without a committed successor")
	}
}

func TestGroupZeroPredecessorsRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkSegmentTx(t, tx, a, emp, g, pred,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "0 predecessors", "group without predecessors")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupNonexistentPredecessorRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	_, err = tx.Exec(ctx(t),
		`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
		 VALUES ($1, $2, '00000000-0000-0000-0000-000000000000')`, a, g)
	expectSQLError(t, err, "23503", "predecessor", "link to missing interval")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupCrossEmployerPredecessorRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	b := mkEmployer(t, key(t, "b"), "UEN-"+key(t, "b"))
	empA := mkEmployee(t, a, "E001")
	empB := mkEmployee(t, b, "E001")
	foreign := mkIntervalSpan(t, b, empB, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, empA)
	_, err = tx.Exec(ctx(t),
		`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
		 VALUES ($1, $2, $3)`, a, g, foreign)
	expectSQLError(t, err, "23503", "predecessor", "cross-employer link")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupCrossEmployeePredecessorRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	other := mkEmployee(t, a, "E002")
	theirs := mkIntervalSpan(t, a, other, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, theirs)
	// Any segment superseding the foreign predecessor must be the
	// group's own employee (member FK), and the 00010 supersede
	// guard rejects the cross-employee hop before COMMIT.
	var segID string
	err = tx.QueryRow(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T17:00+08', 'Asia/Singapore',
		  '2026-08-03', 'work', 'correction', $3::jsonb, $4, $5) RETURNING id::text`,
		a, emp, segRefs(g, theirs), theirs, g).Scan(&segID)
	expectSQLError(t, err, "P0001", "of another employee", "cross-employee binding")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupMixedKindRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, pred)
	mkSegmentTx(t, tx, a, emp, g, pred,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	mkSegmentTx(t, tx, a, emp, g, pred,
		"2026-08-03T12:00+08", "2026-08-03T17:00+08", "2026-08-03", "break_unpaid")
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "mixes 2 segment kinds", "mixed-kind group")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupStraySegmentRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	p1 := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08")
	p2 := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e2"]}`,
		"2026-08-03T13:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, p1)
	// Bound to p1 but superseding p2: outside the set.
	mkSegmentTx(t, tx, a, emp, g, p2,
		"2026-08-03T13:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "outside its predecessor set", "stray segment")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
	if !isCurrent(t, p1) || !isCurrent(t, p2) {
		t.Fatal("predecessor flipped without a committed successor")
	}
}

func TestGroupOrphanPredecessorRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	p1 := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08")
	p2 := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e2"]}`,
		"2026-08-03T13:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, p1)
	mkLinkTx(t, tx, a, g, p2)
	mkSegmentTx(t, tx, a, emp, g, p1,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "without a successor segment", "orphan predecessor")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupScopeMismatchSegmentRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	other := mkEmployee(t, a, "E002")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	theirs := mkIntervalSpan(t, a, other, `{"clock_event_ids": ["e2"]}`,
		"2026-08-04T09:00+08", "2026-08-04T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, pred)
	// The smuggled segment arrives with its own predecessor
	// bound too — and the 00010 member FK rejects the foreign
	// employee before COMMIT.
	mkSegmentTx(t, tx, a, emp, g, pred,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	mkLinkTx(t, tx, a, g, theirs)
	_, err = tx.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id)
		 VALUES ($1, $2, '2026-08-04T09:00+08', '2026-08-04T17:00+08', 'Asia/Singapore',
		  '2026-08-04', 'work', 'correction', $3::jsonb, $4, $5)`,
		a, other, segRefs(g, theirs), theirs, g)
	expectSQLError(t, err, "23503", "correction_group_member_fk", "cross-employee segment")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupSelfMembershipRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	x := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, x)
	s := mkSegmentTx(t, tx, a, emp, g, x,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	// Now list the group's own segment as a second predecessor and
	// cover it, so only the self-membership check can fire.
	mkLinkTx(t, tx, a, g, s)
	mkSegmentTx(t, tx, a, emp, g, s,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "own segment as a predecessor", "self-membership")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupNonCurrentPredecessorRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	// Supersede pred outside any group with a non-overlapping span
	// (supersession is lineage, not necessarily same-span), so the
	// group's own segment insert passes the exclusion and the
	// deferred already-superseded check is the one that fires.
	var succ string
	err := appPool.QueryRow(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T18:00+08', '2026-08-03T20:00+08', 'Asia/Singapore', '2026-08-03',
		  'work', 'clock', '{"clock_event_ids": ["e3"]}', $3) RETURNING id::text`,
		a, emp, pred).Scan(&succ)
	expectOK(t, err, "outside successor")
	if isCurrent(t, pred) || !isCurrent(t, succ) {
		t.Fatal("outside supersession did not transfer currency")
	}
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, pred)
	mkSegmentTx(t, tx, a, emp, g, pred,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	err = tx.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "already superseded outside the group", "stale predecessor")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero residue", n)
	}
}

func TestGroupConcurrentSingleWinner(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	txA, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin A")
	gA := mkGroupTx(t, txA, a, emp)
	mkLinkTx(t, txA, a, gA, pred)
	mkSegmentTx(t, txA, a, emp, gA, pred,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	// B races the same predecessor: whether its link insert waits
	// for A then fails, or runs after A's commit and fails at once,
	// it must fail loudly with the single-successor violation.
	bDone := make(chan error, 1)
	go func() {
		btx, err := appPool.Begin(ctx(t))
		if err != nil {
			bDone <- err
			return
		}
		defer btx.Rollback(ctx(t))
		var gB string
		err = btx.QueryRow(ctx(t),
			`INSERT INTO attendance_correction_group (employer_id, employee_id, reason, corrected_by)
			 VALUES ($1, $2, 'race', 'test') RETURNING id::text`, a, emp).Scan(&gB)
		if err != nil {
			bDone <- err
			return
		}
		_, err = btx.Exec(ctx(t),
			`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
			 VALUES ($1, $2, $3)`, a, gB, pred)
		bDone <- err
	}()
	expectOK(t, txA.Commit(ctx(t)), "commit winner")
	select {
	case err := <-bDone:
		expectSQLError(t, err, "23505", "predecessor_interval_id", "loser single-successor")
	case <-time.After(15 * time.Second):
		t.Fatal("loser never returned after the winner committed")
	}
	if n := groupCount(t, a); n != 1 {
		t.Fatalf("groups=%d want exactly the winner", n)
	}
}

func TestGroupAppendAfterCloseRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	p1 := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08")
	p2 := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e2"]}`,
		"2026-08-03T13:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, p1)
	mkSegmentTx(t, tx, a, emp, g, p1,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	expectOK(t, tx.Commit(ctx(t)), "commit group")
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
		 VALUES ($1, $2, $3)`, a, g, p2)
	expectSQLError(t, err, "P0001", "closed to new rows", "late link append")
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id)
		 VALUES ($1, $2, '2026-08-03T13:00+08', '2026-08-03T17:00+08', 'Asia/Singapore', '2026-08-03',
		  'work', 'correction', $3::jsonb, $4, $5)`,
		a, emp, segRefs(g, p2), p2, g)
	expectSQLError(t, err, "P0001", "closed to new rows", "late segment append")
	var links int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group_predecessor WHERE group_id = $1`, g).Scan(&links)
	expectOK(t, err, "count links")
	if links != 1 {
		t.Fatalf("links=%d want the original one only", links)
	}
}

func TestGroupFrozenMutationsDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, pred)
	seg := mkSegmentTx(t, tx, a, emp, g, pred,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	expectOK(t, tx.Commit(ctx(t)), "commit group")
	_, err = appPool.Exec(ctx(t),
		`UPDATE attendance_correction_group SET reason = 'forged' WHERE id = $1`, g)
	expectSQLError(t, err, "42501", "permission denied", "group update")
	_, err = appPool.Exec(ctx(t),
		`DELETE FROM attendance_correction_group WHERE id = $1`, g)
	expectSQLError(t, err, "42501", "permission denied", "group delete")
	_, err = appPool.Exec(ctx(t),
		`UPDATE attendance_correction_group_predecessor SET predecessor_interval_id = $1 WHERE group_id = $2`, pred, g)
	expectSQLError(t, err, "42501", "permission denied", "link update")
	_, err = appPool.Exec(ctx(t),
		`DELETE FROM attendance_correction_group_predecessor WHERE group_id = $1`, g)
	expectSQLError(t, err, "42501", "permission denied", "link delete")
	_, err = appPool.Exec(ctx(t),
		`UPDATE attendance_interval SET correction_group_id = NULL WHERE id = $1`, seg)
	expectSQLError(t, err, "42501", "permission denied", "segment group-pointer update")
	var reason string
	err = appPool.QueryRow(ctx(t),
		`SELECT reason FROM attendance_correction_group WHERE id = $1`, g).Scan(&reason)
	expectOK(t, err, "re-read group")
	if reason != "matrix" {
		t.Fatalf("reason=%q want the committed value", reason)
	}
	var links int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group_predecessor WHERE group_id = $1`, g).Scan(&links)
	expectOK(t, err, "re-read links")
	if links != 1 {
		t.Fatalf("links=%d want the committed link", links)
	}
}

func TestGroupMalformedKeepsSlot(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	pred := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	bad, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin bad")
	g := mkGroupTx(t, bad, a, emp)
	mkLinkTx(t, bad, a, g, pred)
	err = bad.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "0 segments", "malformed commit")
	_ = bad.Rollback(ctx(t))
	good, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin good")
	g2 := mkGroupTx(t, good, a, emp)
	mkLinkTx(t, good, a, g2, pred)
	mkSegmentTx(t, good, a, emp, g2, pred,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	expectOK(t, good.Commit(ctx(t)), "valid commit after malformed attempt")
	if n := groupCount(t, a); n != 1 {
		t.Fatalf("groups=%d want exactly the valid group", n)
	}
}

func TestGroupPartialRecorrectionRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	p := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	first, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin first")
	g1 := mkGroupTx(t, first, a, emp)
	mkLinkTx(t, first, a, g1, p)
	s1 := mkSegmentTx(t, first, a, emp, g1, p,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	s2 := mkSegmentTx(t, first, a, emp, g1, p,
		"2026-08-03T12:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	expectOK(t, first.Commit(ctx(t)), "commit two-segment group")
	part, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin partial")
	g2 := mkGroupTx(t, part, a, emp)
	mkLinkTx(t, part, a, g2, s1)
	mkSegmentTx(t, part, a, emp, g2, s1,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	err = part.Commit(ctx(t))
	expectSQLError(t, err, "P0001", "partial re-correction", "partial sibling set")
	_ = part.Rollback(ctx(t))
	if !isCurrent(t, s1) || !isCurrent(t, s2) {
		t.Fatal("siblings flipped without a committed successor")
	}
	// Control: the complete sibling set commits cleanly.
	whole, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin whole")
	g3 := mkGroupTx(t, whole, a, emp)
	mkLinkTx(t, whole, a, g3, s1)
	mkLinkTx(t, whole, a, g3, s2)
	mkSegmentTx(t, whole, a, emp, g3, s1,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	mkSegmentTx(t, whole, a, emp, g3, s2,
		"2026-08-03T12:00+08", "2026-08-03T17:00+08", "2026-08-03", "work")
	expectOK(t, whole.Commit(ctx(t)), "whole-group re-correction")
	if n := groupCount(t, a); n != 2 {
		t.Fatalf("groups=%d want the original plus its successor", n)
	}
}

func TestGroupAtomicityRollback(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	p := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T17:00+08")
	q := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e2"]}`,
		"2026-08-03T18:00+08", "2026-08-03T20:00+08")
	tx, err := appPool.Begin(ctx(t))
	expectOK(t, err, "begin")
	g := mkGroupTx(t, tx, a, emp)
	mkLinkTx(t, tx, a, g, p)
	mkSegmentTx(t, tx, a, emp, g, p,
		"2026-08-03T09:00+08", "2026-08-03T12:00+08", "2026-08-03", "work")
	// The second segment overlaps a live interval: the exclusion
	// aborts mid-construction, and the whole group must vanish.
	_, err = tx.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end,
		  timezone, local_date, kind, source, source_refs, supersedes_id, correction_group_id)
		 VALUES ($1, $2, '2026-08-03T12:00+08', '2026-08-03T19:00+08', 'Asia/Singapore', '2026-08-03',
		  'work', 'correction', $3::jsonb, $4, $5)`,
		a, emp, segRefs(g, p), p, g)
	expectSQLError(t, err, "23P01", "attendance_interval_live_no_overlap", "overlapping second segment")
	_ = tx.Rollback(ctx(t))
	if n := groupCount(t, a); n != 0 {
		t.Fatalf("groups=%d want zero partial residue", n)
	}
	var segs int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND source = 'correction'`,
		a).Scan(&segs)
	expectOK(t, err, "count segments")
	if segs != 0 {
		t.Fatalf("segments=%d want zero partial successors", segs)
	}
	if !isCurrent(t, p) || !isCurrent(t, q) {
		t.Fatal("live intervals flipped without a committed successor")
	}
}
