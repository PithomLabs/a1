// Package api provides the shared Echo host for a1 HTTP processes.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Server is a configured Echo host with liveness/readiness probes.
type Server struct {
	Echo *echo.Echo
	pool *pgxpool.Pool
}

// New builds a host. In prod, client error bodies stay generic while details
// are logged server-side (ported from bchat's production error handler).
func New(prod bool) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod:    true,
		LogURI:       true,
		LogStatus:    true,
		LogLatency:   true,
		LogRemoteIP:  true,
		LogRequestID: true,
		LogValuesFunc: func(_ echo.Context, v middleware.RequestLoggerValues) error {
			slog.Info("request",
				"request_id", v.RequestID,
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
				"latency_ms", v.Latency.Milliseconds(),
				"remote", v.RemoteIP,
			)
			return nil
		},
	}))
	if prod {
		e.HTTPErrorHandler = func(err error, c echo.Context) {
			slog.Error("request error",
				"error", err,
				"path", c.Request().URL.Path,
				"method", c.Request().Method,
			)
			code := http.StatusInternalServerError
			if he, ok := err.(*echo.HTTPError); ok {
				code = he.Code
			}
			if !c.Response().Committed {
				_ = c.JSON(code, map[string]string{"error": "Internal server error"})
			}
		}
	}

	s := &Server{Echo: e}
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	e.GET("/readyz", s.readyz)
	return s
}

// AttachPool enables the database readiness check.
func (s *Server) AttachPool(pool *pgxpool.Pool) { s.pool = pool }

func (s *Server) readyz(c echo.Context) error {
	if s.pool == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "no database pool"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "database unreachable"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
}

// Start blocks serving addr.
func (s *Server) Start(addr string) error {
	if err := s.Echo.Start(addr); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown stops the host gracefully.
func (s *Server) Shutdown(ctx context.Context) error { return s.Echo.Shutdown(ctx) }
