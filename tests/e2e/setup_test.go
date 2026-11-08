// Package e2e proves cross-service operator flows against a real
// PostgreSQL: vendor feed to reviewed timesheet through the same
// service calls the CLI and REST layer serve.
//
// The suite owns a dedicated database derived from
// WORKFORCEOPS_TEST_DATABASE_URL (same server, "<name>_e2e"), so it
// never races other suites that share the base database.
package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

var appPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("e2e: WORKFORCEOPS_TEST_DATABASE_URL not set, skipping")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fmt.Printf("e2e: parse DSN: %v\n", err)
		os.Exit(1)
	}
	dbName := cfg.ConnConfig.Database
	if dbName == "" || dbName == "postgres" {
		fmt.Println("e2e: test DSN must name a dedicated test database")
		os.Exit(1)
	}
	ownDB := dbName + "_e2e"

	setup, err := dbutil.SetupDB(ctx, dsn, ownDB,
		func(ctx context.Context, migDSN string) error {
			_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		fmt.Printf("e2e: setup database: %v\n", err)
		os.Exit(1)
	}
	appPool, err = pgxpool.New(ctx, setup.AppDSN)
	if err != nil {
		fmt.Printf("e2e: app pool: %v\n", err)
		os.Exit(1)
	}
	var who, where string
	if err := appPool.QueryRow(ctx, `SELECT current_user, current_database()`).Scan(&who, &where); err != nil {
		fmt.Printf("e2e: identify app pool: %v\n", err)
		os.Exit(1)
	}
	if who != "app" || where != ownDB {
		fmt.Printf("e2e: app pool is %s@%s, want app@%s\n", who, where, ownDB)
		os.Exit(1)
	}

	code := m.Run()
	appPool.Close()
	os.Exit(code)
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}
