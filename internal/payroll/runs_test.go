package payroll

import (
	"testing"
)

func TestCreateRunAndExclude(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	r, err := CreateRun(ctx(t), pool, emp, period, "", "", "test-op")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.Status != "draft" || r.Kind != "regular" || r.PeriodID != period {
		t.Fatalf("run=%+v", r)
	}
	id, err := AddExclusion(ctx(t), pool, emp, r.ID, "E002", "second job elsewhere", "test-op")
	if err != nil {
		t.Fatalf("exclude: %v", err)
	}
	if id == "" {
		t.Fatal("empty exclusion id")
	}
	got, err := Assess(ctx(t), pool, emp, period, &r.ID)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(got.Excluded) != 1 || got.Excluded[0].EmployeeNo != "E002" {
		t.Fatalf("excluded=%+v", got.Excluded)
	}
}

func TestExclusionDecidedRunRejected(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, _ := mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if v, err := ValidateRun(ctx(t), pool, emp, run, 0, "test-op"); err != nil || !v.Valid {
		t.Fatalf("validate: v=%+v err=%v", v, err)
	}
	if _, err := AddExclusion(ctx(t), pool, emp, run, eID, "too late", "test-op"); err == nil {
		t.Fatal("expected rejection for exclusion on a decided run")
	}
	// Draft runs still take exclusions, including a linked
	// correction of the decided run.
	c, err := CreateRun(ctx(t), pool, emp, period, "correction", run, "test-op")
	if err != nil {
		t.Fatalf("correction: %v", err)
	}
	if _, err := AddExclusion(ctx(t), pool, emp, c.ID, eID, "second look", "test-op"); err != nil {
		t.Fatalf("draft correction exclusion: %v", err)
	}
}

func TestExclusionValidation(t *testing.T) {
	pool := testPool(t)
	emp, period := setup(t, pool)
	r, err := CreateRun(ctx(t), pool, emp, period, "", "", "test-op")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := AddExclusion(ctx(t), pool, emp, r.ID, "E001", "  ", "test-op"); err == nil {
		t.Fatal("expected validation error for empty reason")
	}
	if _, err := AddExclusion(ctx(t), pool, emp, r.ID, "NOBODY", "reason", "test-op"); err == nil {
		t.Fatal("expected not_found for unknown employee")
	}
	if _, err := AddExclusion(ctx(t), pool, emp, "00000000-0000-0000-0000-000000000000", "E001", "reason", "test-op"); err == nil {
		t.Fatal("expected not_found for unknown run")
	}
	if _, err := CreateRun(ctx(t), pool, emp, "NO-PERIOD", "", "", "test-op"); err == nil {
		t.Fatal("expected not_found for unknown period")
	}
	if _, err := CreateRun(ctx(t), pool, emp, period, "bonus", "", "test-op"); err == nil {
		t.Fatal("expected validation error for bad kind")
	}
}
