package timesheet

import (
	"errors"
	"sync"
	"testing"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/attendance"
)

// TestRejectedRecoveryFlow is the §5B correction lifecycle:
// Generate → Submit → Reject → Reissue → Correct → Submit → Approve.
// The rejected original is never mutated; the correction version links
// to it and carries the corrected hours.
func TestRejectedRecoveryFlow(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "E")
	gen, err := Generate(ctx(t), pool, emp, "PP-0826", "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen.Generated) != 1 {
		t.Fatalf("generated=%+v skipped=%+v", gen.Generated, gen.Skipped)
	}
	orig := gen.Generated[0].TimesheetID
	if _, err := Submit(ctx(t), pool, emp, orig, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := Reject(ctx(t), pool, emp, orig, "test-op"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	re, err := Reissue(ctx(t), pool, emp, orig, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if re.SupersedesID == nil || *re.SupersedesID != orig {
		t.Fatalf("reissue links %+v, want %s", re.SupersedesID, orig)
	}
	if re.Status != "draft" {
		t.Fatalf("reissue status=%s want draft", re.Status)
	}
	var intervalID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND kind = 'work' AND is_current LIMIT 1`,
		emp).Scan(&intervalID); err != nil {
		t.Fatalf("find interval: %v", err)
	}
	// Correction succeeds: the only live link is the reissued draft; the
	// rejected link is inert history.
	if _, err := attendance.Correct(ctx(t), pool, emp, attendance.CorrectionInput{
		IntervalIDs: []string{intervalID}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "test-op", RegenerateDraft, CorrectionDrafts); err != nil {
		t.Fatalf("correct: %v", err)
	}
	got, err := Get(ctx(t), pool, emp, re.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].Hours != 8 {
		t.Fatalf("draft lines=%+v want one 8h line", got.Lines)
	}
	if _, err := Submit(ctx(t), pool, emp, re.ID, "test-op"); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	final, err := Approve(ctx(t), pool, emp, re.ID, "test-op")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if final.Status != "approved" {
		t.Fatalf("status=%s", final.Status)
	}
	// The rejected original is byte-identical history: still rejected,
	// still 9 hours, still linked to the old interval.
	origGot, err := Get(ctx(t), pool, emp, orig)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if origGot.Status != "rejected" || len(origGot.Lines) != 1 || origGot.Lines[0].Hours != 9 {
		t.Fatalf("original mutated: %+v", origGot)
	}
	eff, err := EffectiveApproved(ctx(t), pool, emp, origGot.EmployeeID, origGot.PayPeriodID)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if eff != re.ID {
		t.Fatalf("effective=%s want %s", eff, re.ID)
	}
}

// TestReissueGuards covers the chain rules: drafts correct in place,
// submitted sheets decide first, one open correction at a time.
func TestReissueGuards(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "E")
	gen, err := Generate(ctx(t), pool, emp, "PP-0826", "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("generate: %v %+v", err, gen)
	}
	orig := gen.Generated[0].TimesheetID
	if _, err := Reissue(ctx(t), pool, emp, orig, "test-op"); err == nil {
		t.Fatal("reissue of draft succeeded")
	}
	if _, err := Submit(ctx(t), pool, emp, orig, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := Reissue(ctx(t), pool, emp, orig, "test-op"); err == nil {
		t.Fatal("reissue of submitted succeeded")
	}
	if _, err := Approve(ctx(t), pool, emp, orig, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	r1, err := Reissue(ctx(t), pool, emp, orig, "test-op")
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if _, err := Reissue(ctx(t), pool, emp, orig, "test-op"); err == nil {
		t.Fatal("second open correction succeeded")
	}
	// Deciding the correction frees the chain: reject it, then reissue
	// chains from the rejected correction.
	if _, err := Submit(ctx(t), pool, emp, r1.ID, "test-op"); err != nil {
		t.Fatalf("submit r1: %v", err)
	}
	if _, err := Reject(ctx(t), pool, emp, r1.ID, "test-op"); err != nil {
		t.Fatalf("reject r1: %v", err)
	}
	r2, err := Reissue(ctx(t), pool, emp, orig, "test-op")
	if err != nil {
		t.Fatalf("reissue after reject: %v", err)
	}
	if r2.SupersedesID == nil || *r2.SupersedesID != r1.ID {
		t.Fatalf("r2 links %+v, want %s", r2.SupersedesID, r1.ID)
	}
	// The standing approval is still effective while the correction
	// is a draft.
	eff, err := EffectiveApproved(ctx(t), pool, emp, r2.EmployeeID, r2.PayPeriodID)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if eff != orig {
		t.Fatalf("effective=%s want standing approval %s", eff, orig)
	}
}

// TestConcurrentGenerateExactlyOnce races two full generations: exactly
// one sheet per employee exists afterwards.
func TestConcurrentGenerateExactlyOnce(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "E")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Generate(ctx(t), pool, emp, "PP-0826", "test-op")
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM timesheet WHERE employer_id = $1`, emp).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sheets=%d err=%v want exactly 1", n, err)
	}
}

// TestCorrectRegeneratesDraft proves a correction behind a draft sheet
// rebuilds the draft's lines in the same transaction.
func TestCorrectRegeneratesDraft(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "E")
	gen, err := Generate(ctx(t), pool, emp, "PP-0826", "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("generate: %v %+v", err, gen)
	}
	sheet := gen.Generated[0].TimesheetID
	var intervalID string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM attendance_interval WHERE employer_id = $1 AND is_current LIMIT 1`,
		emp).Scan(&intervalID); err != nil {
		t.Fatalf("find interval: %v", err)
	}
	if _, err := attendance.Correct(ctx(t), pool, emp, attendance.CorrectionInput{
		IntervalIDs: []string{intervalID}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T12:00:00+08:00",
		Kind: "work", Reason: "short day",
	}, "test-op", RegenerateDraft, CorrectionDrafts); err != nil {
		t.Fatalf("correct: %v", err)
	}
	got, err := Get(ctx(t), pool, emp, sheet)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].Hours != 3 {
		t.Fatalf("draft lines=%+v want one 3h line", got.Lines)
	}
}

// Two concurrent approvals of one submitted sheet produce exactly one
// approval transition: the winner flips the status, the loser either
// observes the approval or loses the status-guarded UPDATE.
func TestConcurrentApproveSingleTransition(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "E")
	gen, err := Generate(ctx(t), pool, emp, "PP-0826", "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("generate: %v %+v", err, gen)
	}
	sheet := gen.Generated[0].TimesheetID
	if _, err := Submit(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = Approve(ctx(t), pool, emp, sheet, "test-op")
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err == nil {
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeInvalidState {
			t.Fatalf("loser error = %v, want invalid_state", err)
		}
	}
	got, err := Get(ctx(t), pool, emp, sheet)
	if err != nil || got.Status != "approved" {
		t.Fatalf("sheet=%+v err=%v want approved", got, err)
	}
	var audits int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'timesheet.approved'`,
		emp).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("approval audits = %d, err = %v, want exactly one transition", audits, err)
	}
}

// Two concurrent reissues of one approved sheet open exactly one
// correction draft: the single-successor UNIQUE admits one winner.
func TestConcurrentReissueSingleWinner(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "E")
	gen, err := Generate(ctx(t), pool, emp, "PP-0826", "test-op")
	if err != nil || len(gen.Generated) != 1 {
		t.Fatalf("generate: %v %+v", err, gen)
	}
	sheet := gen.Generated[0].TimesheetID
	if _, err := Submit(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := Approve(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = Reissue(ctx(t), pool, emp, sheet, "test-op")
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || (ae.Code != apperr.CodeConflict && ae.Code != apperr.CodeInvalidState) {
			t.Fatalf("loser error = %v, want conflict or invalid_state", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly one correction draft", wins)
	}
	var versions int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM timesheet WHERE employer_id = $1`, emp).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versions = %d, err = %v, want original plus one draft", versions, err)
	}
}
