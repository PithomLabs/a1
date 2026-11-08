package payroll

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// Run is one payroll run: the exclusion container readiness can be
// scoped by. It carries no calculations in this milestone.
type Run struct {
	ID         string `json:"id"`
	EmployerID string `json:"employer_id"`
	PeriodID   string `json:"pay_period_id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
}

// CreateRun opens a draft run for the period. inputs_hash is a
// deterministic placeholder over employer, period and kind until
// payroll calculation exists; rule versions stay empty and the calc
// version names the absence honestly.
func CreateRun(ctx context.Context, pool *pgxpool.Pool, employerID, periodCodeOrID, kind, operator string) (*Run, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if kind == "" {
		kind = "regular"
	}
	if kind != "regular" && kind != "correction" {
		return nil, apperr.New(apperr.CodeValidation, "kind must be regular or correction")
	}
	var periodID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM pay_period WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, periodCodeOrID).Scan(&periodID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "pay period %s not found", periodCodeOrID)
		}
		return nil, apperr.FromDB(err, "resolve pay period")
	}
	sum := sha256.Sum256([]byte(employerID + ":" + periodID + ":" + kind))
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin payroll run")
	}
	defer tx.Rollback(ctx)
	var r Run
	err = tx.QueryRow(ctx,
		`INSERT INTO payroll_run (id, employer_id, pay_period_id, kind, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, '{}', 'none (milestone 1.1: no calculations)', '{}')
		 RETURNING id, employer_id, pay_period_id, kind, status`,
		employerID, periodID, kind, hex.EncodeToString(sum[:])).Scan(
		&r.ID, &r.EmployerID, &r.PeriodID, &r.Kind, &r.Status)
	if err != nil {
		return nil, apperr.FromDB(err, "create payroll run")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "payroll",
		"payroll.run_create", "payroll_run", &r.ID, nil, r); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit payroll run")
	}
	return &r, nil
}

// AddExclusion records an operator's explicit decision to keep an
// employee out of a draft run. Reason and actor are mandatory; the
// actor is the caller. Decided runs (validated, approved) reject new
// exclusions loudly instead of silently narrowing history.
func AddExclusion(ctx context.Context, pool *pgxpool.Pool, employerID, runID, employeeNoOrID, reason, operator string) (string, error) {
	if strings.TrimSpace(operator) == "" {
		return "", apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(reason) == "" {
		return "", apperr.New(apperr.CodeValidation, "exclusion reason is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", apperr.FromDB(err, "begin exclusion")
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM payroll_run WHERE employer_id = $1 AND id = $2 FOR UPDATE`,
		employerID, runID).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "payroll run %s not found", runID)
		}
		return "", apperr.FromDB(err, "resolve payroll run")
	}
	if status != "draft" {
		return "", apperr.New(apperr.CodeInvalidState,
			"payroll run is %s; exclusions apply to draft runs", status)
	}
	var employeeID, employeeNo string
	err = tx.QueryRow(ctx,
		`SELECT id, employee_no FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, employeeNoOrID).Scan(&employeeID, &employeeNo)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "employee %s not found", employeeNoOrID)
		}
		return "", apperr.FromDB(err, "resolve employee")
	}
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO payroll_run_exclusion (id, employer_id, payroll_run_id, employee_id, reason, actor)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5) RETURNING id`,
		employerID, runID, employeeID, reason, operator).Scan(&id)
	if err != nil {
		return "", apperr.FromDB(err, "record exclusion")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "payroll",
		"payroll.exclude", "payroll_run_exclusion", &id, nil,
		map[string]any{"run_id": runID, "employee_no": employeeNo, "reason": reason}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", apperr.FromDB(err, "commit exclusion")
	}
	return id, nil
}
