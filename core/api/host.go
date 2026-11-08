package api

import (
	"context"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/PithomLabs/a1/core/config"
	"github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/core/observability"
)

// ServiceSpec describes one API host process.
type ServiceSpec struct {
	Name string // e.g. "workforceops"
	// PortEnv selects the port override variable: A1_<PortEnv>_PORT.
	PortEnv     string
	DefaultPort int
	// Bind is the listen address; empty binds all interfaces.
	// Single-user loopback servers set "127.0.0.1".
	Bind string
	// Register mounts the process's routes onto the shared host.
	Register func(e *echo.Echo, pool *pgxpool.Pool) error
}

// Run boots a service: config, logging, database, routes, graceful shutdown.
// It returns the process exit code.
func Run(spec ServiceSpec) int {
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
	if err := database.RefusePrivilegedRuntime(ctx, pool); err != nil {
		slog.Error("refusing privileged runtime connection", "error", err)
		return 1
	}

	srv := New(cfg.IsProd())
	srv.AttachPool(pool)
	if err := spec.Register(srv.Echo, pool); err != nil {
		slog.Error("route registration failed", "error", err)
		return 1
	}
	addr := fmt.Sprintf("%s:%d", spec.Bind, config.Port(spec.PortEnv, spec.DefaultPort))
	slog.Info("starting service", "service", spec.Name, "addr", addr)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(addr) }()
	select {
	case <-ctx.Done():
		stop()
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			slog.Error("shutdown error", "error", err)
			return 1
		}
		slog.Info("stopped", "service", spec.Name)
		return 0
	case err := <-errCh:
		if err != nil {
			slog.Error("server error", "error", err)
			return 1
		}
		return 0
	}
}
