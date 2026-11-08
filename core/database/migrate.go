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
