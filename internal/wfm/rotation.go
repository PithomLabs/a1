// Package wfm rotation patterns and assignments.
//
// A rotation pattern is a named, deterministic day-cycle: code, cycle
// length in days, and a JSONB week map of day-offset to shift template.
// Assignments bind a pattern to one employee or one team from an
// effective date. Approval of bulk pattern imports is frozen (not
// served); this slice carries only direct pattern and assignment
// management.
package wfm

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
)

// Rotation is one rotation pattern header.
type Rotation struct {
	ID         string `json:"id"`
	EmployerID string `json:"employer_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	CycleDays  int    `json:"cycle_days"`
	Pattern    any    `json:"pattern"`
}

// RotationRow maps one day offset to a shift template.
type RotationRow struct {
	Offset   int    `json:"offset"`
	Weekday  int    `json:"weekday"`
	Template string `json:"template"`
	IsOff    bool   `json:"is_off,omitempty"`
}

// CreateRotationInput names a new rotation pattern and its day rows.
type CreateRotationInput struct {
	Code      string
	Name      string
	CycleDays int
	Rows      []RotationRow
}

// CreateRotation stores a rotation pattern with its day map as JSONB.
func CreateRotation(ctx context.Context, pool *pgxpool.Pool, employerID string, in CreateRotationInput) (*Rotation, error) {
	if in.Code == "" || in.Name == "" || in.CycleDays < 1 || len(in.Rows) == 0 {
		return nil, apperr.New(apperr.CodeValidation, "code, name, positive cycle days and at least one row are required")
	}
	for _, r := range in.Rows {
		if r.Offset < 0 || r.Offset >= in.CycleDays || r.Weekday < 0 || r.Weekday > 6 {
			return nil, apperr.New(apperr.CodeValidation, "day offset %d / weekday %d out of range", r.Offset, r.Weekday)
		}
		if !r.IsOff && r.Template == "" {
			return nil, apperr.New(apperr.CodeValidation, "non-off row needs a shift template")
		}
		if r.Template != "" {
			if _, err := resolveTemplate(ctx, pool, employerID, r.Template); err != nil {
				return nil, err
			}
		}
	}
	raw, err := json.Marshal(in.Rows)
	if err != nil {
		return nil, apperr.New(apperr.CodeValidation, "rotation rows are not encodable")
	}
	var p Rotation
	var stored json.RawMessage
	err = pool.QueryRow(ctx,
		`INSERT INTO rotation_pattern (employer_id, code, name, cycle_days, pattern)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, employer_id, code, name, cycle_days, pattern`,
		employerID, in.Code, in.Name, in.CycleDays, raw).
		Scan(&p.ID, &p.EmployerID, &p.Code, &p.Name, &p.CycleDays, &stored)
	if err != nil {
		return nil, apperr.FromDB(err, "create rotation pattern")
	}
	_ = json.Unmarshal(stored, &p.Pattern)
	return &p, nil
}

// ListRotations returns all rotation patterns of one employer.
func ListRotations(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Rotation, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, code, name, cycle_days, pattern FROM rotation_pattern
		 WHERE employer_id = $1 ORDER BY code`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list rotation patterns")
	}
	defer rows.Close()
	out := []Rotation{}
	for rows.Next() {
		var p Rotation
		var stored json.RawMessage
		if err := rows.Scan(&p.ID, &p.EmployerID, &p.Code, &p.Name, &p.CycleDays, &stored); err != nil {
			return nil, apperr.FromDB(err, "scan rotation pattern")
		}
		_ = json.Unmarshal(stored, &p.Pattern)
		out = append(out, p)
	}
	return out, apperr.FromDB(rows.Err(), "list rotation patterns")
}

// RotationAssign binds a pattern to one employee or one team.
type RotationAssign struct {
	ID          string  `json:"id"`
	EmployerID  string  `json:"employer_id"`
	PatternCode string  `json:"pattern_code"`
	EmployeeNo  *string `json:"employee_no,omitempty"`
	TeamCode    *string `json:"team_code,omitempty"`
	Effective   string  `json:"effective_from"`
}

// CreateRotationAssignInput binds an existing pattern to an employee
// or team.
type CreateRotationAssignInput struct {
	Pattern     string `json:"pattern"`
	Employee    string `json:"employee,omitempty"`
	Team        string `json:"team,omitempty"`
	EffectiveOn string `json:"effective_from"`
}

// CreateRotationAssign stores one pattern assignment.
func CreateRotationAssign(ctx context.Context, pool *pgxpool.Pool, employerID string, in CreateRotationAssignInput) (*RotationAssign, error) {
	if in.Pattern == "" || in.EffectiveOn == "" {
		return nil, apperr.New(apperr.CodeValidation, "pattern and effective date are required")
	}
	if (in.Employee == "") == (in.Team == "") {
		return nil, apperr.New(apperr.CodeValidation, "exactly one of employee or team is required")
	}
	if _, err := time.Parse("2006-01-02", in.EffectiveOn); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "effective date must be YYYY-MM-DD")
	}
	var pid, pcode string
	if err := pool.QueryRow(ctx,
		`SELECT id, code FROM rotation_pattern WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, in.Pattern).Scan(&pid, &pcode); err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "rotation pattern %s not found", in.Pattern)
		}
		return nil, apperr.FromDB(err, "resolve rotation pattern")
	}
	var eid, tid *string
	if in.Employee != "" {
		id, err := resolveEmployee(ctx, pool, employerID, in.Employee)
		if err != nil {
			return nil, err
		}
		eid = &id
	} else {
		id, err := resolveTeam(ctx, pool, employerID, in.Team)
		if err != nil {
			return nil, err
		}
		tid = &id
	}
	var a RotationAssign
	err := pool.QueryRow(ctx,
		`INSERT INTO rotation_assignment (employer_id, pattern_id, employee_id, team_id, effective_from)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, employer_id, effective_from::text`,
		employerID, pid, eid, tid, in.EffectiveOn).
		Scan(&a.ID, &a.EmployerID, &a.Effective)
	if err != nil {
		return nil, apperr.FromDB(err, "create rotation assignment")
	}
	a.PatternCode = pcode
	if in.Employee != "" {
		no := in.Employee
		a.EmployeeNo = &no
	} else {
		code := in.Team
		a.TeamCode = &code
	}
	return &a, nil
}

// ListRotationAssigns returns all rotation assignments of one employer.
func ListRotationAssigns(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]RotationAssign, error) {
	rows, err := pool.Query(ctx,
		`SELECT a.id, a.employer_id, p.code, e.employee_no, t.code, a.effective_from::text
		 FROM rotation_assignment a
		 JOIN rotation_pattern p ON p.id = a.pattern_id
		 LEFT JOIN employee e ON e.id = a.employee_id
		 LEFT JOIN team t ON t.id = a.team_id
		 WHERE a.employer_id = $1 ORDER BY a.effective_from`,
		employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list rotation assignments")
	}
	defer rows.Close()
	out := []RotationAssign{}
	for rows.Next() {
		var a RotationAssign
		if err := rows.Scan(&a.ID, &a.EmployerID, &a.PatternCode, &a.EmployeeNo,
			&a.TeamCode, &a.Effective); err != nil {
			return nil, apperr.FromDB(err, "scan rotation assignment")
		}
		out = append(out, a)
	}
	return out, apperr.FromDB(rows.Err(), "list rotation assignments")
}

func resolveTemplate(ctx context.Context, pool *pgxpool.Pool, employerID, codeOrID string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`SELECT id FROM shift_template WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, codeOrID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "shift template %s not found", codeOrID)
		}
		return "", apperr.FromDB(err, "resolve shift template")
	}
	return id, nil
}

func resolveEmployee(ctx context.Context, pool *pgxpool.Pool, employerID, noOrID string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, noOrID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "employee %s not found", noOrID)
		}
		return "", apperr.FromDB(err, "resolve employee")
	}
	return id, nil
}

func resolveTeam(ctx context.Context, pool *pgxpool.Pool, employerID, codeOrID string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`SELECT id FROM team WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, codeOrID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "team %s not found", codeOrID)
		}
		return "", apperr.FromDB(err, "resolve team")
	}
	return id, nil
}
