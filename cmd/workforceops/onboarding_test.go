package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// D3: employer onboarding works without SQL or seed demo — CLI create
// and show plus POST /api/v1/employers — against a fresh database,
// with repeats safe and second codes rejected.

func onboardDB(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	dsn := dbutil.DSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	name := cfg.ConnConfig.Database + "_onboard"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	maint, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer maint.Close(context.Background())
	if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	setup, err := dbutil.SetupDB(ctx, dsn, name,
		func(ctx context.Context, migDSN string) error {
			_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		t.Fatalf("setup %s: %v", name, err)
	}
	pool, err := pgxpool.New(ctx, setup.AppDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return setup.AppDSN, pool
}

// runCLIErr runs the CLI in-process like runCLI but returns failures
// instead of failing, for negative-path assertions.
func runCLIErr(t *testing.T, dsn string, args ...string) (string, error, string) {
	t.Helper()
	root := rootCmd()
	full := append([]string{"--dsn", dsn, "--format", "json", "--operator", "onboard"}, args...)
	root.SetArgs(full)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	err := root.Execute()
	return stdout.String(), err, stderr.String()
}

func restPost(t *testing.T, url string, body any, operator string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if operator != "" {
		req.Header.Set("X-Operator", operator)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("POST %s: non-JSON %d: %s", url, resp.StatusCode, out)
	}
	m, _ := v.(map[string]any)
	return resp.StatusCode, m
}

func onboardInput() map[string]any {
	return map[string]any{
		"code": "MERIDIAN", "name": "Meridian BPO", "uen": "UEN-M1",
		"address": "1 Test Row", "jurisdiction": "SG",
	}
}

func TestEmployerOnboardingCLI(t *testing.T) {
	dsn, pool := onboardDB(t)
	file := writeJSON(t, onboardInput())
	stdout, err, _ := runCLIErr(t, dsn, "employer", "create", "--file", file)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created struct {
		Employer struct {
			Code string `json:"code"`
		} `json:"employer"`
		Existed bool `json:"existed"`
	}
	if err := json.Unmarshal([]byte(stdout), &created); err != nil {
		t.Fatalf("decode create: %v\n%s", err, stdout)
	}
	if created.Existed || created.Employer.Code != "MERIDIAN" {
		t.Fatalf("create = %+v", created)
	}
	stdout, err, _ = runCLIErr(t, dsn, "employer", "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(stdout, "MERIDIAN") {
		t.Fatalf("show = %s", stdout)
	}
	// Safe repeat: same code returns the existing row flagged.
	stdout, err, _ = runCLIErr(t, dsn, "employer", "create", "--file", file)
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	var again struct {
		Existed bool `json:"existed"`
	}
	if err := json.Unmarshal([]byte(stdout), &again); err != nil || !again.Existed {
		t.Fatalf("repeat = %s, err = %v, want existed=true", stdout, err)
	}
	// Second code rejected, exactly one row survives.
	other := onboardInput()
	other["code"] = "OTHER"
	other["uen"] = "UEN-O1"
	stdout, err, stderr := runCLIErr(t, dsn, "employer", "create", "--file", writeJSON(t, other))
	if err == nil {
		t.Fatalf("second code accepted: %s", stdout)
	}
	if !strings.Contains(stderr, "conflict") {
		t.Fatalf("stderr = %s, want conflict", stderr)
	}
	var n int
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM employer`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("employers = %d, err = %v", n, err)
	}
}

func TestEmployerOnboardingREST(t *testing.T) {
	_, pool := onboardDB(t)
	base := restServer(t, pool)
	code, body := restPost(t, base+"/api/v1/employers", onboardInput(), "onboard")
	if code != http.StatusCreated {
		t.Fatalf("POST = %d %v, want 201", code, body)
	}
	if body["existed"] == true {
		t.Fatalf("POST = %v, want existed=false", body)
	}
	code, body = restPost(t, base+"/api/v1/employers", onboardInput(), "onboard")
	if code != http.StatusOK || body["existed"] != true {
		t.Fatalf("repeat = %d %v, want 200 existed=true", code, body)
	}
	other := onboardInput()
	other["code"] = "OTHER"
	other["uen"] = "UEN-O1"
	code, body = restPost(t, base+"/api/v1/employers", other, "onboard")
	if code != http.StatusConflict {
		t.Fatalf("second code = %d %v, want 409", code, body)
	}
	code, _ = restPost(t, base+"/api/v1/employers", onboardInput(), "")
	if code != http.StatusBadRequest && code != http.StatusUnprocessableEntity && code != http.StatusUnauthorized {
		t.Fatalf("missing operator = %d, want a 4xx rejection", code)
	}
}
