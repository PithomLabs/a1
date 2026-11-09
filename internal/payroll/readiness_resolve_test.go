package payroll

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/hr"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// P3: an ambiguous compensation basis blocks readiness with a
// machine-readable conflict instead of resolving arbitrarily.
func TestAssessAmbiguousCompBasisBlocked(t *testing.T) {
	pool, admin := resolvePools(t)
	emp, period := setup(t, pool)
	e1, terms, sheet := mkCompFixture(t, pool, emp, period)
	no := compNo(t, pool, emp, e1)
	if _, err := hr.AddCompensation(ctx(t), pool, emp, terms, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
		EffectiveFrom: "2026-01-01", Reason: "basis",
	}, "test-op"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Smuggle an overlapping second chain past the trigger as admin.
	actx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.Exec(actx, `ALTER TABLE compensation_line DISABLE TRIGGER compensation_line_containment`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_, smuggleErr := admin.Exec(actx,
		`INSERT INTO compensation_line (employer_id, employee_id, terms_id, kind, code, amount_monthly, effective_from)
		 VALUES ($1, $2, $3, 'allowance', 'SHIFT', 120, '2026-06-01')`, emp, e1, terms)
	if _, err := admin.Exec(actx, `ALTER TABLE compensation_line ENABLE TRIGGER compensation_line_containment`); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if smuggleErr != nil {
		t.Fatalf("smuggle: %v", smuggleErr)
	}
	approveAt(t, pool, sheet, `now()`)
	r, err := Assess(ctx(t), pool, emp, period, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if r.Ready {
		t.Fatalf("ready=%+v want blocked", r)
	}
	found := false
	for _, c := range r.CompConflicts {
		if c.EmployeeNo == no && strings.Contains(c.Reason, "multiple-effective") &&
			strings.Contains(c.Reason, "allowance:SHIFT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts=%+v want the machine-readable basis conflict", r.CompConflicts)
	}
}

func resolvePools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
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
