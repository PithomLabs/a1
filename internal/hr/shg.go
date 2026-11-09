package hr

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
)

// SHGFunds are the recognised self-help groups.
var SHGFunds = []string{"CDAC", "MBMF", "SINDA", "ECF"}

// SHGInput records one self-help-group instruction: an employee's fund
// choice and contribution basis as declared by the employee. A pending
// declaration carries no verified triple; verified_none requires the
// full triple; active and opt_out must be unverified.
type SHGInput struct {
	Fund              string  `json:"fund"`
	Status            string  `json:"status"`
	AmountOrBasis     *string `json:"amount_or_basis"`
	SourceDeclaration string  `json:"source_declaration"`
	EffectiveFrom     string  `json:"effective_from"`
	EffectiveTo       *string `json:"effective_to"`
	VerifiedAt        *string `json:"verified_at"`
	VerifiedBy        *string `json:"verified_by"`
	VerificationBasis *string `json:"verification_basis"`
}

// SHGVerifyInput verifies one pending instruction into a verified_none
// successor. The evidence triple is required; the fund is inherited.
type SHGVerifyInput struct {
	SourceDeclaration string  `json:"source_declaration"`
	EffectiveFrom     string  `json:"effective_from"`
	EffectiveTo       *string `json:"effective_to"`
	VerifiedAt        string  `json:"verified_at"`
	VerifiedBy        string  `json:"verified_by"`
	VerificationBasis string  `json:"verification_basis"`
}

// SHGCorrectInput mints the audited successor of one open-ended SHG
// instruction. The target status is never pending; verified_none
// requires the full triple. The fund is inherited.
type SHGCorrectInput struct {
	Status            string  `json:"status"`
	AmountOrBasis     *string `json:"amount_or_basis"`
	SourceDeclaration string  `json:"source_declaration"`
	EffectiveFrom     string  `json:"effective_from"`
	EffectiveTo       *string `json:"effective_to"`
	VerifiedAt        *string `json:"verified_at"`
	VerifiedBy        *string `json:"verified_by"`
	VerificationBasis *string `json:"verification_basis"`
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
	VerifiedAt        *string `json:"verified_at"`
	VerifiedBy        *string `json:"verified_by"`
	VerificationBasis *string `json:"verification_basis"`
	SupersedesID      *string `json:"supersedes_id"`
	BasisSeq          int64   `json:"basis_seq"`
}

// SetSHG records one SHG instruction for the employee through the
// governed add function: the database takes the barrier lock, assigns
// the basis version, enforces the lifecycle CHECKs and writes the
// canonical audit event atomically. Different funds coexist; a
// same-fund live overlap is rejected loudly.
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
	case "pending", "active", "opt_out", "verified_none":
	default:
		return nil, apperr.New(apperr.CodeValidation, "status must be pending, active, opt_out or verified_none")
	}
	if strings.TrimSpace(in.SourceDeclaration) == "" {
		return nil, apperr.New(apperr.CodeValidation, "source_declaration is required")
	}
	if err := validateSHGDates(in.EffectiveFrom, in.EffectiveTo); err != nil {
		return nil, err
	}
	if err := validateSHGTriple(in.Status, in.VerifiedAt, in.VerifiedBy, in.VerificationBasis); err != nil {
		return nil, err
	}
	employeeID, err := resolveSHGEmployee(ctx, pool, employerID, employeeNoOrID)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`SELECT add_shg_instruction($1::uuid, $2::uuid, $3, $4, $5, $6, $7::date, $8::date,
		  $9::timestamptz, $10, $11, $12)::text`,
		employerID, employeeID, fund, in.Status, nullStr(in.AmountOrBasis),
		in.SourceDeclaration, in.EffectiveFrom, nullStr(in.EffectiveTo),
		nullStr(in.VerifiedAt), nullStr(in.VerifiedBy), nullStr(in.VerificationBasis),
		operator).Scan(&id)
	if err != nil {
		return nil, apperr.FromDB(err, "record SHG instruction")
	}
	return getSHG(ctx, pool, employerID, id)
}

// VerifySHG verifies one pending instruction into a verified_none
// successor carrying the evidence triple. The pending row is never
// mutated; a second successor of the same row is rejected.
func VerifySHG(ctx context.Context, pool *pgxpool.Pool, employerID, instructionID string, in SHGVerifyInput, operator string) (*SHGInstruction, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(in.SourceDeclaration) == "" {
		return nil, apperr.New(apperr.CodeValidation, "source_declaration is required")
	}
	if err := validateSHGDates(in.EffectiveFrom, in.EffectiveTo); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.VerifiedAt) == "" || strings.TrimSpace(in.VerifiedBy) == "" ||
		strings.TrimSpace(in.VerificationBasis) == "" {
		return nil, apperr.New(apperr.CodeValidation, "verification requires the full verified triple")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(in.VerifiedAt)); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "verified_at must be RFC 3339")
	}
	employeeID, err := shgPredecessorEmployee(ctx, pool, employerID, instructionID)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`SELECT verify_shg_instruction($1::uuid, $2::uuid, $3::uuid, $4, $5::date, $6::date,
		  $7::timestamptz, $8, $9, $10)::text`,
		employerID, employeeID, instructionID, in.SourceDeclaration,
		in.EffectiveFrom, nullStr(in.EffectiveTo),
		strings.TrimSpace(in.VerifiedAt), strings.TrimSpace(in.VerifiedBy),
		strings.TrimSpace(in.VerificationBasis), operator).Scan(&id)
	if err != nil {
		return nil, apperr.FromDB(err, "verify SHG instruction")
	}
	return getSHG(ctx, pool, employerID, id)
}

// CorrectSHG mints the audited successor of one open-ended SHG
// instruction. The original row is never mutated; a second successor
// of the same original is rejected, as is any regression to pending.
func CorrectSHG(ctx context.Context, pool *pgxpool.Pool, employerID, instructionID string, in SHGCorrectInput, operator string) (*SHGInstruction, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	switch in.Status {
	case "active", "opt_out", "verified_none":
	default:
		return nil, apperr.New(apperr.CodeValidation, "correction status must be active, opt_out or verified_none, never pending")
	}
	if strings.TrimSpace(in.SourceDeclaration) == "" {
		return nil, apperr.New(apperr.CodeValidation, "source_declaration is required")
	}
	if err := validateSHGDates(in.EffectiveFrom, in.EffectiveTo); err != nil {
		return nil, err
	}
	if err := validateSHGTriple(in.Status, in.VerifiedAt, in.VerifiedBy, in.VerificationBasis); err != nil {
		return nil, err
	}
	employeeID, err := shgPredecessorEmployee(ctx, pool, employerID, instructionID)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`SELECT correct_shg_instruction($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::date, $8::date,
		  $9::timestamptz, $10, $11, $12)::text`,
		employerID, employeeID, instructionID, in.Status, nullStr(in.AmountOrBasis),
		in.SourceDeclaration, in.EffectiveFrom, nullStr(in.EffectiveTo),
		nullStr(in.VerifiedAt), nullStr(in.VerifiedBy), nullStr(in.VerificationBasis),
		operator).Scan(&id)
	if err != nil {
		return nil, apperr.FromDB(err, "correct SHG instruction")
	}
	return getSHG(ctx, pool, employerID, id)
}

// ListSHG returns the employee's SHG instructions, newest first.
func ListSHG(ctx context.Context, pool *pgxpool.Pool, employerID, employeeNoOrID string) ([]SHGInstruction, error) {
	employeeID, err := resolveSHGEmployee(ctx, pool, employerID, employeeNoOrID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration,
		  effective_from::text, effective_to::text,
		  verified_at::text, verified_by, verification_basis, supersedes_id::text, basis_seq
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
			&s.AmountOrBasis, &s.SourceDeclaration, &s.EffectiveFrom, &s.EffectiveTo,
			&s.VerifiedAt, &s.VerifiedBy, &s.VerificationBasis, &s.SupersedesID, &s.BasisSeq); err != nil {
			return nil, apperr.FromDB(err, "scan SHG instruction")
		}
		out = append(out, s)
	}
	return out, apperr.FromDB(rows.Err(), "list SHG instructions")
}

// validateSHGDates checks the YYYY-MM-DD range shape.
func validateSHGDates(from string, to *string) error {
	f, err := time.Parse("2006-01-02", from)
	if err != nil {
		return apperr.New(apperr.CodeValidation, "effective_from must be YYYY-MM-DD")
	}
	if to != nil {
		t, err := time.Parse("2006-01-02", *to)
		if err != nil {
			return apperr.New(apperr.CodeValidation, "effective_to must be YYYY-MM-DD")
		}
		if t.Before(f) {
			return apperr.New(apperr.CodeValidation, "effective_to must not precede effective_from")
		}
	}
	return nil
}

// validateSHGTriple enforces the verified-triple contract: the triple
// is present if and only if the status is verified_none.
func validateSHGTriple(status string, at, by, basis *string) error {
	present := 0
	for _, p := range []*string{at, by, basis} {
		if p != nil && strings.TrimSpace(*p) != "" {
			present++
		}
	}
	if status == "verified_none" {
		if present != 3 {
			return apperr.New(apperr.CodeValidation, "verified_none requires the full verified triple")
		}
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(*at)); err != nil {
			return apperr.New(apperr.CodeValidation, "verified_at must be RFC 3339")
		}
		return nil
	}
	if present != 0 {
		return apperr.New(apperr.CodeValidation, "%s must be unverified", status)
	}
	return nil
}

// resolveSHGEmployee resolves an employee number or id within the employer.
func resolveSHGEmployee(ctx context.Context, pool *pgxpool.Pool, employerID, noOrID string) (string, error) {
	var employeeID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, noOrID).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "employee %s not found", noOrID)
		}
		return "", apperr.FromDB(err, "resolve employee")
	}
	return employeeID, nil
}

// shgPredecessorEmployee resolves the employee owning one SHG
// instruction, so verify/correct stay inside one employee's chain.
func shgPredecessorEmployee(ctx context.Context, pool *pgxpool.Pool, employerID, instructionID string) (string, error) {
	var employeeID string
	err := pool.QueryRow(ctx,
		`SELECT employee_id FROM shg_instruction WHERE employer_id = $1 AND id = $2`,
		employerID, instructionID).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "SHG instruction %s not found", instructionID)
		}
		return "", apperr.FromDB(err, "resolve SHG instruction")
	}
	return employeeID, nil
}

// getSHG re-reads one persisted SHG row after a lifecycle write.
func getSHG(ctx context.Context, pool *pgxpool.Pool, employerID, id string) (*SHGInstruction, error) {
	var s SHGInstruction
	err := pool.QueryRow(ctx,
		`SELECT id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration,
		  effective_from::text, effective_to::text,
		  verified_at::text, verified_by, verification_basis, supersedes_id::text, basis_seq
		 FROM shg_instruction WHERE employer_id = $1 AND id = $2`,
		employerID, id).Scan(&s.ID, &s.EmployerID, &s.EmployeeID, &s.Fund, &s.Status,
		&s.AmountOrBasis, &s.SourceDeclaration, &s.EffectiveFrom, &s.EffectiveTo,
		&s.VerifiedAt, &s.VerifiedBy, &s.VerificationBasis, &s.SupersedesID, &s.BasisSeq)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "SHG instruction %s not found", id)
		}
		return nil, apperr.FromDB(err, "read SHG instruction")
	}
	return &s, nil
}
