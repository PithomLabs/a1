// Package dbutil shares DB-gated test setup across packages.
// Tests skip unless WORKFORCEOPS_TEST_DATABASE_URL names a dedicated database.
package dbutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	adminCfg := cfg.ConnConfig.Copy()
	adminCfg.Database = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{dbName}.Sanitize())); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			return dsn // already exists (parallel package setup race)
		}
		t.Fatalf("create test database: %v", err)
	}
	return dsn
}
