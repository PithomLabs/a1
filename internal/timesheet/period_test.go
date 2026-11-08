package timesheet

import (
	"strings"
	"testing"
)

func TestCreatePeriod(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "P")
	p, err := CreatePeriod(ctx(t), pool, emp, PeriodInput{
		Code: "PP-0926", StartsOn: "2026-09-01", EndsOn: "2026-09-30",
		PayDate: "2026-10-07", SalaryDeadline: "2026-10-07", OTDeadline: "2026-10-14",
	}, "test-op")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Status != "open" || p.Code != "PP-0926" {
		t.Fatalf("period=%+v", p)
	}
	periods, err := ListPeriods(ctx(t), pool, emp)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(periods) != 2 {
		t.Fatalf("periods=%d want 2", len(periods))
	}
	// Duplicate code conflicts.
	if _, err := CreatePeriod(ctx(t), pool, emp, PeriodInput{
		Code: "PP-0926", StartsOn: "2026-10-01", EndsOn: "2026-10-31",
		PayDate: "2026-11-07", SalaryDeadline: "2026-11-07", OTDeadline: "2026-11-14",
	}, "test-op"); err == nil {
		t.Fatal("expected conflict for duplicate code")
	}
	// Overlapping range conflicts even under a fresh code.
	if _, err := CreatePeriod(ctx(t), pool, emp, PeriodInput{
		Code: "PP-OVER", StartsOn: "2026-09-15", EndsOn: "2026-10-15",
		PayDate: "2026-10-22", SalaryDeadline: "2026-10-22", OTDeadline: "2026-10-29",
	}, "test-op"); err == nil {
		t.Fatal("expected conflict for overlapping range")
	} else if !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("overlap error %v is not a machine-readable conflict", err)
	}
}

func TestCreatePeriodValidation(t *testing.T) {
	pool := testPool(t)
	emp, _ := setup(t, pool, "P")
	base := PeriodInput{
		Code: "PP-X", StartsOn: "2026-09-01", EndsOn: "2026-09-30",
		PayDate: "2026-10-07", SalaryDeadline: "2026-10-07", OTDeadline: "2026-10-14",
	}
	for name, mutate := range map[string]func(*PeriodInput){
		"no code":     func(i *PeriodInput) { i.Code = "" },
		"bad start":   func(i *PeriodInput) { i.StartsOn = "09/01/2026" },
		"ends before": func(i *PeriodInput) { i.EndsOn = "2026-08-31" },
	} {
		in := base
		mutate(&in)
		if _, err := CreatePeriod(ctx(t), pool, emp, in, "test-op"); err == nil {
			t.Fatalf("%s: expected validation error", name)
		} else if !strings.Contains(err.Error(), "validation") {
			t.Fatalf("%s: error %v is not machine-readable validation", name, err)
		}
	}
	if _, err := CreatePeriod(ctx(t), pool, emp, base, "  "); err == nil {
		t.Fatal("expected validation error for missing operator")
	}
}
