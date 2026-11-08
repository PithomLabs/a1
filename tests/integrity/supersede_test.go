package integrity

import (
	"testing"
)

// C3: supersession identity is a database rule, not a service
// convention. Same employer, same employee, no self-links, no cycles.

func intervalCount(t *testing.T, employer, employee string) int {
	t.Helper()
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND employee_id = $2`,
		employer, employee).Scan(&n)
	expectOK(t, err, "count intervals")
	return n
}

func TestSupersedeCrossEmployeeRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	e1 := mkEmployee(t, a, "E001")
	e2 := mkEmployee(t, a, "E002")
	victim := mkIntervalSpan(t, a, e1, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T10:00+08")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e2"]}', $3)`,
		a, e2, victim)
	expectSQLError(t, err, "P0001", "another employee", "cross-employee supersession")
	if intervalCount(t, a, e2) != 0 {
		t.Fatal("cross-employee successor row persisted")
	}
	if !isCurrent(t, victim) {
		t.Fatal("victim interval flipped despite rejected supersession")
	}
}

func TestSupersedeCrossEmployerRejected(t *testing.T) {
	a := mkEmployer(t, key(t, "A"), "UEN-"+key(t, "A"))
	b := mkEmployer(t, key(t, "B"), "UEN-"+key(t, "B"))
	empA := mkEmployee(t, a, "E001")
	empB := mkEmployee(t, b, "E001")
	foreign := mkInterval(t, b, empB, `{"clock_event_ids": ["e1"]}`)
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e2"]}', $3)`,
		a, empA, foreign)
	expectSQLError(t, err, "P0001", "does not exist in this employer", "cross-employer supersession")
	if intervalCount(t, a, empA) != 0 {
		t.Fatal("cross-employer successor row persisted")
	}
}

func TestSupersedeSelfRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	self := "00000000-0000-0000-0000-00000000c3e1"
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (id, employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, $3, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e"]}', $1)`,
		self, a, emp)
	expectSQLError(t, err, "P0001", "cannot supersede itself", "self-supersession")
	if intervalCount(t, a, emp) != 0 {
		t.Fatal("self-superseding row persisted")
	}
}

// Closing a two-node cycle needs an UPDATE, which the app role cannot
// perform; the trigger independently rejects the cycle for writers
// that can (owner path), and the app-role attempt dies on privilege.
func TestSupersedeCycleRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	first := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T10:00+08")
	second := queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e2"]}', $3)
		 RETURNING id`,
		a, emp, first)
	_, err := appPool.Exec(ctx(t),
		`UPDATE attendance_interval SET supersedes_id = $1 WHERE id = $2`, second, first)
	expectSQLError(t, err, "42501", "permission denied", "app cycle-closing update")
	_, err = adminPool.Exec(ctx(t),
		`UPDATE attendance_interval SET supersedes_id = $1 WHERE id = $2`, second, first)
	expectSQLError(t, err, "P0001", "cycle", "owner-path cycle-closing update")
	var back *string
	err = appPool.QueryRow(ctx(t),
		`SELECT supersedes_id::text FROM attendance_interval WHERE id = $1`, first).Scan(&back)
	expectOK(t, err, "read first supersedes")
	if back != nil {
		t.Fatalf("cycle edge persisted: first supersedes %s", *back)
	}
}

// A same-employee chain supersedes cleanly however long it grows, and
// currency always rests on the tip alone.
func TestSupersedeValidChain(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	prev := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e0"]}`,
		"2026-08-03T09:00+08", "2026-08-03T10:00+08")
	for i := 1; i <= 3; i++ {
		prev = queryID(t, appPool,
			`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
			 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e"]}', $3)
			 RETURNING id`,
			a, emp, prev)
	}
	var tips int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND employee_id = $2 AND is_current`,
		a, emp).Scan(&tips)
	expectOK(t, err, "count current")
	if tips != 1 || !isCurrent(t, prev) {
		t.Fatalf("current tips = %d, want exactly the chain tip", tips)
	}
}

// A correction-style successor dated in another pay period is a
// legitimate multi-date correction, not an attack: the trigger allows
// it, and the decided-period guard lives in the Correct service,
// pinned by its own regression tests.
func TestSupersedeCrossPeriodAllowed(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	mkPeriod(t, a, "PP-AUG")
	mkPeriodSpan(t, a, "PP-SEP", "2026-09-01", "2026-09-30")
	orig := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-31T23:00+08", "2026-08-31T23:30+08")
	succ := queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-09-01T00:10+08', '2026-09-01T00:40+08', 'Asia/Singapore', '2026-09-01', 'work', 'clock', '{"clock_event_ids": ["e2"]}', $3)
		 RETURNING id`,
		a, emp, orig)
	if isCurrent(t, orig) || !isCurrent(t, succ) {
		t.Fatal("cross-period supersession did not transfer currency")
	}
}
