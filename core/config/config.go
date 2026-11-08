// Package config loads explicit environment-first configuration for a1 processes.
package config

import (
	"errors"
	"os"
	"strconv"
)

// Config holds process configuration. Use Load; the zero value is not usable.
type Config struct {
	Env         string // dev (default) or prod
	LogFormat   string // json (default) or text
	LogLevel    string // debug, info (default), warn, error
	DatabaseURL string
	JWTSecret   string // enforced when auth is enabled (Phase 4+)
}

// Load reads configuration from the environment.
func Load() Config {
	return Config{
		Env:         envOr("A1_ENV", "dev"),
		LogFormat:   envOr("A1_LOG_FORMAT", "json"),
		LogLevel:    envOr("A1_LOG_LEVEL", "info"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("A1_JWT_SECRET"),
	}
}

// IsProd reports whether the process runs with production behaviour.
func (c Config) IsProd() bool { return c.Env == "prod" }

// ValidateForServing ensures the config can boot an API host or worker.
func (c Config) ValidateForServing() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	return nil
}

// Port reads A1_<NAME>_PORT, returning def for missing or invalid values.
func Port(name string, def int) int {
	v := os.Getenv("A1_" + name + "_PORT")
	if v == "" {
		return def
	}
	p, err := strconv.Atoi(v)
	if err != nil || p <= 0 || p > 65535 {
		return def
	}
	return p
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
