package hr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/compbasis"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// P3: one deterministic effective compensation basis per
// employee/classification/period. Chain semantics B1–B10 plus the
// §10A matrix; all writes go through the governed service path.

func mkResChain(t *testing.T, pool *pgxpool.Pool) (emp, ee, terms string) {
	t.Helper()
	emp = mkEmployer(t, pool, uniq(t, "E"))
	e, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "R1", LegalName: "x", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tm, err := SetTerms(ctx(t), pool, emp, e.ID, TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	return emp, e.ID, tm.ID
}

func addLine(t *testing.T, pool *pgxpool.Pool, emp, terms, kind, code, amount, from, to string) string {
	t.Helper()
	var toPtr *string
	if to != "" {
		toPtr = &to
	}
	id, err := AddCompensation(ctx(t), pool, emp, terms, CompensationInput{
		Kind: kind, Code: code, AmountMonthly: amount,
		EffectiveFrom: from, EffectiveTo: toPtr, Reason: "basis",
	}, "test-op")
	if err != nil {
		t.Fatalf("add %s: %v", code, err)
	}
	return id
}

func correctLine(t *testing.T, pool *pgxpool.Pool, emp, orig, amount, from, to string) string {
	t.Helper()
	var toPtr *string
	if to != "" {
		toPtr = &to
	}
	id, err := CorrectCompensation(ctx(t), pool, emp, orig, CorrectionInput{
		AmountMonthly: amount, EffectiveFrom: from, EffectiveTo: toPtr, Reason: "fix",
	}, "test-op")
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	return id
}

func resolveOK(t *testing.T, pool *pgxpool.Pool, emp, ee, from, to string) map[string][]compbasis.EffectiveSegment {
	t.Helper()
	got, err := compbasis.ResolveEffectiveCompensation(ctx(t), pool, emp, ee, from, to)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return got
}

func asConflict(t *testing.T, err error) *compbasis.BasisConflict {
	t.Helper()
	var c *compbasis.BasisConflict
	if !errors.As(err, &c) {
		t.Fatalf("err=%v want *BasisConflict", err)
	}
	return c
}

// B1: original without a successor resolves, clipped to the period.
func TestResolveOriginalOnly(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "199.90", "2026-01-01", "")
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	segs := got["allowance:SHIFT"]
	if len(segs) != 1 {
		t.Fatalf("segments=%+v want exactly one", segs)
	}
	s := segs[0]
	if s.RecordID != orig || s.Amount != "199.90" || s.TermsID != terms {
		t.Fatalf("seg=%+v want the exact original", s)
	}
	if s.From != "2026-08-01" || s.To == nil || *s.To != "2026-08-31" {
		t.Fatalf("seg=%+v want period-clipped interval", s)
	}
	if len(s.Lineage) != 1 || s.Lineage[0] != orig {
		t.Fatalf("lineage=%v want [orig]", s.Lineage)
	}
}

// B2: one successor wins; the predecessor is excluded, never rewritten.
func TestResolveSingleSuccessor(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	succ := correctLine(t, pool, emp, orig, "120.00", "2026-01-01", "")
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	segs := got["allowance:SHIFT"]
	if len(segs) != 1 || segs[0].RecordID != succ || segs[0].Amount != "120.00" {
		t.Fatalf("segments=%+v want the successor only", segs)
	}
	if len(segs[0].Lineage) != 2 || segs[0].Lineage[0] != orig || segs[0].Lineage[1] != succ {
		t.Fatalf("lineage=%v want orig→succ", segs[0].Lineage)
	}
	var amt string
	if err := pool.QueryRow(ctx(t),
		`SELECT amount_monthly::text FROM compensation_line WHERE id = $1::uuid`, orig).Scan(&amt); err != nil || amt != "100.00" {
		t.Fatalf("orig amount=%s err=%v want untouched 100.00", amt, err)
	}
}

// B3: chained successors resolve the tip with full lineage.
func TestResolveChainedSuccessors(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	s1 := correctLine(t, pool, emp, orig, "110.00", "2026-01-01", "")
	s2 := correctLine(t, pool, emp, s1, "120.00", "2026-01-01", "")
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	segs := got["allowance:SHIFT"]
	if len(segs) != 1 || segs[0].RecordID != s2 {
		t.Fatalf("segments=%+v want the tip only", segs)
	}
	want := []string{orig, s1, s2}
	for i, id := range want {
		if segs[0].Lineage[i] != id {
			t.Fatalf("lineage=%v want %v", segs[0].Lineage, want)
		}
	}
}

// B4: a competing second successor is rejected at write time; the
// resolver keeps resolving the single winner.
func TestResolveCompetingSuccessorRejected(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	succ := correctLine(t, pool, emp, orig, "120.00", "2026-01-01", "")
	if _, err := CorrectCompensation(ctx(t), pool, emp, orig, CorrectionInput{
		AmountMonthly: "130.00", EffectiveFrom: "2026-01-01", Reason: "competing",
	}, "test-op"); err == nil {
		t.Fatal("expected the competing successor to fail")
	}
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != succ {
		t.Fatalf("segments=%+v want the single winner", segs)
	}
}

// B5/B6: cross-employee and cross-employer corrections are rejected;
// resolution on either side is unaffected.
func TestResolveCrossScopeCorrectionRejected(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	other, err := CreateEmployee(ctx(t), pool, emp, EmployeeInput{
		EmployeeNo: "R2", LegalName: "y", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	var otherTerms string
	if err := pool.QueryRow(ctx(t),
		`SELECT id::text FROM employment_terms WHERE employer_id = $1 AND employee_id = $2`,
		emp, other.ID).Scan(&otherTerms); err != nil {
		// Other employee has no terms; resolve must simply not see orig.
		got := resolveOK(t, pool, emp, other.ID, "2026-08-01", "2026-08-31")
		if len(got) != 0 {
			t.Fatalf("other resolves %+v want nothing", got)
		}
	} else {
		got := resolveOK(t, pool, emp, other.ID, "2026-08-01", "2026-08-31")
		if len(got) != 0 {
			t.Fatalf("other resolves %+v want nothing", got)
		}
	}
	emp2 := mkEmployer(t, pool, uniq(t, "E2"))
	if _, err := CorrectCompensation(ctx(t), pool, emp2, orig, CorrectionInput{
		AmountMonthly: "120.00", EffectiveFrom: "2026-01-01", Reason: "x-scope",
	}, "test-op"); err == nil {
		t.Fatal("expected cross-employer correction to fail")
	}
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != orig {
		t.Fatalf("segments=%+v want the original intact", segs)
	}
}

// B7: correcting a non-tip (the only cycle-closing move available)
// fails; the chain stays a line.
func TestResolveNonTipCorrectionRejected(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	s1 := correctLine(t, pool, emp, orig, "110.00", "2026-01-01", "")
	if _, err := CorrectCompensation(ctx(t), pool, emp, orig, CorrectionInput{
		AmountMonthly: "999.00", EffectiveFrom: "2026-01-01", Reason: "cycle attempt",
	}, "test-op"); err == nil {
		t.Fatal("expected non-tip correction to fail")
	}
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != s1 {
		t.Fatalf("segments=%+v want the tip", segs)
	}
}

// B8: a superseded predecessor outside the queried period never leaks in.
func TestResolveSupersededOutsidePeriod(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "2026-03-31")
	succ := correctLine(t, pool, emp, orig, "100.00", "2026-01-01", "2026-06-30")
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	if segs, ok := got["allowance:SHIFT"]; ok && len(segs) != 0 {
		t.Fatalf("segments=%+v want none in August", segs)
	}
	got = resolveOK(t, pool, emp, ee, "2026-02-01", "2026-02-28")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != succ {
		t.Fatalf("segments=%+v want the successor in February", segs)
	}
}

// B9: a future-only correction does not affect the current period.
func TestResolveFutureCorrection(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	succ := correctLine(t, pool, emp, orig, "200.00", "2027-01-01", "")
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != orig {
		t.Fatalf("segments=%+v want the original for August", segs)
	}
	got = resolveOK(t, pool, emp, ee, "2027-02-01", "2027-02-28")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != succ {
		t.Fatalf("segments=%+v want the successor for next February", segs)
	}
}

// B10: historical resolution for an earlier period.
func TestResolveHistoricalPeriod(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	_ = correctLine(t, pool, emp, orig, "200.00", "2026-06-01", "")
	got := resolveOK(t, pool, emp, ee, "2026-02-01", "2026-02-28")
	if segs := got["allowance:SHIFT"]; len(segs) != 1 || segs[0].RecordID != orig {
		t.Fatalf("segments=%+v want the original for February", segs)
	}
}

// Multiple classifications resolve independently.
func TestResolveMultipleClassifications(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	shift := addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	meal := addLine(t, pool, emp, terms, "allowance", "MEAL", "50.00", "2026-01-01", "")
	got := resolveOK(t, pool, emp, ee, "2026-08-01", "2026-08-31")
	if len(got) != 2 {
		t.Fatalf("classifications=%v want two", got)
	}
	if got["allowance:SHIFT"][0].RecordID != shift || got["allowance:MEAL"][0].RecordID != meal {
		t.Fatalf("got=%+v want both lines", got)
	}
}

// Amendment C: a narrowing correction surfaces as ordered segments,
// never a sum; exact amounts preserved.
func TestResolveNarrowingSegments(t *testing.T) {
	pool := testPool(t)
	emp, ee, terms := mkResChain(t, pool)
	orig := addLine(t, pool, emp, terms, "allowance", "SHIFT", "199.90", "2026-01-01", "")
	succ := correctLine(t, pool, emp, orig, "210.05", "2026-01-01", "2026-06-30")
	got := resolveOK(t, pool, emp, ee, "2026-01-01", "2026-12-31")
	segs := got["allowance:SHIFT"]
	if len(segs) != 2 {
		t.Fatalf("segments=%+v want successor span plus freed tail", segs)
	}
	if segs[0].RecordID != succ || segs[0].Amount != "210.05" {
		t.Fatalf("first=%+v want the successor span", segs[0])
	}
	if segs[1].RecordID != orig || segs[1].Amount != "199.90" {
		t.Fatalf("second=%+v want the surviving predecessor tail", segs[1])
	}
	if segs[1].From != "2026-07-01" {
		t.Fatalf("tail=%+v want it starting 2026-07-01", segs[1])
	}
}

// Adversarial end-to-end: overlapping live rows smuggled past the
// trigger (admin-disabled) must conflict, not resolve.
func TestResolveAdversarialOverlapConflicts(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, ee, terms := mkResChain(t, pool)
	_ = addLine(t, pool, emp, terms, "allowance", "SHIFT", "100.00", "2026-01-01", "")
	actx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.Exec(actx, `ALTER TABLE compensation_line DISABLE TRIGGER compensation_line_containment`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_, err := admin.Exec(actx,
		`INSERT INTO compensation_line (employer_id, employee_id, terms_id, kind, code, amount_monthly, effective_from)
		 VALUES ($1, $2, $3, 'allowance', 'SHIFT', 120, '2026-06-01')`, emp, ee, terms)
	if _, rerr := admin.Exec(actx, `ALTER TABLE compensation_line ENABLE TRIGGER compensation_line_containment`); rerr != nil {
		t.Fatalf("re-enable: %v", rerr)
	}
	if err != nil {
		t.Fatalf("smuggle: %v", err)
	}
	_, rerr := compbasis.ResolveEffectiveCompensation(ctx(t), pool, emp, ee, "2026-08-01", "2026-08-31")
	c := asConflict(t, rerr)
	if c.Reason != "multiple-effective" || c.Classification != "allowance:SHIFT" {
		t.Fatalf("conflict=%+v want the machine-readable overlap", c)
	}
	if !strings.Contains(c.Error(), "allowance:SHIFT") {
		t.Fatalf("err=%v want the classification named", c)
	}
}

func testPoolAndAdmin(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	migrate := func(ctx context.Context, migDSN string) error {
		_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	}
	setup := dbutil.Setup(t, migrate)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := coredatabase.Open(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	t.Cleanup(pool.Close)
	admin, err := coredatabase.Open(ctx, setup.AdminDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(admin.Close)
	return pool, admin
}
