package wfm

import (
	"testing"
)

func TestRotationPatternAndAssignment(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	no := uniq(t, "EMP")
	mkEmployee(t, pool, emp, no)
	tpl, err := CreateTemplate(ctx(t), pool, emp, TemplateInput{
		Code: "DAY", Name: "Day shift", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []SegmentInput{{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true}},
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	p, err := CreateRotation(ctx(t), pool, emp, CreateRotationInput{
		Code: "R2", Name: "Two-day", CycleDays: 2,
		Rows: []RotationRow{
			{Offset: 0, Weekday: 1, Template: tpl.Code},
			{Offset: 1, Weekday: 2, Template: tpl.Code, IsOff: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateRotation: %v", err)
	}
	if p.CycleDays != 2 {
		t.Fatalf("cycle days = %d, want 2", p.CycleDays)
	}
	a, err := CreateRotationAssign(ctx(t), pool, emp, CreateRotationAssignInput{
		Pattern: p.Code, Employee: no, EffectiveOn: "2026-08-03",
	})
	if err != nil {
		t.Fatalf("CreateRotationAssign: %v", err)
	}
	if a.PatternCode != "R2" || a.EmployeeNo == nil || *a.EmployeeNo != no {
		t.Fatalf("assignment echoes %+v", a)
	}
	list, err := ListRotationAssigns(ctx(t), pool, emp)
	if err != nil {
		t.Fatalf("ListRotationAssigns: %v", err)
	}
	if len(list) != 1 || list[0].Effective != "2026-08-03" {
		t.Fatalf("assignments = %+v", list)
	}
	pats, err := ListRotations(ctx(t), pool, emp)
	if err != nil {
		t.Fatalf("ListRotations: %v", err)
	}
	if len(pats) != 1 || pats[0].Code != "R2" {
		t.Fatalf("patterns = %+v", pats)
	}
}

func TestRotationValidation(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	no := uniq(t, "EMP")
	mkEmployee(t, pool, emp, no)
	if _, err := CreateRotation(ctx(t), pool, emp, CreateRotationInput{Code: "X"}); err == nil {
		t.Fatal("empty pattern accepted")
	}
	if _, err := CreateRotation(ctx(t), pool, emp, CreateRotationInput{
		Code: "X", Name: "X", CycleDays: 1,
		Rows: []RotationRow{{Offset: 5, Weekday: 1, Template: "T"}},
	}); err == nil {
		t.Fatal("out-of-range offset accepted")
	}
	p, err := CreateRotation(ctx(t), pool, emp, CreateRotationInput{
		Code: "R1", Name: "One", CycleDays: 1,
		Rows: []RotationRow{{Offset: 0, Weekday: 1, IsOff: true}},
	})
	if err != nil {
		t.Fatalf("off-row pattern: %v", err)
	}
	if _, err := CreateRotationAssign(ctx(t), pool, emp, CreateRotationAssignInput{
		Pattern: p.Code, EffectiveOn: "2026-08-03",
	}); err == nil {
		t.Fatal("assignment without employee/team accepted")
	}
	if _, err := CreateRotationAssign(ctx(t), pool, emp, CreateRotationAssignInput{
		Pattern: "NOPE", Employee: no, EffectiveOn: "2026-08-03",
	}); err == nil {
		t.Fatal("assignment to missing pattern accepted")
	}
}
