package payroll

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// mkConflictEmployeeBase builds the employee half of the conflict
// fixture (employee, terms, SHG); approveConflictSheet adds the
// approved sheet. Split so tests can interleave evidence in honest
// commit order: insertion order is the only clock (triggers
// normalize created_at and assign basis_seq), and no fixture may
// fabricate history with an explicit backdated timestamp.
func mkConflictEmployeeBase(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	no := fmt.Sprintf("EC%06d", time.Now().UnixNano()%1000000)
	var e string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, $2, $2, 'a', 'F', '1990-01-01', 'SG', 'active', '2026-01-01') RETURNING id`,
		emp, no).Scan(&e); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'support', 'workman', true, 'test', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		emp, e); err != nil {
		t.Fatalf("terms: %v", err)
	}
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, e, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	return e
}

func approveConflictSheet(t *testing.T, pool *pgxpool.Pool, emp, period, e string) {
	t.Helper()
	var sheet string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
		 VALUES ($1, $2, $3, 'draft') RETURNING id`,
		emp, e, period).Scan(&sheet); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, sheet, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	// Approve at now(): earlier rows predate it in real time and
	// later evidence rows sort strictly after it, deterministically.
	if _, err := pool.Exec(ctx(t),
		`UPDATE timesheet SET status = 'approved', approved_by = 'op', approved_at = now()
		 WHERE id = $1`, sheet); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

// mkConflictEmployee builds a complete employee (terms, SHG, approved
// sheet with lines) whose approval timestamp the test owns: the shared
// fixture's approvals are frozen and cannot be backdated.
func mkConflictEmployee(t *testing.T, pool *pgxpool.Pool, emp, period string) string {
	t.Helper()
	e := mkConflictEmployeeBase(t, pool, emp)
	approveConflictSheet(t, pool, emp, period, e)
	return e
}

func employeeNo(t *testing.T, pool *pgxpool.Pool, emp, id string) string {
	t.Helper()
	var no string
	if err := pool.QueryRow(ctx(t),
		`SELECT employee_no FROM employee WHERE employer_id = $1 AND id = $2`, emp, id).Scan(&no); err != nil {
		t.Fatalf("no: %v", err)
	}
	return no
}

// Attendance-conflict pins: payroll readiness must notice attendance
// evidence created after the effective approval — corrected intervals
// and unpaired clock events alike — and clear once a newer approval
// covers them. Dispositioned duplicates are handled evidence, not
// conflicts.
func TestAssessAttendanceConflictInterval(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1 := mkConflictEmployee(t, pool, emp, period)
	no := employeeNo(t, pool, emp, e1)
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-03T09:00:00+08:00', '2026-08-03T17:00:00+08:00', 'Asia/Singapore', '2026-08-03', 'work', 'assumption', '{"assumption": "test"}')`,
		emp, e1); err != nil {
		t.Fatalf("interval: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r.Ready {
		t.Fatal("expected not ready under attendance conflict")
	}
	if len(r.AttendanceConflicts) != 1 || r.AttendanceConflicts[0].EmployeeNo != no {
		t.Fatalf("conflicts=%+v", r.AttendanceConflicts)
	}
	for _, in := range r.Included {
		if in == no {
			t.Fatalf("included=%v want %s excluded under conflict", r.Included, no)
		}
	}
	// Approving a correction version built from the current evidence
	// clears the conflict through the controlled workflow.
	var v1 string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM timesheet WHERE employer_id = $1 AND employee_id = $2 AND supersedes_id IS NULL`,
		emp, e1).Scan(&v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	r2, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r2.AttendanceConflicts) != 0 {
		t.Fatalf("conflicts=%+v want cleared", r2.AttendanceConflicts)
	}
}

func TestAssessAttendanceConflictUnpairedEvent(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1 := mkConflictEmployee(t, pool, emp, period)
	no := employeeNo(t, pool, emp, e1)
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, '2026-08-03T09:00:00+08:00', 'Asia/Singapore', 'in', 'vendor-x', 'late-1')`,
		emp, e1); err != nil {
		t.Fatalf("event: %v", err)
	}
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.AttendanceConflicts) != 1 || r.AttendanceConflicts[0].EmployeeNo != no {
		t.Fatalf("conflicts=%+v want the late unpaired evidence", r.AttendanceConflicts)
	}
}

func TestAssessAttendanceConflictHandledEvidence(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	e1 := mkConflictEmployeeBase(t, pool, emp)
	// A duplicate delivery confirmed against its canonical event is
	// handled evidence: it must not hold readiness hostage. All
	// evidence below commits before the approval in real order, with
	// honest trigger-assigned timestamps: fabricating an old
	// created_at after approval is the A1-F5 backdate dodge, not
	// covered evidence.
	var dup, canon string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, '2026-08-03T09:00:00+08:00', 'Asia/Singapore', 'in', 'vendor-x', 'dup-1')
		 RETURNING id::text`, emp, e1).Scan(&dup); err != nil {
		t.Fatalf("dup: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, '2026-08-03T09:00:00+08:00', 'Asia/Singapore', 'in', 'vendor-y', 'canon-1')
		 RETURNING id::text`, emp, e1).Scan(&canon); err != nil {
		t.Fatalf("canon: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO clock_event_disposition (employer_id, event_id, disposition, duplicate_of_event_id, reason, decided_by)
		 VALUES ($1, $2::uuid, 'duplicate', $3::uuid, 'resend', 'test')`, emp, dup, canon); err != nil {
		t.Fatalf("disposition: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO attendance_interval (employer_id, employee_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs)
		 VALUES ($1, $2, '2026-08-03T09:00:00+08:00', '2026-08-03T17:00:00+08:00', 'Asia/Singapore', '2026-08-03', 'work', 'clock', $3::jsonb)`,
		emp, e1, `{"clock_event_ids": ["`+canon+`"]}`); err != nil {
		t.Fatalf("interval: %v", err)
	}
	approveConflictSheet(t, pool, emp, period, e1)
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.AttendanceConflicts) != 0 {
		t.Fatalf("conflicts=%+v want none for handled and covered evidence", r.AttendanceConflicts)
	}
}
