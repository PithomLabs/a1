package main

import (
	"os"

	"github.com/spf13/cobra"

	coreapi "github.com/PithomLabs/a1/core/api"
	"github.com/PithomLabs/a1/internal/httpapi"
)

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the REST API on loopback (use --port or $A1_API_PORT)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn, _ := cmd.Flags().GetString("dsn"); dsn != "" {
				os.Setenv("DATABASE_URL", dsn)
			}
			port, _ := cmd.Flags().GetInt("port")
			code := coreapi.Run(coreapi.ServiceSpec{
				Name:        "workforceops",
				PortEnv:     "API",
				DefaultPort: port,
				Bind:        "127.0.0.1",
				Register:    httpapi.Register,
			})
			if code != 0 {
				return cmdError("serve failed")
			}
			return nil
		},
	}
	cmd.Flags().Int("port", 8081, "loopback port")
	return cmd
}
