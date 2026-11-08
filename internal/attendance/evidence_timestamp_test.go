package attendance

import (
	"strings"
	"testing"
	"time"

	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// Stage D readiness flow (§7): after approval, a directly inserted
// clock event with a backdated created_at normalizes to now and
// readiness flags it as unresolved new evidence.
func TestReadinessDetectsPostApprovalEvidence(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyChain(t, pool, "TS")
	importCSV(t, pool, emp, spanCSV(no, "ts-1", "ts-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	approveSheet(t, pool, emp, gen.Generated[0].TimesheetID)
	var ee string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&ee); err != nil {
		t.Fatalf("employee: %v", err)
	}
	var id string
	err = pool.QueryRow(ctx(t),
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind,
		  source, source_event_id, created_at)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-10T09:00:00+08:00', 'Asia/Singapore', 'in',
		  'vendor-x', $3, '2020-01-01T00:00:00Z')
		 RETURNING id::text`,
		emp, ee, "ts-late").Scan(&id)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	var year int
	if err := pool.QueryRow(ctx(t),
		`SELECT EXTRACT(YEAR FROM created_at)::int FROM clock_event WHERE id = $1::uuid`,
		id).Scan(&year); err != nil || year != time.Now().Year() {
		t.Fatalf("year=%d err=%v want normalization to now", year, err)
	}
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(r.AttendanceConflicts) != 1 ||
		!strings.Contains(r.AttendanceConflicts[0].Reason, "unpaired clock evidence") {
		t.Fatalf("conflicts=%+v want the late evidence flagged", r.AttendanceConflicts)
	}
}

// Normalization provably does NOT fix D-RACE: an evidence
// transaction that starts before approval but commits after it keeps
// a transaction-start stamp predating approved_at, and only the
// lock-serialized basis_seq flags it. If the seq leg ever regresses,
// this test fails while every timestamp check stays green.
func TestNormalizationDoesNotFixDRACE(t *testing.T) {
	pool := testPool(t)
	dsn := dbutil.RaceDSN(t, pool)
	emp, no, period := readyChain(t, pool, "DR")
	importCSV(t, pool, emp, spanCSV(no, "dr-1", "dr-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	sheet := gen.Generated[0].TimesheetID
	var ee string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&ee); err != nil {
		t.Fatalf("employee: %v", err)
	}
	late := dbutil.AppConn(t, dsn)
	ltx, err := late.Begin(ctx(t))
	if err != nil {
		t.Fatalf("begin late: %v", err)
	}
	// Approval commits while the evidence transaction is still open.
	approveSheet(t, pool, emp, sheet)
	if _, err := ltx.Exec(ctx(t),
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind,
		  source, source_event_id)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-10T09:00:00+08:00', 'Asia/Singapore', 'in',
		  'vendor-x', 'dr-late')`,
		emp, ee); err != nil {
		t.Fatalf("late insert: %v", err)
	}
	var diverged bool
	if err := ltx.QueryRow(ctx(t),
		`SELECT max(e.created_at) < (SELECT approved_at FROM timesheet WHERE id = $1)
		 FROM clock_event e WHERE e.employer_id = $2 AND e.source_event_id = 'dr-late'`,
		sheet, emp).Scan(&diverged); err != nil || !diverged {
		t.Fatalf("diverged=%v err=%v want the tx-start stamp predating approval", diverged, err)
	}
	if err := ltx.Commit(ctx(t)); err != nil {
		t.Fatalf("commit late: %v", err)
	}
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	found := false
	for _, c := range r.AttendanceConflicts {
		if strings.Contains(c.Reason, "created after approval") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts=%+v want the seq leg flagging the diverged evidence", r.AttendanceConflicts)
	}
}
