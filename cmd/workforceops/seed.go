package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/PithomLabs/a1/internal/seed"
)

func seedCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "seed", Short: "Deterministic synthetic datasets"}
	demo := &cobra.Command{
		Use:   "demo",
		Short: "Load the Meridian demo dataset (synthetic; use --reset with --admin-dsn to reload)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, false, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			reset, _ := cmd.Flags().GetBool("reset")
			if reset {
				adminDSN, _ := cmd.Flags().GetString("admin-dsn")
				if adminDSN == "" {
					adminDSN = os.Getenv("SEED_ADMIN_DATABASE_URL")
				}
				if adminDSN == "" {
					return out(cmd, c, nil, cmdError("--reset requires --admin-dsn (or $SEED_ADMIN_DATABASE_URL)"))
				}
				target, _ := cmd.Flags().GetString("target")
				if target == "" {
					return out(cmd, c, nil, cmdError("--reset requires --target naming the database to recycle (retype to confirm)"))
				}
				rolePassword, _ := cmd.Flags().GetString("role-password")
				if rolePassword == "" {
					rolePassword = os.Getenv("A1_DEV_ROLE_PASSWORD")
				}
				appDSN, _ := cmd.Flags().GetString("dsn")
				if appDSN == "" {
					appDSN = os.Getenv("DATABASE_URL")
				}
				c.close()
				rctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if err := seed.Reset(rctx, appDSN, adminDSN, target, rolePassword); err != nil {
					return out(cmd, nil, nil, err)
				}
				c2, err := resolveCLI(cmd, false, true)
				if err != nil {
					return out(cmd, nil, nil, err)
				}
				defer c2.close()
				msg, err := seed.Demo(c2.ctx, c2.pool)
				return out(cmd, c2, map[string]string{"result": msg}, err)
			}
			msg, err := seed.Demo(c.ctx, c.pool)
			return out(cmd, c, map[string]string{"result": msg}, err)
		},
	}
	demo.Flags().Bool("reset", false, "recycle the demo database, then reload the MERIDIAN dataset")
	demo.Flags().String("admin-dsn", "", "privileged DSN for --reset (default $SEED_ADMIN_DATABASE_URL)")
	demo.Flags().String("target", "", "database name to recycle for --reset (must match --dsn; retype to confirm)")
	demo.Flags().String("role-password", "", "runtime role password for --reset on password-authenticated servers (default $A1_DEV_ROLE_PASSWORD)")
	cmd.AddCommand(demo)
	return cmd
}
