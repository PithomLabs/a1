package rules

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

func testPool(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	migrate := func(ctx context.Context, migDSN string) error {
		_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	}
	setup := dbutil.Setup(t, migrate)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	app, err := coredatabase.Open(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	t.Cleanup(app.Close)
	admin, err := coredatabase.Open(ctx, setup.AdminDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(admin.Close)
	return app, admin
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func mkEmployer(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	code = fmt.Sprintf("%s_%d", code, time.Now().UnixNano())
	var id string
	err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&id)
	if err != nil {
		t.Fatalf("employer: %v", err)
	}
	return id
}

// pinFixturePack registers a fixture pack identity with a reviewed
// version pin, as a migration would for a genuinely qualified pack.
func pinFixturePack(t *testing.T, admin *pgxpool.Pool, pack, version, digest string) {
	t.Helper()
	if _, err := admin.Exec(ctx(t),
		`INSERT INTO rule_pack (pack, jurisdiction, scope, reviewed_version, manifest_digest, spec_version, verification_status)
		 VALUES ($1, 'SG', 'test-fixture', $2, $3, 'spec-1', 'GOLDENS_VERIFIED')`,
		pack, version, digest); err != nil {
		t.Fatalf("pin pack: %v", err)
	}
}

func fixtureEvidence(t *testing.T) *Evidence {
	t.Helper()
	dir := t.TempDir()
	writeFixturePack(t, dir)
	// The test database is shared across tests and reruns, so each
	// fixture pack needs a globally unique identity.
	pack := fmt.Sprintf("TEST-%d", time.Now().UnixNano())
	raw, err := os.ReadFile(dir + "/manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	raw = []byte(strings.Replace(string(raw), "TEST-FIXTURE-001", pack, 1))
	if err := os.WriteFile(dir+"/manifest.json", raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ev, err := m.Verify(dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return ev
}

func activationStatus(t *testing.T, pool *pgxpool.Pool, emp, pack string) (version, status string) {
	t.Helper()
	err := pool.QueryRow(ctx(t),
		`SELECT version, status FROM rule_pack_activation WHERE employer_id = $1 AND pack = $2`,
		emp, pack).Scan(&version, &status)
	if err != nil {
		t.Fatalf("activation: %v", err)
	}
	return version, status
}

func TestActivateHappyPath(t *testing.T) {
	app, admin := testPool(t)
	emp := mkEmployer(t, app, "ACT1")
	ev := fixtureEvidence(t)
	pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
	id, err := Activate(ctx(t), admin, emp, "test-fixture", ev, "admin-op", "fixture qualifies")
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	version, status := activationStatus(t, app, emp, ev.Pack)
	if version != ev.Version || status != "IMPLEMENTABLE" {
		t.Fatalf("activation=%s/%s", version, status)
	}
	// History carries the full pin; the audit event is atomic with it.
	var digest, actor, reason string
	var supersedes *string
	err = app.QueryRow(ctx(t),
		`SELECT manifest_digest, actor, reason, supersedes_id::text
		 FROM rule_pack_activation_history WHERE id = $1`, id).Scan(&digest, &actor, &reason, &supersedes)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if digest != ev.ManifestDigest || actor != "admin-op" || reason != "fixture qualifies" || supersedes != nil {
		t.Fatalf("history row mangled")
	}
	var action, entity, entityID string
	err = app.QueryRow(ctx(t),
		`SELECT action, entity, entity_id::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'rule_pack.activate'`, emp).Scan(&action, &entity, &entityID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if entity != "rule_pack_activation_history" || entityID != id {
		t.Fatalf("audit=%s/%s/%s", action, entity, entityID)
	}
}

func TestActivateAppRoleRefused(t *testing.T) {
	app, admin := testPool(t)
	emp := mkEmployer(t, app, "ACT2")
	ev := fixtureEvidence(t)
	pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
	// The app role holds no EXECUTE on the activation function: the
	// controlled path is admin-only, whatever the caller claims. The
	// database refuses with 42501, which FromDB masks as internal by
	// design (no internals reach clients), so assert behaviorally.
	if _, err := Activate(ctx(t), app, emp, "test-fixture", ev, "app-op", "present"); err == nil {
		t.Fatal("app-role activation accepted")
	}
	var n int
	if err := app.QueryRow(ctx(t),
		`SELECT count(*) FROM rule_pack_activation WHERE employer_id = $1`, emp).Scan(&n); err != nil || n != 0 {
		t.Fatalf("activation rows=%d err=%v want none", n, err)
	}
}

func TestActivateRejects(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string)
		want  string
	}{
		{"unknown pack", func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string) {
			return ev, "test-fixture"
		}, "unknown rule pack"},
		{"invented version", func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string) {
			pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
			out := *ev
			out.Version = "9.9.9-invented"
			return &out, "test-fixture"
		}, "version"},
		{"digest mismatch", func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string) {
			pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
			out := *ev
			out.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
			return &out, "test-fixture"
		}, "digest"},
		{"jurisdiction mismatch", func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string) {
			pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
			out := *ev
			out.Jurisdiction = "XX"
			return &out, "test-fixture"
		}, "jurisdiction"},
		{"scope mismatch", func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string) {
			pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
			return ev, "overtime"
		}, "scope"},
		{"missing actor", func(t *testing.T, admin *pgxpool.Pool, emp string, ev *Evidence) (*Evidence, string) {
			pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
			return ev, "test-fixture"
		}, "attribution"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app, admin := testPool(t)
			emp := mkEmployer(t, app, fmt.Sprintf("ACTR%d", time.Now().UnixNano()))
			ev := fixtureEvidence(t)
			in, scope := c.setup(t, admin, emp, ev)
			actor := "admin-op"
			if c.name == "missing actor" {
				actor = "  "
			}
			if _, err := Activate(ctx(t), admin, emp, scope, in, actor, "reason"); err == nil {
				t.Fatalf("accepted, want rejection containing %q", c.want)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v want %q", err, c.want)
			}
		})
	}
}

func TestActivateTransitions(t *testing.T) {
	app, admin := testPool(t)
	emp := mkEmployer(t, app, "ACTT")
	ev := fixtureEvidence(t)
	pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
	if _, err := Activate(ctx(t), admin, emp, "test-fixture", ev, "admin-op", "first"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	// Same version twice: the approved activation cannot be rewritten.
	if _, err := Activate(ctx(t), admin, emp, "test-fixture", ev, "admin-op", "again"); err == nil {
		t.Fatal("repeat activation accepted")
	} else if !strings.Contains(err.Error(), "already") {
		t.Fatalf("err=%v want already-active rejection", err)
	}
	// A different reviewed version supersedes, preserving history.
	ev2 := *ev
	ev2.Version = "1.1.0"
	ev2.ManifestDigest = "sha256:" + strings.Repeat("1", 64)
	if _, err := admin.Exec(ctx(t),
		`UPDATE rule_pack SET reviewed_version = '1.1.0', manifest_digest = $2 WHERE pack = $1`,
		ev.Pack, ev2.ManifestDigest); err != nil {
		t.Fatalf("repin: %v", err)
	}
	id2, err := Activate(ctx(t), admin, emp, "test-fixture", &ev2, "admin-op", "second")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	version, status := activationStatus(t, app, emp, ev.Pack)
	if version != "1.1.0" || status != "IMPLEMENTABLE" {
		t.Fatalf("activation=%s/%s want the superseding version current", version, status)
	}
	var n int
	var sup *string
	err = app.QueryRow(ctx(t),
		`SELECT count(*), (SELECT supersedes_id::text FROM rule_pack_activation_history WHERE id = $1)
		 FROM rule_pack_activation_history WHERE employer_id = $2 AND pack = $3`,
		id2, emp, ev.Pack).Scan(&n, &sup)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if n != 2 || sup == nil {
		t.Fatalf("history rows=%d supersedes=%v want the preserved chain", n, sup)
	}
}

func TestActivateConcurrentDeterministic(t *testing.T) {
	app, admin := testPool(t)
	emp := mkEmployer(t, app, "ACTC")
	ev := fixtureEvidence(t)
	pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
	const racers = 8
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			_, err := Activate(ctx(t), admin, emp, "test-fixture", ev, "admin-op", "race")
			errs <- err
		}()
	}
	won, lost := 0, 0
	for i := 0; i < racers; i++ {
		if err := <-errs; err == nil {
			won++
		} else if strings.Contains(err.Error(), "already") {
			lost++
		} else {
			t.Fatalf("racer err=%v", err)
		}
	}
	if won != 1 || lost != racers-1 {
		t.Fatalf("won=%d lost=%d want exactly one winner", won, lost)
	}
	var n int
	if err := app.QueryRow(ctx(t),
		`SELECT count(*) FROM rule_pack_activation_history WHERE employer_id = $1`, emp).Scan(&n); err != nil || n != 1 {
		t.Fatalf("history rows=%d err=%v want exactly one", n, err)
	}
}

func TestEnsurePackBlocked(t *testing.T) {
	app, admin := testPool(t)
	emp := mkEmployer(t, app, "ACTB")
	// Unknown packs cannot be noted, even as BLOCKED.
	if _, err := app.Exec(ctx(t), `SELECT ensure_pack_blocked($1::uuid, 'NOPE-NOT-A-PACK')`, emp); err == nil {
		t.Fatal("unknown pack noted as blocked")
	}
	blk := fmt.Sprintf("TEST-BLK-%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx(t),
		`INSERT INTO rule_pack (pack, jurisdiction, scope) VALUES ($1, 'SG', 'test-fixture')`, blk,
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	// The app role may note the default, repeatedly and idempotently.
	for i := 0; i < 2; i++ {
		if _, err := app.Exec(ctx(t), `SELECT ensure_pack_blocked($1::uuid, $2)`, emp, blk); err != nil {
			t.Fatalf("ensure: %v", err)
		}
	}
	version, status := activationStatus(t, app, emp, blk)
	if version != "v0" || status != "BLOCKED" {
		t.Fatalf("activation=%s/%s", version, status)
	}
	// Noting the default never downgrades a live activation.
	ev := fixtureEvidence(t)
	pinFixturePack(t, admin, ev.Pack, ev.Version, ev.ManifestDigest)
	if _, err := Activate(ctx(t), admin, emp, "test-fixture", ev, "admin-op", "live"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := app.Exec(ctx(t), `SELECT ensure_pack_blocked($1::uuid, $2)`, emp, ev.Pack); err != nil {
		t.Fatalf("ensure over live: %v", err)
	}
	version, status = activationStatus(t, app, emp, ev.Pack)
	if version != ev.Version || status != "IMPLEMENTABLE" {
		t.Fatalf("activation=%s/%s want the live version untouched", version, status)
	}
}
