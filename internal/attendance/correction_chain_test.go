package attendance

import (
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// readyChain builds a day roster whose employee can reach payroll
// readiness: employment terms, SHG instruction and implementable pack
// activations, all predating any approval the test performs.
func readyChain(t *testing.T, pool *pgxpool.Pool, tag string, extraDates ...string) (emp, no, period string) {
	t.Helper()
	emp, no = dayChain(t, pool, tag, extraDates...)
	var empID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&empID); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'support', 'workman', true, 'test', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'office', 'monthly')`,
		emp, empID); err != nil {
		t.Fatalf("terms: %v", err)
	}
	// Terms predate any approval the test performs by fixture order
	// (C8 normalizes created_at, so insertion order is the clock).
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, empID, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	// IMPLEMENTABLE packs are privileged state: the fixture sets them
	// as admin without testing activation itself.
	if _, err := testAdmin(t).Exec(ctx(t),
		`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES
		 ($1, 'SG-CPF-2026', 'v0', 'IMPLEMENTABLE'), ($1, 'SG-SDL', 'v0', 'IMPLEMENTABLE'),
		 ($1, 'SG-SHG', 'v0', 'IMPLEMENTABLE'), ($1, 'SG-MOM-KET', 'v0', 'IMPLEMENTABLE')`, emp); err != nil {
		t.Fatalf("activations: %v", err)
	}
	return emp, no, mkPeriod(t, pool, emp)
}

type sheetSnapshot struct {
	status     string
	approvedAt string
	hours      float64
	links      [][2]string
}

func snapshotSheet(t *testing.T, pool *pgxpool.Pool, emp, sheetID string) sheetSnapshot {
	t.Helper()
	var s sheetSnapshot
	var approvedAt *string
	if err := pool.QueryRow(ctx(t),
		`SELECT status, approved_at::text FROM timesheet WHERE employer_id = $1 AND id = $2::uuid`,
		emp, sheetID).Scan(&s.status, &approvedAt); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if approvedAt != nil {
		s.approvedAt = *approvedAt
	}
	got, err := timesheet.Get(ctx(t), pool, emp, sheetID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, l := range got.Lines {
		s.hours += l.Hours
	}
	rows, err := pool.Query(ctx(t),
		`SELECT li.line_id::text, li.interval_id::text FROM timesheet_line_interval li
		 JOIN timesheet_line l ON l.employer_id = li.employer_id AND l.id = li.line_id
		 WHERE li.employer_id = $1 AND l.timesheet_id = $2::uuid ORDER BY 1, 2`,
		emp, sheetID)
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			t.Fatalf("scan: %v", err)
		}
		s.links = append(s.links, pair)
	}
	return s
}

func (s sheetSnapshot) assertEqual(t *testing.T, other sheetSnapshot, what string) {
	t.Helper()
	if len(s.links) != len(other.links) {
		t.Fatalf("%s links %d vs %d", what, len(s.links), len(other.links))
	}
	if s.status != other.status || s.approvedAt != other.approvedAt || s.hours != other.hours {
		t.Fatalf("%s changed: %+v vs %+v", what, s, other)
	}
	for i := range s.links {
		if s.links[i] != other.links[i] {
			t.Fatalf("%s link %d changed: %v vs %v", what, i, s.links[i], other.links[i])
		}
	}
}

func approveSheet(t *testing.T, pool *pgxpool.Pool, emp, sheetID string) {
	t.Helper()
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheetID, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheetID, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

// Generate → Submit → Approve → Attempt correction → Reissue → Correct
// → Regenerate → Submit → Approve: the approved original stays
// byte-identical, readiness reports the conflict until the correction
// approves, and the correction becomes effective.
func TestCorrectionChainFullFlow(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyChain(t, pool, "CF")
	importCSV(t, pool, emp, spanCSV(no, "cf-1", "cf-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	r1, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !r1.Ready || len(r1.AttendanceConflicts) != 0 || len(r1.Included) != 1 {
		t.Fatalf("r1=%+v want ready with no conflicts", r1)
	}
	orig := liveWorkID(t, pool, emp)
	// Direct correction against the effective approval is rejected.
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "too late",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err == nil {
		t.Fatalf("direct correction of an approved-sheet interval must fail")
	} else {
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeInvalidState {
			t.Fatalf("err=%v want invalid_state", err)
		}
	}
	before := snapshotSheet(t, pool, emp, v1)
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	// With the correction draft open, the correction lands and the
	// draft regenerates atomically: no manual rebuild step exists.
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct after reissue: %v", err)
	}
	if len(rep.SegmentIDs) != 1 {
		t.Fatalf("rep=%+v", rep)
	}
	if h := sheetHours(t, pool, emp, v2.ID); h != 8 {
		t.Fatalf("v2=%.1fh want the regenerated 8h", h)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1")
	r2, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r2.Ready || len(r2.AttendanceConflicts) != 1 || r2.AttendanceConflicts[0].EmployeeNo != no {
		t.Fatalf("r2=%+v want the attendance conflict blocking readiness", r2)
	}
	if len(r2.Included) != 0 {
		t.Fatalf("included=%v want nobody included under conflict", r2.Included)
	}
	approveSheet(t, pool, emp, v2.ID)
	r3, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !r3.Ready || len(r3.AttendanceConflicts) != 0 {
		t.Fatalf("r3=%+v want ready once the correction approves", r3)
	}
	var empID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&empID); err != nil {
		t.Fatalf("employee: %v", err)
	}
	eff, err := timesheet.EffectiveApproved(ctx(t), pool, emp, empID, period)
	if err != nil || eff != v2.ID {
		t.Fatalf("effective=%s err=%v want v2", eff, err)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1 after v2 approval")
}

// A correction of an already-corrected approved sheet chains one more
// version: v1 → v2 approved → reissue → v3 draft → correct the v2
// segment → approve v3. Both decided versions stay immutable.
func TestCorrectionAfterReissueOfCorrected(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyChain(t, pool, "CC2")
	importCSV(t, pool, emp, spanCSV(no, "cc-1", "cc-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	snap1 := snapshotSheet(t, pool, emp, v1)
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	rep1, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{liveWorkID(t, pool, emp)},
		Start:       "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	approveSheet(t, pool, emp, v2.ID)
	snap2 := snapshotSheet(t, pool, emp, v2.ID)
	v3, err := timesheet.Reissue(ctx(t), pool, emp, v2.ID, "test-op")
	if err != nil {
		t.Fatalf("reissue v2: %v", err)
	}
	// The v2 segment feeds an approved sheet, but v3 is the open
	// correction draft of its chain: the correction is permitted.
	rep2, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{rep1.SegmentIDs[0]},
		Start:       "2026-08-03T09:00:00+08:00", End: "2026-08-03T16:00:00+08:00",
		Kind: "work", Reason: "earlier close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct v2 segment: %v", err)
	}
	if len(rep2.SegmentIDs) != 1 {
		t.Fatalf("rep=%+v", rep2)
	}
	if h := sheetHours(t, pool, emp, v3.ID); h != 7 {
		t.Fatalf("v3=%.1fh want 7", h)
	}
	approveSheet(t, pool, emp, v3.ID)
	snapshotSheet(t, pool, emp, v1).assertEqual(t, snap1, "v1")
	snapshotSheet(t, pool, emp, v2.ID).assertEqual(t, snap2, "v2")
	var empID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		emp, no).Scan(&empID); err != nil {
		t.Fatalf("employee: %v", err)
	}
	eff, err := timesheet.EffectiveApproved(ctx(t), pool, emp, empID, period)
	if err != nil || eff != v3.ID {
		t.Fatalf("effective=%s err=%v want v3", eff, err)
	}
}

// A rejected correction stays immutable; reissuing it opens a fresh
// correction draft that receives the next correction.
func TestRejectedCorrectionRecovery(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyChain(t, pool, "CR")
	importCSV(t, pool, emp, spanCSV(no, "cr-1", "cr-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	snap1 := snapshotSheet(t, pool, emp, v1)
	v2, err := timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	rep1, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{liveWorkID(t, pool, emp)},
		Start:       "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("submit v2: %v", err)
	}
	if _, err := timesheet.Reject(ctx(t), pool, emp, v2.ID, "test-op"); err != nil {
		t.Fatalf("reject v2: %v", err)
	}
	snap2 := snapshotSheet(t, pool, emp, v2.ID)
	v3, err := timesheet.Reissue(ctx(t), pool, emp, v2.ID, "test-op")
	if err != nil {
		t.Fatalf("reissue rejected: %v", err)
	}
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{rep1.SegmentIDs[0]},
		Start:       "2026-08-03T09:00:00+08:00", End: "2026-08-03T16:30:00+08:00",
		Kind: "work", Reason: "revised close",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("correct after rejection: %v", err)
	}
	if h := sheetHours(t, pool, emp, v3.ID); h != 7.5 {
		t.Fatalf("v3=%.1fh want 7.5", h)
	}
	approveSheet(t, pool, emp, v3.ID)
	snapshotSheet(t, pool, emp, v1).assertEqual(t, snap1, "v1")
	snapshotSheet(t, pool, emp, v2.ID).assertEqual(t, snap2, "v2")
	r, err := payroll.Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !r.Ready {
		t.Fatalf("r=%+v want ready", r)
	}
}

// Correction racing reissue stays coherent either way: the approval
// never mutates, at most one correction group exists, and a winner
// lands in a regenerated open draft.
func TestConcurrentCorrectionAndReissue(t *testing.T) {
	pool := testPool(t)
	emp, no, period := readyChain(t, pool, "CX")
	importCSV(t, pool, emp, spanCSV(no, "cx-1", "cx-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	gen, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("gen=%+v err=%v", gen, err)
	}
	v1 := gen.Generated[0].TimesheetID
	approveSheet(t, pool, emp, v1)
	before := snapshotSheet(t, pool, emp, v1)
	orig := liveWorkID(t, pool, emp)
	var wg sync.WaitGroup
	var cerr, rerr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, cerr = Correct(ctx(t), pool, emp, CorrectionInput{
			IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
			Kind: "work", Reason: "race",
		}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	}()
	go func() {
		defer wg.Done()
		_, rerr = timesheet.Reissue(ctx(t), pool, emp, v1, "test-op")
	}()
	wg.Wait()
	if rerr != nil {
		t.Fatalf("reissue: %v", rerr)
	}
	if cerr != nil {
		var ae *apperr.Error
		if !errors.As(cerr, &ae) || ae.Code != apperr.CodeInvalidState {
			t.Fatalf("correct err=%v want invalid_state or success", cerr)
		}
	}
	var groups int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM attendance_correction_group WHERE employer_id = $1`, emp).Scan(&groups); err != nil || groups > 1 {
		t.Fatalf("groups=%d err=%v", groups, err)
	}
	if (groups == 1) == (cerr != nil) {
		t.Fatalf("groups=%d cerr=%v want exactly the winner's state", groups, cerr)
	}
	snapshotSheet(t, pool, emp, v1).assertEqual(t, before, "v1")
	if groups == 1 {
		var drafts int
		if err := pool.QueryRow(ctx(t),
			`SELECT count(*) FROM timesheet WHERE employer_id = $1 AND supersedes_id = $2::uuid AND status = 'draft'`,
			emp, v1).Scan(&drafts); err != nil || drafts != 1 {
			t.Fatalf("drafts=%d err=%v want the open correction draft", drafts, err)
		}
		var hours float64
		if err := pool.QueryRow(ctx(t),
			`SELECT COALESCE(sum(hours), 0) FROM timesheet_line l
			 JOIN timesheet t ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
			 WHERE t.employer_id = $1 AND t.supersedes_id = $2::uuid`,
			emp, v1).Scan(&hours); err != nil || hours != 8 {
			t.Fatalf("draft hours=%.1f err=%v want the regenerated 8h", hours, err)
		}
	}
}
