package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/rules"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// adminDB provisions a migrated database for admin CLI tests and
// returns the app DSN, admin DSN and an app pool.
func adminDB(t *testing.T) (string, string, *pgxpool.Pool) {
	t.Helper()
	dsn := dbutil.DSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	name := cfg.ConnConfig.Database + "_adminact"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	maint, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer maint.Close(context.Background())
	if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	setup, err := dbutil.SetupDB(ctx, dsn, name,
		func(ctx context.Context, migDSN string) error {
			_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		t.Fatalf("setup %s: %v", name, err)
	}
	pool, err := pgxpool.New(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return setup.AppDSN, setup.MigDSN, pool
}

func adminCtx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return c
}

// writeCLIFixturePack writes a minimal valid IMPLEMENTABLE fixture
// pack. CLI activation tests use temp-dir packs only, never rules/.
func writeCLIFixturePack(t *testing.T, dir string) {
	t.Helper()
	manifest := `{
  "rule_id": "TEST-CLI-001",
  "name": "CLI fixture pack",
  "description": "Synthetic pack exercising the admin activation command.",
  "jurisdiction": "SG",
  "authority": "TEST",
  "rigor": "L1",
  "version": "2.0.0",
  "status": "IMPLEMENTABLE",
  "effective_from": "2026-01-01",
  "effective_until": null,
  "applicability": {},
  "sources": [{"id": "S1", "digest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "retrieved_at": "2026-01-01T00:00:00Z"}],
  "dependencies": [],
  "spec_files": ["SPEC.pseudo.md"],
  "review": {"reviewer": "cli-reviewer", "date": "2026-01-02", "protocol": "MRC", "outcome": "accepted"},
  "implementation": {"owner": "cli-owner", "version": "2.0.0", "fixtures_gate": ["fixtures/golden.json"]}
}`
	spec := "# source: S1\n\n```text\nclaim cli-rate\n  given monthly-rated\n```\n"
	for name, body := range map[string]string{
		"manifest.json":        manifest,
		"SPEC.pseudo.md":       spec,
		"REVIEW.md":            "# CLI fixture review\n\nOutcome: accepted.\n",
		"SOURCES.md":           "# S1: cli fixture source\n",
		"fixtures/golden.json": `{"cases": []}`,
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func mkCLIEmployer(t *testing.T, pool *pgxpool.Pool, code string) {
	t.Helper()
	if _, err := pool.Exec(adminCtx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr')`,
		code, code, "UEN-"+code); err != nil {
		t.Fatalf("employer: %v", err)
	}
}

// pinCLIPack pins the fixture pack's reviewed version, computing the
// digest independently through the same verifier the command uses.
func pinCLIPack(t *testing.T, adminDSN, packDir string) {
	t.Helper()
	m, err := rules.LoadManifest(packDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ev, err := m.Verify(packDir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	ctx := adminCtx(t)
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx,
		`INSERT INTO rule_pack (pack, jurisdiction, scope, reviewed_version, manifest_digest, spec_version, verification_status)
		 VALUES ($1, 'SG', 'test-fixture', $2, $3, 'spec-1', 'GOLDENS_VERIFIED')`,
		ev.Pack, ev.Version, ev.ManifestDigest); err != nil {
		t.Fatalf("pin: %v", err)
	}
}

func activationRow(t *testing.T, pool *pgxpool.Pool, code, pack string) (version, status string) {
	t.Helper()
	err := pool.QueryRow(adminCtx(t),
		`SELECT a.version, a.status FROM rule_pack_activation a JOIN employer e ON e.id = a.employer_id
		 WHERE e.code = $1 AND a.pack = $2`, code, pack).Scan(&version, &status)
	if err != nil {
		t.Fatalf("activation: %v", err)
	}
	return version, status
}

func TestAdminActivateHappyPath(t *testing.T) {
	appDSN, adminDSN, pool := adminDB(t)
	mkCLIEmployer(t, pool, "ACTCLI")
	dir := t.TempDir()
	writeCLIFixturePack(t, dir)
	pinCLIPack(t, adminDSN, dir)
	stdout, err, stderr := runCLIErr(t, appDSN, "admin", "activate-rule-pack",
		"--pack-dir", dir, "--employer", "ACTCLI", "--scope", "test-fixture",
		"--reason", "fixture qualifies", "--admin-dsn", adminDSN)
	if err != nil {
		t.Fatalf("activate: %v stderr=%s", err, stderr)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output %q: %v", stdout, err)
	}
	if got["pack"] != "TEST-CLI-001" || got["version"] != "2.0.0" || got["status"] != "IMPLEMENTABLE" {
		t.Fatalf("output=%v", got)
	}
	version, status := activationRow(t, pool, "ACTCLI", "TEST-CLI-001")
	if version != "2.0.0" || status != "IMPLEMENTABLE" {
		t.Fatalf("activation=%s/%s", version, status)
	}
}

func TestAdminActivateRefusals(t *testing.T) {
	appDSN, adminDSN, pool := adminDB(t)
	mkCLIEmployer(t, pool, "ACTREF")
	dir := t.TempDir()
	writeCLIFixturePack(t, dir)
	pinCLIPack(t, adminDSN, dir)
	blocked := t.TempDir()
	writeCLIFixturePack(t, blocked)
	raw, err := os.ReadFile(filepath.Join(blocked, "manifest.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "manifest.json"),
		[]byte(strings.Replace(string(raw), `"IMPLEMENTABLE"`, `"BLOCKED"`, 1)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"blocked manifest", []string{"--pack-dir", blocked}, "not IMPLEMENTABLE"},
		{"missing pack dir", []string{"--pack-dir", filepath.Join(dir, "nope")}, "manifest"},
		{"missing reason", []string{"--pack-dir", dir, "--reason", ""}, "--reason"},
		{"missing scope", []string{"--pack-dir", dir, "--scope", ""}, "--scope"},
		{"app dsn refused", []string{"--pack-dir", dir, "--admin-dsn", appDSN}, "privileged"},
		{"scope mismatch", []string{"--pack-dir", dir, "--scope", "overtime"}, "scope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := []string{"admin", "activate-rule-pack",
				"--employer", "ACTREF", "--scope", "test-fixture",
				"--reason", "attempt", "--admin-dsn", adminDSN}
			// Case args override the shared defaults.
			over := map[string]string{}
			for i := 0; i+1 < len(c.args); i += 2 {
				over[c.args[i]] = c.args[i+1]
			}
			final := []string{args[0], args[1]}
			seen := map[string]bool{}
			for i := 2; i+1 < len(args); i += 2 {
				if v, ok := over[args[i]]; ok {
					final = append(final, args[i], v)
					seen[args[i]] = true
				} else {
					final = append(final, args[i], args[i+1])
				}
			}
			for k, v := range over {
				if !seen[k] {
					final = append(final, k, v)
				}
			}
			_, err, stderr := runCLIErr(t, appDSN, final...)
			if err == nil {
				t.Fatalf("accepted, want refusal containing %q", c.want)
			}
			if !strings.Contains(stderr, c.want) && !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v stderr=%s want %q", err, stderr, c.want)
			}
		})
	}
	// Every refusal left the pack unactivated.
	var n int
	if err := pool.QueryRow(adminCtx(t),
		`SELECT count(*) FROM rule_pack_activation a JOIN employer e ON e.id = a.employer_id
		 WHERE e.code = 'ACTREF'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("activation rows=%d err=%v want none", n, err)
	}
}

// TestOT001RemainsBlocked guards the freeze: the OT-001 manifest keeps
// its BLOCKED workflow marker and no activation of it exists.
func TestOT001RemainsBlocked(t *testing.T) {
	raw, err := os.ReadFile("../../rules/SG-MOM-OT-001/manifest.json")
	if err != nil {
		t.Fatalf("read OT-001 manifest: %v", err)
	}
	var m struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse OT-001 manifest: %v", err)
	}
	if m.Status != "BLOCKED" {
		t.Fatalf("OT-001 manifest status=%q want BLOCKED", m.Status)
	}
	_, _, pool := adminDB(t)
	var n int
	if err := pool.QueryRow(adminCtx(t),
		`SELECT count(*) FROM rule_pack_activation WHERE pack IN ('SG-MOM-OT', 'SG-MOM-OT-001') AND status <> 'BLOCKED'`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d non-default OT activation rows", n)
	}
}
