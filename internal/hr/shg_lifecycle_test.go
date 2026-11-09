package hr

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mkSHGFixture creates an employer with one employee for SHG lifecycle
// tests and returns both ids.
func mkSHGFixture(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	emp := mkEmployer(t, pool, uniq(t, "SHG"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "E001", LegalName: "SHG Person", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	return emp, e.ID
}

func shgTriple() (string, string, string) {
	return "2026-08-02T10:00:00Z", "verifier-1", "signed form SHG-42 on file"
}

func TestSHGPendingSet(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	s, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "pending",
		SourceDeclaration: "employee form 2026-07", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if s.Status != "pending" || s.SupersedesID != nil {
		t.Fatalf("shg=%+v", s)
	}
	if s.VerifiedAt != nil || s.VerifiedBy != nil || s.VerificationBasis != nil {
		t.Fatalf("pending carries verified triple: %+v", s)
	}
	if s.BasisSeq <= 0 {
		t.Fatalf("basis_seq not assigned: %+v", s)
	}
}

func TestSHGVerifyChain(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	pend, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "SINDA", Status: "pending",
		SourceDeclaration: "employee form 2026-07", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	at, by, basis := shgTriple()
	v, err := VerifySHG(ctx(t), pool, emp, pend.ID, SHGVerifyInput{
		EffectiveFrom: "2026-08-01", SourceDeclaration: "employee form 2026-07",
		VerifiedAt: at, VerifiedBy: by, VerificationBasis: basis,
	}, "test-op")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Status != "verified_none" || v.Fund != "SINDA" {
		t.Fatalf("verified=%+v", v)
	}
	if v.SupersedesID == nil || *v.SupersedesID != pend.ID {
		t.Fatalf("supersedes=%v want %s", v.SupersedesID, pend.ID)
	}
	if v.VerifiedBy == nil || *v.VerifiedBy != by {
		t.Fatalf("triple not persisted: %+v", v)
	}
	if v.BasisSeq <= pend.BasisSeq {
		t.Fatalf("successor basis_seq %d not after predecessor %d", v.BasisSeq, pend.BasisSeq)
	}
	list, err := ListSHG(ctx(t), pool, emp, "E001")
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestSHGVerifyRejects(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	at, by, basis := shgTriple()
	active, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "active",
		SourceDeclaration: "form", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	in := SHGVerifyInput{EffectiveFrom: "2026-08-01", SourceDeclaration: "form",
		VerifiedAt: at, VerifiedBy: by, VerificationBasis: basis}
	if _, err := VerifySHG(ctx(t), pool, emp, active.ID, in, "test-op"); err == nil ||
		!strings.Contains(err.Error(), "pending") {
		t.Fatalf("verify active: err=%v want pending-only rejection", err)
	}
	pend, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "MBMF", Status: "pending",
		SourceDeclaration: "form", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set pending: %v", err)
	}
	bad := in
	bad.VerificationBasis = ""
	if _, err := VerifySHG(ctx(t), pool, emp, pend.ID, bad, "test-op"); err == nil {
		t.Fatal("verify with partial triple accepted")
	}
	if _, err := VerifySHG(ctx(t), pool, emp, "00000000-0000-0000-0000-000000000000", in, "test-op"); err == nil {
		t.Fatal("verify of unknown instruction accepted")
	}
	// A verified tip cannot be verified again.
	if _, err := VerifySHG(ctx(t), pool, emp, pend.ID, in, "test-op"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := VerifySHG(ctx(t), pool, emp, pend.ID, in, "test-op"); err == nil {
		t.Fatal("second verify of the same pending row accepted")
	}
}

func TestSHGCorrectChain(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	orig, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "ECF", Status: "active",
		SourceDeclaration: "form v1", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	next, err := CorrectSHG(ctx(t), pool, emp, orig.ID, SHGCorrectInput{
		Status: "opt_out", SourceDeclaration: "opt-out request",
		EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	if next.Status != "opt_out" || next.Fund != "ECF" {
		t.Fatalf("corrected=%+v", next)
	}
	if next.SupersedesID == nil || *next.SupersedesID != orig.ID {
		t.Fatalf("supersedes=%v want %s", next.SupersedesID, orig.ID)
	}
	// The original row is never mutated: same status and open range.
	list, err := ListSHG(ctx(t), pool, emp, "E001")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *SHGInstruction
	for i := range list {
		if list[i].ID == orig.ID {
			found = &list[i]
		}
	}
	if found == nil || found.Status != "active" || found.EffectiveTo != nil {
		t.Fatalf("original mutated: %+v", found)
	}
	// Second successor of the same original is rejected.
	if _, err := CorrectSHG(ctx(t), pool, emp, orig.ID, SHGCorrectInput{
		Status: "active", SourceDeclaration: "form v3", EffectiveFrom: "2026-08-01",
	}, "test-op"); err == nil {
		t.Fatal("second successor accepted")
	}
}

func TestSHGCorrectRejects(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	end := "2026-08-31"
	closed, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "active",
		SourceDeclaration: "form", EffectiveFrom: "2026-08-01", EffectiveTo: &end,
	}, "test-op")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := CorrectSHG(ctx(t), pool, emp, closed.ID, SHGCorrectInput{
		Status: "opt_out", SourceDeclaration: "x", EffectiveFrom: "2026-08-01",
	}, "test-op"); err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("correct closed-range row: err=%v want open-ended-only rejection", err)
	}
	open, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "SINDA", Status: "active",
		SourceDeclaration: "form", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	// Regression to pending is rejected.
	if _, err := CorrectSHG(ctx(t), pool, emp, open.ID, SHGCorrectInput{
		Status: "pending", SourceDeclaration: "x", EffectiveFrom: "2026-08-01",
	}, "test-op"); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("correct to pending: err=%v want rejection", err)
	}
	// verified_none target requires the full triple.
	if _, err := CorrectSHG(ctx(t), pool, emp, open.ID, SHGCorrectInput{
		Status: "verified_none", SourceDeclaration: "x", EffectiveFrom: "2026-08-01",
	}, "test-op"); err == nil {
		t.Fatal("correct to verified_none without triple accepted")
	}
	at, by, basis := shgTriple()
	ok, err := CorrectSHG(ctx(t), pool, emp, open.ID, SHGCorrectInput{
		Status: "verified_none", SourceDeclaration: "form verified",
		EffectiveFrom: "2026-08-01",
		VerifiedAt:    &at, VerifiedBy: &by, VerificationBasis: &basis,
	}, "test-op")
	if err != nil {
		t.Fatalf("correct to verified_none: %v", err)
	}
	if ok.Status != "verified_none" || ok.VerifiedBy == nil {
		t.Fatalf("corrected=%+v", ok)
	}
}

func TestSHGFundCoexistence(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	for _, fund := range []string{"CDAC", "MBMF", "SINDA", "ECF"} {
		if _, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
			Fund: fund, Status: "active",
			SourceDeclaration: "form", EffectiveFrom: "2026-08-01",
		}, "test-op"); err != nil {
			t.Fatalf("set %s: %v", fund, err)
		}
	}
	// Same-fund live overlap is still rejected.
	if _, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "active",
		SourceDeclaration: "dup", EffectiveFrom: "2026-08-15",
	}, "test-op"); err == nil {
		t.Fatal("same-fund live overlap accepted")
	}
}

func TestSHGTripleValidation(t *testing.T) {
	pool := testPool(t)
	emp, _ := mkSHGFixture(t, pool)
	at, by, basis := shgTriple()
	// Pending must be unverified.
	if _, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "pending", SourceDeclaration: "form",
		EffectiveFrom: "2026-08-01", VerifiedBy: &by,
	}, "test-op"); err == nil {
		t.Fatal("pending with verified triple accepted")
	}
	// verified_none requires the full triple.
	if _, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "verified_none", SourceDeclaration: "form",
		EffectiveFrom: "2026-08-01", VerifiedAt: &at, VerifiedBy: &by,
	}, "test-op"); err == nil {
		t.Fatal("verified_none with partial triple accepted")
	}
	// Active must be unverified.
	if _, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "active", SourceDeclaration: "form",
		EffectiveFrom: "2026-08-01",
		VerifiedAt:    &at, VerifiedBy: &by, VerificationBasis: &basis,
	}, "test-op"); err == nil {
		t.Fatal("active with verified triple accepted")
	}
	v, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "verified_none", SourceDeclaration: "form",
		EffectiveFrom: "2026-08-01",
		VerifiedAt:    &at, VerifiedBy: &by, VerificationBasis: &basis,
	}, "test-op")
	if err != nil {
		t.Fatalf("verified_none with triple: %v", err)
	}
	if v.VerifiedAt == nil || v.VerifiedBy == nil || v.VerificationBasis == nil {
		t.Fatalf("triple not persisted: %+v", v)
	}
}
