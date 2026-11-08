// Package dbutil shares DB-gated test setup across packages.
// Tests skip unless WORKFORCEOPS_TEST_DATABASE_URL names a dedicated database.
package dbutil

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureExtensions installs the superuser-owned extensions the baseline
// migrations require. The test DSN is expected to be a superuser, mirroring
// the bootstrap script that runs before migrations in composed environments.
func EnsureExtensions(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer conn.Close(ctx)
	for _, ext := range []string{"pgcrypto", "btree_gist"} {
		if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext); err != nil {
			t.Fatalf("create extension %s: %v", ext, err)
		}
	}
}

// CreateDatabase creates name through the maintenance database of adminDSN.
// Parallel package setups race here: already-exists finishes silently and
// template lock contention (55006) retries with backoff. A 23505 race
// loser is tolerated only after verifying the database actually exists.
func CreateDatabase(ctx context.Context, adminDSN, name string) error {
	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	adminCfg := cfg.ConnConfig.Copy()
	adminCfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("connect maintenance database: %w", err)
	}
	defer admin.Close(ctx)
	stmt := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	for attempt := 0; ; attempt++ {
		if _, err := admin.Exec(ctx, stmt); err == nil {
			return nil
		} else {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
				return nil // already exists (parallel setup race)
			}
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				var exists bool
				if qerr := admin.QueryRow(ctx,
					`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); qerr != nil {
					return fmt.Errorf("create database: verify after 23505: %w", qerr)
				}
				if exists {
					return nil // lost the creation race; the winner's DB is real
				}
				return fmt.Errorf("create database: 23505 without a resulting database")
			}
			if errors.As(err, &pgErr) && pgErr.Code == "55006" && attempt < 10 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(200 * time.Millisecond):
				}
				continue // template1 contention (parallel CREATE DATABASE)
			}
			return fmt.Errorf("create database: %w", err)
		}
	}
}

// DSN returns the test database URL, creating the database if needed.
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WORKFORCEOPS_TEST_DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	dbName := cfg.ConnConfig.Database
	if dbName == "" || dbName == "postgres" {
		t.Fatal("test DSN must name a dedicated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := CreateDatabase(ctx, dsn, dbName); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	return dsn
}

// TestDB is a fully provisioned test database: roles exist, the schema is
// owned by migration_admin, migrations applied, and the app role carries
// production-like grants. Suites must use AppDSN; AdminDSN is setup-only.
type TestDB struct {
	Name     string
	AdminDSN string
	MigDSN   string
	AppDSN   string
}

// Setup provisions the shared test database named by
// WORKFORCEOPS_TEST_DATABASE_URL, runs migrate over the migration_admin
// DSN under the setup lock, and returns role DSNs for it.
func Setup(t *testing.T, migrate func(ctx context.Context, migDSN string) error) TestDB {
	t.Helper()
	dsn := DSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	setup, err := SetupDB(ctx, dsn, cfg.ConnConfig.Database, migrate)
	if err != nil {
		t.Fatalf("setup test database: %v", err)
	}
	return setup
}

// WithSetupLock runs fn holding the cross-process setup lock for name.
// Every MigrateUp against a shared test database must run inside it:
// goose does not serialize concurrent migrators itself.
func WithSetupLock(ctx context.Context, adminDSN, name string, fn func() error) error {
	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	adminCfg := cfg.ConnConfig.Copy()
	adminCfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("connect maintenance database: %w", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, "m11:setup:"+name); err != nil {
		return fmt.Errorf("setup lock: %w", err)
	}
	defer admin.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, "m11:setup:"+name)
	return fn()
}

// SetupDB provisions name on the server of adminDSN (extensions, roles,
// grants, schema ownership) and runs migrate over MigDSN, all under the
// cross-process setup lock. migrate is a callback because dbutil cannot
// import core/database (import cycle); nil skips migration.
func SetupDB(ctx context.Context, adminDSN, name string, migrate func(ctx context.Context, migDSN string) error) (TestDB, error) {
	out := TestDB{Name: name, AdminDSN: WithDB(adminDSN, name)}
	err := WithSetupLock(ctx, adminDSN, name, func() error {
		return setupLocked(ctx, adminDSN, name, &out, migrate)
	})
	return out, err
}

func setupLocked(ctx context.Context, adminDSN, name string, out *TestDB, migrate func(ctx context.Context, migDSN string) error) error {
	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	if err := CreateDatabase(ctx, adminDSN, name); err != nil {
		return err
	}
	dbCfg := cfg.ConnConfig.Copy()
	dbCfg.Database = name
	super, err := pgx.ConnectConfig(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("connect test database: %w", err)
	}
	defer super.Close(ctx)
	for _, ext := range []string{"pgcrypto", "btree_gist"} {
		if _, err := super.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext); err != nil {
			var pgErr *pgconn.PgError
			if !(errors.As(err, &pgErr) && pgErr.Code == "23505") {
				return fmt.Errorf("create extension %s: %w", ext, err)
			}
			// 23505 race loser: verify below that the winner installed it.
		}
	}
	var installed bool
	if err := super.QueryRow(ctx,
		`SELECT count(*) = 2 FROM pg_extension WHERE extname IN ('pgcrypto', 'btree_gist')`).Scan(&installed); err != nil {
		return fmt.Errorf("verify extensions: %w", err)
	}
	if !installed {
		return fmt.Errorf("extensions missing after setup race")
	}
	// S7-P2-FRESHFLAKE: roles are cluster-global but the setup lock
	// above is per-database, so parallel setups for different
	// databases race CREATE ROLE. Serialize role provisioning on a
	// dedicated global advisory lock (same key as
	// RecycleDatabase, which provisions the same roles). The lock
	// lives on the shared postgres maintenance database because
	// advisory locks do not cross database boundaries: locking on
	// the per-test database would serialize nothing.
	maintCfg := cfg.ConnConfig.Copy()
	maintCfg.Database = "postgres"
	maint, err := pgx.ConnectConfig(ctx, maintCfg)
	if err != nil {
		return fmt.Errorf("connect maintenance database for role lock: %w", err)
	}
	defer maint.Close(ctx)
	if _, err := maint.Exec(ctx, `SELECT pg_advisory_lock(hashtext('m13:roles:global'))`); err != nil {
		return fmt.Errorf("global role lock: %w", err)
	}
	roleErr := func() error {
		if _, err := super.Exec(ctx, `
DO $do$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'migration_admin') THEN
    CREATE ROLE migration_admin LOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
    CREATE ROLE app LOGIN;
  END IF;
END $do$;
GRANT CONNECT ON DATABASE `+pgx.Identifier{name}.Sanitize()+` TO migration_admin, app;
REVOKE TEMPORARY ON DATABASE `+pgx.Identifier{name}.Sanitize()+` FROM app, PUBLIC;
ALTER SCHEMA public OWNER TO migration_admin;
GRANT USAGE ON SCHEMA public TO app;
ALTER DEFAULT PRIVILEGES FOR ROLE migration_admin IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app;`); err != nil {
			return fmt.Errorf("provision roles: %w", err)
		}
		// Verify the actual post-race outcome: both roles exist,
		// can log in, connect here, and app holds no TEMPORARY.
		var ok bool
		if err := super.QueryRow(ctx, `
SELECT (SELECT count(*) = 2 FROM pg_roles WHERE rolname IN ('migration_admin', 'app') AND rolcanlogin)
   AND has_database_privilege('migration_admin', current_database(), 'CONNECT')
   AND has_database_privilege('app', current_database(), 'CONNECT')
   AND NOT has_database_privilege('app', current_database(), 'TEMP')`).Scan(&ok); err != nil {
			return fmt.Errorf("verify roles: %w", err)
		}
		if !ok {
			return fmt.Errorf("roles missing, not login, or misprivileged after provisioning")
		}
		return nil
	}()
	if _, err := maint.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('m13:roles:global'))`); err != nil && roleErr == nil {
		return fmt.Errorf("global role unlock: %w", err)
	}
	if roleErr != nil {
		return roleErr
	}
	out.MigDSN = RoleDSN(adminDSN, name, "migration_admin")
	out.AppDSN = RoleDSN(adminDSN, name, "app")
	if migrate != nil {
		if err := migrate(ctx, out.MigDSN); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// AwaitAdvisoryWait polls until one of the waiter's backends waits on
// an ungranted advisory lock, proving a live race test's waiter
// really blocks on the barrier instead of starting late and racing
// past the hold. Fails after a generous timeout. Pids come from
// WaiterPool. If the waiter finishes first, its error (or unexpected
// success) is reported instead of timing out.
func AwaitAdvisoryWait(t *testing.T, pool *pgxpool.Pool, pids []int32, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("waiter failed instead of blocking: %v", err)
			}
			t.Fatalf("waiter finished without blocking on the barrier")
		default:
		}
		var waiting bool
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid = ANY($1) AND locktype = 'advisory' AND NOT granted)`,
			pids).Scan(&waiting)
		cancel()
		if err != nil {
			t.Fatalf("poll pg_locks: %v", err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dcancel()
			arows, actErr := pool.Query(dctx,
				`SELECT pid, coalesce(wait_event_type,'-'), coalesce(wait_event,'-'), state
				 FROM pg_stat_activity WHERE pid = ANY($1)`, pids)
			act := "activity query failed"
			if actErr == nil {
				defer arows.Close()
				var states []string
				for arows.Next() {
					var wpid int32
					var evtType, evt, state string
					if err := arows.Scan(&wpid, &evtType, &evt, &state); err == nil && state != "idle" {
						states = append(states, fmt.Sprintf("pid=%d %s/%s %s", wpid, evtType, evt, state))
					}
				}
				if len(states) > 0 {
					act = strings.Join(states, ";")
				} else {
					act = "all waiter backends idle"
				}
			}
			rows, err := pool.Query(dctx,
				`SELECT pid, granted FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)
			detail := "none visible"
			if err == nil {
				defer rows.Close()
				var waits []string
				for rows.Next() {
					var wpid int32
					var g bool
					if err := rows.Scan(&wpid, &g); err == nil {
						waits = append(waits, fmt.Sprintf("pid=%d", wpid))
					}
				}
				if len(waits) > 0 {
					detail = strings.Join(waits, ",")
				}
			} else {
				detail = "pg_locks query failed: " + err.Error()
			}
			t.Fatalf("waiter backends %v never blocked on an advisory lock (%s; other advisory waiters: %s)", pids, act, detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RaceDSN returns the pool's connection string for opening sibling
// connections to the same database in live race tests.
func RaceDSN(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	dsn := pool.Config().ConnString()
	if dsn == "" {
		t.Fatal("pool has no connection string")
	}
	return dsn
}

// waiterPoolSize bounds a race-test waiter's pool. Services
// legitimately hold a rows iterator open while issuing another
// query (e.g. timesheet.Get), so a single-connection pool would
// deadlock client-side; eight backends give ample headroom while
// keeping the pid set exact.
const waiterPoolSize = 8

// WaiterPool builds a race-test waiter's pool and returns the pids
// of every backend it can use, so pg_locks assertions match the
// waiter exactly no matter which connection its blocked statement
// lands on. Role verified as app.
func WaiterPool(t *testing.T, dsn string) (*pgxpool.Pool, []int32) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = waiterPoolSize
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("waiter pool: %v", err)
	}
	t.Cleanup(p.Close)
	conns := make([]*pgxpool.Conn, 0, waiterPoolSize)
	pids := make([]int32, 0, waiterPoolSize)
	for i := 0; i < waiterPoolSize; i++ {
		c, err := p.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		var who string
		var pid int32
		if err := c.QueryRow(ctx, `SELECT current_user, pg_backend_pid()`).Scan(&who, &pid); err != nil {
			t.Fatalf("identify: %v", err)
		}
		if who != "app" {
			t.Fatalf("waiter runs as %q, want app", who)
		}
		pids = append(pids, pid)
	}
	for _, c := range conns {
		c.Release()
	}
	return p, pids
}

// AppConn opens one dedicated app-role connection for the holding
// side of a live race test.
func AppConn(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	var who string
	if err := c.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		t.Fatalf("holder runs as %q, want app", who)
	}
	return c
}

// AwaitDone waits for a released waiter, failing loudly on error or
// timeout instead of hanging the suite.
func AwaitDone(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("%s never finished after release", what)
	}
}

// WithDB returns dsn pointed at database db, same user.
func WithDB(dsn, db string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + db
	return u.String()
}

// RoleDSN rewrites dsn to connect to db as role. The password comes from
// WORKFORCEOPS_TEST_ROLE_PASSWORD when set, else the DSN's own password,
// else none (scratch servers trust local TCP).
func RoleDSN(dsn, db, role string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + db
	pw := os.Getenv("WORKFORCEOPS_TEST_ROLE_PASSWORD")
	if pw == "" {
		if base, perr := url.Parse(dsn); perr == nil && base.User != nil {
			pw, _ = base.User.Password()
		}
	}
	if pw == "" {
		u.User = url.User(role)
	} else {
		u.User = url.UserPassword(role, pw)
	}
	return u.String()
}
