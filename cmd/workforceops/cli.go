package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/employer"
)

// cli holds resolved invocation context for one command run.
type cli struct {
	pool     *pgxpool.Pool
	employer *employer.Employer
	format   string
	operator string
	ctx      context.Context
	cancel   context.CancelFunc
}

func resolveCLI(cmd *cobra.Command, needEmployer, needOperator bool) (*cli, error) {
	dsn, _ := cmd.Flags().GetString("dsn")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return nil, fmt.Errorf("database DSN required: --dsn or $DATABASE_URL")
	}
	format, _ := cmd.Flags().GetString("format")
	if format != "json" && format != "text" {
		return nil, fmt.Errorf("format must be json or text")
	}
	// Mutations attribute to --operator or the invoking OS user, never
	// to a silent generic label. Reads keep a harmless default.
	operator, _ := cmd.Flags().GetString("operator")
	if operator == "" {
		operator = os.Getenv("USER")
	}
	if operator == "" && needOperator {
		return nil, fmt.Errorf("operator required: --operator or $USER")
	}
	if operator == "" {
		operator = "cli"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	pool, err := coredatabase.Open(ctx, dsn)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		cancel()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	if err := coredatabase.RefusePrivilegedRuntime(ctx, pool); err != nil {
		pool.Close()
		cancel()
		return nil, err
	}
	c := &cli{pool: pool, format: format, operator: operator, ctx: ctx, cancel: cancel}
	if needEmployer {
		code, _ := cmd.Flags().GetString("employer")
		e, err := employer.Resolve(ctx, pool, code)
		if err != nil {
			c.close()
			return nil, err
		}
		c.employer = e
	}
	return c, nil
}

func (c *cli) close() {
	c.cancel()
	c.pool.Close()
}

// out renders a result or a machine-readable error envelope.
func out(cmd *cobra.Command, c *cli, v any, err error) error {
	format := "json"
	if c != nil {
		format = c.format
	} else if f, ferr := cmd.Flags().GetString("format"); ferr == nil && f == "text" {
		format = f
	}
	if err != nil {
		code := apperr.AsCode(err)
		if format == "json" {
			payload, _ := json.Marshal(map[string]string{"error": code, "message": errMsg(err)})
			fmt.Fprintln(cmd.ErrOrStderr(), string(payload))
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "error [%s]: %s\n", code, errMsg(err))
		}
		return err
	}
	if v == nil {
		return nil
	}
	if format == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%v\n", v)
	return nil
}

func errMsg(err error) string {
	if ae, ok := err.(*apperr.Error); ok {
		return ae.Message
	}
	return err.Error()
}

// readJSONFile loads a JSON document from --file for file-based inputs.
func readJSONFile(cmd *cobra.Command, v any) error {
	path, _ := cmd.Flags().GetString("file")
	if path == "" {
		return fmt.Errorf("--file is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
