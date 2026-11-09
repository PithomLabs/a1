package integrity

import (
	"testing"
)

// Snapshot rows are written once by capture (app INSERT) and never
// changed: no UPDATE, no DELETE, no re-parenting for the app role.
// Invalidation flows through the one narrow function.

func snapshotFixture(t *testing.T) (string, string, string) {
	t.Helper()
	emp := mkEmployer(t, key(t, "emp"), "UEN-"+key(t, "emp"))
	period := mkPeriod(t, emp, key(t, "pp"))
	run := queryID(t, appPool,
		`INSERT INTO payroll_run (employer_id, pay_period_id, kind, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES ($1, $2, 'regular', 'placeholder', '{}', 'none', '{}') RETURNING id`, emp, period)
	return emp, period, run
}

func TestSnapshotDirectMutationRejected(t *testing.T) {
	emp, _, run := snapshotFixture(t)
	snap := queryID(t, appPool,
		`INSERT INTO payroll_snapshot (employer_id, payroll_run_id, pay_period_id, version, status, content_hash,
		  jurisdiction, currency, captured_by)
		 VALUES ($1, $2, (SELECT pay_period_id FROM payroll_run WHERE id = $2), 1, 'valid', 'abc',
		  'SG', 'SGD', 'test') RETURNING id`, emp, run)
	// Capture-path INSERT stays available to app.
	exec(t, appPool,
		`INSERT INTO payroll_snapshot_employee (snapshot_id, employee_id, employee_basis_seq, decision, inputs, row_hash)
		 VALUES ($1::uuid, gen_random_uuid(), 0, 'incomplete', '{}', 'x')`, snap)
	_, err := appPool.Exec(ctx(t),
		`UPDATE payroll_snapshot SET content_hash = 'tampered' WHERE id = $1::uuid`, snap)
	expectSQLError(t, err, "42501", "payroll_snapshot", "snapshot UPDATE as app")
	_, err = appPool.Exec(ctx(t),
		`DELETE FROM payroll_snapshot WHERE id = $1::uuid`, snap)
	expectSQLError(t, err, "42501", "payroll_snapshot", "snapshot DELETE as app")
	_, err = appPool.Exec(ctx(t),
		`UPDATE payroll_snapshot_employee SET decision = 'included' WHERE snapshot_id = $1::uuid`, snap)
	expectSQLError(t, err, "42501", "payroll_snapshot_employee", "snapshot child UPDATE as app")
	_, err = appPool.Exec(ctx(t),
		`DELETE FROM payroll_snapshot_employee WHERE snapshot_id = $1::uuid`, snap)
	expectSQLError(t, err, "42501", "payroll_snapshot_employee", "snapshot child DELETE as app")
	_, err = appPool.Exec(ctx(t),
		`UPDATE payroll_snapshot_pack SET version = 'v9' WHERE snapshot_id = $1::uuid`, snap)
	expectSQLError(t, err, "42501", "payroll_snapshot_pack", "pack pin UPDATE as app")
	// A second valid snapshot for the same run is rejected.
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO payroll_snapshot (employer_id, payroll_run_id, pay_period_id, version, status, content_hash,
		  jurisdiction, currency, captured_by)
		 VALUES ($1, $2, (SELECT pay_period_id FROM payroll_run WHERE id = $2), 2, 'valid', 'def',
		  'SG', 'SGD', 'test')`, emp, run)
	expectSQLError(t, err, "23505", "payroll_snapshot_one_valid", "second valid snapshot")
}

func TestInvalidateFunction(t *testing.T) {
	emp, _, run := snapshotFixture(t)
	snap := queryID(t, appPool,
		`INSERT INTO payroll_snapshot (employer_id, payroll_run_id, pay_period_id, version, status, content_hash,
		  jurisdiction, currency, captured_by)
		 VALUES ($1, $2, (SELECT pay_period_id FROM payroll_run WHERE id = $2), 1, 'valid', 'abc',
		  'SG', 'SGD', 'test') RETURNING id`, emp, run)
	// Invalidation without a reason is rejected.
	_, err := appPool.Exec(ctx(t),
		`SELECT invalidate_payroll_snapshot($1::uuid, '  ', 'op')`, snap)
	expectSQLError(t, err, "P0001", "reason", "reasonless invalidation")
	// The one legal transition flips valid to invalidated with cause.
	exec(t, appPool, `SELECT invalidate_payroll_snapshot($1::uuid, 'drift in terms', 'op')`, snap)
	var status, reason string
	err = appPool.QueryRow(ctx(t),
		`SELECT status, invalidated_reason FROM payroll_snapshot WHERE id = $1::uuid`, snap).Scan(&status, &reason)
	expectOK(t, err, "read invalidated snapshot")
	if status != "invalidated" || reason != "drift in terms" {
		t.Fatalf("status=%s reason=%s", status, reason)
	}
	// Twice is rejected: invalidation is terminal.
	_, err = appPool.Exec(ctx(t),
		`SELECT invalidate_payroll_snapshot($1::uuid, 'again', 'op')`, snap)
	expectSQLError(t, err, "P0001", "invalidated", "double invalidation")
	// A successor version may now capture.
	exec(t, appPool,
		`INSERT INTO payroll_snapshot (employer_id, payroll_run_id, pay_period_id, version, status, content_hash,
		  jurisdiction, currency, captured_by)
		 VALUES ($1, $2, (SELECT pay_period_id FROM payroll_run WHERE id = $2), 2, 'valid', 'def',
		  'SG', 'SGD', 'test')`, emp, run)
}

func TestPopulationBarrierObjects(t *testing.T) {
	// The population lock helper is executable by app (capture and
	// population writers alike serialize through it).
	var ok bool
	err := appPool.QueryRow(ctx(t),
		`SELECT has_function_privilege('app', 'public.acquire_payroll_population_lock(uuid)', 'EXECUTE')`).Scan(&ok)
	expectOK(t, err, "population lock EXECUTE")
	if !ok {
		t.Fatal("app cannot EXECUTE acquire_payroll_population_lock")
	}
	// Direct-SQL population writes take the barrier through triggers.
	for _, trg := range []struct{ name, table string }{
		{"employee_population_barrier", "employee"},
		{"employment_terms_population_barrier", "employment_terms"},
	} {
		var exists bool
		err := appPool.QueryRow(ctx(t),
			`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE NOT tgisinternal AND tgname = $1 AND tgrelid = $2::regclass)`,
			trg.name, trg.table).Scan(&exists)
		expectOK(t, err, "trigger "+trg.name)
		if !exists {
			t.Fatalf("trigger %s missing on %s", trg.name, trg.table)
		}
	}
}
