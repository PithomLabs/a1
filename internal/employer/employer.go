// Package employer resolves employer context for every employer-owned
// operation. Callers pass a code or UUID; services receive the resolved
// UUID and never guess across employers.
package employer

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// isUUID reports whether s has the canonical UUID text shape.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// Employer is the resolved employer identity.
type Employer struct {
	ID   string
	Code string
	Name string
}

// employmentTimezones pins the business-date authority per jurisdiction.
// Business dates, shift-window anchors and period attribution always use
// the employment timezone: never the vendor's declared timezone (event
// provenance only), never the client's service timezone (display only),
// and never the host process timezone.
var employmentTimezones = map[string]string{
	"SG": "Asia/Singapore",
}

// querier is the read surface shared by pools and transactions.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// EmploymentLocation resolves the employer's business-date authority.
// Unknown or unmapped jurisdictions fail loudly: silently falling back
// to UTC would move payroll dates without evidence.
func EmploymentLocation(ctx context.Context, q querier, employerID string) (*time.Location, error) {
	var jurisdiction string
	err := q.QueryRow(ctx, `SELECT jurisdiction FROM employer WHERE id = $1`, employerID).Scan(&jurisdiction)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employer %s not found", employerID)
		}
		return nil, apperr.FromDB(err, "resolve employer jurisdiction")
	}
	name, ok := employmentTimezones[strings.TrimSpace(jurisdiction)]
	if !ok {
		return nil, apperr.New(apperr.CodeValidation,
			"employer jurisdiction %q has no pinned employment timezone", jurisdiction)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, apperr.New(apperr.CodeValidation, "employment timezone %q unavailable: %v", name, err)
	}
	return loc, nil
}

// Resolve maps code-or-UUID to the employer row. An empty input resolves
// only when exactly one employer exists.
func Resolve(ctx context.Context, pool *pgxpool.Pool, codeOrID string) (*Employer, error) {
	if codeOrID == "" {
		var e Employer
		err := pool.QueryRow(ctx, `SELECT id, code, name FROM employer`).Scan(&e.ID, &e.Code, &e.Name)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, apperr.New(apperr.CodeNotFound, "no employer configured")
			}
			return nil, apperr.FromDB(err, "resolve default employer")
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM employer`).Scan(&n); err != nil {
			return nil, apperr.FromDB(err, "count employers")
		}
		if n != 1 {
			return nil, apperr.New(apperr.CodeValidation, "employer required: %d configured", n)
		}
		return &e, nil
	}
	var e Employer
	if isUUID(codeOrID) {
		err := pool.QueryRow(ctx, `SELECT id, code, name FROM employer WHERE id = $1`, codeOrID).Scan(&e.ID, &e.Code, &e.Name)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, apperr.New(apperr.CodeNotFound, "employer %s not found", codeOrID)
			}
			return nil, apperr.FromDB(err, "resolve employer")
		}
		return &e, nil
	}
	err := pool.QueryRow(ctx, `SELECT id, code, name FROM employer WHERE code = $1`, codeOrID).Scan(&e.ID, &e.Code, &e.Name)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employer %s not found", codeOrID)
		}
		return nil, apperr.FromDB(err, "resolve employer")
	}
	return &e, nil
}

// EmployerInput is the bootstrap record for one employer.
type EmployerInput struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	UEN          string `json:"uen"`
	Address      string `json:"address"`
	Jurisdiction string `json:"jurisdiction"`
}

// Creation is the outcome of Create: Existed reports a safe repeat of
// the already-configured employer instead of a new row.
type Creation struct {
	Employer *Employer `json:"employer"`
	Existed  bool      `json:"existed"`
}

// Create bootstraps the v0.1 employer on an empty database. The schema
// stays multi-employer-ready (code and jurisdiction+uen stay unique),
// but operations admit a single employer: a repeat with the same code
// returns the existing row with Existed set, while a different code is
// rejected as a conflict. The advisory lock plus the in-transaction
// count check admit exactly one winner under concurrency, and the
// audit row commits atomically with the employer row.
func Create(ctx context.Context, pool *pgxpool.Pool, in EmployerInput, operator string) (*Creation, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.UEN = strings.TrimSpace(in.UEN)
	in.Address = strings.TrimSpace(in.Address)
	in.Jurisdiction = strings.TrimSpace(in.Jurisdiction)
	if in.Code == "" || in.Name == "" || in.UEN == "" || in.Address == "" {
		return nil, apperr.New(apperr.CodeValidation, "code, name, uen and address are required")
	}
	if _, ok := employmentTimezones[in.Jurisdiction]; !ok {
		return nil, apperr.New(apperr.CodeValidation,
			"jurisdiction %q is not supported in v0.1", in.Jurisdiction)
	}
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin create employer")
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('employer bootstrap'))`); err != nil {
		return nil, apperr.FromDB(err, "lock employer bootstrap")
	}
	var e Employer
	err = tx.QueryRow(ctx, `SELECT id, code, name FROM employer`).Scan(&e.ID, &e.Code, &e.Name)
	if err != nil && err != pgx.ErrNoRows {
		return nil, apperr.FromDB(err, "check existing employer")
	}
	if err == nil {
		if e.Code != in.Code {
			return nil, apperr.New(apperr.CodeConflict,
				"v0.1 operates a single employer: %s is already configured", e.Code)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, apperr.FromDB(err, "commit employer repeat")
		}
		return &Creation{Employer: &e, Existed: true}, nil
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO employer (code, name, uen, address, jurisdiction)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, code, name`,
		in.Code, in.Name, in.UEN, in.Address, in.Jurisdiction).Scan(&e.ID, &e.Code, &e.Name)
	if err != nil {
		return nil, apperr.FromDB(err, "create employer")
	}
	if err := audit.Write(ctx, tx, e.ID, operator, "employer", "employer.create", "employer",
		&e.ID, nil, map[string]string{
			"code": in.Code, "name": in.Name, "uen": in.UEN, "jurisdiction": in.Jurisdiction,
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit create employer")
	}
	return &Creation{Employer: &e}, nil
}
