package integrity

import (
	"testing"
	"time"
)

// C8: employment history is insert-only for the app role. Closed rows
// cannot be rewritten; the open row advances only through the
// controlled functions, which validate, close and audit atomically.

func TestClosedTermsMutationDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := mkTerms(t, a, emp, "2026-01-01")
	exec(t, appPool, `SELECT close_employment_terms($1::uuid, $2::uuid, '2026-07-01', 'test')`, a, emp)
	_, err := appPool.Exec(ctx(t),
		`UPDATE employment_terms SET monthly_basic = 1 WHERE id = $1`, id)
	expectSQLError(t, err, "42501", "permission denied", "rewrite closed terms salary")
	_, err = appPool.Exec(ctx(t), `DELETE FROM employment_terms WHERE id = $1`, id)
	expectSQLError(t, err, "42501", "permission denied", "delete closed terms")
	var basic, to string
	err = appPool.QueryRow(ctx(t),
		`SELECT monthly_basic::text, effective_to::text FROM employment_terms WHERE id = $1`,
		id).Scan(&basic, &to)
	expectOK(t, err, "read closed terms")
	if basic != "2600" || to != "2026-06-30" {
		t.Fatalf("terms = %s/%s, want 2600/2026-06-30", basic, to)
	}
}

func TestEmployeeStatusResurrectionDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	exec(t, appPool,
		`INSERT INTO employee_status_history (employer_id, employee_id, status, effective_from, reason)
		 VALUES ($1, $2, 'active', '2026-01-01', 'initial')`, a, emp)
	exec(t, appPool,
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'resigned', '2026-06-01', 'left', 'test')`,
		a, emp)
	_, err := appPool.Exec(ctx(t),
		`UPDATE employee SET status = 'active' WHERE id = $1`, emp)
	expectSQLError(t, err, "42501", "permission denied", "resurrect resigned employee")
	_, err = appPool.Exec(ctx(t), `DELETE FROM employee WHERE id = $1`, emp)
	expectSQLError(t, err, "42501", "permission denied", "delete employee")
	var status string
	err = appPool.QueryRow(ctx(t), `SELECT status FROM employee WHERE id = $1`, emp).Scan(&status)
	expectOK(t, err, "read employee status")
	if status != "resigned" {
		t.Fatalf("status = %q, want resigned", status)
	}
}

func TestStatusHistoryMutationDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	exec(t, appPool,
		`INSERT INTO employee_status_history (employer_id, employee_id, status, effective_from, reason)
		 VALUES ($1, $2, 'active', '2026-01-01', 'initial')`, a, emp)
	_, err := appPool.Exec(ctx(t),
		`UPDATE employee_status_history SET status = 'resigned' WHERE employer_id = $1`, a)
	expectSQLError(t, err, "42501", "permission denied", "rewrite status history")
	_, err = appPool.Exec(ctx(t), `DELETE FROM employee_status_history WHERE employer_id = $1`, a)
	expectSQLError(t, err, "42501", "permission denied", "delete status history")
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM employee_status_history WHERE employer_id = $1 AND status = 'active'`,
		a).Scan(&n)
	expectOK(t, err, "count history")
	if n != 1 {
		t.Fatalf("active history rows = %d, want 1", n)
	}
}

func TestMembershipSkillMutationDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	_, prg := mkClientProgramme(t, a, key(t))
	team := queryID(t, appPool,
		`INSERT INTO team (employer_id, programme_id, code, name) VALUES ($1, $2, 'T1', 'T1') RETURNING id`,
		a, prg)
	exec(t, appPool,
		`INSERT INTO team_membership (employer_id, team_id, employee_id, role, effective_from)
		 VALUES ($1, $2, $3, 'agent', '2026-01-01')`, a, team, emp)
	exec(t, appPool, `INSERT INTO skill (code, name, kind) VALUES ($1, $1, 'language') ON CONFLICT DO NOTHING`,
		"SK-"+key(t))
	exec(t, appPool,
		`INSERT INTO employee_skill (employer_id, employee_id, skill_id, level, valid_from)
		 SELECT $1, $2, id, 3, '2026-01-01' FROM skill WHERE code = $3`, a, emp, "SK-"+key(t))
	_, err := appPool.Exec(ctx(t),
		`UPDATE team_membership SET effective_to = '2026-07-31' WHERE employer_id = $1`, a)
	expectSQLError(t, err, "42501", "permission denied", "expire membership directly")
	_, err = appPool.Exec(ctx(t), `DELETE FROM team_membership WHERE employer_id = $1`, a)
	expectSQLError(t, err, "42501", "permission denied", "delete membership")
	_, err = appPool.Exec(ctx(t),
		`UPDATE employee_skill SET level = 1 WHERE employer_id = $1`, a)
	expectSQLError(t, err, "42501", "permission denied", "rewrite skill level")
	_, err = appPool.Exec(ctx(t), `DELETE FROM employee_skill WHERE employer_id = $1`, a)
	expectSQLError(t, err, "42501", "permission denied", "delete skill")
	var n int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM team_membership WHERE employer_id = $1 AND effective_to IS NULL`, a).Scan(&n)
	expectOK(t, err, "count memberships")
	if n != 1 {
		t.Fatalf("open memberships = %d, want 1", n)
	}
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM employee_skill WHERE employer_id = $1 AND level = 3`, a).Scan(&n)
	expectOK(t, err, "count skills")
	if n != 1 {
		t.Fatalf("level-3 skills = %d, want 1", n)
	}
}

// Compensation affecting an approved period cannot be mutated in
// place, and a backdated timestamp cannot be smuggled past the
// conflict detector: created_at normalizes to now().
func TestCompensationMutationDenied(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	terms := mkTerms(t, a, emp, "2026-01-01")
	period := mkPeriod(t, a, "PP")
	sheet := queryID(t, appPool,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		a, emp, period)
	exec(t, appPool,
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now() WHERE id = $1`, sheet)
	id := queryID(t, appPool,
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, 'allowance', 'SHIFT', 100,
		  '2026-08-01'::date, NULL, 'op-1', 'matrix')::text`,
		a, emp, terms)
	_, err := appPool.Exec(ctx(t),
		`UPDATE compensation_line SET amount_monthly = 1 WHERE id = $1`, id)
	expectSQLError(t, err, "42501", "permission denied", "rewrite approved-period compensation")
	_, err = appPool.Exec(ctx(t), `DELETE FROM compensation_line WHERE id = $1`, id)
	expectSQLError(t, err, "42501", "permission denied", "delete approved-period compensation")
	_, err = appPool.Exec(ctx(t),
		`INSERT INTO compensation_line (employer_id, terms_id, kind, code, amount_monthly, effective_from, created_at)
		 VALUES ($1, $2, 'allowance', 'OLD', 50, '2026-08-01', '2020-01-01')`,
		a, terms)
	expectSQLError(t, err, "42501", "permission denied", "backdated raw insert")
	var year int
	err = appPool.QueryRow(ctx(t),
		`SELECT EXTRACT(YEAR FROM created_at)::int FROM compensation_line WHERE id = $1`,
		id).Scan(&year)
	expectOK(t, err, "read normalized created_at")
	if year != time.Now().Year() {
		t.Fatalf("created_at year = %d, want the current year (normalization bypassed)", year)
	}
}

func TestTermsCloseFunction(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := mkTerms(t, a, emp, "2026-01-01")
	var closed string
	err := appPool.QueryRow(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-07-01', 'op-1')::text`,
		a, emp).Scan(&closed)
	expectOK(t, err, "close open terms")
	if closed != "2026-06-30" {
		t.Fatalf("closed = %s, want 2026-06-30", closed)
	}
	var to string
	err = appPool.QueryRow(ctx(t),
		`SELECT effective_to::text FROM employment_terms WHERE id = $1`, id).Scan(&to)
	expectOK(t, err, "read closed row")
	if to != "2026-06-30" {
		t.Fatalf("effective_to = %s, want 2026-06-30", to)
	}
	var actor, after string
	err = appPool.QueryRow(ctx(t),
		`SELECT actor, after_state::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'terms.close'`, a).Scan(&actor, &after)
	expectOK(t, err, "read close audit")
	if actor != "op-1" {
		t.Fatalf("audit actor = %q, want op-1", actor)
	}
	if after != `{"effective_to": "2026-06-30"}` {
		t.Fatalf("audit after = %s", after)
	}
	// The successor now fits without overlapping.
	exec(t, appPool,
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-07-01', 'Agent', 'Support', 'workman', true, 'b', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')`,
		a, emp)
}

func TestTermsCloseFunctionValidation(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	mkTerms(t, a, emp, "2026-06-01")
	_, err := appPool.Exec(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-06-01', 'op')`, a, emp)
	expectSQLError(t, err, "P0001", "must be after the open row", "close on the open date")
	_, err = appPool.Exec(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-07-01', '')`, a, emp)
	expectSQLError(t, err, "P0001", "requires an actor", "close without actor")
	other := mkEmployee(t, a, "E002")
	_, err = appPool.Exec(ctx(t),
		`SELECT close_employment_terms($1::uuid, $2::uuid, '2026-07-01', 'op')`, a, other)
	expectSQLError(t, err, "P0001", "no open employment terms", "close with no open row")
	var to *string
	err = appPool.QueryRow(ctx(t),
		`SELECT effective_to::text FROM employment_terms WHERE employer_id = $1 AND employee_id = $2`,
		a, emp).Scan(&to)
	expectOK(t, err, "read terms row")
	if to != nil {
		t.Fatalf("effective_to = %s after failed closes, want NULL", *to)
	}
	var audits int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'terms.close'`,
		a).Scan(&audits)
	expectOK(t, err, "count close audits")
	if audits != 0 {
		t.Fatalf("close audits = %d after failed closes, want 0", audits)
	}
}

func TestStatusTransitionFunction(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	exec(t, appPool,
		`INSERT INTO employee_status_history (employer_id, employee_id, status, effective_from, reason)
		 VALUES ($1, $2, 'active', '2026-01-01', 'initial')`, a, emp)
	var oldFrom string
	err := appPool.QueryRow(ctx(t),
		`SELECT status_effective_date::text FROM employee WHERE id = $1`, emp).Scan(&oldFrom)
	expectOK(t, err, "read pre-transition date")
	exec(t, appPool,
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'suspended', '2026-03-01', 'probe', 'op-2')`,
		a, emp)
	var status, from string
	err = appPool.QueryRow(ctx(t),
		`SELECT status, status_effective_date::text FROM employee WHERE id = $1`, emp).Scan(&status, &from)
	expectOK(t, err, "read employee")
	if status != "suspended" || from != "2026-03-01" {
		t.Fatalf("employee = %s/%s, want suspended/2026-03-01", status, from)
	}
	rows, err := appPool.Query(ctx(t),
		`SELECT status, effective_from::text, effective_to::text FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 ORDER BY effective_from`, a, emp)
	expectOK(t, err, "read history")
	defer rows.Close()
	type span struct{ status, from, to string }
	var spans []span
	for rows.Next() {
		var s span
		var to *string
		if err := rows.Scan(&s.status, &s.from, &to); err != nil {
			t.Fatalf("scan history: %v", err)
		}
		if to != nil {
			s.to = *to
		}
		spans = append(spans, s)
	}
	if len(spans) != 2 || spans[0].status != "active" || spans[0].to != "2026-02-28" ||
		spans[1].status != "suspended" || spans[1].from != "2026-03-01" || spans[1].to != "" {
		t.Fatalf("spans = %+v, want contiguous active/suspended", spans)
	}
	var actor, before, after string
	err = appPool.QueryRow(ctx(t),
		`SELECT actor, before_state::text, after_state::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'employee.status'`, a).Scan(&actor, &before, &after)
	expectOK(t, err, "read transition audit")
	if actor != "op-2" {
		t.Fatalf("audit actor = %q, want op-2", actor)
	}
	// before.from is the employee row's pre-transition status date,
	// read back rather than assumed.
	if want := `{"from": "` + oldFrom + `", "status": "active"}`; before != want {
		t.Fatalf("audit before = %s, want %s", before, want)
	}
	if after != `{"from": "2026-03-01", "reason": "probe", "status": "suspended"}` {
		t.Fatalf("audit after = %s", after)
	}
}

func TestStatusTransitionFunctionValidation(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	exec(t, appPool,
		`INSERT INTO employee_status_history (employer_id, employee_id, status, effective_from, reason)
		 VALUES ($1, $2, 'active', CURRENT_DATE, 'initial')`, a, emp)
	_, err := appPool.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'active', CURRENT_DATE, 'r', 'op')`, a, emp)
	expectSQLError(t, err, "P0001", "already active", "transition to same status")
	_, err = appPool.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'resigned', CURRENT_DATE - 1, 'r', 'op')`, a, emp)
	expectSQLError(t, err, "P0001", "must be after the current row", "transition before open row")
	_, err = appPool.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'suspended', CURRENT_DATE + 1, '', 'op')`, a, emp)
	expectSQLError(t, err, "P0001", "requires a reason", "transition without reason")
	_, err = appPool.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'suspended', CURRENT_DATE + 1, 'r', '')`, a, emp)
	expectSQLError(t, err, "P0001", "requires an actor", "transition without actor")
	_, err = appPool.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'ghost', CURRENT_DATE + 1, 'r', 'op')`, a, emp)
	expectSQLError(t, err, "P0001", "unknown status", "transition to unknown status")
	exec(t, appPool,
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'resigned', CURRENT_DATE + 1, 'left', 'op')`,
		a, emp)
	_, err = appPool.Exec(ctx(t),
		`SELECT transition_employee_status($1::uuid, $2::uuid, 'active', CURRENT_DATE + 2, 'back', 'op')`, a, emp)
	expectSQLError(t, err, "P0001", "not allowed", "resigned -> active resurrection")
	var status string
	err = appPool.QueryRow(ctx(t), `SELECT status FROM employee WHERE id = $1`, emp).Scan(&status)
	expectOK(t, err, "read employee")
	if status != "resigned" {
		t.Fatalf("status = %q, want resigned", status)
	}
	var audits int
	err = appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'employee.status'`,
		a).Scan(&audits)
	expectOK(t, err, "count transition audits")
	if audits != 1 {
		t.Fatalf("transition audits = %d, want exactly the one success", audits)
	}
}

// Terms created_at normalizes to now() like compensation: a backdated
// INSERT cannot dodge the post-approval conflict detector.
func TestTermsCreatedAtNormalized(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	id := queryID(t, appPool,
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period, created_at)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'Support', 'workman', true, 'b', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly', '2020-01-01')
		 RETURNING id`,
		a, emp)
	var year int
	err := appPool.QueryRow(ctx(t),
		`SELECT EXTRACT(YEAR FROM created_at)::int FROM employment_terms WHERE id = $1`,
		id).Scan(&year)
	expectOK(t, err, "read normalized created_at")
	if year != time.Now().Year() {
		t.Fatalf("created_at year = %d, want the current year (normalization bypassed)", year)
	}
}
