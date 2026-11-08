// Package wfm implements BPO configuration and roster management:
// clients, programmes, teams, skills, shift templates, draft rosters,
// assignments and publication. Scheduling here is operational only;
// legal validation stays unavailable until SG-MOM-SHIFT is IMPLEMENTABLE.
package wfm

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// Client is one BPO client of the employer.
type Client struct {
	ID         string `json:"id"`
	EmployerID string `json:"employer_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
}

// CreateClient inserts one client.
func CreateClient(ctx context.Context, pool *pgxpool.Pool, employerID, code, name string) (*Client, error) {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	if code == "" || name == "" {
		return nil, apperr.New(apperr.CodeValidation, "code and name are required")
	}
	var c Client
	err := pool.QueryRow(ctx,
		`INSERT INTO client (employer_id, code, name) VALUES ($1, $2, $3)
		 RETURNING id, employer_id, code, name`,
		employerID, code, name).Scan(&c.ID, &c.EmployerID, &c.Code, &c.Name)
	if err != nil {
		return nil, apperr.FromDB(err, "create client")
	}
	return &c, nil
}

// ListClients returns all clients of one employer.
func ListClients(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Client, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, code, name FROM client WHERE employer_id = $1 ORDER BY code`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list clients")
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ID, &c.EmployerID, &c.Code, &c.Name); err != nil {
			return nil, apperr.FromDB(err, "scan client")
		}
		out = append(out, c)
	}
	return out, apperr.FromDB(rows.Err(), "list clients")
}

// Programme is one client programme.
type Programme struct {
	ID         string `json:"id"`
	EmployerID string `json:"employer_id"`
	ClientID   string `json:"client_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Timezone   string `json:"timezone"`
}

// ProgrammeInput creates a programme under a client code or ID.
type ProgrammeInput struct {
	Client string `json:"client"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	// Timezone defaults to Asia/Singapore.
	Timezone string `json:"timezone"`
}

// CreateProgramme inserts one programme.
func CreateProgramme(ctx context.Context, pool *pgxpool.Pool, employerID string, in ProgrammeInput) (*Programme, error) {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	if in.Client == "" || in.Code == "" || in.Name == "" {
		return nil, apperr.New(apperr.CodeValidation, "client, code and name are required")
	}
	if in.Timezone == "" {
		in.Timezone = "Asia/Singapore"
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "unknown timezone %q", in.Timezone)
	}
	var clientID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM client WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, in.Client).Scan(&clientID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "client %s not found", in.Client)
		}
		return nil, apperr.FromDB(err, "resolve client")
	}
	var p Programme
	err = pool.QueryRow(ctx,
		`INSERT INTO programme (employer_id, client_id, code, name, timezone)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, employer_id, client_id, code, name, timezone`,
		employerID, clientID, in.Code, in.Name, in.Timezone).Scan(
		&p.ID, &p.EmployerID, &p.ClientID, &p.Code, &p.Name, &p.Timezone)
	if err != nil {
		return nil, apperr.FromDB(err, "create programme")
	}
	return &p, nil
}

// ListProgrammes returns all programmes of one employer.
func ListProgrammes(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Programme, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, client_id, code, name, timezone
		 FROM programme WHERE employer_id = $1 ORDER BY code`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list programmes")
	}
	defer rows.Close()
	out := []Programme{}
	for rows.Next() {
		var p Programme
		if err := rows.Scan(&p.ID, &p.EmployerID, &p.ClientID, &p.Code, &p.Name, &p.Timezone); err != nil {
			return nil, apperr.FromDB(err, "scan programme")
		}
		out = append(out, p)
	}
	return out, apperr.FromDB(rows.Err(), "list programmes")
}

// Team is one programme team.
type Team struct {
	ID          string `json:"id"`
	EmployerID  string `json:"employer_id"`
	ProgrammeID string `json:"programme_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
}

// TeamInput creates a team under a programme code or ID.
type TeamInput struct {
	Programme string `json:"programme"`
	Code      string `json:"code"`
	Name      string `json:"name"`
}

// CreateTeam inserts one team.
func CreateTeam(ctx context.Context, pool *pgxpool.Pool, employerID string, in TeamInput) (*Team, error) {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	if in.Programme == "" || in.Code == "" || in.Name == "" {
		return nil, apperr.New(apperr.CodeValidation, "programme, code and name are required")
	}
	programmeID, err := resolveProgramme(ctx, pool, employerID, in.Programme)
	if err != nil {
		return nil, err
	}
	var t Team
	err = pool.QueryRow(ctx,
		`INSERT INTO team (employer_id, programme_id, code, name)
		 VALUES ($1, $2, $3, $4) RETURNING id, employer_id, programme_id, code, name`,
		employerID, programmeID, in.Code, in.Name).Scan(&t.ID, &t.EmployerID, &t.ProgrammeID, &t.Code, &t.Name)
	if err != nil {
		return nil, apperr.FromDB(err, "create team")
	}
	return &t, nil
}

// ListTeams returns all teams of one employer.
func ListTeams(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Team, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, programme_id, code, name FROM team WHERE employer_id = $1 ORDER BY code`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list teams")
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.EmployerID, &t.ProgrammeID, &t.Code, &t.Name); err != nil {
			return nil, apperr.FromDB(err, "scan team")
		}
		out = append(out, t)
	}
	return out, apperr.FromDB(rows.Err(), "list teams")
}

// AddMembership adds an employee to a team from the effective date.
func AddMembership(ctx context.Context, pool *pgxpool.Pool, employerID, teamCodeOrID, employeeNoOrID, role, effectiveFrom, operator string) (string, error) {
	switch role {
	case "agent", "lead", "supervisor":
	default:
		return "", apperr.New(apperr.CodeValidation, "unknown team role %q", role)
	}
	if effectiveFrom == "" {
		return "", apperr.New(apperr.CodeValidation, "effective_from is required")
	}
	if strings.TrimSpace(operator) == "" {
		return "", apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	var teamID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM team WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, teamCodeOrID).Scan(&teamID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "team %s not found", teamCodeOrID)
		}
		return "", apperr.FromDB(err, "resolve team")
	}
	var employeeID string
	err = pool.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, employeeNoOrID).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "employee %s not found", employeeNoOrID)
		}
		return "", apperr.FromDB(err, "resolve employee")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", apperr.FromDB(err, "begin add membership")
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO team_membership (employer_id, team_id, employee_id, role, effective_from)
		 VALUES ($1, $2, $3, $4, $5::date) RETURNING id`,
		employerID, teamID, employeeID, role, effectiveFrom).Scan(&id)
	if err != nil {
		return "", apperr.FromDB(err, "add team membership")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "wfm", "membership.add", "team_membership",
		&id, nil, map[string]string{
			"team": teamCodeOrID, "employee": employeeNoOrID,
			"role": role, "effective_from": effectiveFrom,
		}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", apperr.FromDB(err, "commit add membership")
	}
	return id, nil
}

// Skill is one global catalogue entry.
type Skill struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ListSkills returns the global skill catalogue.
func ListSkills(ctx context.Context, pool *pgxpool.Pool) ([]Skill, error) {
	rows, err := pool.Query(ctx, `SELECT id, code, name, kind FROM skill ORDER BY code`)
	if err != nil {
		return nil, apperr.FromDB(err, "list skills")
	}
	defer rows.Close()
	out := []Skill{}
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.Code, &s.Name, &s.Kind); err != nil {
			return nil, apperr.FromDB(err, "scan skill")
		}
		out = append(out, s)
	}
	return out, apperr.FromDB(rows.Err(), "list skills")
}

// SkillGrant assigns one skill level to an employee.
type SkillGrant struct {
	Skill string `json:"skill"`
	Level int    `json:"level"`
}

// AssignSkills records employee skill levels from the valid date.
func AssignSkills(ctx context.Context, pool *pgxpool.Pool, employerID, employeeNoOrID string, validFrom string, grants []SkillGrant, operator string) error {
	var employeeID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, employeeNoOrID).Scan(&employeeID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperr.New(apperr.CodeNotFound, "employee %s not found", employeeNoOrID)
		}
		return apperr.FromDB(err, "resolve employee")
	}
	if validFrom == "" {
		return apperr.New(apperr.CodeValidation, "valid_from is required")
	}
	if strings.TrimSpace(operator) == "" {
		return apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	for _, g := range grants {
		if g.Level <= 0 {
			return apperr.New(apperr.CodeValidation, "skill %s needs a positive level", g.Skill)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return apperr.FromDB(err, "begin assign skills")
	}
	defer tx.Rollback(ctx)
	for _, g := range grants {
		var skillID string
		err := tx.QueryRow(ctx, `SELECT id FROM skill WHERE code = $1 OR id::text = $1`, g.Skill).Scan(&skillID)
		if err != nil {
			if err == pgx.ErrNoRows {
				return apperr.New(apperr.CodeNotFound, "skill %s not found", g.Skill)
			}
			return apperr.FromDB(err, "resolve skill")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO employee_skill (employer_id, employee_id, skill_id, level, valid_from)
			 VALUES ($1, $2, $3, $4, $5::date)`,
			employerID, employeeID, skillID, g.Level, validFrom); err != nil {
			return apperr.FromDB(err, "assign skill")
		}
	}
	if err := audit.Write(ctx, tx, employerID, operator, "wfm", "skills.assign", "employee_skill",
		nil, nil, map[string]any{
			"employee": employeeNoOrID, "valid_from": validFrom, "grants": grants,
		}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.FromDB(err, "commit assign skills")
	}
	return nil
}

// RequireSkills records the minimum skill levels a programme demands.
func RequireSkills(ctx context.Context, pool *pgxpool.Pool, employerID, programmeCodeOrID string, grants []SkillGrant, operator string) error {
	programmeID, err := resolveProgramme(ctx, pool, employerID, programmeCodeOrID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(operator) == "" {
		return apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	for _, g := range grants {
		if g.Level <= 0 {
			return apperr.New(apperr.CodeValidation, "skill %s needs a positive level", g.Skill)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return apperr.FromDB(err, "begin require skills")
	}
	defer tx.Rollback(ctx)
	for _, g := range grants {
		var skillID string
		err := tx.QueryRow(ctx, `SELECT id FROM skill WHERE code = $1 OR id::text = $1`, g.Skill).Scan(&skillID)
		if err != nil {
			if err == pgx.ErrNoRows {
				return apperr.New(apperr.CodeNotFound, "skill %s not found", g.Skill)
			}
			return apperr.FromDB(err, "resolve skill")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO programme_required_skill (employer_id, programme_id, skill_id, minimum_level)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (employer_id, programme_id, skill_id)
			 DO UPDATE SET minimum_level = EXCLUDED.minimum_level`,
			employerID, programmeID, skillID, g.Level); err != nil {
			return apperr.FromDB(err, "require skill")
		}
	}
	if err := audit.Write(ctx, tx, employerID, operator, "wfm", "skills.require", "programme_required_skill",
		nil, nil, map[string]any{
			"programme": programmeCodeOrID, "grants": grants,
		}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.FromDB(err, "commit require skills")
	}
	return nil
}

func resolveProgramme(ctx context.Context, pool *pgxpool.Pool, employerID, codeOrID string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`SELECT id FROM programme WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, codeOrID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "programme %s not found", codeOrID)
		}
		return "", apperr.FromDB(err, "resolve programme")
	}
	return id, nil
}
