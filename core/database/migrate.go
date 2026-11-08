package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// AppliedMigration describes one migration goose applied.
type AppliedMigration struct {
	Version int64
	Path    string
}

// ReportedMigration describes one migration's recorded status.
type ReportedMigration struct {
	Version   int64
	Path      string
	State     string
	AppliedAt time.Time
}

// Preflight fails loudly unless dsn connects as migration_admin to a
// database with the app role and required extensions present. It runs
// before MigrateUp so a wrong-role invocation explains itself instead of
// applying half a chain or silently skipping protections.
func Preflight(ctx context.Context, dsn string) error {
	pool, err := Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("migration preflight: connect: %w", err)
	}
	defer pool.Close()
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil {
		return fmt.Errorf("migration preflight: identify role: %w", err)
	}
	if who != "migration_admin" {
		return fmt.Errorf("migration preflight: connected as %q, want migration_admin (set MIGRATION_DATABASE_URL)", who)
	}
	var roles bool
	if err := pool.QueryRow(ctx,
		`SELECT count(*) = 2 FROM pg_roles WHERE rolname IN ('migration_admin', 'app')`).Scan(&roles); err != nil {
		return fmt.Errorf("migration preflight: check roles: %w", err)
	}
	if !roles {
		return fmt.Errorf("migration preflight: roles migration_admin/app missing: provision roles before migrating")
	}
	var exts bool
	if err := pool.QueryRow(ctx,
		`SELECT count(*) = 2 FROM pg_extension WHERE extname IN ('pgcrypto', 'btree_gist')`).Scan(&exts); err != nil {
		return fmt.Errorf("migration preflight: check extensions: %w", err)
	}
	if !exts {
		return fmt.Errorf("migration preflight: extensions pgcrypto/btree_gist missing: run the superuser bootstrap first")
	}
	return nil
}

// MigrateUp applies all pending migrations from fsys (goose *.sql at FS root)
// using a dedicated connection. dsn must belong to the migration_admin role,
// never a runtime application role.
func MigrateUp(ctx context.Context, dsn string, fsys fs.FS) ([]AppliedMigration, error) {
	p, db, err := openProvider(dsn, fsys)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	results, err := p.Up(ctx)
	if err != nil {
		return nil, err
	}
	applied := make([]AppliedMigration, 0, len(results))
	for _, r := range results {
		if r.Source == nil {
			continue
		}
		applied = append(applied, AppliedMigration{Version: r.Source.Version, Path: r.Source.Path})
	}
	return applied, nil
}

// MigrationStatus reports recorded migration state without applying anything.
func MigrationStatus(ctx context.Context, dsn string, fsys fs.FS) ([]ReportedMigration, error) {
	p, db, err := openProvider(dsn, fsys)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	statuses, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReportedMigration, 0, len(statuses))
	for _, st := range statuses {
		if st.Source == nil {
			continue
		}
		out = append(out, ReportedMigration{
			Version:   st.Source.Version,
			Path:      st.Source.Path,
			State:     fmt.Sprintf("%v", st.State),
			AppliedAt: st.AppliedAt,
		})
	}
	return out, nil
}

func openProvider(dsn string, fsys fs.FS) (*goose.Provider, *sql.DB, error) {
	if dsn == "" {
		return nil, nil, fmt.Errorf("migration DSN is empty")
	}
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		return nil, nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return p, db, nil
}
