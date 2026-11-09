package compbasis

import (
	"errors"
	"testing"
)

func strptr(s string) *string { return &s }

func asConflict(t *testing.T, err error) *BasisConflict {
	t.Helper()
	var c *BasisConflict
	if !errors.As(err, &c) {
		t.Fatalf("err=%v want *BasisConflict", err)
	}
	return c
}

// Pure carving: competing successors conflict, never resolve.
func TestCarveCompetingSuccessorsConflict(t *testing.T) {
	rows := []compRow{
		{id: "o", kind: "allowance", code: "SHIFT", amount: "100.00", from: "2026-01-01", terms: "t"},
		{id: "s1", kind: "allowance", code: "SHIFT", amount: "110.00", from: "2026-01-01", terms: "t", supersedes: strptr("o")},
		{id: "s2", kind: "allowance", code: "SHIFT", amount: "120.00", from: "2026-01-01", terms: "t", supersedes: strptr("o")},
	}
	_, err := carveSegments(rows, "2026-08-01", "2026-08-31")
	c := asConflict(t, err)
	if c.Reason != "competing-successors" {
		t.Fatalf("reason=%s want competing-successors", c.Reason)
	}
}

// Pure carving: cycles conflict.
func TestCarveCycleConflict(t *testing.T) {
	rows := []compRow{
		{id: "a", kind: "allowance", code: "SHIFT", amount: "100.00", from: "2026-01-01", terms: "t", supersedes: strptr("b")},
		{id: "b", kind: "allowance", code: "SHIFT", amount: "110.00", from: "2026-01-01", terms: "t", supersedes: strptr("a")},
	}
	_, err := carveSegments(rows, "2026-08-01", "2026-08-31")
	c := asConflict(t, err)
	if c.Reason != "cycle" {
		t.Fatalf("reason=%s want cycle", c.Reason)
	}
}

// Pure carving: dangling supersedes conflicts.
func TestCarveDanglingConflict(t *testing.T) {
	rows := []compRow{
		{id: "s", kind: "allowance", code: "SHIFT", amount: "110.00", from: "2026-01-01", terms: "t", supersedes: strptr("ghost")},
	}
	_, err := carveSegments(rows, "2026-08-01", "2026-08-31")
	c := asConflict(t, err)
	if c.Reason != "dangling-supersedes" {
		t.Fatalf("reason=%s want dangling-supersedes", c.Reason)
	}
}

// Pure carving: overlapping separate chains conflict.
func TestCarveOverlapConflict(t *testing.T) {
	rows := []compRow{
		{id: "o1", kind: "allowance", code: "SHIFT", amount: "100.00", from: "2026-01-01", terms: "t"},
		{id: "o2", kind: "allowance", code: "SHIFT", amount: "120.00", from: "2026-06-01", terms: "t"},
	}
	_, err := carveSegments(rows, "2026-08-01", "2026-08-31")
	c := asConflict(t, err)
	if c.Reason != "multiple-effective" {
		t.Fatalf("reason=%s want multiple-effective", c.Reason)
	}
}
