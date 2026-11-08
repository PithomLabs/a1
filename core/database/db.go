// Package database owns PostgreSQL connectivity and migration execution.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RefusePrivilegedRuntime rejects runtime connections that bypass the
// application-role protections: superusers, and the migration_admin
// owner (table ownership overrides every REVOKE). CLI commands, seed
// operations and the API server must run as the app role; only
// migrations and explicitly privileged admin flows may use the other
// roles. The check reads the actual current_user off the connection.
func RefusePrivilegedRuntime(ctx context.Context, pool *pgxpool.Pool) error {
	var who string
	var super bool
	if err := pool.QueryRow(ctx,
		`SELECT current_user, (SELECT rolsuper FROM pg_roles WHERE rolname = current_user)`).Scan(&who, &super); err != nil {
		return fmt.Errorf("runtime role check: %w", err)
	}
	if super {
		return fmt.Errorf("refusing to run as superuser %q: point DATABASE_URL at the app role", who)
	}
	if who == "migration_admin" {
		return fmt.Errorf("refusing to run as %q: the schema owner bypasses application-role protections; point DATABASE_URL at the app role", who)
	}
	return nil
}

// Open creates a tuned pgxpool and verifies connectivity before returning.
// Pool sizing follows the values proven in bchat's postgres store.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = time.Minute

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
