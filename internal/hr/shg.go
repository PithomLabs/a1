package hr

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// SHGFunds are the recognised self-help groups.
var SHGFunds = []string{"CDAC", "MBMF", "SINDA", "ECF"}

// SHGInput records one self-help-group instruction: an employee's fund
// choice and contribution basis as declared by the employee. Readiness
// treats the declaration as an input record, not a verified deduction.
type SHGInput struct {
	Fund              string  `json:"fund"`
	Status            string  `json:"status"`
	AmountOrBasis     *string `json:"amount_or_basis"`
	SourceDeclaration string  `json:"source_declaration"`
	EffectiveFrom     string  `json:"effective_from"`
	EffectiveTo       *string `json:"effective_to"`
}

// SHGInstruction is one persisted SHG record.
type SHGInstruction struct {
	ID                string  `json:"id"`
	EmployerID        string  `json:"employer_id"`
	EmployeeID        string  `json:"employee_id"`
	Fund              string  `json:"fund"`
	Status            string  `json:"status"`
	AmountOrBasis     *string `json:"amount_or_basis"`
	SourceDeclaration string  `json:"source_declaration"`
	EffectiveFrom     string  `json:"effective_from"`
	EffectiveTo       *string `json:"effective_to"`
}

// SetSHG records one SHG instruction for the employee, rejecting a
// same-fund overlap loudly. Different funds coexist; history stays
// append-only (a superseding instruction closes its predecessor by
// effective_to, it never edits it).
func SetSHG(ctx context.Context, pool *pgxpool.Pool, employerID, employeeNoOrID string, in SHGInput, operator string) (*SHGInstruction, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	fund := strings.ToUpper(strings.TrimSpace(in.Fund))
	validFund := false
	for _, f := range SHGFunds {
		if fund == f {
			validFund = true
		}
	}
	if !validFund {
		return nil, apperr.New(apperr.CodeValidation, "fund must be one of CDAC, MBMF, SINDA, ECF")
	}
	switch in.Status {
	case "active", "opt_out", "verified_none":
	default:
		return nil, apperr.New(apperr.CodeValidation, "status must be active, opt_out or verified_none")
	}
	if strings.TrimSpace(in.SourceDeclaration) == "" {
		return nil, apperr.New(apperr.CodeValidation, "source_declaration is required")
	}
	from, err := time.Parse("2006-01-02", in.EffectiveFrom)
	if err != nil {
		return nil, apperr.New(apperr.CodeValidation, "effective_from must be YYYY-MM-DD")
	}
	if in.EffectiveTo != nil {
		t, err := time.Parse("2006-01-02", *in.EffectiveTo)
		if err != nil {
			return nil, apperr.New(apperr.CodeValidation, "effective_to must be YYYY-MM-DD")
		}
		if t.Before(from) {
			return nil, apperr.New(apperr.CodeValidation, "effective_to must not precede effective_from")
		}
	}
	var employeeID string
	err = pool.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, employeeNoOrID).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employee %s not found", employeeNoOrID)
		}
		return nil, apperr.FromDB(err, "resolve employee")
	}
	var clash bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM shg_instruction
		 WHERE employer_id = $1 AND employee_id = $2 AND fund = $3
		   AND effective_from <= COALESCE($5::date, 'infinity'::date)
		   AND (effective_to IS NULL OR effective_to >= $4::date))`,
		employerID, employeeID, fund, in.EffectiveFrom, nullStr(in.EffectiveTo)).Scan(&clash)
	if err != nil {
		return nil, apperr.FromDB(err, "check SHG overlap")
	}
	if clash {
		return nil, apperr.New(apperr.CodeConflict, "an overlapping %s instruction already exists", fund)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin SHG instruction")
	}
	defer tx.Rollback(ctx)
	// Amendment A: SHG instructions join the payroll-input barrier
	// (their readiness check is presence-based and race-immune, but
	// the ordering is uniform and future-proof).
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, employeeID); err != nil {
		return nil, err
	}
	var s SHGInstruction
	err = tx.QueryRow(ctx,
		`INSERT INTO shg_instruction (id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration, effective_from, effective_to)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7::date, $8::date)
		 RETURNING id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration, effective_from::text, effective_to::text`,
		employerID, employeeID, fund, in.Status, nullStr(in.AmountOrBasis),
		in.SourceDeclaration, in.EffectiveFrom, nullStr(in.EffectiveTo)).Scan(
		&s.ID, &s.EmployerID, &s.EmployeeID, &s.Fund, &s.Status, &s.AmountOrBasis,
		&s.SourceDeclaration, &s.EffectiveFrom, &s.EffectiveTo)
	if err != nil {
		return nil, apperr.FromDB(err, "record SHG instruction")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "hr",
		"shg.set", "shg_instruction", &s.ID, nil, s); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit SHG instruction")
	}
	return &s, nil
}

// ListSHG returns the employee's SHG instructions, newest first.
func ListSHG(ctx context.Context, pool *pgxpool.Pool, employerID, employeeNoOrID string) ([]SHGInstruction, error) {
	var employeeID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, employeeNoOrID).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employee %s not found", employeeNoOrID)
		}
		return nil, apperr.FromDB(err, "resolve employee")
	}
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration, effective_from::text, effective_to::text
		 FROM shg_instruction WHERE employer_id = $1 AND employee_id = $2
		 ORDER BY effective_from DESC, created_at DESC`,
		employerID, employeeID)
	if err != nil {
		return nil, apperr.FromDB(err, "list SHG instructions")
	}
	defer rows.Close()
	out := []SHGInstruction{}
	for rows.Next() {
		var s SHGInstruction
		if err := rows.Scan(&s.ID, &s.EmployerID, &s.EmployeeID, &s.Fund, &s.Status,
			&s.AmountOrBasis, &s.SourceDeclaration, &s.EffectiveFrom, &s.EffectiveTo); err != nil {
			return nil, apperr.FromDB(err, "scan SHG instruction")
		}
		out = append(out, s)
	}
	return out, apperr.FromDB(rows.Err(), "list SHG instructions")
}
