package integrity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runID makes every fixture key unique per suite run, so reruns pass
// against a database that already holds earlier runs' rows.
var runID = fmt.Sprintf("%d", time.Now().UnixNano())

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func key(t *testing.T, parts ...string) string {
	t.Helper()
	base := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	return strings.Join(append([]string{base, runID}, parts...), "_")
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx(t), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func queryID(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t), sql, args...).Scan(&id); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return id
}

func expectOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", what, err)
	}
}

func expectSQLError(t *testing.T, err error, code, substr, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected SQL error, got nil", what)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: expected *pgconn.PgError, got %T (%v)", what, err, err)
	}
	if code != "" && pgErr.Code != code {
		t.Fatalf("%s: expected SQLSTATE %s, got %s (%v)", what, code, pgErr.Code, err)
	}
	if substr != "" && !strings.Contains(pgErr.Message, substr) {
		t.Fatalf("%s: expected message containing %q, got %q", what, substr, pgErr.Message)
	}
}

// Fixture builders below all use the app pool: the app role must be able
// to do the product's everyday writes.

func mkEmployer(t *testing.T, code, uen string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, '8 Test Row') RETURNING id`,
		code, code+" Pte Ltd", uen)
}

func mkEmployee(t *testing.T, employerID, empNo string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, $2, $3, '9 Test Ave', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE) RETURNING id`,
		employerID, empNo, empNo)
}

func mkTerms(t *testing.T, employerID, employeeID, from string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, $3, 'Agent', 'Support', 'workman', true, 'manual-wages', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')
		 RETURNING id`,
		employerID, employeeID, from)
}

func mkPeriod(t *testing.T, employerID, code string) string {
	t.Helper()
	return mkPeriodSpan(t, employerID, code, "2026-08-01", "2026-08-31")
}

// mkPeriodSpan varies the range: same-employer periods must not overlap.
func mkPeriodSpan(t *testing.T, employerID, code, startsOn, endsOn string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, $2, $3::date, $4::date, '2026-09-07', '2026-09-07', '2026-09-14') RETURNING id`,
		employerID, code, startsOn, endsOn)
}

func mkPayCode(t *testing.T, code string) {
	t.Helper()
	exec(t, appPool,
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ($1, $1, 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`, code)
}

func mkClientProgramme(t *testing.T, employerID, code string) (clientID, programmeID string) {
	t.Helper()
	clientID = queryID(t, appPool,
		`INSERT INTO client (employer_id, code, name) VALUES ($1, $2, $2) RETURNING id`,
		employerID, code+"_CLI")
	programmeID = queryID(t, appPool,
		`INSERT INTO programme (employer_id, client_id, code, name, timezone) VALUES ($1, $2, $3, $3, 'Asia/Singapore') RETURNING id`,
		employerID, clientID, code+"_PRG")
	return clientID, programmeID
}

func mkTemplate(t *testing.T, employerID, code string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO shift_template (employer_id, code, name, start_local, end_local, cross_midnight)
		 VALUES ($1, $2, $2, '09:00', '18:00', false) RETURNING id`,
		employerID, code)
}

func mkRoster(t *testing.T, employerID, programmeID, week string) (rosterID, revisionID string) {
	t.Helper()
	rosterID = queryID(t, appPool,
		`INSERT INTO roster (employer_id, programme_id, week_starting) VALUES ($1, $2, $3) RETURNING id`,
		employerID, programmeID, week)
	revisionID = queryID(t, appPool,
		`INSERT INTO roster_revision (employer_id, roster_id, version) VALUES ($1, $2, 1) RETURNING id`,
		employerID, rosterID)
	return rosterID, revisionID
}

func mkAssignment(t *testing.T, employerID, revisionID, employeeID, templateID, date string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, template_id, rest_day)
		 VALUES ($1, $2, $3, $4, $5, false) RETURNING id`,
		employerID, revisionID, employeeID, date, templateID)
}

func mkInterval(t *testing.T, employerID, employeeID, refs string) string {
	t.Helper()
	return mkIntervalSpan(t, employerID, employeeID, refs, "2026-08-03T09:00+08", "2026-08-03T18:00+08")
}

// mkIntervalSpan varies the span: live same-employee intervals must not
// overlap (exclusion constraint), so multi-interval tests need distinct
// ranges.
func mkIntervalSpan(t *testing.T, employerID, employeeID, refs, start, end string) string {
	t.Helper()
	// local_date is trigger-pinned to the employment-local start
	// date: derive it honestly instead of hardcoding one.
	loc, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("load SGT: %v", err)
	}
	// Fixture instants use short offsets ("+08") and often omit
	// seconds; normalize to full RFC3339 before parsing.
	norm := start
	if len(norm) >= 3 && (norm[len(norm)-3] == '+' || norm[len(norm)-3] == '-') {
		norm += ":00"
	}
	var inst time.Time
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00"} {
		if inst, err = time.Parse(layout, norm); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("parse start %q: %v", start, err)
	}
	return queryID(t, appPool,
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, $3, $4, 'Asia/Singapore', $6::date, 'work', 'clock', $5::jsonb)
		 RETURNING id`,
		employerID, employeeID, start, end, refs, inst.In(loc).Format("2006-01-02"))
}

func mkRecord(t *testing.T, employerID, employeeID, date string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO attendance_record (employer_id, employee_id, business_date, actual_min, actual_us)
		 VALUES ($1, $2, $3, 480, 28800000000) RETURNING id`,
		employerID, employeeID, date)
}

func mkRun(t *testing.T, employerID, periodID string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO payroll_run (employer_id, pay_period_id, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES ($1, $2, 'test', '{}', 'test/0', '{}') RETURNING id`,
		employerID, periodID)
}

// requireStatus fails unless the row's status column reads want. Every
// state-transition test asserts through this: a trigger that silently
// discards an update must fail the suite, not the assertion after it.
func requireStatus(t *testing.T, table, id, want string) {
	t.Helper()
	var got string
	err := appPool.QueryRow(ctx(t),
		"SELECT status FROM "+table+" WHERE id = $1", id).Scan(&got)
	expectOK(t, err, "read "+table+" status")
	if got != want {
		t.Fatalf("%s %s status = %q, want %q (update silently lost?)", table, id, got, want)
	}
}

func lineRefs(t *testing.T) string {
	t.Helper()
	return `{"timesheet_line_ids": [], "terms_id": "00000000-0000-0000-0000-000000000000", "rule_pack_versions": {}, "calculation": {}}`
}
