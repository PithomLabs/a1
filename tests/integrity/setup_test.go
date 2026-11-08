// Package integrity proves the pivot invariants against a real PostgreSQL:
// two-employer isolation (G1), temporal freezing (G2) and lineage (G3).
//
// The suite owns a dedicated database derived from
// WORKFORCEOPS_TEST_DATABASE_URL (same server, "<name>_integrity"), so it
// never races the core migration tests that share the base database.
package integrity

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	coredatabase "github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	adminPool *pgxpool.Pool
	appPool   *pgxpool.Pool
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("integrity: WORKFORCEOPS_TEST_DATABASE_URL not set, skipping")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fmt.Printf("integrity: parse DSN: %v\n", err)
		os.Exit(1)
	}
	dbName := cfg.ConnConfig.Database
	if dbName == "" || dbName == "postgres" {
		fmt.Println("integrity: test DSN must name a dedicated test database")
		os.Exit(1)
	}
	ownDB := dbName + "_integrity"

	// Provision through the shared helper (extensions, roles, grants,
	// ownership) and migrate as migration_admin: the suite now exercises
	// the real fresh-install path instead of hand-granting around it.
	setup, err := dbutil.SetupDB(ctx, dsn, ownDB,
		func(ctx context.Context, migDSN string) error {
			_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		fmt.Printf("integrity: setup database: %v\n", err)
		os.Exit(1)
	}

	adminPool, err = pgxpool.New(ctx, setup.AdminDSN)
	if err != nil {
		fmt.Printf("integrity: admin pool: %v\n", err)
		os.Exit(1)
	}
	appPool, err = pgxpool.New(ctx, setup.AppDSN)
	if err != nil {
		fmt.Printf("integrity: app pool: %v\n", err)
		os.Exit(1)
	}
	// Prove the suite really runs as the unprivileged role in its own
	// database: a pool mixup here silently voids every G1/G2 assertion.
	var who, where string
	if err := appPool.QueryRow(ctx, `SELECT current_user, current_database()`).Scan(&who, &where); err != nil {
		fmt.Printf("integrity: identify app pool: %v\n", err)
		os.Exit(1)
	}
	if who != "app" || where != ownDB {
		fmt.Printf("integrity: app pool is %s@%s, want app@%s\n", who, where, ownDB)
		os.Exit(1)
	}

	code := m.Run()
	adminPool.Close()
	appPool.Close()
	os.Exit(code)
}
