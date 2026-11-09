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
// CorrectsRunID links a correction run to its original; it is empty
// for regular runs.
type Run struct {
	ID            string `json:"id"`
	EmployerID    string `json:"employer_id"`
	PeriodID      string `json:"pay_period_id"`
	Kind          string `json:"kind"`
	CorrectsRunID string `json:"corrects_run_id,omitempty"`
	Status        string `json:"status"`
}

// CreateRun opens a draft run for the period. inputs_hash is a
// deterministic placeholder over employer, period and kind until
// payroll calculation exists; rule versions stay empty and the calc
// version names the absence honestly. A correction run must name
// its original in the same employer and period; a regular run must
// not link anywhere. The database CHECK, foreign key and lifecycle
// trigger backstop every rule here.
func CreateRun(ctx context.Context, pool *pgxpool.Pool, employerID, periodCodeOrID, kind, correctsRunID, operator string) (*Run, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if kind == "" {
		kind = "regular"
	}
	if kind != "regular" && kind != "correction" {
		return nil, apperr.New(apperr.CodeValidation, "kind must be regular or correction")
	}
	if (kind == "correction") != (strings.TrimSpace(correctsRunID) != "") {
		return nil, apperr.New(apperr.CodeValidation,
			"correction runs must link their original; regular runs must not link anywhere")
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
	if strings.TrimSpace(correctsRunID) != "" {
		var targetEmployer, targetPeriod string
		err := pool.QueryRow(ctx,
			`SELECT employer_id::text, pay_period_id::text FROM payroll_run WHERE id::text = $1`,
			correctsRunID).Scan(&targetEmployer, &targetPeriod)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, apperr.New(apperr.CodeNotFound, "payroll run %s not found", correctsRunID)
			}
			return nil, apperr.FromDB(err, "resolve correction target")
		}
		if targetEmployer != employerID || targetPeriod != periodID {
			return nil, apperr.New(apperr.CodeValidation,
				"correction runs must target the same employer and pay period")
		}
	}
	sum := sha256.Sum256([]byte(employerID + ":" + periodID + ":" + kind))
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin payroll run")
	}
	defer tx.Rollback(ctx)
	var r Run
	var link *string
	if strings.TrimSpace(correctsRunID) != "" {
		link = &correctsRunID
	}
	var linkOut *string
	err = tx.QueryRow(ctx,
		`INSERT INTO payroll_run (id, employer_id, pay_period_id, kind, corrects_run_id, inputs_hash, rule_versions, calc_version, rounding_conventions)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '{}', 'none (milestone 1.1: no calculations)', '{}')
		 RETURNING id, employer_id, pay_period_id, kind, corrects_run_id::text, status`,
		employerID, periodID, kind, link, hex.EncodeToString(sum[:])).Scan(
		&r.ID, &r.EmployerID, &r.PeriodID, &r.Kind, &linkOut, &r.Status)
	if linkOut != nil {
		r.CorrectsRunID = *linkOut
	}
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

// ExclusionDetail is one recorded run exclusion with its employee
// number resolved.
type ExclusionDetail struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
	Actor      string `json:"actor"`
}

// SnapshotPointer names the run's currently valid snapshot tip, if
// one stands.
type SnapshotPointer struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	ContentHash string `json:"content_hash"`
}

// RunDetail is the inspectable state of one run: identity, status,
// exclusions and the valid snapshot tip.
type RunDetail struct {
	Run
	Exclusions    []ExclusionDetail `json:"exclusions"`
	ValidSnapshot *SnapshotPointer  `json:"valid_snapshot,omitempty"`
}

// InspectRun reads one employer's run with its exclusions and valid
// snapshot tip. It is a read: nothing is locked or audited.
func InspectRun(ctx context.Context, pool *pgxpool.Pool, employerID, runID string) (*RunDetail, error) {
	var d RunDetail
	var link *string
	err := pool.QueryRow(ctx,
		`SELECT id::text, employer_id::text, pay_period_id::text, kind, corrects_run_id::text, status
		 FROM payroll_run WHERE employer_id::text = $1 AND id::text = $2`,
		employerID, runID).Scan(&d.ID, &d.EmployerID, &d.PeriodID, &d.Kind, &link, &d.Status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "payroll run %s not found", runID)
		}
		return nil, apperr.FromDB(err, "inspect payroll run")
	}
	if link != nil {
		d.CorrectsRunID = *link
	}
	rows, err := pool.Query(ctx,
		`SELECT x.id::text, x.employee_id::text, e.employee_no, x.reason, x.actor
		 FROM payroll_run_exclusion x JOIN employee e ON e.id = x.employee_id
		 WHERE x.employer_id::text = $1 AND x.payroll_run_id::text = $2 ORDER BY e.employee_no`,
		employerID, runID)
	if err != nil {
		return nil, apperr.FromDB(err, "inspect exclusions")
	}
	defer rows.Close()
	d.Exclusions = []ExclusionDetail{}
	for rows.Next() {
		var x ExclusionDetail
		if err := rows.Scan(&x.ID, &x.EmployeeID, &x.EmployeeNo, &x.Reason, &x.Actor); err != nil {
			return nil, apperr.FromDB(err, "scan exclusion")
		}
		d.Exclusions = append(d.Exclusions, x)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "inspect exclusions")
	}
	var tip SnapshotPointer
	err = pool.QueryRow(ctx,
		`SELECT id::text, version, content_hash FROM payroll_snapshot
		 WHERE payroll_run_id::text = $1 AND status = 'valid'`, runID).Scan(&tip.ID, &tip.Version, &tip.ContentHash)
	if err != nil && err != pgx.ErrNoRows {
		return nil, apperr.FromDB(err, "inspect snapshot tip")
	}
	if err == nil {
		d.ValidSnapshot = &tip
	}
	return &d, nil
}
