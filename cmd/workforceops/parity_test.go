// Package main parity test: the CLI and REST adapters reach the same
// services and the same rows. Each leg writes through one adapter and
// reads through the other against one real PostgreSQL database.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/httpapi"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

func parityPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	migrate := func(ctx context.Context, migDSN string) error {
		_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
		return err
	}
	setup := dbutil.Setup(t, migrate)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := coredatabase.Open(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		pool.Close()
		t.Fatalf("suite pool runs as %q, want app role", who)
	}
	t.Cleanup(pool.Close)
	return pool
}

func parityCtx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

// runCLI executes the root command in-process and returns stdout.
func runCLI(t *testing.T, dsn string, args ...string) string {
	t.Helper()
	root := rootCmd()
	full := append([]string{"--dsn", dsn, "--format", "json", "--operator", "parity"}, args...)
	root.SetArgs(full)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("cli %v: %v\nstderr: %s", args, err, stderr.String())
	}
	return stdout.String()
}

func writeJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	return p
}

func restServer(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	e := echo.New()
	if err := httpapi.Register(e, pool); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv.URL
}

func restDo(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Operator", "parity")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s %s: non-JSON %d: %s", method, url, resp.StatusCode, raw)
	}
	m, _ := v.(map[string]any)
	if resp.StatusCode >= 400 {
		t.Fatalf("%s %s: %d %s", method, url, resp.StatusCode, raw)
	}
	return resp.StatusCode, m
}

func TestCLIAPIParity(t *testing.T) {
	pool := parityPool(t)
	dsn := dbutil.Setup(t, nil).AppDSN
	code := fmt.Sprintf("PARITY_%d", time.Now().UnixNano())
	ctx := parityCtx(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr')`,
		code, code, "UEN-"+code); err != nil {
		t.Fatalf("mkEmployer: %v", err)
	}
	base := restServer(t, pool) + "/api/v1/employers/" + code

	// Leg 1: create employee via CLI, read via REST.
	empFile := writeJSON(t, map[string]any{
		"employee_no": "E-001", "legal_name": "Parity Employee", "address": "a",
		"gender": "F", "date_of_birth": "1990-01-01", "citizenship": "SG",
		"status_effective_date": "2026-01-01",
	})
	cliOut := runCLI(t, dsn, "--employer", code, "employee", "create", "--file", empFile)
	var created struct {
		ID         string `json:"id"`
		EmployeeNo string `json:"employee_no"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal([]byte(cliOut), &created); err != nil {
		t.Fatalf("parse cli employee: %v\n%s", err, cliOut)
	}
	_, got := restDo(t, "GET", base+"/employees/"+created.ID, nil)
	if got["employee_no"] != "E-001" || got["legal_name"] != "Parity Employee" {
		t.Fatalf("REST read of CLI-created employee: %v", got)
	}

	// Leg 2: status change via REST, observed via CLI.
	effective := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	_, upd := restDo(t, "PATCH", base+"/employees/"+created.ID, map[string]any{
		"status": "suspended", "reason": "parity", "effective": effective,
	})
	if upd["status"] != "suspended" {
		t.Fatalf("REST status update: %v", upd)
	}
	listOut := runCLI(t, dsn, "--employer", code, "employee", "list")
	if !strings.Contains(listOut, "E-001") || !strings.Contains(listOut, "suspended") {
		t.Fatalf("CLI list after REST update: %s", listOut)
	}

	// Leg 3: catalogue via CLI, read via REST.
	runCLI(t, dsn, "--employer", code, "client", "create", "--file",
		writeJSON(t, map[string]any{"code": "C1", "name": "Client One"}))
	runCLI(t, dsn, "--employer", code, "programme", "create", "--file",
		writeJSON(t, map[string]any{"client": "C1", "code": "P1", "name": "Prog One", "timezone": "Asia/Singapore"}))
	// programmes list is a bare array; fetch directly.
	req, _ := http.NewRequest("GET", base+"/programmes", nil)
	req.Header.Set("X-Operator", "parity")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET programmes: %v", err)
	}
	defer resp.Body.Close()
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		t.Fatalf("decode programmes: %v", err)
	}
	if len(arr) != 1 || arr[0]["code"] != "P1" {
		t.Fatalf("REST programmes after CLI creates: %v", arr)
	}

	// Leg 4: roster via REST, staffed, published via CLI, status via REST.
	_, roster := restDo(t, "POST", base+"/rosters", map[string]any{
		"programme": "P1", "week_starting": "2026-08-03",
	})
	rid, _ := roster["id"].(string)
	if rid == "" {
		t.Fatalf("REST roster create: %v", roster)
	}
	runCLI(t, dsn, "--employer", code, "team", "create", "--file",
		writeJSON(t, map[string]any{"programme": "P1", "code": "T1", "name": "Team One"}))
	runCLI(t, dsn, "--employer", code, "team", "add-member", "T1", "E-001",
		"--role", "agent", "--from", "2026-08-01")
	_, _ = restDo(t, "POST", base+"/rosters/"+rid+"/assignments", map[string]any{
		"employee": "E-001", "date": "2026-08-05", "rest_day": true,
	})
	runCLI(t, dsn, "--employer", code, "roster", "publish", rid)
	_, shown := restDo(t, "GET", base+"/rosters/"+rid, nil)
	nested, _ := shown["roster"].(map[string]any)
	if nested["status"] != "published" {
		t.Fatalf("REST roster after CLI publish: %v", shown)
	}
}
