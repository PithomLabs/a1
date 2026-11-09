package integrity

import (
	"testing"
)

// shgFixture creates an employer and employee for SHG governance tests.
func shgFixture(t *testing.T) (string, string) {
	t.Helper()
	emp := mkEmployer(t, key(t, "emp"), "UEN-"+key(t, "emp"))
	return emp, mkEmployee(t, emp, key(t, "e"))
}

func TestSHGDirectWriteRejected(t *testing.T) {
	emp, ee := shgFixture(t)
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'active', 'direct', '2026-08-01')`,
		emp, ee)
	expectSQLError(t, err, "42501", "shg_instruction", "direct SHG INSERT as app")
	id := queryID(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'active', 'direct', '2026-08-01') RETURNING id`,
		emp, ee)
	_, err = appPool.Exec(ctx(t),
		`UPDATE shg_instruction SET status = 'opt_out' WHERE employer_id = $1 AND id = $2`, emp, id)
	expectSQLError(t, err, "42501", "shg_instruction", "direct SHG UPDATE as app")
	_, err = appPool.Exec(ctx(t),
		`DELETE FROM shg_instruction WHERE employer_id = $1 AND id = $2`, emp, id)
	expectSQLError(t, err, "42501", "shg_instruction", "direct SHG DELETE as app")
}

func TestSHGStatusCheck(t *testing.T) {
	emp, ee := shgFixture(t)
	_, err := adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'bogus', 'direct', '2026-08-01')`,
		emp, ee)
	expectSQLError(t, err, "23514", "shg_status_values", "bad SHG status")
}

func TestSHGVerifiedChecks(t *testing.T) {
	emp, ee := shgFixture(t)
	// verified_none without the triple.
	_, err := adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'verified_none', 'direct', '2026-08-01')`,
		emp, ee)
	expectSQLError(t, err, "23514", "shg_verified_consistency", "verified_none without triple")
	// Pending must be unverified.
	_, err = adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from,
		  verified_at, verified_by, verification_basis)
		 VALUES (gen_random_uuid(), $1, $2, 'MBMF', 'pending', 'direct', '2026-08-01',
		  now(), 'op', 'basis')`,
		emp, ee)
	expectSQLError(t, err, "23514", "shg_verified_consistency", "pending with triple")
	// Partial triple rejected on any status.
	_, err = adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from,
		  verified_at, verified_by)
		 VALUES (gen_random_uuid(), $1, $2, 'SINDA', 'verified_none', 'direct', '2026-08-01',
		  now(), 'op')`,
		emp, ee)
	expectSQLError(t, err, "23514", "shg_verified_consistency", "partial triple")
	// Active must be unverified.
	_, err = adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from,
		  verified_at, verified_by, verification_basis)
		 VALUES (gen_random_uuid(), $1, $2, 'ECF', 'active', 'direct', '2026-08-01',
		  now(), 'op', 'basis')`,
		emp, ee)
	expectSQLError(t, err, "23514", "shg_verified_consistency", "active with triple")
}

func TestSHGLiveOverlap(t *testing.T) {
	emp, ee := shgFixture(t)
	exec(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'active', 'direct', '2026-08-01')`,
		emp, ee)
	// Same-fund live overlap rejected.
	_, err := adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'active', 'direct', '2026-08-15')`,
		emp, ee)
	expectSQLError(t, err, "P0001", "overlaps a live", "same-fund live overlap")
	// Different fund coexists.
	exec(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'SINDA', 'active', 'direct', '2026-08-15')`,
		emp, ee)
	// A successor over its own predecessor's range is exempt.
	pred := queryID(t, adminPool,
		`SELECT id::text FROM shg_instruction WHERE employer_id = $1 AND employee_id = $2 AND fund = 'CDAC'`,
		emp, ee)
	exec(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from, supersedes_id)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'opt_out', 'direct', '2026-08-01', $3::uuid)`,
		emp, ee, pred)
}

func TestSHGSupersedesIntegrity(t *testing.T) {
	emp, ee := shgFixture(t)
	// Dangling supersedes rejected. The lifecycle trigger reports the
	// missing original before the FK fires, mirroring compensation;
	// the FK stays as the race backstop.
	_, err := adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from, supersedes_id)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'active', 'direct', '2026-08-01', '00000000-0000-0000-0000-000000000000')`,
		emp, ee)
	expectSQLError(t, err, "P0001", "successor original", "dangling supersedes")
	pred := queryID(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'MBMF', 'active', 'direct', '2026-08-01') RETURNING id`,
		emp, ee)
	// The first successor takes a closed range, so a second successor
	// on a disjoint range reaches the single-successor UNIQUE instead
	// of the overlap trigger.
	exec(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from, effective_to, supersedes_id)
		 VALUES (gen_random_uuid(), $1, $2, 'MBMF', 'opt_out', 'direct', '2026-08-01', '2026-08-31', $3::uuid)`,
		emp, ee, pred)
	// Second successor of the same row rejected.
	_, err = adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from, supersedes_id)
		 VALUES (gen_random_uuid(), $1, $2, 'MBMF', 'active', 'direct', '2026-09-01', $3::uuid)`,
		emp, ee, pred)
	expectSQLError(t, err, "23505", "shg_instruction_single_successor", "second successor")
}

func TestSHGNoRegressionToPending(t *testing.T) {
	emp, ee := shgFixture(t)
	pred := queryID(t, adminPool,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'ECF', 'active', 'direct', '2026-08-01') RETURNING id`,
		emp, ee)
	// A successor that regresses its chain to pending is trigger-blocked,
	// even for a role that bypasses the REVOKE.
	_, err := adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from, supersedes_id)
		 VALUES (gen_random_uuid(), $1, $2, 'ECF', 'pending', 'direct', '2026-08-01', $3::uuid)`,
		emp, ee, pred)
	expectSQLError(t, err, "P0001", "pending", "regression to pending")
}

func TestSHGBasisSeqAssigned(t *testing.T) {
	emp, ee := shgFixture(t)
	var seq int64
	err := adminPool.QueryRow(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from)
		 VALUES (gen_random_uuid(), $1, $2, 'CDAC', 'active', 'direct', '2026-08-01')
		 RETURNING basis_seq`, emp, ee).Scan(&seq)
	expectOK(t, err, "SHG basis_seq insert")
	if seq <= 0 {
		t.Fatalf("basis_seq=%d want system-assigned positive", seq)
	}
	_, err = adminPool.Exec(ctx(t),
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, source_declaration, effective_from, basis_seq)
		 VALUES (gen_random_uuid(), $1, $2, 'MBMF', 'active', 'direct', '2026-08-01', 42)`,
		emp, ee)
	expectSQLError(t, err, "P0001", "system-assigned", "caller-supplied basis_seq")
}
