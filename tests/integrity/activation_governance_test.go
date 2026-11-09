package integrity

import (
	"testing"
)

// The adversarial finding, closed: the app role cannot flip a pack to
// IMPLEMENTABLE, with an invented version or otherwise. Activation
// records and statutory parameter tables are read-only to app; the
// only write path is the privileged activation function.

func activationFixture(t *testing.T) string {
	t.Helper()
	return mkEmployer(t, key(t, "emp"), "UEN-"+key(t, "emp"))
}

func TestActivationDirectWriteRejected(t *testing.T) {
	emp := activationFixture(t)
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES ($1, 'SG-CPF-2026', 'v9-invented', 'IMPLEMENTABLE')`,
		emp)
	expectSQLError(t, err, "42501", "rule_pack_activation", "direct activation INSERT as app")
	exec(t, adminPool,
		`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES ($1, 'SG-CPF-2026', 'v0', 'BLOCKED')`,
		emp)
	_, err = appPool.Exec(ctx(t),
		`UPDATE rule_pack_activation SET status = 'IMPLEMENTABLE', version = 'v9-invented' WHERE employer_id = $1`,
		emp)
	expectSQLError(t, err, "42501", "rule_pack_activation", "direct activation UPDATE as app")
	_, err = appPool.Exec(ctx(t),
		`DELETE FROM rule_pack_activation WHERE employer_id = $1`, emp)
	expectSQLError(t, err, "42501", "rule_pack_activation", "direct activation DELETE as app")
	// Reads stay available.
	var status string
	err = appPool.QueryRow(ctx(t),
		`SELECT status FROM rule_pack_activation WHERE employer_id = $1 AND pack = 'SG-CPF-2026'`,
		emp).Scan(&status)
	expectOK(t, err, "activation SELECT as app")
	if status != "BLOCKED" {
		t.Fatalf("status=%s", status)
	}
}

func TestActivationHistoryAppendOnly(t *testing.T) {
	emp := activationFixture(t)
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO rule_pack_activation_history (employer_id, pack, version, status, manifest_digest, evidence_refs,
		  spec_version, review_ref, verification_status, actor, reason)
		 VALUES ($1, 'SG-CPF-2026', 'v9-invented', 'IMPLEMENTABLE', 'sha256:0', '{}', 's', 'r', 'v', 'a', 'b')`,
		emp)
	expectSQLError(t, err, "42501", "rule_pack_activation_history", "direct history INSERT as app")
}

func TestParamTablesReadOnlyToApp(t *testing.T) {
	emp := activationFixture(t)
	_ = emp
	inserts := []struct {
		name string
		sql  string
	}{
		{"mom_ot_params", `INSERT INTO mom_ot_params (key, value, effective_from) VALUES ('k', '{}', '2026-01-01')`},
		{"mom_shift_params", `INSERT INTO mom_shift_params (key, value, effective_from) VALUES ('k', '{}', '2026-01-01')`},
		{"cpf_rate_band", `INSERT INTO cpf_rate_band (age_from, employee_rate, employer_rate, effective_from) VALUES (0, 0.2, 0.17, '2026-01-01')`},
		{"cpf_ceiling", `INSERT INTO cpf_ceiling (kind, amount, effective_from) VALUES ('ow_monthly', 6800, '2026-01-01')`},
		{"sdl_params", `INSERT INTO sdl_params (rate, min_amount, max_amount, rounding, effective_from) VALUES (0.0025, 2, 11.25, 'up', '2026-01-01')`},
		{"shg_contribution", `INSERT INTO shg_contribution (fund, wage_from, amount, effective_from) VALUES ('CDAC', 0, 1, '2026-01-01')`},
	}
	for _, in := range inserts {
		_, err := appPool.Exec(ctx(t), in.sql)
		expectSQLError(t, err, "42501", in.name, "direct param INSERT as app")
	}
	// Reads stay available.
	for _, table := range []string{"mom_ot_params", "mom_shift_params", "cpf_rate_band", "cpf_ceiling", "sdl_params", "shg_contribution"} {
		var n int
		err := appPool.QueryRow(ctx(t), `SELECT count(*) FROM `+table).Scan(&n)
		expectOK(t, err, "param SELECT as app on "+table)
	}
}

func TestActivationFunctionRejects(t *testing.T) {
	emp := activationFixture(t)
	gov := key(t, "gov1")
	call := func(pack, version, jurisdiction, scope, digest string) error {
		_, err := adminPool.Exec(ctx(t),
			`SELECT activate_rule_pack($1::uuid, $2, $3, $4, $5, '2026-01-01'::date, NULL, $6,
			  '{"sources":[]}'::jsonb, 'spec-1', 'reviewer (MRC): accepted', 'GOLDENS_VERIFIED', 'op', 'reason')`,
			emp, pack, version, jurisdiction, scope, digest)
		return err
	}
	// Unknown pack.
	expectSQLError(t, call("NOPE-NOT-A-PACK", "1.0.0", "SG", "test-fixture", "sha256:"), "P0001", "unknown rule pack", "unknown pack")
	// Known identity but nothing reviewed: no version is claimable.
	expectSQLError(t, call("SG-CPF-2026", "v0", "SG", "contributions", "sha256:"), "P0001", "no reviewed version pinned", "unpinned pack")
	// Pin a fixture pack, then try every forgery against it.
	exec(t, adminPool,
		`INSERT INTO rule_pack (pack, jurisdiction, scope, reviewed_version, manifest_digest, spec_version, verification_status)
		 VALUES ($1, 'SG', 'test-fixture', '1.0.0', 'sha256:aaa', 'spec-1', 'GOLDENS_VERIFIED')`, gov)
	expectSQLError(t, call(gov, "9.9.9", "SG", "test-fixture", "sha256:aaa"), "P0001", "not the reviewed version", "invented version")
	expectSQLError(t, call(gov, "1.0.0", "SG", "test-fixture", "sha256:bbb"), "P0001", "does not match the reviewed pin", "digest mismatch")
	expectSQLError(t, call(gov, "1.0.0", "XX", "test-fixture", "sha256:aaa"), "P0001", "jurisdiction", "jurisdiction mismatch")
	expectSQLError(t, call(gov, "1.0.0", "SG", "overtime", "sha256:aaa"), "P0001", "scope", "scope mismatch")
	// Empty evidence rejected even with a matching pin.
	_, err := adminPool.Exec(ctx(t),
		`SELECT activate_rule_pack($1::uuid, $2, '1.0.0', 'SG', 'test-fixture',
		  '2026-01-01'::date, NULL, 'sha256:aaa', '{}'::jsonb, 'spec-1', 'r', 'v', 'op', 'reason')`,
		emp, gov)
	expectSQLError(t, err, "P0001", "evidence references", "empty evidence")
	// The app role cannot call the function at all.
	_, err = appPool.Exec(ctx(t),
		`SELECT activate_rule_pack($1::uuid, $2, '1.0.0', 'SG', 'test-fixture',
		  '2026-01-01'::date, NULL, 'sha256:aaa', '{"sources":[]}'::jsonb, 'spec-1', 'r', 'v', 'op', 'reason')`,
		emp, gov)
	expectSQLError(t, err, "42501", "activate_rule_pack", "app EXECUTE on activate")
	// Nothing activated: the current table has no row for the pack.
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM rule_pack_activation WHERE employer_id = $1 AND pack = $2`, emp, gov).Scan(&n)
	expectOK(t, err, "no activation row")
	if n != 0 {
		t.Fatalf("activation rows=%d want none", n)
	}
}

func TestActivationTransitionAndHistory(t *testing.T) {
	emp := activationFixture(t)
	gov := key(t, "gov2")
	exec(t, adminPool,
		`INSERT INTO rule_pack (pack, jurisdiction, scope, reviewed_version, manifest_digest, spec_version, verification_status)
		 VALUES ($1, 'SG', 'test-fixture', '1.0.0', 'sha256:aaa', 'spec-1', 'GOLDENS_VERIFIED')`, gov)
	activate := func(version, digest string) string {
		t.Helper()
		return queryID(t, adminPool,
			`SELECT activate_rule_pack($1::uuid, $2, $3, 'SG', 'test-fixture',
			  '2026-01-01'::date, NULL, $4, '{"sources":[]}'::jsonb, 'spec-1', 'r', 'v', 'op', 'reason')::text`,
			emp, gov, version, digest)
	}
	first := activate("1.0.0", "sha256:aaa")
	// Same version twice is rejected: no silent rewrite.
	_, err := adminPool.Exec(ctx(t),
		`SELECT activate_rule_pack($1::uuid, $2, '1.0.0', 'SG', 'test-fixture',
		  '2026-01-01'::date, NULL, 'sha256:aaa', '{"sources":[]}'::jsonb, 'spec-1', 'r', 'v', 'op', 'reason')`,
		emp, gov)
	expectSQLError(t, err, "P0001", "already active", "repeat activation")
	// Repin and supersede: history preserves both, linked.
	exec(t, adminPool,
		`UPDATE rule_pack SET reviewed_version = '1.1.0', manifest_digest = 'sha256:bbb' WHERE pack = $1`, gov)
	second := activate("1.1.0", "sha256:bbb")
	var sup string
	var status, version string
	err = appPool.QueryRow(ctx(t),
		`SELECT supersedes_id::text FROM rule_pack_activation_history WHERE id = $1::uuid`, second).Scan(&sup)
	expectOK(t, err, "supersedes link")
	if sup != first {
		t.Fatalf("supersedes=%s want %s", sup, first)
	}
	err = appPool.QueryRow(ctx(t),
		`SELECT version, status FROM rule_pack_activation WHERE employer_id = $1 AND pack = $2`,
		emp, gov).Scan(&version, &status)
	expectOK(t, err, "current pointer")
	if version != "1.1.0" || status != "IMPLEMENTABLE" {
		t.Fatalf("current=%s/%s", version, status)
	}
}
