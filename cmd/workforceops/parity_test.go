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

// The M2 legs: payroll-run validation and SHG governance reach the
// same services and rows through either adapter. Each leg writes
// through one adapter and reads through the other against one real
// PostgreSQL database. Pack IMPLEMENTABLE states are fixture setup
// through the admin DSN only; every operation under test runs as app
// through CLI or REST.
func TestCLIAPIParityM2(t *testing.T) {
	pool := parityPool(t)
	setup := dbutil.Setup(t, nil)
	dsn := setup.AppDSN
	admin, err := pgxpool.New(parityCtx(t), setup.AdminDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(admin.Close)
	code := fmt.Sprintf("PARITY_M2_%d", time.Now().UnixNano())
	ctx := parityCtx(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr')`,
		code, code, "UEN-"+code); err != nil {
		t.Fatalf("mkEmployer: %v", err)
	}
	base := restServer(t, pool) + "/api/v1/employers/" + code
	cli := func(args ...string) string {
		return runCLI(t, dsn, append([]string{"--employer", code}, args...)...)
	}

	// Period via CLI.
	cli("pay-period", "create", "--file", writeJSON(t, map[string]any{
		"code": "PP", "starts_on": "2026-08-01", "ends_on": "2026-08-31",
		"pay_date": "2026-09-07", "salary_deadline": "2026-09-07", "ot_deadline": "2026-09-14",
	}))

	// Employee via REST, terms via CLI.
	_, e1 := restDo(t, "POST", base+"/employees", map[string]any{
		"employee_no": "E-001", "legal_name": "M2 Employee", "address": "a",
		"gender": "F", "date_of_birth": "1990-01-01", "citizenship": "SG",
		"status_effective_date": "2026-01-01",
	})
	if e1["employee_no"] != "E-001" {
		t.Fatalf("REST employee create: %v", e1)
	}
	cli("employee", "terms", "set", "E-001", "--file", writeJSON(t, map[string]any{
		"effective_from": "2026-01-01", "job_title": "Agent", "job_duties": "support",
		"employment_category": "workman", "part_iv_eligible": true, "eligibility_basis": "test",
		"monthly_basic": "2600", "working_arrangement": "shift", "normal_hours_weekly": 44,
		"working_days": "Mon-Fri", "daily_hours": 8.8, "rest_day": "Sunday",
		"start_date": "2026-01-01", "contract_type": "permanent",
		"place_of_work": "office", "salary_period": "monthly",
	}))

	// SHG instruction via CLI, read back via REST.
	shgOut := cli("employee", "shg", "E-001", "--file", writeJSON(t, map[string]any{
		"fund": "CDAC", "status": "active", "amount_or_basis": "fixed",
		"source_declaration": "parity form", "effective_from": "2026-01-01",
	}))
	var shg struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(shgOut), &shg); err != nil {
		t.Fatalf("parse cli shg: %v\n%s", err, shgOut)
	}
	_, shgList := restDo(t, "GET", base+"/employees/E-001/shg", nil)
	_ = shgList

	// Roster via CLI: the day needs a roster link and breaks.
	cli("client", "create", "--file", writeJSON(t, map[string]any{"code": "C1", "name": "Client One"}))
	cli("programme", "create", "--file", writeJSON(t, map[string]any{"client": "C1", "code": "P1", "name": "Prog One", "timezone": "Asia/Singapore"}))
	cli("team", "create", "--file", writeJSON(t, map[string]any{"programme": "P1", "code": "T1", "name": "Team One"}))
	cli("team", "add-member", "T1", "E-001", "--role", "agent", "--from", "2026-08-01")
	cli("shift-template", "create", "--file", writeJSON(t, map[string]any{
		"code": "DAY", "name": "Day shift", "start_local": "09:00", "end_local": "18:00",
		"segments": []map[string]any{
			{"kind": "work", "start_offset_min": 0, "duration_min": 180, "paid": true},
			{"kind": "break_unpaid", "start_offset_min": 180, "duration_min": 60, "paid": false},
			{"kind": "work", "start_offset_min": 240, "duration_min": 300, "paid": true},
		},
	}))
	rosterOut := cli("roster", "create", "--file", writeJSON(t, map[string]any{"programme": "P1", "week_starting": "2026-08-03"}))
	var roster struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(rosterOut), &roster); err != nil {
		t.Fatalf("parse cli roster: %v\n%s", err, rosterOut)
	}
	cli("roster", "assign", roster.ID, "--file", writeJSON(t, map[string]any{
		"employee": "E-001", "date": "2026-08-03", "template": "DAY",
	}))
	cli("roster", "publish", roster.ID)

	// Clock evidence via CLI import, reviewed via REST.
	csvPath := filepath.Join(t.TempDir(), "clocks.csv")
	csvBody := "source,source_event_id,employee_no,occurred_at,timezone,kind\n" +
		"parity,p1,E-001,2026-08-03T09:00:00+08:00,Asia/Singapore,in\n" +
		"parity,p2,E-001,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start\n" +
		"parity,p3,E-001,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end\n" +
		"parity,p4,E-001,2026-08-03T18:00:00+08:00,Asia/Singapore,out\n"
	if err := os.WriteFile(csvPath, []byte(csvBody), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	cli("attendance", "import", "--file", csvPath)
	_, rev := restDo(t, "GET", base+"/attendance?from=2026-08-01&to=2026-08-31", nil)
	_ = rev

	// Timesheet generated via REST, decided via CLI.
	_, gen := restDo(t, "POST", base+"/timesheets/generate", map[string]any{"pay_period": "PP"})
	generated, _ := gen["generated"].([]any)
	if len(generated) != 1 {
		t.Fatalf("REST generate: %v", gen)
	}
	sheet, _ := generated[0].(map[string]any)["timesheet_id"].(string)
	if sheet == "" {
		t.Fatalf("REST generate: %v", gen)
	}
	cli("timesheet", "submit", sheet)
	cli("timesheet", "approve", sheet)

	// Second employee via REST, excluded from the run via CLI.
	_, e2 := restDo(t, "POST", base+"/employees", map[string]any{
		"employee_no": "E-002", "legal_name": "Excluded", "address": "a",
		"gender": "F", "date_of_birth": "1990-01-01", "citizenship": "SG",
		"status_effective_date": "2026-01-01",
	})
	if e2["employee_no"] != "E-002" {
		t.Fatalf("REST employee create: %v", e2)
	}

	// Pack states are privileged fixture setup, never an operation
	// under test.
	for _, p := range []string{"SG-CPF-2026", "SG-SDL", "SG-SHG", "SG-MOM-KET"} {
		if _, err := admin.Exec(ctx,
			`INSERT INTO rule_pack_activation (employer_id, pack, version, status, activated_at)
			 VALUES ((SELECT id FROM employer WHERE code = $1), $2, 'v0', 'IMPLEMENTABLE', now())
			 ON CONFLICT (employer_id, pack) DO UPDATE SET status = 'IMPLEMENTABLE'`, code, p); err != nil {
			t.Fatalf("pack %s: %v", p, err)
		}
	}

	// Run created via CLI, validated via REST.
	runOut := cli("payroll", "run-create", "PP")
	var run struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(runOut), &run); err != nil {
		t.Fatalf("parse cli run: %v\n%s", err, runOut)
	}
	cli("payroll", "exclude", run.ID, "E-002", "--reason", "parity exclusion")
	_, ready := restDo(t, "GET", base+"/payroll-readiness?pay_period=PP&run="+run.ID, nil)
	if ready["ready"] != true {
		t.Fatalf("REST readiness: %v", ready)
	}
	_, verdict := restDo(t, "POST", base+"/payroll-runs/"+run.ID+"/validate", map[string]any{"offered_version": 0})
	if verdict["valid"] != true {
		t.Fatalf("REST validate: %v", verdict)
	}
	tip, _ := verdict["snapshot_id"].(string)
	if tip == "" {
		t.Fatalf("REST validate: %v", verdict)
	}

	// Run inspection via CLI names the same tip.
	showOut := cli("payroll", "run-show", run.ID)
	var shown struct {
		Status string `json:"status"`
		Valid  *struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
			Hash    string `json:"content_hash"`
		} `json:"valid_snapshot"`
		Excluded []struct {
			EmployeeNo string `json:"employee_no"`
		} `json:"exclusions"`
	}
	if err := json.Unmarshal([]byte(showOut), &shown); err != nil {
		t.Fatalf("parse cli run-show: %v\n%s", err, showOut)
	}
	if shown.Status != "validated" || shown.Valid == nil || shown.Valid.ID != tip {
		t.Fatalf("CLI run-show: %s", showOut)
	}
	if len(shown.Excluded) != 1 || shown.Excluded[0].EmployeeNo != "E-002" {
		t.Fatalf("CLI run-show exclusions: %s", showOut)
	}

	// Snapshot inspection agrees across adapters.
	snapOut := cli("payroll", "snapshot-show", tip)
	var snapCLI struct {
		Version int    `json:"version"`
		Hash    string `json:"content_hash"`
	}
	if err := json.Unmarshal([]byte(snapOut), &snapCLI); err != nil {
		t.Fatalf("parse cli snapshot-show: %v\n%s", err, snapOut)
	}
	_, snapREST := restDo(t, "GET", base+"/snapshots/"+tip, nil)
	if snapREST["content_hash"] != snapCLI.Hash || snapREST["content_hash"] != shown.Valid.Hash {
		t.Fatalf("snapshot hash mismatch: cli=%v rest=%v show=%v", snapCLI.Hash, snapREST["content_hash"], shown.Valid.Hash)
	}

	// Rule-pack status agrees across adapters.
	packsOut := cli("payroll", "rule-packs")
	var packsCLI []map[string]any
	if err := json.Unmarshal([]byte(packsOut), &packsCLI); err != nil {
		t.Fatalf("parse cli rule-packs: %v\n%s", err, packsOut)
	}
	req, _ := http.NewRequest("GET", base+"/rule-activations", nil)
	req.Header.Set("X-Operator", "parity")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET rule-activations: %v", err)
	}
	defer resp.Body.Close()
	var packsREST []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&packsREST); err != nil {
		t.Fatalf("decode rule-activations: %v", err)
	}
	if len(packsCLI) != len(packsREST) || len(packsREST) == 0 {
		t.Fatalf("pack status lengths: cli=%d rest=%d", len(packsCLI), len(packsREST))
	}
	byPack := map[string]any{}
	for _, p := range packsREST {
		byPack[p["pack"].(string)] = p["status"]
	}
	for _, p := range packsCLI {
		if byPack[p["pack"].(string)] != p["status"] {
			t.Fatalf("pack status mismatch: cli=%v rest=%v", packsCLI, packsREST)
		}
	}

	// SHG verify via CLI on a REST-recorded pending instruction,
	// then correction via REST: the excluded employee's chain never
	// touches the validated run.
	_, pend := restDo(t, "POST", base+"/employees/E-002/shg", map[string]any{
		"fund": "SINDA", "status": "pending", "amount_or_basis": "fixed",
		"source_declaration": "parity form", "effective_from": "2026-01-01",
	})
	pendID, _ := pend["id"].(string)
	if pendID == "" {
		t.Fatalf("REST shg record: %v", pend)
	}
	verifyOut := cli("employee", "shg-verify", pendID, "--file", writeJSON(t, map[string]any{
		"source_declaration": "parity form", "effective_from": "2026-01-01",
		"verified_at": "2026-08-15T00:00:00Z", "verified_by": "parity",
		"verification_basis": "parity evidence",
	}))
	var verified struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		Supersedes string `json:"supersedes_id"`
		VerifiedBy string `json:"verified_by"`
	}
	if err := json.Unmarshal([]byte(verifyOut), &verified); err != nil {
		t.Fatalf("parse cli shg-verify: %v\n%s", err, verifyOut)
	}
	if verified.Supersedes != pendID || verified.VerifiedBy != "parity" {
		t.Fatalf("CLI shg-verify: %s", verifyOut)
	}
	_, succ := restDo(t, "POST", base+"/employees/E-002/shg/"+verified.ID+"/correct", map[string]any{
		"status": "active", "amount_or_basis": "fixed",
		"source_declaration": "parity correction", "effective_from": "2026-02-01",
	})
	if succ["id"] == "" || succ["id"] == verified.ID || succ["supersedes_id"] != verified.ID {
		t.Fatalf("REST shg correct: %v", succ)
	}
	// The validated run still stands on its tip, untouched.
	showAgain := cli("payroll", "run-show", run.ID)
	if !strings.Contains(showAgain, tip) || !strings.Contains(showAgain, "validated") {
		t.Fatalf("CLI run-show after SHG chain: %s", showAgain)
	}
}
