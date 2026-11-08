package seed

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// E5: reset target safety. The normal successful disposable reset is
// TestDemoResetRecycles; these cases pin every refusal path plus a
// full reset cycle against a password-authenticated server.

func resetCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestResetRefusesProtectedDatabase(t *testing.T) {
	admin := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	appDSN := dbutil.WithDB(admin, "postgres")
	err := Reset(resetCtx(t), appDSN, admin, "postgres", "")
	if err == nil {
		t.Fatal("reset of postgres accepted")
	}
	if !strings.Contains(err.Error(), "protected") {
		t.Fatalf("err = %v, want protected refusal", err)
	}
}

func TestResetRefusesUnknownDatabase(t *testing.T) {
	admin := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	ctx := resetCtx(t)
	for _, name := range []string{"myapp_prod", "a1"} {
		appDSN := dbutil.WithDB(admin, name)
		err := Reset(ctx, appDSN, admin, name, "")
		if err == nil {
			t.Fatalf("reset of %q accepted", name)
		}
		if !strings.Contains(err.Error(), "recognizably disposable") {
			t.Fatalf("err = %v, want disposable-policy refusal", err)
		}
	}
}

func TestResetRequiresTargetConfirmation(t *testing.T) {
	admin := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	appDSN := dbutil.RoleDSN(admin, "a1test_seedreset", "app")
	err := Reset(resetCtx(t), appDSN, admin, "a1test_seedreso", "")
	if err == nil {
		t.Fatal("reset with mismatched --target accepted")
	}
	if !strings.Contains(err.Error(), "--target") {
		t.Fatalf("err = %v, want target-confirmation refusal", err)
	}
}

func TestResetWrongServer(t *testing.T) {
	admin := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	host := u.Hostname()
	port := u.Port()
	otherPort := "5434"
	if port == "5434" {
		otherPort = "5435"
	}
	u.Host = host + ":" + otherPort
	appDSN := dbutil.RoleDSN(admin, "demo_wrongserver", "app")
	err = Reset(resetCtx(t), appDSN, u.String(), "demo_wrongserver", "")
	if err == nil {
		t.Fatal("reset across servers accepted")
	}
	if !strings.Contains(err.Error(), "same server") {
		t.Fatalf("err = %v, want same-server refusal", err)
	}
}

func TestResetMissingOrInvalidAdminDSN(t *testing.T) {
	admin := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	ctx := resetCtx(t)
	appDSN := dbutil.RoleDSN(admin, "demo_noadmin", "app")
	if err := Reset(ctx, appDSN, "", "demo_noadmin", ""); err == nil {
		t.Fatal("reset without admin DSN accepted")
	} else if !strings.Contains(err.Error(), "admin-dsn") {
		t.Fatalf("err = %v, want missing-admin refusal", err)
	}
	if err := Reset(ctx, appDSN, "://bad%%%", "demo_noadmin", ""); err == nil {
		t.Fatal("reset with unparseable admin DSN accepted")
	} else if !strings.Contains(err.Error(), "same server") {
		t.Fatalf("err = %v, want same-server refusal", err)
	}
}

// findPostgresBinary locates a server binary for the ephemeral
// password-authenticated cluster: PATH first, then the common
// installation layouts.
func findPostgresBinary(t *testing.T, name string) string {
	t.Helper()
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	matches, _ := filepath.Glob("/usr/lib/postgresql/*/bin/" + name)
	if len(matches) > 0 {
		return matches[0]
	}
	matches, _ = filepath.Glob("/tmp/pg*/usr/lib/postgresql/*/bin/" + name)
	if len(matches) > 0 {
		return matches[0]
	}
	matches, _ = filepath.Glob("/tmp/pg*/bin/" + name)
	if len(matches) > 0 {
		return matches[0]
	}
	t.Skipf("%s not available: cannot start an ephemeral cluster", name)
	return ""
}

// A full reset cycle works against a password-authenticated server
// when credentials are supplied, and wrong credentials fail: trust
// is never assumed.
func TestResetPasswordAuthenticatedServer(t *testing.T) {
	initdb := findPostgresBinary(t, "initdb")
	pgctl := findPostgresBinary(t, "pg_ctl")
	const superPW, rolePW = "super-pw-e5", "role-pw-e5"
	dir := t.TempDir()
	pwFile := filepath.Join(dir, "superpw")
	if err := os.WriteFile(pwFile, []byte(superPW), 0o600); err != nil {
		t.Fatalf("pwfile: %v", err)
	}
	data := filepath.Join(dir, "data")
	initOut, err := exec.Command(initdb, "-D", data, "-U", "postgres",
		"--auth=scram-sha-256", "--pwfile="+pwFile).CombinedOutput()
	if err != nil {
		t.Fatalf("initdb: %v\n%s", err, initOut)
	}
	const port = "5434"
	startOut, err := exec.Command(pgctl, "-D", data, "-l", filepath.Join(dir, "log"),
		"-o", "-p "+port+" -k ''", "start").CombinedOutput()
	if err != nil {
		t.Fatalf("pg_ctl start: %v\n%s", err, startOut)
	}
	defer func() {
		_ = exec.Command(pgctl, "-D", data, "stop").Run()
	}()
	adminDSN := fmt.Sprintf("postgresql://postgres:%s@127.0.0.1:%s/postgres?sslmode=disable", superPW, port)
	appDSN := fmt.Sprintf("postgresql://app:%s@127.0.0.1:%s/demo_pwreset?sslmode=disable", rolePW, port)
	ctx := resetCtx(t)
	// Sanity: the server really enforces passwords.
	bad, err := pgx.Connect(ctx, fmt.Sprintf(
		"postgresql://postgres:wrong@127.0.0.1:%s/postgres?sslmode=disable", port))
	if err == nil {
		bad.Close(ctx)
		t.Fatal("wrong superuser password accepted: server is not password-enforcing")
	}
	if err := Reset(ctx, appDSN, adminDSN, "demo_pwreset", rolePW); err != nil {
		t.Fatalf("reset on password server: %v", err)
	}
	pool, err := coredatabase.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer pool.Close()
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		t.Fatalf("pool runs as %q, want app", who)
	}
	if _, err := Demo(ctx, pool); err != nil {
		t.Fatalf("demo after password reset: %v", err)
	}
	// Wrong role password fails against the recycled database.
	badApp := fmt.Sprintf("postgresql://app:wrong@127.0.0.1:%s/demo_pwreset?sslmode=disable", port)
	if _, err := pgx.Connect(ctx, badApp); err == nil {
		t.Fatal("wrong app password accepted after reset")
	}
}
