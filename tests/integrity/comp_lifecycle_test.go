package integrity

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stage E2–E5 compensation lifecycle (§14C): containment,
// precision, audit, and controlled correction, all as the app role.
// Every write goes through the controlled SECURITY DEFINER
// functions; raw table INSERT is revoked. Persisted-state re-reads
// follow every rejection.

func addComp(t *testing.T, pool *pgxpool.Pool, employer, employee, terms, kind, code, amount, from, to string) string {
	t.Helper()
	var id string
	var toArg any
	if to != "" {
		toArg = to
	}
	err := pool.QueryRow(ctx(t),
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::numeric, $7::date, $8::date, 'op-1', 'matrix')::text`,
		employer, employee, terms, kind, code, amount, from, toArg).Scan(&id)
	expectOK(t, err, "add_compensation")
	return id
}

func tryAddComp(t *testing.T, pool *pgxpool.Pool, employer, employee, terms, kind, code, amount, from, to string) error {
	t.Helper()
	var toArg any
	if to != "" {
		toArg = to
	}
	var id string
	return pool.QueryRow(ctx(t),
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::numeric, $7::date, $8::date, 'op-1', 'matrix')::text`,
		employer, employee, terms, kind, code, amount, from, toArg).Scan(&id)
}

func tryCorrectComp(t *testing.T, pool *pgxpool.Pool, employer, employee, orig, amount, from, to string) (string, error) {
	t.Helper()
	var toArg any
	if to != "" {
		toArg = to
	}
	var id string
	err := pool.QueryRow(ctx(t),
		`SELECT correct_compensation($1::uuid, $2::uuid, $3::uuid, $4::numeric, $5::date, $6::date, 'op-1', 'matrix fix')::text`,
		employer, employee, orig, amount, from, toArg).Scan(&id)
	return id, err
}

func compCount(t *testing.T, employer string) int {
	t.Helper()
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM compensation_line WHERE employer_id = $1`, employer).Scan(&n)
	expectOK(t, err, "count compensation")
	return n
}

func compRow(t *testing.T, id string) (amount, from, to string, supersedes *string) {
	t.Helper()
	var toOut *string
	err := appPool.QueryRow(ctx(t),
		`SELECT amount_monthly::text, effective_from::text, effective_to::text, supersedes_id::text
		 FROM compensation_line WHERE id = $1::uuid`, id).
		Scan(&amount, &from, &toOut, &supersedes)
	expectOK(t, err, "read compensation row")
	if toOut != nil {
		to = *toOut
	}
	return amount, from, to, supersedes
}

func auditCount(t *testing.T, employer, action string) int {
	t.Helper()
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = $2`,
		employer, action).Scan(&n)
	expectOK(t, err, "count audit events")
	return n
}

func closeTerms(t *testing.T, employer, employee, successorFrom string) string {
	t.Helper()
	return queryID(t, appPool,
		`SELECT close_employment_terms($1::uuid, $2::uuid, $3::date, 'op-1')::text`,
		employer, employee, successorFrom)
}

// E2: compensation starting before its terms is rejected, with no
// row and no audit residue.
func TestCompBeforeTermsStartRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	err := tryAddComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2025-12-01", "")
	expectSQLError(t, err, "P0001", "containment", "pre-terms compensation")
	if n := compCount(t, a); n != 0 {
		t.Fatalf("rows=%d want zero residue", n)
	}
	if n := auditCount(t, a, "compensation.add"); n != 0 {
		t.Fatalf("audit=%d want zero residue", n)
	}
}

// E2: compensation starting after its (closed) terms end is rejected.
func TestCompAfterTermsEndRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	if got := closeTerms(t, a, emp, "2026-07-01"); got != "2026-06-30" {
		t.Fatalf("closed=%s want 2026-06-30", got)
	}
	err := tryAddComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-07-01", "")
	expectSQLError(t, err, "P0001", "containment", "post-terms compensation")
	if n := compCount(t, a); n != 0 {
		t.Fatalf("rows=%d want zero residue", n)
	}
}

// E2: closing terms while a line outlives the close is rejected; the
// controlled path caps the line first via correction, then closes.
func TestCompOutlivingCloseRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	orig := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	var closed string
	err := appPool.QueryRow(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-07-01', 'op-1')::text`,
		a, emp).Scan(&closed)
	expectSQLError(t, err, "P0001", "outlive", "close over open-ended line")
	succ, err := tryCorrectComp(t, appPool, a, emp, orig, "100.00", "2026-01-01", "2026-06-30")
	expectOK(t, err, "cap the line first")
	if succ == orig {
		t.Fatal("successor must be a new row, not the original")
	}
	if got := closeTerms(t, a, emp, "2026-07-01"); got != "2026-06-30" {
		t.Fatalf("closed=%s want 2026-06-30 once capped", got)
	}
}

// E2: the same employee+code may not overlap across terms. Terms
// are disjoint by exclusion, so a cross-terms reach-back necessarily
// escapes the row's own terms and containment refuses it; the
// employee-level (not per-terms) exclusion stands behind it as the
// backstop. Adjacent ranges across the boundary are fine.
func TestCompCrossTermsOverlapRefused(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	t1 := mkTerms(t, a, emp, "2026-01-01")
	addComp(t, appPool, a, emp, t1, "allowance", "SHIFT", "100.00", "2026-01-01", "2026-06-30")
	closeTerms(t, a, emp, "2026-07-01")
	t2 := mkTerms(t, a, emp, "2026-07-01")
	err := tryAddComp(t, appPool, a, emp, t2, "allowance", "SHIFT", "120.00", "2026-06-01", "")
	expectSQLError(t, err, "P0001", "containment", "cross-terms reach-back")
	addComp(t, appPool, a, emp, t2, "allowance", "SHIFT", "120.00", "2026-07-01", "")
	if n := compCount(t, a); n != 2 {
		t.Fatalf("rows=%d want the two adjacent lines", n)
	}
}

// E2: concurrent overlapping inserts serialize on the employee lock;
// exactly one wins and the loser reports overlap.
func TestCompConcurrentOverlapSingleWinner(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	c1, err := appPool.Acquire(ctx(t))
	expectOK(t, err, "acquire conn 1")
	defer c1.Release()
	c2, err := appPool.Acquire(ctx(t))
	expectOK(t, err, "acquire conn 2")
	defer c2.Release()
	tx1, err := c1.Begin(ctx(t))
	expectOK(t, err, "begin tx1")
	var first string
	err = tx1.QueryRow(ctx(t),
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, 'allowance', 'SHIFT', 100, '2026-01-01'::date, NULL, 'op-1', 'matrix')::text`,
		a, emp, terms).Scan(&first)
	expectOK(t, err, "tx1 add holds the lock")
	done := make(chan error, 1)
	go func() {
		tx2, err := c2.Begin(ctx(t))
		if err != nil {
			done <- err
			return
		}
		defer tx2.Rollback(ctx(t))
		var second string
		err = tx2.QueryRow(ctx(t),
			`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, 'allowance', 'SHIFT', 120, '2026-01-01'::date, NULL, 'op-1', 'matrix')::text`,
			a, emp, terms).Scan(&second)
		if err == nil {
			err = tx2.Commit(ctx(t))
		}
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	expectOK(t, tx1.Commit(ctx(t)), "commit tx1")
	select {
	case err := <-done:
		expectSQLError(t, err, "P0001", "overlaps live", "concurrent overlap loser")
	case <-time.After(15 * time.Second):
		t.Fatal("loser never returned after the winner committed")
	}
	if n := compCount(t, a); n != 1 {
		t.Fatalf("rows=%d want exactly the winner", n)
	}
}

// E2/E5: narrowing a line frees the tail — superseded history is
// dead basis, so a fresh line may cover the narrowed-away range.
func TestCompNarrowedRangeFreesTail(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	orig := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	if _, err := tryCorrectComp(t, appPool, a, emp, orig, "100.00", "2026-01-01", "2026-06-30"); err != nil {
		t.Fatalf("narrow: %v", err)
	}
	addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "120.00", "2026-07-01", "")
	if n := compCount(t, a); n != 3 {
		t.Fatalf("rows=%d want original, successor, and tail line", n)
	}
}

// E2 positive: disjoint historical and future lines coexist, as do
// overlapping lines under different codes.
func TestCompValidHistoricalAndFuture(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "2026-03-31")
	addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "120.00", "2026-09-01", "")
	addComp(t, appPool, a, emp, terms, "allowance", "MEAL", "50.00", "2026-01-01", "")
	if n := compCount(t, a); n != 3 {
		t.Fatalf("rows=%d want all three valid lines", n)
	}
}

// E3: exact cents accepted and persisted verbatim; thirds of a cent,
// negatives, and >10 integer digits rejected — through the same
// CHECK the service path shares.
func TestCompMoneyPrecision(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	id := addComp(t, appPool, a, emp, terms, "allowance", "A", "199.99", "2026-01-01", "2026-01-31")
	id2 := addComp(t, appPool, a, emp, terms, "allowance", "B", "199.90", "2026-02-01", "2026-02-28")
	if got, _, _, _ := compRow(t, id); got != "199.99" {
		t.Fatalf("persisted=%s want the exact input", got)
	}
	if got, _, _, _ := compRow(t, id2); got != "199.90" {
		t.Fatalf("persisted=%s want the exact input", got)
	}
	for _, tc := range []struct{ code, amount, what string }{
		{"C", "199.999", "third of a cent"},
		{"D", "-5.00", "negative amount"},
		{"E", "10000000000", "eleven integer digits"},
	} {
		err := tryAddComp(t, appPool, a, emp, terms, "allowance", tc.code, tc.amount, "2026-03-01", "2026-03-31")
		expectSQLError(t, err, "23514", "", tc.what)
	}
	maxID := addComp(t, appPool, a, emp, terms, "allowance", "F", "9999999999.99", "2026-03-01", "2026-03-31")
	if got, _, _, _ := compRow(t, maxID); got != "9999999999.99" {
		t.Fatalf("persisted=%s want the exact maximum", got)
	}
}

// E3: monthly_basic shares the exact-scale CHECK: thirds of a cent
// rejected instead of silently rounded.
func TestTermsMoneyPrecision(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'Support', 'workman', true, 'manual-wages', 2600.789,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')`,
		a, emp)
	expectSQLError(t, err, "23514", "", "third of a cent in monthly_basic")
	id := queryID(t, appPool,
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'Support', 'workman', true, 'manual-wages', 2600.50,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')
		 RETURNING id`,
		a, emp)
	var got string
	err = appPool.QueryRow(ctx(t),
		`SELECT monthly_basic::text FROM employment_terms WHERE id = $1::uuid`, id).Scan(&got)
	expectOK(t, err, "read monthly_basic")
	if got != "2600.50" {
		t.Fatalf("persisted=%s want the exact input", got)
	}
}

// E4: raw table INSERT is revoked; the function is the only write path.
func TestCompRawInsertRevoked(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO compensation_line (employer_id, terms_id, kind, code, amount_monthly, effective_from)
		 VALUES ($1, $2, 'allowance', 'SHIFT', 100, '2026-01-01')`,
		a, terms)
	expectSQLError(t, err, "42501", "permission denied", "raw compensation INSERT")
	_ = emp
}

// E4: one canonical audit row per committed add — actor, reason,
// canonical cents — and none when the write fails.
func TestCompAuditCanonical(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	id := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100", "2026-01-01", "")
	if n := auditCount(t, a, "compensation.add"); n != 1 {
		t.Fatalf("audit=%d want exactly one canonical row", n)
	}
	var actor, entity, entityID, after string
	err := appPool.QueryRow(ctx(t),
		`SELECT actor, entity, entity_id::text, after_state::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'compensation.add'`, a).
		Scan(&actor, &entity, &entityID, &after)
	expectOK(t, err, "read audit row")
	if actor != "op-1" || entity != "compensation_line" || entityID != id {
		t.Fatalf("audit actor=%s entity=%s id=%s want the add attribution", actor, entity, entityID)
	}
	for _, want := range []string{`"amount": "100.00"`, `"code": "SHIFT"`, `"reason": "matrix"`} {
		if !strings.Contains(after, want) {
			t.Fatalf("after=%s want %s", after, want)
		}
	}
	// A failed write leaves no audit shadow.
	if err := tryAddComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "120", "2026-01-01", ""); err == nil {
		t.Fatal("expected the overlapping add to fail")
	}
	if n := auditCount(t, a, "compensation.add"); n != 1 {
		t.Fatalf("audit=%d want still exactly one row", n)
	}
}

// E5: correcting a draft-period line mints an immutable successor;
// the original row never changes; a second successor is rejected.
func TestCompCorrectDraftPeriod(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	orig := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-08-01", "")
	succ, err := tryCorrectComp(t, appPool, a, emp, orig, "120.00", "2026-08-01", "")
	expectOK(t, err, "correct the line")
	if amt, from, to, sup := compRow(t, orig); amt != "100.00" || from != "2026-08-01" || to != "" || sup != nil {
		t.Fatalf("original=(%s %s %s %v) want it untouched", amt, from, to, sup)
	}
	if amt, _, _, sup := compRow(t, succ); amt != "120.00" || sup == nil || *sup != orig {
		t.Fatalf("successor=(%s %v) want 120.00 linked to the original", amt, sup)
	}
	if _, err := tryCorrectComp(t, appPool, a, emp, orig, "130.00", "2026-08-01", ""); err == nil {
		t.Fatal("expected a second successor of the same original to fail")
	} else {
		expectSQLError(t, err, "P0001", "already has a successor", "second successor")
	}
	// Chained correction stays open: the live successor corrects.
	if _, err := tryCorrectComp(t, appPool, a, emp, succ, "130.00", "2026-08-01", ""); err != nil {
		t.Fatalf("correct the successor: %v", err)
	}
	if n := auditCount(t, a, "compensation.correct"); n != 2 {
		t.Fatalf("audit=%d want one row per correction", n)
	}
}

// E5: a future-effective line corrects the same way.
func TestCompCorrectFutureEffective(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	orig := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2027-01-01", "")
	succ, err := tryCorrectComp(t, appPool, a, emp, orig, "110.00", "2027-01-01", "")
	expectOK(t, err, "correct the future line")
	if _, _, _, sup := compRow(t, succ); sup == nil || *sup != orig {
		t.Fatalf("successor link=%v want the original", sup)
	}
}

// E5: two concurrent successors of one original — exactly one wins.
func TestCompConcurrentSuccessorsSingleWinner(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	orig := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	c1, err := appPool.Acquire(ctx(t))
	expectOK(t, err, "acquire conn 1")
	defer c1.Release()
	c2, err := appPool.Acquire(ctx(t))
	expectOK(t, err, "acquire conn 2")
	defer c2.Release()
	tx1, err := c1.Begin(ctx(t))
	expectOK(t, err, "begin tx1")
	var first string
	err = tx1.QueryRow(ctx(t),
		`SELECT correct_compensation($1::uuid, $2::uuid, $3::uuid, 120, '2026-01-01'::date, NULL, 'op-1', 'matrix fix')::text`,
		a, emp, orig).Scan(&first)
	expectOK(t, err, "tx1 correction holds the lock")
	done := make(chan error, 1)
	go func() {
		tx2, err := c2.Begin(ctx(t))
		if err != nil {
			done <- err
			return
		}
		defer tx2.Rollback(ctx(t))
		var second string
		err = tx2.QueryRow(ctx(t),
			`SELECT correct_compensation($1::uuid, $2::uuid, $3::uuid, 130, '2026-01-01'::date, NULL, 'op-1', 'matrix fix')::text`,
			a, emp, orig).Scan(&second)
		if err == nil {
			err = tx2.Commit(ctx(t))
		}
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	expectOK(t, tx1.Commit(ctx(t)), "commit tx1")
	select {
	case err := <-done:
		expectSQLError(t, err, "P0001", "already has a successor", "concurrent successor loser")
	case <-time.After(15 * time.Second):
		t.Fatal("loser never returned after the winner committed")
	}
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM compensation_line WHERE supersedes_id = $1::uuid`, orig).Scan(&n)
	expectOK(t, err, "count successors")
	if n != 1 {
		t.Fatalf("successors=%d want exactly the winner", n)
	}
}

// E5: a successor may not cross employees; the add path pins the
// terms row to the same employee too.
func TestCompCrossEmployeeSuccessorRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	other := mkEmployee(t, a, "E002")
	terms := mkTerms(t, a, emp, "2026-01-01")
	orig := addComp(t, appPool, a, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	_, err := tryCorrectComp(t, appPool, a, other, orig, "120.00", "2026-01-01", "")
	expectSQLError(t, err, "P0001", "employee", "cross-employee successor")
	err = tryAddComp(t, appPool, a, other, terms, "allowance", "MEAL", "50.00", "2026-01-01", "")
	expectSQLError(t, err, "P0001", "employee", "cross-employee add")
	if n := compCount(t, a); n != 1 {
		t.Fatalf("rows=%d want only the original", n)
	}
}
