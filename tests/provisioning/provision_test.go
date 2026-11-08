package provisioning

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PithomLabs/a1/tests/dbutil"
)

// D1: the actual role-provisioning script runs against a disposable
// PostgreSQL 16 server — an ephemeral cluster, so role creation
// executes for real and no shared test state is touched. Grants and
// revokes are verified functionally, and the re-run is idempotent
// without silently resetting passwords.

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return c
}

func findPostgresBinary(t *testing.T, name string) string {
	t.Helper()
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, pattern := range []string{
		"/usr/lib/postgresql/*/bin/" + name,
		"/tmp/pg*/usr/lib/postgresql/*/bin/" + name,
		"/tmp/pg*/bin/" + name,
	} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return matches[0]
		}
	}
	t.Skipf("%s not available: cannot start an ephemeral cluster", name)
	return ""
}

// ephemeralCluster starts a throwaway PostgreSQL on port 5435 and
// returns a superuser maintenance DSN. Trust auth is fine: the script
// under test sets role passwords, and authentication itself is E5's
// subject, not D1's.
func ephemeralCluster(t *testing.T) string {
	t.Helper()
	initdb := findPostgresBinary(t, "initdb")
	pgctl := findPostgresBinary(t, "pg_ctl")
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	initOut, err := exec.Command(initdb, "-D", data, "-U", "postgres", "--auth=trust").CombinedOutput()
	if err != nil {
		t.Fatalf("initdb: %v\n%s", err, initOut)
	}
	const port = "5435"
	startOut, err := exec.Command(pgctl, "-D", data, "-l", filepath.Join(dir, "log"),
		"-o", "-p "+port+" -k ''", "start").CombinedOutput()
	if err != nil {
		t.Fatalf("pg_ctl start: %v\n%s", err, startOut)
	}
	t.Cleanup(func() { _ = exec.Command(pgctl, "-D", data, "stop").Run() })
	return fmt.Sprintf("postgresql://postgres@127.0.0.1:%s/postgres?sslmode=disable", port)
}

func runScript(t *testing.T, db, password string) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "db", "init-roles.sh"))
	if err != nil {
		t.Fatalf("script path: %v", err)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(),
		"POSTGRES_DB="+db,
		"POSTGRES_USER=postgres",
		"A1_DEV_ROLE_PASSWORD="+password,
		"PGHOST=127.0.0.1",
		"PGPORT=5435",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init-roles.sh: %v\n%s", err, out)
	}
	return string(out)
}

func TestInitRolesScript(t *testing.T) {
	adminDSN := ephemeralCluster(t)
	const db = "provision_test"
	maint, err := pgx.Connect(ctx(t), adminDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer maint.Close(context.Background())
	if _, err := maint.Exec(ctx(t), `CREATE DATABASE `+db); err != nil {
		t.Fatalf("create %s: %v", db, err)
	}

	out := runScript(t, db, "pw-first-run")
	if strings.Contains(out, "keeping its password") {
		t.Fatalf("fresh run skipped role creation:\n%s", out)
	}

	c, err := pgx.Connect(ctx(t), dbutil.WithDB(adminDSN, db))
	if err != nil {
		t.Fatalf("connect %s: %v", db, err)
	}
	defer c.Close(context.Background())
	var login, connect, temp, usage bool
	if err := c.QueryRow(ctx(t),
		`SELECT count(*) = 2 FROM pg_roles WHERE rolname IN ('migration_admin', 'app') AND rolcanlogin`).Scan(&login); err != nil || !login {
		t.Fatalf("roles login = %v, err = %v", login, err)
	}
	for _, r := range []string{"migration_admin", "app"} {
		if err := c.QueryRow(ctx(t),
			`SELECT has_database_privilege($1, current_database(), 'CONNECT')`, r).Scan(&connect); err != nil || !connect {
			t.Fatalf("%s CONNECT = %v, err = %v", r, connect, err)
		}
		if err := c.QueryRow(ctx(t),
			`SELECT has_database_privilege($1, current_database(), 'TEMP')`, r).Scan(&temp); err != nil || temp {
			t.Fatalf("%s TEMP = %v, err = %v, want revoked", r, temp, err)
		}
	}
	var owner string
	if err := c.QueryRow(ctx(t),
		`SELECT u.rolname FROM pg_namespace n JOIN pg_roles u ON u.oid = n.nspowner WHERE n.nspname = 'public'`).Scan(&owner); err != nil || owner != "migration_admin" {
		t.Fatalf("schema owner = %q, err = %v", owner, err)
	}
	if err := c.QueryRow(ctx(t),
		`SELECT has_schema_privilege('app', 'public', 'USAGE')`).Scan(&usage); err != nil || !usage {
		t.Fatalf("app USAGE = %v, err = %v", usage, err)
	}
	var pubCreate int
	if err := c.QueryRow(ctx(t),
		`SELECT count(*) FROM aclexplode((SELECT nspacl FROM pg_namespace WHERE nspname = 'public'))
		 AS a(grantor, grantee, priv, grantable) WHERE a.grantee = 0 AND a.priv = 'CREATE'`).Scan(&pubCreate); err != nil || pubCreate != 0 {
		t.Fatalf("PUBLIC CREATE entries = %d, err = %v", pubCreate, err)
	}
	var exts bool
	if err := c.QueryRow(ctx(t),
		`SELECT count(*) = 2 FROM pg_extension WHERE extname IN ('pgcrypto', 'btree_gist')`).Scan(&exts); err != nil || !exts {
		t.Fatalf("extensions = %v, err = %v", exts, err)
	}

	// Functional default privileges: a migration_admin table is app
	// read/write, and app cannot DDL.
	mig, err := pgx.Connect(ctx(t), dbutil.RoleDSN(adminDSN, db, "migration_admin"))
	if err != nil {
		t.Fatalf("connect migration_admin: %v", err)
	}
	defer mig.Close(context.Background())
	if _, err := mig.Exec(ctx(t), `CREATE TABLE canary (id int PRIMARY KEY, v text)`); err != nil {
		t.Fatalf("create canary: %v", err)
	}
	app, err := pgx.Connect(ctx(t), dbutil.RoleDSN(adminDSN, db, "app"))
	if err != nil {
		t.Fatalf("connect app: %v", err)
	}
	defer app.Close(context.Background())
	if _, err := app.Exec(ctx(t), `INSERT INTO canary VALUES (1, 'a')`); err != nil {
		t.Fatalf("app insert: %v", err)
	}
	var v string
	if err := app.QueryRow(ctx(t), `SELECT v FROM canary WHERE id = 1`).Scan(&v); err != nil || v != "a" {
		t.Fatalf("app select = %q, err = %v", v, err)
	}
	if _, err := app.Exec(ctx(t), `UPDATE canary SET v = 'b' WHERE id = 1`); err != nil {
		t.Fatalf("app update: %v", err)
	}
	if _, err := app.Exec(ctx(t), `DELETE FROM canary WHERE id = 1`); err != nil {
		t.Fatalf("app delete: %v", err)
	}
	if _, err := app.Exec(ctx(t), `CREATE TABLE app_ddl (id int)`); err == nil {
		t.Fatal("app CREATE TABLE succeeded, want denied")
	}

	passwords := func() map[string]string {
		rows, err := c.Query(ctx(t),
			`SELECT rolname, rolpassword FROM pg_authid WHERE rolname IN ('migration_admin', 'app')`)
		if err != nil {
			t.Fatalf("role passwords: %v", err)
		}
		defer rows.Close()
		got := map[string]string{}
		for rows.Next() {
			var name string
			var pw *string
			if err := rows.Scan(&name, &pw); err != nil {
				t.Fatalf("scan role: %v", err)
			}
			if pw == nil || *pw == "" {
				t.Fatalf("role %s has no password set", name)
			}
			got[name] = *pw
		}
		return got
	}
	before := passwords()

	// Re-run with a different password: idempotent, existing checks
	// reported, passwords untouched.
	out = runScript(t, db, "pw-second-run")
	if !strings.Contains(out, "keeping its password") {
		t.Fatalf("re-run did not report existing roles:\n%s", out)
	}
	after := passwords()
	for _, r := range []string{"migration_admin", "app"} {
		if after[r] != before[r] {
			t.Fatal("role password changed on re-run for ", r)
		}
	}
}
