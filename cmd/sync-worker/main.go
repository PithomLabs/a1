// Command sync-worker runs scheduled sync, retry and reconciliation jobs.
// Job execution arrives in Phase 3; this skeleton validates config, database
// connectivity and graceful shutdown.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/PithomLabs/a1/core/config"
	"github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/core/observability"
)

func main() { os.Exit(run()) }

func run() int {
	cfg := config.Load()
	observability.Setup(cfg.LogFormat, cfg.LogLevel)
	if err := cfg.ValidateForServing(); err != nil {
		slog.Error("invalid configuration", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "error", err)
		return 1
	}
	defer pool.Close()

	slog.Info("sync worker started", "jobs", "none registered until Phase 3")
	<-ctx.Done()
	slog.Info("sync worker stopped")
	return 0
}
