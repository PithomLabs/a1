package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/employer"
	"github.com/PithomLabs/a1/internal/rules"
)

// Elevated-privilege operations, kept separate from schema migration.
//
// activate-rule-pack connects with a privileged DSN (migration_admin)
// and is the only path that can move a rule pack to IMPLEMENTABLE.
// The operator label is attribution, not authentication: whoever can
// invoke this command with the privileged DSN can activate any pack
// whose registry pin the supplied evidence matches. Guard the DSN
// accordingly; it must never be the application's runtime credential.
func adminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Elevated administrative operations (privileged DSN required)",
	}
	cmd.AddCommand(adminActivateCmd())
	return cmd
}

func adminActivateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activate-rule-pack",
		Short: "Activate a reviewed rule pack from its on-disk package",
		Long: `Verify a rule-pack directory against the evidence lifecycle and record the activation.

The pack must claim IMPLEMENTABLE with pinned sources, present spec, review and
golden-fixture files, a spec without unresolved load-bearing markers, and a
complete independent-review record. The bundle digest is recomputed and must
match the registry's reviewed pin exactly.

Requires --admin-dsn with a privileged role (migration_admin). The --operator
label attributes the activation; it does not authenticate it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminActivate(cmd)
		},
	}
	cmd.Flags().String("pack-dir", "", "rule-pack directory holding manifest.json (required)")
	cmd.Flags().String("scope", "", "rule-pack scope, must match the registry (required)")
	cmd.Flags().String("reason", "", "reason for activation (required)")
	cmd.Flags().String("admin-dsn", "", "privileged DSN (default $ACTIVATION_ADMIN_DATABASE_URL)")
	return cmd
}

func runAdminActivate(cmd *cobra.Command) error {
	packDir, _ := cmd.Flags().GetString("pack-dir")
	if packDir == "" {
		return out(cmd, nil, nil, cmdError("--pack-dir is required"))
	}
	scope, _ := cmd.Flags().GetString("scope")
	if scope == "" {
		return out(cmd, nil, nil, cmdError("--scope is required"))
	}
	reason, _ := cmd.Flags().GetString("reason")
	if reason == "" {
		return out(cmd, nil, nil, cmdError("--reason is required"))
	}
	operator, _ := cmd.Flags().GetString("operator")
	if operator == "" {
		operator = os.Getenv("USER")
	}
	if operator == "" {
		return out(cmd, nil, nil, cmdError("operator required: --operator or $USER"))
	}
	adminDSN, _ := cmd.Flags().GetString("admin-dsn")
	if adminDSN == "" {
		adminDSN = os.Getenv("ACTIVATION_ADMIN_DATABASE_URL")
	}
	if adminDSN == "" {
		return out(cmd, nil, nil, cmdError("--admin-dsn is required (or $ACTIVATION_ADMIN_DATABASE_URL)"))
	}
	// Evidence first: never open a privileged connection for a pack
	// that fails verification.
	m, err := rules.LoadManifest(packDir)
	if err != nil {
		return out(cmd, nil, nil, err)
	}
	ev, err := m.Verify(packDir)
	if err != nil {
		return out(cmd, nil, nil, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := coredatabase.Open(ctx, adminDSN)
	if err != nil {
		return out(cmd, nil, nil, err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return out(cmd, nil, nil, fmt.Errorf("database unreachable: %w", err))
	}
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil {
		return out(cmd, nil, nil, err)
	}
	if who == "app" {
		return out(cmd, nil, nil, cmdError("activation requires a privileged connection, not the app role"))
	}
	code, _ := cmd.Flags().GetString("employer")
	e, err := employer.Resolve(ctx, pool, code)
	if err != nil {
		return out(cmd, nil, nil, err)
	}
	format, _ := cmd.Flags().GetString("format")
	c := &cli{pool: pool, employer: e, format: format, operator: operator, ctx: ctx, cancel: cancel}
	id, err := rules.Activate(ctx, pool, e.ID, scope, ev, operator, reason)
	return out(cmd, c, map[string]string{
		"activation_id": id,
		"pack":          ev.Pack,
		"version":       ev.Version,
		"status":        "IMPLEMENTABLE",
	}, err)
}
