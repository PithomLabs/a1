package database

import (
	"context"
	"strings"
	"testing"
	"time"

	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

func TestMigrateUpAndStatus(t *testing.T) {
	dsn := dbutil.DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	applied, err := MigrateUp(ctx, dsn, coremigrations.FS)
	if err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	t.Logf("applied %d migration(s)", len(applied))

	// Second run must be a no-op.
	again, err := MigrateUp(ctx, dsn, coremigrations.FS)
	if err != nil {
		t.Fatalf("MigrateUp (second run): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second MigrateUp applied %d migrations, want 0", len(again))
	}

	statuses, err := MigrationStatus(ctx, dsn, coremigrations.FS)
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("expected at least one recorded migration")
	}

	// The schema skeleton must exist.
	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	for _, schema := range []string{"platform", "investigation", "evaluation_private", "shifthub", "payloop"} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)", schema).Scan(&exists); err != nil {
			t.Fatalf("check schema %s: %v", schema, err)
		}
		if !exists {
			t.Errorf("schema %s missing after migrate up", schema)
		}
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
