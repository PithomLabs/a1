package seed

import (
	"context"
	"os"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// TestDemoResetRecycles exercises the controlled administrative reset:
// seed as the app role, decide records (frozen rows no role may
// row-delete), recycle through the admin DSN, and reseed. It runs on a
// dedicated disposable database, never the shared suite database.
func TestDemoResetRecycles(t *testing.T) {
	admin := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	const name = "a1test_seedreset"
	appDSN := dbutil.RoleDSN(admin, name, "app")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := Reset(ctx, appDSN, admin, name, ""); err != nil {
		t.Fatalf("reset fresh: %v", err)
	}
	pool, err := coredatabase.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer pool.Close()
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		t.Fatalf("seed pool runs as %q, want app role", who)
	}
	if _, err := Demo(ctx, pool); err != nil {
		t.Fatalf("demo: %v", err)
	}
	// Decide records: generate, submit and approve every sheet so the
	// database holds frozen rows before the reset.
	var emp string
	if err := pool.QueryRow(ctx, `SELECT id FROM employer WHERE code = 'MERIDIAN'`).Scan(&emp); err != nil {
		t.Fatalf("resolve employer: %v", err)
	}
	gen, err := timesheet.Generate(ctx, pool, emp, Period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, g := range gen.Generated {
		if _, err := timesheet.Submit(ctx, pool, emp, g.TimesheetID, "test-op"); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if _, err := timesheet.Approve(ctx, pool, emp, g.TimesheetID, "test-op"); err != nil {
			t.Fatalf("approve: %v", err)
		}
	}
	if len(gen.Generated) == 0 {
		t.Fatal("seed produced no sheets to decide")
	}
	pool.Close()
	// Reload refuses without reset; reset recycles and reseeds.
	pool2, err := coredatabase.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	if _, err := Demo(ctx, pool2); err == nil {
		pool2.Close()
		t.Fatal("expected conflict reseeding without --reset")
	}
	pool2.Close()
	if err := Reset(ctx, appDSN, admin, name, ""); err != nil {
		t.Fatalf("reset decided: %v", err)
	}
	pool3, err := coredatabase.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open after reset: %v", err)
	}
	defer pool3.Close()
	if _, err := Demo(ctx, pool3); err != nil {
		t.Fatalf("demo after reset: %v", err)
	}
	var employers, sheets int
	if err := pool3.QueryRow(ctx, `SELECT count(*) FROM employer`).Scan(&employers); err != nil || employers != 1 {
		t.Fatalf("employers=%d err=%v", employers, err)
	}
	if err := pool3.QueryRow(ctx, `SELECT count(*) FROM timesheet`).Scan(&sheets); err != nil || sheets != 0 {
		t.Fatalf("sheets=%d err=%v", sheets, err)
	}
}
