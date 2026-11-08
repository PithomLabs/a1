package database

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

func TestMigrateUpAndStatus(t *testing.T) {
	var applied []AppliedMigration
	setup := dbutil.Setup(t, func(ctx context.Context, migDSN string) error {
		var err error
		applied, err = MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Logf("applied %d migration(s)", len(applied))

	// Second run must be a no-op (under the setup lock: shared DB).
	var again []AppliedMigration
	if err := dbutil.WithSetupLock(ctx, setup.AdminDSN, setup.Name, func() error {
		var err error
		again, err = MigrateUp(ctx, setup.MigDSN, coremigrations.FS)
		return err
	}); err != nil {
		t.Fatalf("MigrateUp (second run): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second MigrateUp applied %d migrations, want 0", len(again))
	}

	var statuses []ReportedMigration
	if err := dbutil.WithSetupLock(ctx, setup.AdminDSN, setup.Name, func() error {
		var err error
		statuses, err = MigrationStatus(ctx, setup.MigDSN, coremigrations.FS)
		return err
	}); err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if len(statuses) < 9 {
		t.Fatalf("expected at least 9 recorded migrations, got %d", len(statuses))
	}

	// Migrations must refuse to run as any role but migration_admin.
	// Proven on a fresh database: the shared DB may already be migrated,
	// which would make any-role MigrateUp a no-op success.
	neg, err := dbutil.SetupDB(ctx, setup.AdminDSN,
		fmt.Sprintf("m11_fresh_neg_%d", time.Now().UnixNano()), nil)
	if err != nil {
		t.Fatalf("setup fresh database: %v", err)
	}
	if _, err := MigrateUp(ctx, neg.AdminDSN, coremigrations.FS); err == nil {
		t.Fatal("MigrateUp as superuser succeeded, want loud role refusal")
	} else if !strings.Contains(err.Error(), "migration_admin") {
		t.Fatalf("MigrateUp as superuser failed without naming the role: %v", err)
	}
	pos, err := dbutil.SetupDB(ctx, setup.AdminDSN,
		fmt.Sprintf("m11_fresh_pos_%d", time.Now().UnixNano()), nil)
	if err != nil {
		t.Fatalf("setup fresh database: %v", err)
	}
	var appliedFresh []AppliedMigration
	if err := dbutil.WithSetupLock(ctx, pos.AdminDSN, pos.Name, func() error {
		var err error
		appliedFresh, err = MigrateUp(ctx, pos.MigDSN, coremigrations.FS)
		return err
	}); err != nil {
		t.Fatalf("MigrateUp as migration_admin on fresh DB: %v", err)
	}
	if len(appliedFresh) != len(statuses) {
		t.Fatalf("fresh MigrateUp applied %d, want %d", len(appliedFresh), len(statuses))
	}

	// The single-schema pivot baseline must exist: no legacy schemas,
	// core tables present, owned by migration_admin.
	pool, err := Open(ctx, setup.AdminDSN)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	for _, schema := range []string{"platform", "investigation", "evaluation_private", "shifthub", "payloop"} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)", schema).Scan(&exists); err != nil {
			t.Fatalf("check schema %s: %v", schema, err)
		}
		if exists {
			t.Errorf("legacy schema %s present after pivot baseline", schema)
		}
	}
	for _, table := range []string{"employer", "employee", "roster_assignment", "attendance_interval", "timesheet", "payroll_run", "payroll_run_exclusion", "rule_pack_activation", "audit_event"} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)", table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s missing after migrate up", table)
		}
	}

	// Post-migration privilege verification: migration_admin owns the
	// schema objects and app holds insert-only grants on history tables.
	var badOwners int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_tables
		 WHERE schemaname = 'public' AND tablename NOT LIKE 'goose\_%'
		   AND tableowner <> 'migration_admin'`).Scan(&badOwners); err != nil {
		t.Fatalf("check table owners: %v", err)
	}
	if badOwners != 0 {
		t.Errorf("%d tables not owned by migration_admin", badOwners)
	}
	var leaked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.role_table_grants
		 WHERE grantee = 'app' AND privilege_type IN ('UPDATE', 'DELETE')
		   AND table_name IN ('clock_event', 'attendance_interval',
		     'attendance_correction', 'roster_revision', 'ket_record',
		     'payslip', 'audit_event')`).Scan(&leaked); err != nil {
		t.Fatalf("check insert-only revokes: %v", err)
	}
	if leaked != 0 {
		t.Errorf("app holds %d UPDATE/DELETE grants on insert-only tables", leaked)
	}
}

func TestMigrateUpRejectsEmptyDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := MigrateUp(ctx, "", coremigrations.FS); err == nil {
		t.Fatal("expected error for empty DSN")
	} else if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}
