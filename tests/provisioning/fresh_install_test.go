package provisioning

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/employer"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// Section 9C: the documented fresh-install path end to end on a
// disposable server — provision, migrate as migration_admin, verify
// ownership and privileges, then operate as app. (Employer onboarding
// through CLI and REST is pinned per-adapter in cmd/workforceops;
// the full HR/WFM/attendance/timesheet workflow is the e2e suite.)
func TestFreshInstallChain(t *testing.T) {
	adminDSN := ephemeralCluster(t)
	const db = "fresh_install"
	maint, err := pgx.Connect(ctx(t), adminDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer maint.Close(context.Background())
	if _, err := maint.Exec(ctx(t), `CREATE DATABASE `+db); err != nil {
		t.Fatalf("create %s: %v", db, err)
	}
	runScript(t, db, "pw-fresh-install")

	migDSN := dbutil.RoleDSN(adminDSN, db, "migration_admin")
	if err := coredatabase.Preflight(ctx(t), migDSN); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	applied, err := coredatabase.MigrateUp(ctx(t), migDSN, coremigrations.FS)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(applied) != 11 {
		t.Fatalf("applied %d migrations, want 00001-00011", len(applied))
	}

	c, err := pgx.Connect(ctx(t), dbutil.WithDB(adminDSN, db))
	if err != nil {
		t.Fatalf("connect %s: %v", db, err)
	}
	defer c.Close(context.Background())
	var owner, fnOwner string
	var definer, appExec, appUpd bool
	if err := c.QueryRow(ctx(t),
		`SELECT u.rolname FROM pg_namespace n JOIN pg_roles u ON u.oid = n.nspowner WHERE n.nspname = 'public'`).Scan(&owner); err != nil || owner != "migration_admin" {
		t.Fatalf("schema owner = %q, err = %v", owner, err)
	}
	if err := c.QueryRow(ctx(t),
		`SELECT u.rolname, p.prosecdef FROM pg_proc p JOIN pg_roles u ON u.oid = p.proowner
		 WHERE p.proname = 'maintain_interval_currency'`).Scan(&fnOwner, &definer); err != nil || fnOwner != "migration_admin" || !definer {
		t.Fatalf("maintain_interval_currency = %s/%v, err = %v", fnOwner, definer, err)
	}
	if err := c.QueryRow(ctx(t),
		`SELECT has_function_privilege('app', 'close_employment_terms(uuid,uuid,date,text)', 'EXECUTE')`).Scan(&appExec); err != nil || !appExec {
		t.Fatalf("app EXECUTE on close = %v, err = %v", appExec, err)
	}
	if err := c.QueryRow(ctx(t),
		`SELECT has_table_privilege('app', 'employment_terms', 'UPDATE')`).Scan(&appUpd); err != nil || appUpd {
		t.Fatalf("app UPDATE on terms = %v, err = %v, want revoked", appUpd, err)
	}

	// Operate as app: bootstrap the employer with no prior context.
	appDSN := dbutil.RoleDSN(adminDSN, db, "app")
	pool, err := pgxpool.New(ctx(t), appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	defer pool.Close()
	var who string
	if err := pool.QueryRow(ctx(t), `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		t.Fatalf("pool runs as %q, want app", who)
	}
	got, err := employer.Create(ctx(t), pool, employer.EmployerInput{
		Code: "FRESH", Name: "Fresh Pte Ltd", UEN: "UEN-F1",
		Address: "1 Test Row", Jurisdiction: "SG",
	}, "installer")
	if err != nil {
		t.Fatalf("bootstrap employer: %v", err)
	}
	if got.Existed || got.Employer.Code != "FRESH" {
		t.Fatalf("creation = %+v", got)
	}

	// The fresh install refuses privileged operation by default.
	superPool, err := pgxpool.New(ctx(t), dbutil.WithDB(adminDSN, db))
	if err != nil {
		t.Fatalf("super pool: %v", err)
	}
	defer superPool.Close()
	if err := coredatabase.RefusePrivilegedRuntime(ctx(t), superPool); err == nil {
		t.Fatal("fresh install operates as superuser")
	} else if !strings.Contains(err.Error(), "superuser") {
		t.Fatalf("err = %v, want superuser refusal", err)
	}
	if err := coredatabase.Preflight(ctx(t), appDSN); err == nil {
		t.Fatal("migrate preflight accepted the app role")
	} else if !strings.Contains(err.Error(), "migration_admin") {
		t.Fatalf("err = %v, want migration_admin preflight", err)
	}
}
