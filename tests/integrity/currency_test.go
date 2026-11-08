package integrity

import (
	"testing"
)

// C1/C2: the app role cannot control interval currency, and the
// SECURITY DEFINER maintenance function cannot be redirected through
// a temporary shadow table.

func isCurrent(t *testing.T, id string) bool {
	t.Helper()
	var cur bool
	err := appPool.QueryRow(ctx(t),
		`SELECT is_current FROM attendance_interval WHERE id = $1`, id).Scan(&cur)
	expectOK(t, err, "read is_current")
	return cur
}

// An explicit is_current=false insert does not stick: the trigger
// unconditionally establishes the new row as current.
func TestCurrencyExplicitFalseOverridden(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, is_current)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T18:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e"]}', false)
		 RETURNING id`,
		a, emp)
	if !isCurrent(t, id) {
		t.Fatal("explicit is_current=false survived the maintenance trigger")
	}
}

// Overlapping "non-current" inserts are rejected: the exclusion
// constraint sees the trigger-established current predicate, not the
// caller-supplied flag.
func TestCurrencyOverlapRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	mkInterval(t, a, emp, `{"clock_event_ids": ["e1"]}`)
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, is_current)
		 VALUES ($1, $2, '2026-08-03T10:00+08', '2026-08-03T11:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e2"]}', false)`,
		a, emp)
	expectSQLError(t, err, "23P01", "attendance_interval_live_no_overlap", "overlapping non-current insert")
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND employee_id = $2`,
		a, emp).Scan(&n)
	expectOK(t, err, "count intervals")
	if n != 1 {
		t.Fatalf("intervals = %d, want 1 (failed insert left a row)", n)
	}
}

// Direct currency flips stay revoked for the app role.
func TestCurrencyMetadataUpdateDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := mkInterval(t, a, emp, `{"clock_event_ids": ["e"]}`)
	_, err := appPool.Exec(ctx(t),
		`UPDATE attendance_interval SET is_current = false WHERE id = $1`, id)
	expectSQLError(t, err, "42501", "permission denied", "direct is_current update")
	if !isCurrent(t, id) {
		t.Fatal("is_current flipped despite revoked UPDATE")
	}
}

// Valid supersession flips the predecessor exactly once; a failed
// supersession leaves the original state unchanged.
func TestCurrencySupersessionAtomic(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	orig := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T10:00+08")
	mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e2"]}`,
		"2026-08-03T12:00+08", "2026-08-03T13:00+08")
	succ := queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e3"]}', $3)
		 RETURNING id`,
		a, emp, orig)
	if isCurrent(t, orig) || !isCurrent(t, succ) {
		t.Fatal("supersession did not transfer currency predecessor -> successor")
	}
	// A successor overlapping another live row fails whole: the
	// predecessor it names keeps whatever currency it had, and no
	// partial row persists.
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T12:30+08', '2026-08-03T14:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e4"]}', $3)`,
		a, emp, orig)
	expectSQLError(t, err, "23P01", "attendance_interval_live_no_overlap", "overlapping successor")
	if isCurrent(t, orig) {
		t.Fatal("failed supersession flipped the predecessor")
	}
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_interval WHERE employer_id = $1 AND employee_id = $2`,
		a, emp).Scan(&n)
	expectOK(t, err, "count intervals")
	if n != 3 {
		t.Fatalf("intervals = %d, want 3 (failed supersession left a row)", n)
	}
}

// The app role cannot create the temporary shadow table the historic
// exploit needs.
func TestTempTableCreationDenied(t *testing.T) {
	c, err := appPool.Acquire(ctx(t))
	expectOK(t, err, "acquire app conn")
	defer c.Release()
	_, err = c.Exec(ctx(t), `CREATE TEMP TABLE attendance_interval (id uuid)`)
	expectSQLError(t, err, "42501", "permission denied", "app CREATE TEMP TABLE")
}

// Even in a session that owns a temp shadow, the maintenance UPDATE
// lands on the public table: the function is schema-qualified under a
// pg_catalog-only search path. (A superuser session stands in for the
// attacker here because the app role can no longer create temp tables
// at all; the assertion is about where the function's write lands.)
func TestTempShadowCannotRedirectMaintenance(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	orig := mkIntervalSpan(t, a, emp, `{"clock_event_ids": ["e1"]}`,
		"2026-08-03T09:00+08", "2026-08-03T10:00+08")
	c, err := adminPool.Acquire(ctx(t))
	expectOK(t, err, "acquire admin conn")
	defer c.Release()
	_, err = c.Exec(ctx(t),
		`CREATE TEMP TABLE attendance_interval (id uuid, employer_id uuid, is_current boolean)`)
	expectOK(t, err, "create temp shadow")
	// Temp tables outlive Release on the pooled connection: drop the
	// shadow so later checkouts never resolve it by accident.
	defer func() {
		_, _ = c.Exec(ctx(t), `DROP TABLE IF EXISTS pg_temp.attendance_interval`)
	}()
	var id string
	err = c.QueryRow(ctx(t),
		`INSERT INTO public.attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, supersedes_id)
		 VALUES ($1, $2, '2026-08-03T09:00+08', '2026-08-03T10:00+08', 'Asia/Singapore', '2026-08-03', 'work', 'clock', '{"clock_event_ids": ["e2"]}', $3)
		 RETURNING id`,
		a, emp, orig).Scan(&id)
	expectOK(t, err, "insert successor beside temp shadow")
	var shadowed int
	err = c.QueryRow(ctx(t), `SELECT count(*) FROM pg_temp.attendance_interval`).Scan(&shadowed)
	expectOK(t, err, "read temp shadow")
	if shadowed != 0 {
		t.Fatalf("temp shadow holds %d rows, want 0", shadowed)
	}
	var origCur, succCur bool
	err = c.QueryRow(ctx(t),
		`SELECT is_current FROM public.attendance_interval WHERE id = $1`, orig).Scan(&origCur)
	expectOK(t, err, "read predecessor")
	err = c.QueryRow(ctx(t),
		`SELECT is_current FROM public.attendance_interval WHERE id = $1`, id).Scan(&succCur)
	expectOK(t, err, "read successor")
	if origCur || !succCur {
		t.Fatal("currency flip missed the public table under a temp shadow")
	}
}
