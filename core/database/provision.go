package database

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Disposable database names the recycle operation always refuses.
var protectedDatabases = []string{"postgres", "template0", "template1"}

// disposableDatabaseName reports whether name is recognizably a
// throwaway database. The rule is deliberately lexical: recycling is
// a destructive administrative operation, and an explicit disposable
// marker keeps a typo'd DSN from pointing it at real work.
func disposableDatabaseName(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range []string{"demo", "test", "a1test", "tmp", "scratch"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	for _, s := range []string{"_demo", "_test", "_tmp", "_scratch"} {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

// RecycleDatabase drops and recreates name on the admin server, then
// provisions extensions, roles and grants. It is the controlled
// administrative operation behind `seed demo --reset`: demo data is
// disposed at the database level, never by deleting frozen rows past
// the immutability triggers (which bind every role, including admins).
// The provisioning SQL mirrors tests/dbutil setupLocked; keep the two
// in step.
//
// The target must be recognizably disposable (see disposableDatabaseName):
// a typo'd DSN pointing at an unrelated developer database is refused
// before anything is dropped. rolePassword sets the runtime roles'
// passwords when non-empty (required on password-authenticated
// servers); empty leaves existing passwords untouched.
func RecycleDatabase(ctx context.Context, adminDSN, name, rolePassword string) error {
	if name == "" {
		return fmt.Errorf("refusing to recycle a database with no name")
	}
	lower := strings.ToLower(name)
	for _, p := range protectedDatabases {
		if lower == p {
			return fmt.Errorf("refusing to recycle protected database %q", name)
		}
	}
	if !disposableDatabaseName(name) {
		return fmt.Errorf("refusing to recycle %q: not a recognizably disposable database name "+
			"(use a demo/test/tmp/scratch prefix or suffix, or rename explicitly)", name)
	}
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return fmt.Errorf("parse admin DSN: %w", err)
	}
	cfg.Database = "postgres"
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect admin server: %w", err)
	}
	defer admin.Close(ctx)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+ident+` WITH (FORCE)`); err != nil {
		return fmt.Errorf("drop database %s: %w", name, err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+ident); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	dbCfg := cfg.Copy()
	dbCfg.Database = name
	super, err := pgx.ConnectConfig(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("connect database %s: %w", name, err)
	}
	defer super.Close(ctx)
	for _, ext := range []string{"pgcrypto", "btree_gist"} {
		if _, err := super.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext); err != nil {
			return fmt.Errorf("create extension %s: %w", ext, err)
		}
	}
	pwClause := ""
	if rolePassword != "" {
		pwClause = " PASSWORD '" + strings.ReplaceAll(rolePassword, "'", "''") + "'"
	}
	// Same global role lock as test setup: recycle runs beside
	// parallel package setups on clean servers, and CREATE ROLE
	// races across databases without it. The lock lives on the
	// admin maintenance connection because advisory locks do not
	// cross database boundaries.
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(hashtext('m13:roles:global'))`); err != nil {
		return fmt.Errorf("global role lock: %w", err)
	}
	roleErr := func() error {
		if _, err := super.Exec(ctx, `
DO $do$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'migration_admin') THEN
    CREATE ROLE migration_admin LOGIN`+pwClause+`;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
    CREATE ROLE app LOGIN`+pwClause+`;
  END IF;
END $do$;
GRANT CONNECT ON DATABASE `+ident+` TO migration_admin, app;
REVOKE TEMPORARY ON DATABASE `+ident+` FROM app, PUBLIC;
ALTER SCHEMA public OWNER TO migration_admin;
GRANT USAGE ON SCHEMA public TO app;
ALTER DEFAULT PRIVILEGES FOR ROLE migration_admin IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app;`); err != nil {
			return fmt.Errorf("provision roles: %w", err)
		}
		// On password-authenticated servers the roles must authenticate
		// even when they predate this recycle (cluster-global roles are
		// shared across databases): align both passwords explicitly.
		if rolePassword != "" {
			lit := "'" + strings.ReplaceAll(rolePassword, "'", "''") + "'"
			if _, err := super.Exec(ctx, `ALTER ROLE migration_admin LOGIN PASSWORD `+lit); err != nil {
				return fmt.Errorf("set migration_admin password: %w", err)
			}
			if _, err := super.Exec(ctx, `ALTER ROLE app LOGIN PASSWORD `+lit); err != nil {
				return fmt.Errorf("set app password: %w", err)
			}
		}
		return nil
	}()
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_unlock(hashtext('m13:roles:global'))`); err != nil && roleErr == nil {
		return fmt.Errorf("global role unlock: %w", err)
	}
	if roleErr != nil {
		return roleErr
	}
	return nil
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

// RoleDSN rewrites dsn to connect to db as role, keeping the DSN's own
// password when one is set (scratch servers trust local TCP).
func RoleDSN(dsn, db, role string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + db
	var pw string
	if base, perr := url.Parse(dsn); perr == nil && base.User != nil {
		pw, _ = base.User.Password()
	}
	if pw == "" {
		u.User = url.User(role)
	} else {
		u.User = url.UserPassword(role, pw)
	}
	return u.String()
}

// SetPassword returns dsn with the connection password replaced,
// keeping the username. Unparseable input passes through unchanged
// so the connector reports the original problem.
func SetPassword(dsn, password string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	u.User = url.UserPassword(u.User.Username(), password)
	return u.String()
}

// SameServer reports whether two DSNs address the same host and port.
func SameServer(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Hostname(), ub.Hostname()) && ua.Port() == ub.Port()
}

// DatabaseName returns the database named by dsn, or "" when absent.
func DatabaseName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}
