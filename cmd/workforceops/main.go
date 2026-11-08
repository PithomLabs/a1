// Command workforceops is the canonical automation interface: migrations,
// seeding, oracle runs, scenarios, investigations, evaluation and demos.
// Only migrate and version are implemented in Phase 1; the rest arrive with
// their phases and fail with an explicit not-implemented error until then.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
)

// version is overridden at link time (task build sets it from git describe).
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "workforceops",
		Short: "WorkforceOps Lab automation: migrate, seed, simulate, investigate, evaluate",
	}
	root.PersistentFlags().String("dsn", "", "database DSN (default: MIGRATION_DATABASE_URL, then DATABASE_URL)")
	_ = viper.BindPFlag("dsn", root.PersistentFlags().Lookup("dsn"))
	viper.AutomaticEnv()

	root.AddCommand(versionCmd(), migrateCmd())
	root.AddCommand(stubCmd("seed", "Generate the deterministic synthetic dataset", "Phase 2"))
	root.AddCommand(stubCmd("oracle", "Run the independent reference oracle", "Phase 2"))
	root.AddCommand(stubCmd("scenario", "List, run and reset incident scenarios", "Phase 3"))
	root.AddCommand(stubCmd("investigate", "Run a CaseTrace investigation", "Phase 4"))
	root.AddCommand(stubCmd("evaluate", "Score investigations against ground truth", "Phase 5"))
	root.AddCommand(stubCmd("demo", "Run the reproducible end-to-end demonstration", "Phase 6"))
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "workforceops %s\n", version)
		},
	}
}

func stubCmd(use, short, phase string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("%s: not implemented until %s", use, phase)
		},
	}
}

func migrateCmd() *cobra.Command {
	m := &cobra.Command{Use: "migrate", Short: "Apply or inspect core database migrations"}
	m.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "Apply pending core migrations",
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
				defer cancel()
				applied, err := database.MigrateUp(ctx, resolveDSN(), coremigrations.FS)
				if err != nil {
					return err
				}
				if len(applied) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "already up to date")
					return nil
				}
				for _, a := range applied {
					fmt.Fprintf(cmd.OutOrStdout(), "applied %d %s\n", a.Version, a.Path)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show recorded core migration status",
			RunE: func(cmd *cobra.Command, _ []string) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
				defer cancel()
				rows, err := database.MigrationStatus(ctx, resolveDSN(), coremigrations.FS)
				if err != nil {
					return err
				}
				for _, r := range rows {
					fmt.Fprintf(cmd.OutOrStdout(), "%d %s %s\n", r.Version, r.State, r.Path)
				}
				return nil
			},
		},
	)
	return m
}

// resolveDSN prefers --dsn, then MIGRATION_DATABASE_URL, then DATABASE_URL.
func resolveDSN() string {
	if v := viper.GetString("dsn"); v != "" {
		return v
	}
	if v := os.Getenv("MIGRATION_DATABASE_URL"); v != "" {
		return v
	}
	return os.Getenv("DATABASE_URL")
}
