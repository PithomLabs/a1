package hr

import (
	"strings"
	"testing"
)

// E5 service path: CorrectCompensation mints an audited successor
// through the controlled function; the original never changes.
func TestCorrectCompensation(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "K1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	orig, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
		EffectiveFrom: "2026-08-01", Reason: "night shift",
	}, "test-op")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	succ, err := CorrectCompensation(ctx(t), pool, emp, orig, CorrectionInput{
		AmountMonthly: "120.00", EffectiveFrom: "2026-08-01", Reason: "rate fix",
	}, "test-op")
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if succ == orig {
		t.Fatal("successor must be a new row")
	}
	lines, err := ListCompensation(ctx(t), pool, emp, tm.ID)
	if err != nil || len(lines) != 2 {
		t.Fatalf("lines=%+v err=%v want original plus successor", lines, err)
	}
	byID := map[string]Compensation{}
	for _, l := range lines {
		byID[l.ID] = l
	}
	if byID[orig].AmountMonthly != "100.00" {
		t.Fatalf("original=%+v want it untouched", byID[orig])
	}
	if byID[succ].AmountMonthly != "120.00" {
		t.Fatalf("successor=%+v want the corrected amount", byID[succ])
	}
	if byID[succ].SupersedesID == nil || *byID[succ].SupersedesID != orig {
		t.Fatalf("successor=%+v want the link to the original", byID[succ])
	}
	r := lastAudit(t, pool, emp, "compensation.correct")
	if r.actor != "test-op" || r.entity != "compensation_line" ||
		r.entityID == nil || *r.entityID != succ {
		t.Fatalf("audit=%+v want the correction attribution", r)
	}
	for _, want := range []string{`"amount": "120.00"`, `"reason": "rate fix"`, orig} {
		if !strings.Contains(r.after, want) && !strings.Contains(r.before, want) {
			t.Fatalf("audit before=%s after=%s want %s", r.before, r.after, want)
		}
	}
}

// Reason and operator are mandatory; money validation matches the
// database CHECK exactly (Go==SQL parity).
func TestCorrectCompensationValidation(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "K2", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, termsInput("Sunday"), "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	orig, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
		EffectiveFrom: "2026-08-01", Reason: "night shift",
	}, "test-op")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   CorrectionInput
		op   string
	}{
		{"no reason", CorrectionInput{AmountMonthly: "120.00", EffectiveFrom: "2026-08-01"}, "test-op"},
		{"no operator", CorrectionInput{AmountMonthly: "120.00", EffectiveFrom: "2026-08-01", Reason: "x"}, ""},
		{"third of a cent", CorrectionInput{AmountMonthly: "120.005", EffectiveFrom: "2026-08-01", Reason: "x"}, "test-op"},
		{"negative", CorrectionInput{AmountMonthly: "-1", EffectiveFrom: "2026-08-01", Reason: "x"}, "test-op"},
		{"bad date", CorrectionInput{AmountMonthly: "120.00", EffectiveFrom: "08/01/2026", Reason: "x"}, "test-op"},
	} {
		if _, err := CorrectCompensation(ctx(t), pool, emp, orig, tc.in, tc.op); err == nil {
			t.Fatalf("%s: expected a validation error", tc.name)
		}
	}
	// The add path requires a reason too: every audit row carries one.
	if _, err := AddCompensation(ctx(t), pool, emp, tm.ID, CompensationInput{
		Kind: "allowance", Code: "MEAL", AmountMonthly: "50.00", EffectiveFrom: "2026-08-01",
	}, "test-op"); err == nil {
		t.Fatal("expected a validation error for a reason-less add")
	}
}
