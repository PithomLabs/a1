package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	coredatabase "github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
)

func migrateCmd() *cobra.Command {
	m := &cobra.Command{
		Use:   "migrate",
		Short: "Run core database migrations",
	}
	m.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "Apply pending migrations",
			RunE: func(cmd *cobra.Command, args []string) error {
				dsn := mustMigrateDSN(cmd)
				if dsn == "" {
					return fmt.Errorf("migration DSN required: --dsn, $MIGRATION_DATABASE_URL or $DATABASE_URL")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err := coredatabase.Preflight(ctx, dsn); err != nil {
					return err
				}
				applied, err := coredatabase.MigrateUp(ctx, dsn, coremigrations.FS)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "applied %d migration(s)\n", len(applied))
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show migration status",
			RunE: func(cmd *cobra.Command, args []string) error {
				dsn := mustMigrateDSN(cmd)
				if dsn == "" {
					return fmt.Errorf("migration DSN required: --dsn, $MIGRATION_DATABASE_URL or $DATABASE_URL")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err := coredatabase.Preflight(ctx, dsn); err != nil {
					return err
				}
				statuses, err := coredatabase.MigrationStatus(ctx, dsn, coremigrations.FS)
				if err != nil {
					return err
				}
				for _, s := range statuses {
					fmt.Fprintf(cmd.OutOrStdout(), "%d %s\n", s.Version, s.State)
				}
				return nil
			},
		},
	)
	return m
}

func mustDSN(cmd *cobra.Command) string {
	dsn, _ := cmd.Flags().GetString("dsn")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	return dsn
}

// mustMigrateDSN prefers the migration role: --dsn, then
// $MIGRATION_DATABASE_URL, then $DATABASE_URL as a last resort.
func mustMigrateDSN(cmd *cobra.Command) string {
	if dsn, _ := cmd.Flags().GetString("dsn"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("MIGRATION_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return os.Getenv("DATABASE_URL")
}
