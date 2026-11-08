package main

import (
	"context"
	"strings"
	"testing"

	"github.com/PithomLabs/a1/tests/dbutil"
)

// D2 at the CLI boundary: normal commands refuse privileged runtime
// connections, and migration commands preflight for migration_admin.

func TestCLIRolesEnforced(t *testing.T) {
	superDSN := dbutil.DSN(t)
	setup := dbutil.Setup(t, func(ctx context.Context, migDSN string) error { return nil })
	_, err, stderr := runCLIErr(t, superDSN, "employer", "show")
	if err == nil {
		t.Fatal("CLI command accepted a superuser runtime connection")
	}
	if !strings.Contains(stderr, "superuser") {
		t.Fatalf("stderr = %s, want superuser refusal", stderr)
	}
	_, err, stderr = runCLIErr(t, setup.MigDSN, "employer", "show")
	if err == nil {
		t.Fatal("CLI command accepted a migration_admin runtime connection")
	}
	if !strings.Contains(stderr, "migration_admin") {
		t.Fatalf("stderr = %s, want migration_admin refusal", stderr)
	}
	_, err, stderr = runCLIErr(t, setup.AppDSN, "migrate", "status")
	if err == nil {
		t.Fatal("migrate status accepted the app role")
	}
	if !strings.Contains(stderr, "migration_admin") {
		t.Fatalf("stderr = %s, want migration_admin preflight", stderr)
	}
}
