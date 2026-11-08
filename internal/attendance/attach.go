package attendance

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
	"github.com/PithomLabs/a1/internal/employer"
)

// AttachReport is the outcome of one explicit evidence-attach pass.
type AttachReport struct {
	EmployeeNo     string      `json:"employee_no"`
	IntervalsBuilt int         `json:"intervals_built"`
	Exceptions     []Exception `json:"exceptions"`
	Resolved       int         `json:"exceptions_resolved"`
}

// AttachPending re-runs pairing for one employee without new vendor
// evidence. It exists for held late evidence: a break that arrived
// while its span fed a decided timesheet waits with an explicit
// evidence conflict, and once the operator reissues that sheet this
// operation attaches the break to the correction draft's successors
// and regenerates the draft atomically. Anything else still unpaired
// pairs under the same rules as an import.
func AttachPending(ctx context.Context, pool *pgxpool.Pool, employerID, employeeNo, operator string, regen RegenerateFunc, chain ChainFunc) (*AttachReport, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(employeeNo) == "" {
		return nil, apperr.New(apperr.CodeValidation, "employee_no is required")
	}
	if regen == nil || chain == nil {
		return nil, apperr.New(apperr.CodeValidation, "draft regeneration and chain resolution are required")
	}
	loc, err := employer.EmploymentLocation(ctx, pool, employerID)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin attach")
	}
	defer tx.Rollback(ctx)
	var employeeID string
	err = tx.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
		employerID, employeeNo).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employee %s not found", employeeNo)
		}
		return nil, apperr.FromDB(err, "resolve employee")
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, employeeID); err != nil {
		return nil, err
	}
	built, excs, resolved, err := pairEmployee(ctx, tx, employerID, employeeID, employeeNo, loc, regen, chain)
	if err != nil {
		return nil, err
	}
	if err := audit.Write(ctx, tx, employerID, operator, "attendance",
		"evidence.attach", "attendance_interval", nil, nil, map[string]any{
			"employee_no":         employeeNo,
			"intervals_built":     built,
			"exceptions_resolved": resolved,
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit attach")
	}
	return &AttachReport{EmployeeNo: employeeNo, IntervalsBuilt: built, Exceptions: excs, Resolved: resolved}, nil
}
