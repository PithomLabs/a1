package hr

import (
	"strings"
	"testing"
)

func TestSetAndListSHG(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "S"))
	if _, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "E001", LegalName: "SHG Person", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err != nil {
		t.Fatalf("employee: %v", err)
	}
	amt := "fixed"
	s, err := SetSHG(ctx(t), pool, emp, "E001", SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "employee form 2026-07", EffectiveFrom: "2026-08-01",
	}, "test-op")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if s.Fund != "CDAC" || s.EffectiveTo != nil {
		t.Fatalf("shg=%+v", s)
	}
	list, err := ListSHG(ctx(t), pool, emp, "E001")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list=%+v", list)
	}
}

func TestSHGOverlapRejected(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "S"))
	if _, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "E001", LegalName: "SHG Person", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err != nil {
		t.Fatalf("employee: %v", err)
	}
	in := SHGInput{Fund: "SINDA", Status: "active",
		SourceDeclaration: "declared", EffectiveFrom: "2026-08-01"}
	if _, err := SetSHG(ctx(t), pool, emp, "E001", in, "test-op"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := SetSHG(ctx(t), pool, emp, "E001", in, "test-op"); err == nil {
		t.Fatal("expected conflict for overlapping same-fund instruction")
	}
	// A different fund coexists with the first.
	in.Fund = "CDAC"
	if _, err := SetSHG(ctx(t), pool, emp, "E001", in, "test-op"); err != nil {
		t.Fatalf("other fund: %v", err)
	}
}

func TestSHGValidation(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "S"))
	if _, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "E001", LegalName: "SHG Person", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err != nil {
		t.Fatalf("employee: %v", err)
	}
	base := SHGInput{Fund: "ECF", Status: "active",
		SourceDeclaration: "declared", EffectiveFrom: "2026-08-01"}
	for name, mutate := range map[string]func(*SHGInput){
		"bad fund":   func(i *SHGInput) { i.Fund = "NOPE" },
		"bad status": func(i *SHGInput) { i.Status = "bogus" },
		"no source":  func(i *SHGInput) { i.SourceDeclaration = "  " },
		"bad from":   func(i *SHGInput) { i.EffectiveFrom = "08/01/2026" },
		"to before":  func(i *SHGInput) { to := "2026-07-01"; i.EffectiveTo = &to },
	} {
		in := base
		mutate(&in)
		if _, err := SetSHG(ctx(t), pool, emp, "E001", in, "test-op"); err == nil {
			t.Fatalf("%s: expected validation error", name)
		} else if !strings.Contains(err.Error(), "validation") {
			t.Fatalf("%s: error %v is not machine-readable validation", name, err)
		}
	}
	if _, err := SetSHG(ctx(t), pool, emp, "NOBODY", base, "test-op"); err == nil {
		t.Fatal("expected not_found for unknown employee")
	}
	if _, err := SetSHG(ctx(t), pool, emp, "E001", base, "  "); err == nil {
		t.Fatal("expected validation error for missing operator")
	}
}
