package wfm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// dbtx is the query surface shared by *pgxpool.Pool and pgx.Tx.
type dbtx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Template is one shift template with its segments.
type Template struct {
	ID            string    `json:"id"`
	EmployerID    string    `json:"employer_id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	StartLocal    string    `json:"start_local"`
	EndLocal      string    `json:"end_local"`
	CrossMidnight bool      `json:"cross_midnight"`
	Segments      []Segment `json:"segments"`
}

// Segment is one work/break span inside a template.
type Segment struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	StartOffsetMin int    `json:"start_offset_min"`
	DurationMin    int    `json:"duration_min"`
	Paid           bool   `json:"paid"`
}

// SegmentInput creates one segment.
type SegmentInput struct {
	Kind           string `json:"kind"`
	StartOffsetMin int    `json:"start_offset_min"`
	DurationMin    int    `json:"duration_min"`
	Paid           bool   `json:"paid"`
}

// TemplateInput creates a template with segments.
type TemplateInput struct {
	Code          string         `json:"code"`
	Name          string         `json:"name"`
	StartLocal    string         `json:"start_local"`
	EndLocal      string         `json:"end_local"`
	CrossMidnight bool           `json:"cross_midnight"`
	Segments      []SegmentInput `json:"segments"`
}

// CreateTemplate inserts one template with its segments atomically.
func CreateTemplate(ctx context.Context, pool *pgxpool.Pool, employerID string, in TemplateInput) (*Template, error) {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	if in.Code == "" || in.Name == "" || in.StartLocal == "" || in.EndLocal == "" {
		return nil, apperr.New(apperr.CodeValidation, "code, name, start_local and end_local are required")
	}
	for _, s := range in.Segments {
		switch s.Kind {
		case "work", "break_paid", "break_unpaid":
		default:
			return nil, apperr.New(apperr.CodeValidation, "unknown segment kind %q", s.Kind)
		}
		if s.StartOffsetMin < 0 || s.DurationMin <= 0 {
			return nil, apperr.New(apperr.CodeValidation, "segment offsets must be non-negative with positive duration")
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin template")
	}
	defer tx.Rollback(ctx)
	t := Template{EmployerID: employerID, Code: in.Code, Name: in.Name,
		StartLocal: in.StartLocal, EndLocal: in.EndLocal, CrossMidnight: in.CrossMidnight}
	err = tx.QueryRow(ctx,
		`INSERT INTO shift_template (employer_id, code, name, start_local, end_local, cross_midnight)
		 VALUES ($1, $2, $3, $4::time, $5::time, $6)
		 RETURNING id, start_local::text, end_local::text`,
		employerID, in.Code, in.Name, in.StartLocal, in.EndLocal, in.CrossMidnight).Scan(&t.ID, &t.StartLocal, &t.EndLocal)
	if err != nil {
		return nil, apperr.FromDB(err, "create shift template")
	}
	for _, s := range in.Segments {
		var seg Segment
		err := tx.QueryRow(ctx,
			`INSERT INTO shift_segment (employer_id, template_id, kind, start_offset_min, duration_min, paid)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id, kind, start_offset_min, duration_min, paid`,
			employerID, t.ID, s.Kind, s.StartOffsetMin, s.DurationMin, s.Paid).Scan(
			&seg.ID, &seg.Kind, &seg.StartOffsetMin, &seg.DurationMin, &seg.Paid)
		if err != nil {
			return nil, apperr.FromDB(err, "create shift segment")
		}
		t.Segments = append(t.Segments, seg)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit template")
	}
	return &t, nil
}

// ListTemplates returns all templates with segments for one employer.
func ListTemplates(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Template, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, code, name, start_local::text, end_local::text, cross_midnight
		 FROM shift_template WHERE employer_id = $1 ORDER BY code`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list templates")
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.EmployerID, &t.Code, &t.Name, &t.StartLocal, &t.EndLocal, &t.CrossMidnight); err != nil {
			return nil, apperr.FromDB(err, "scan template")
		}
		t.Segments, err = listSegments(ctx, pool, employerID, t.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, apperr.FromDB(rows.Err(), "list templates")
}

func listSegments(ctx context.Context, pool *pgxpool.Pool, employerID, templateID string) ([]Segment, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, kind, start_offset_min, duration_min, paid
		 FROM shift_segment WHERE employer_id = $1 AND template_id = $2 ORDER BY start_offset_min`,
		employerID, templateID)
	if err != nil {
		return nil, apperr.FromDB(err, "list segments")
	}
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var s Segment
		if err := rows.Scan(&s.ID, &s.Kind, &s.StartOffsetMin, &s.DurationMin, &s.Paid); err != nil {
			return nil, apperr.FromDB(err, "scan segment")
		}
		out = append(out, s)
	}
	return out, apperr.FromDB(rows.Err(), "list segments")
}

// Roster is one draft or published roster week.
type Roster struct {
	ID                    string `json:"id"`
	EmployerID            string `json:"employer_id"`
	ProgrammeID           string `json:"programme_id"`
	WeekStarting          string `json:"week_starting"`
	Status                string `json:"status"`
	LegalValidationStatus string `json:"legal_validation_status"`
	CurrentVersion        int    `json:"current_version"`
}

// RosterInput creates a draft roster for a programme week.
type RosterInput struct {
	Programme    string `json:"programme"`
	WeekStarting string `json:"week_starting"`
}

// CreateRoster inserts a draft roster with revision 1.
func CreateRoster(ctx context.Context, pool *pgxpool.Pool, employerID string, in RosterInput) (*Roster, error) {
	if in.Programme == "" || in.WeekStarting == "" {
		return nil, apperr.New(apperr.CodeValidation, "programme and week_starting are required")
	}
	programmeID, err := resolveProgramme(ctx, pool, employerID, in.Programme)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin roster")
	}
	defer tx.Rollback(ctx)
	var r Roster
	err = tx.QueryRow(ctx,
		`INSERT INTO roster (employer_id, programme_id, week_starting)
		 VALUES ($1, $2, $3::date)
		 RETURNING id, employer_id, programme_id, week_starting::text, status, legal_validation_status, current_version`,
		employerID, programmeID, in.WeekStarting).Scan(
		&r.ID, &r.EmployerID, &r.ProgrammeID, &r.WeekStarting, &r.Status, &r.LegalValidationStatus, &r.CurrentVersion)
	if err != nil {
		return nil, apperr.FromDB(err, "create roster")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO roster_revision (employer_id, roster_id, version) VALUES ($1, $2, 1)`,
		employerID, r.ID); err != nil {
		return nil, apperr.FromDB(err, "create roster revision")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit roster")
	}
	return &r, nil
}

// Assignment is one rostered shift or rest day.
type Assignment struct {
	ID           string  `json:"id"`
	RevisionID   string  `json:"revision_id"`
	EmployeeID   string  `json:"employee_id"`
	EmployeeNo   string  `json:"employee_no"`
	BusinessDate string  `json:"business_date"`
	TemplateID   *string `json:"template_id"`
	TemplateCode *string `json:"template_code"`
	RestDay      bool    `json:"rest_day"`
	Status       string  `json:"status"`
}

// AssignmentInput places one employee on one date of the current draft.
type AssignmentInput struct {
	Employee string `json:"employee"`
	Date     string `json:"date"`
	// Template is the shift code; empty with RestDay means a rest day.
	Template string `json:"template"`
	RestDay  bool   `json:"rest_day"`
}

// Assign validates and inserts one assignment on the current revision
// in one transaction. The roster must be a draft; published rosters
// reject edits (revise them instead).
func Assign(ctx context.Context, pool *pgxpool.Pool, employerID, rosterID string, in AssignmentInput) (*Assignment, error) {
	if in.Employee == "" || in.Date == "" {
		return nil, apperr.New(apperr.CodeValidation, "employee and date are required")
	}
	if (in.Template == "") == (!in.RestDay) {
		return nil, apperr.New(apperr.CodeValidation, "exactly one of template or rest_day is required")
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "date must be YYYY-MM-DD")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin assignment")
	}
	defer tx.Rollback(ctx)
	var roster struct {
		programmeID  string
		status       string
		version      int
		revisionID   string
		weekStarting string
	}
	err = tx.QueryRow(ctx,
		`SELECT r.programme_id, r.status, r.current_version, rev.id, r.week_starting::text
		 FROM roster r JOIN roster_revision rev
		   ON rev.employer_id = r.employer_id AND rev.roster_id = r.id AND rev.version = r.current_version
		 WHERE r.employer_id = $1 AND r.id = $2 FOR UPDATE OF r`,
		employerID, rosterID).Scan(&roster.programmeID, &roster.status, &roster.version, &roster.revisionID, &roster.weekStarting)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "roster not found")
		}
		return nil, apperr.FromDB(err, "resolve roster")
	}
	if roster.status != "draft" {
		return nil, apperr.New(apperr.CodeFrozen, "roster is published; revise it to open a new draft revision")
	}
	var employee struct {
		id string
		no string
	}
	err = tx.QueryRow(ctx,
		`SELECT id, employee_no FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, in.Employee).Scan(&employee.id, &employee.no)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employee %s not found", in.Employee)
		}
		return nil, apperr.FromDB(err, "resolve employee")
	}
	if in.Date < roster.weekStarting || in.Date > addDays(roster.weekStarting, 6) {
		return nil, apperr.New(apperr.CodeValidation,
			"date %s is outside roster week %s..%s", in.Date, roster.weekStarting, addDays(roster.weekStarting, 6))
	}
	if err := validatePlacement(ctx, tx, employerID, roster.programmeID, employee.id, employee.no, in.Date, in.RestDay); err != nil {
		return nil, err
	}
	var templateID *string
	var templateCode *string
	if !in.RestDay {
		var tid, tcode string
		err := tx.QueryRow(ctx,
			`SELECT id, code FROM shift_template WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
			employerID, in.Template).Scan(&tid, &tcode)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, apperr.New(apperr.CodeNotFound, "shift template %s not found", in.Template)
			}
			return nil, apperr.FromDB(err, "resolve template")
		}
		templateID, templateCode = &tid, &tcode
	}
	var a Assignment
	a.RevisionID = roster.revisionID
	a.EmployeeID = employee.id
	a.EmployeeNo = employee.no
	a.BusinessDate = in.Date
	a.RestDay = in.RestDay
	err = tx.QueryRow(ctx,
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, template_id, rest_day)
		 VALUES ($1, $2, $3, $4::date, $5, $6)
		 RETURNING id, status`,
		employerID, roster.revisionID, employee.id, in.Date, templateID, in.RestDay).Scan(&a.ID, &a.Status)
	if err != nil {
		return nil, apperr.FromDB(err, "create assignment")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit assignment")
	}
	a.TemplateID, a.TemplateCode = templateID, templateCode
	return &a, nil
}

func addDays(ymd string, n int) string {
	d, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		return ymd
	}
	return d.AddDate(0, 0, n).Format("2006-01-02")
}

// validatePlacement enforces the status, membership, skill and rest-day
// rules for one employee on one date. Assign and Publish share it so a
// roster that validated at assign time revalidates identically at
// publish.
func validatePlacement(ctx context.Context, q dbtx, employerID, programmeID, employeeID, employeeNo, date string, restDay bool) error {
	// Employee must be active on the scheduled date itself, from the
	// effective-dated history — never from wall-clock status. Past
	// dates under an earlier active span stay valid; suspended,
	// resigned and terminated spans cannot receive roster rows.
	var status string
	err := q.QueryRow(ctx,
		`SELECT status FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 AND effective_from <= $3::date
		   AND (effective_to IS NULL OR effective_to >= $3::date)
		 ORDER BY effective_from DESC LIMIT 1`,
		employerID, employeeID, date).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperr.New(apperr.CodeValidation,
				"employee %s has no effective status on %s", employeeNo, date)
		}
		return apperr.FromDB(err, "check employee status")
	}
	if status != "active" {
		return apperr.New(apperr.CodeValidation,
			"employee %s is %s on %s; only active employees are schedulable", employeeNo, status, date)
	}
	// Employee must belong to a team of this programme with an effective membership.
	var member bool
	err = q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM team_membership m JOIN team t
		   ON t.employer_id = m.employer_id AND t.id = m.team_id
		 WHERE m.employer_id = $1 AND m.employee_id = $2 AND t.programme_id = $3
		   AND m.effective_from <= $4::date AND (m.effective_to IS NULL OR m.effective_to >= $4::date))`,
		employerID, employeeID, programmeID, date).Scan(&member)
	if err != nil {
		return apperr.FromDB(err, "check team membership")
	}
	if !member {
		return apperr.New(apperr.CodeValidation, "employee %s has no effective team membership in this programme on %s", employeeNo, date)
	}
	// Employee must hold every required programme skill at the minimum level.
	var missing string
	err = q.QueryRow(ctx,
		`SELECT s.code FROM programme_required_skill r JOIN skill s ON s.id = r.skill_id
		 WHERE r.employer_id = $1 AND r.programme_id = $2
		   AND NOT EXISTS (SELECT 1 FROM employee_skill e
		     WHERE e.employer_id = r.employer_id AND e.employee_id = $3 AND e.skill_id = r.skill_id
		       AND e.level >= r.minimum_level
		       AND e.valid_from <= $4::date AND (e.valid_to IS NULL OR e.valid_to >= $4::date))
		 LIMIT 1`,
		employerID, programmeID, employeeID, date).Scan(&missing)
	if err != nil && err != pgx.ErrNoRows {
		return apperr.FromDB(err, "check required skills")
	}
	if missing != "" {
		return apperr.New(apperr.CodeValidation, "employee %s lacks required skill %s on %s", employeeNo, missing, date)
	}
	// A shift assignment must not land on the employee's contractual rest
	// day; taking rest on another weekday is always fine.
	if !restDay {
		var rest string
		err := q.QueryRow(ctx,
			`SELECT rest_day FROM employment_terms
			 WHERE employer_id = $1 AND employee_id = $2 AND effective_from <= $3::date
			   AND (effective_to IS NULL OR effective_to >= $3::date)
			 ORDER BY effective_from DESC LIMIT 1`,
			employerID, employeeID, date).Scan(&rest)
		if err != nil && err != pgx.ErrNoRows {
			return apperr.FromDB(err, "load rest day")
		}
		if rest != "" {
			if d, derr := time.Parse("2006-01-02", date); derr == nil && d.Weekday().String() == rest {
				return apperr.New(apperr.CodeValidation,
					"employee %s rests on %ss; %s must be a rest-day assignment", employeeNo, rest, date)
			}
		}
	}
	return nil
}

// RosterDetail is a roster with its current assignments.
type RosterDetail struct {
	Roster      Roster       `json:"roster"`
	RevisionID  string       `json:"revision_id"`
	Assignments []Assignment `json:"assignments"`
}

// Show returns the roster with current-revision assignments.
func Show(ctx context.Context, pool *pgxpool.Pool, employerID, rosterID string) (*RosterDetail, error) {
	var d RosterDetail
	err := pool.QueryRow(ctx,
		`SELECT r.id, r.employer_id, r.programme_id, r.week_starting::text, r.status,
		   r.legal_validation_status, r.current_version, rev.id
		 FROM roster r JOIN roster_revision rev
		   ON rev.employer_id = r.employer_id AND rev.roster_id = r.id AND rev.version = r.current_version
		 WHERE r.employer_id = $1 AND r.id = $2`,
		employerID, rosterID).Scan(
		&d.Roster.ID, &d.Roster.EmployerID, &d.Roster.ProgrammeID, &d.Roster.WeekStarting,
		&d.Roster.Status, &d.Roster.LegalValidationStatus, &d.Roster.CurrentVersion, &d.RevisionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "roster not found")
		}
		return nil, apperr.FromDB(err, "show roster")
	}
	rows, err := pool.Query(ctx,
		`SELECT a.id, a.revision_id, a.employee_id, e.employee_no, a.business_date::text,
		   a.template_id, t.code, a.rest_day, a.status
		 FROM roster_assignment a
		 JOIN employee e ON e.id = a.employee_id
		 LEFT JOIN shift_template t ON t.id = a.template_id
		 WHERE a.employer_id = $1 AND a.revision_id = $2
		 ORDER BY a.business_date, e.employee_no`,
		employerID, d.RevisionID)
	if err != nil {
		return nil, apperr.FromDB(err, "list assignments")
	}
	defer rows.Close()
	d.Assignments = []Assignment{}
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.RevisionID, &a.EmployeeID, &a.EmployeeNo, &a.BusinessDate,
			&a.TemplateID, &a.TemplateCode, &a.RestDay, &a.Status); err != nil {
			return nil, apperr.FromDB(err, "scan assignment")
		}
		d.Assignments = append(d.Assignments, a)
	}
	return &d, apperr.FromDB(rows.Err(), "list assignments")
}

// Publish flips a draft roster to published. Legal validation is recorded
// as unavailable: operational checks never imply legal compliance.
// Publish revalidates every current-revision assignment in one
// transaction and, only if all pass, publishes the roster: status,
// revision stamps, check summary and audit row commit together. An
// empty roster cannot publish; stale memberships or skills fail the
// publish with the first offending assignment named.
func Publish(ctx context.Context, pool *pgxpool.Pool, employerID, rosterID, operator string) (*Roster, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin publish")
	}
	defer tx.Rollback(ctx)
	var roster struct {
		programmeID  string
		weekStarting string
		version      int
		revisionID   string
	}
	err = tx.QueryRow(ctx,
		`SELECT r.programme_id, r.week_starting::text, r.current_version, rev.id
		 FROM roster r JOIN roster_revision rev
		   ON rev.employer_id = r.employer_id AND rev.roster_id = r.id AND rev.version = r.current_version
		 WHERE r.employer_id = $1 AND r.id = $2 AND r.status = 'draft' FOR UPDATE OF r`,
		employerID, rosterID).Scan(&roster.programmeID, &roster.weekStarting, &roster.version, &roster.revisionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeInvalidState, "roster not found or not a draft")
		}
		return nil, apperr.FromDB(err, "resolve draft roster")
	}
	rows, err := tx.Query(ctx,
		`SELECT a.id, a.employee_id, e.employee_no, a.business_date::text, a.rest_day
		 FROM roster_assignment a JOIN employee e ON e.id = a.employee_id
		 WHERE a.employer_id = $1 AND a.revision_id = $2 ORDER BY a.business_date, e.employee_no`,
		employerID, roster.revisionID)
	if err != nil {
		return nil, apperr.FromDB(err, "load draft assignments")
	}
	type row struct {
		id         string
		employeeID string
		employeeNo string
		date       string
		restDay    bool
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.employeeID, &r.employeeNo, &r.date, &r.restDay); err != nil {
			rows.Close()
			return nil, apperr.FromDB(err, "scan draft assignments")
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "load draft assignments")
	}
	if len(list) == 0 {
		return nil, apperr.New(apperr.CodeInvalidState, "roster has no assignments")
	}
	for _, a := range list {
		if a.date < roster.weekStarting || a.date > addDays(roster.weekStarting, 6) {
			return nil, apperr.New(apperr.CodeInvalidState,
				"assignment %s on %s is outside roster week %s", a.id, a.date, roster.weekStarting)
		}
		if err := validatePlacement(ctx, tx, employerID, roster.programmeID, a.employeeID, a.employeeNo, a.date, a.restDay); err != nil {
			return nil, err
		}
	}
	note := fmt.Sprintf("publish gate: %d assignments revalidated (membership, skills, week window, rest day)", len(list))
	var r Roster
	err = tx.QueryRow(ctx,
		`UPDATE roster SET status = 'published', published_at = now(), published_by = $3,
		   legal_validation_status = 'unavailable', legal_validation_checked_at = now(),
		   legal_validation_result = 'SG-MOM-SHIFT not IMPLEMENTABLE: operational schedule only'
		 WHERE employer_id = $1 AND id = $2 AND status = 'draft'
		 RETURNING id, employer_id, programme_id, week_starting::text, status, legal_validation_status, current_version`,
		employerID, rosterID, operator).Scan(
		&r.ID, &r.EmployerID, &r.ProgrammeID, &r.WeekStarting, &r.Status, &r.LegalValidationStatus, &r.CurrentVersion)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeInvalidState, "roster not found or not a draft")
		}
		return nil, apperr.FromDB(err, "publish roster")
	}
	// The revision row stays insert-only: publish metadata (revision
	// pointer + check summary) lives in the audit event, not in a
	// revision UPDATE the app role cannot perform.
	if err := audit.Write(ctx, tx, employerID, operator, "wfm", "roster.publish", "roster",
		&r.ID, map[string]string{"status": "draft"},
		map[string]string{"status": "published", "revision": roster.revisionID, "checks": note}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit publish")
	}
	return &r, nil
}

// Revise opens a new draft revision of a published roster: the current
// assignments copy forward, the pointer advances, and the roster
// returns to draft. Published history stays immutable in the old
// revision rows, which the freeze trigger keeps read-only.
func Revise(ctx context.Context, pool *pgxpool.Pool, employerID, rosterID, reason, operator string) (*Roster, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, apperr.New(apperr.CodeValidation, "revise requires a reason")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin revise")
	}
	defer tx.Rollback(ctx)
	var cur struct {
		version    int
		revisionID string
	}
	var r Roster
	err = tx.QueryRow(ctx,
		`SELECT r.id, r.employer_id, r.programme_id, r.week_starting::text, r.status,
		   r.legal_validation_status, r.current_version, rev.id
		 FROM roster r JOIN roster_revision rev
		   ON rev.employer_id = r.employer_id AND rev.roster_id = r.id AND rev.version = r.current_version
		 WHERE r.employer_id = $1 AND r.id = $2 AND r.status = 'published' FOR UPDATE OF r`,
		employerID, rosterID).Scan(
		&r.ID, &r.EmployerID, &r.ProgrammeID, &r.WeekStarting, &r.Status,
		&r.LegalValidationStatus, &r.CurrentVersion, &cur.revisionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeInvalidState, "roster not found or not published")
		}
		return nil, apperr.FromDB(err, "resolve published roster")
	}
	cur.version = r.CurrentVersion
	// Order matters for the freeze trigger: create the revision, flip
	// the roster back to draft and advance the pointer, then copy the
	// assignments into the new current draft.
	var newRev string
	err = tx.QueryRow(ctx,
		`INSERT INTO roster_revision (employer_id, roster_id, version, note)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		employerID, rosterID, cur.version+1, reason).Scan(&newRev)
	if err != nil {
		return nil, apperr.FromDB(err, "create revision")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE roster SET status = 'draft', current_version = $3 WHERE employer_id = $1 AND id = $2`,
		employerID, rosterID, cur.version+1); err != nil {
		return nil, apperr.FromDB(err, "advance revision pointer")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO roster_assignment (employer_id, revision_id, employee_id, business_date, template_id, rest_day)
		 SELECT employer_id, $3, employee_id, business_date, template_id, rest_day
		 FROM roster_assignment WHERE employer_id = $1 AND revision_id = $2`,
		employerID, cur.revisionID, newRev); err != nil {
		return nil, apperr.FromDB(err, "copy assignments forward")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "wfm", "roster.revise", "roster",
		&r.ID, map[string]any{"status": "published", "version": cur.version},
		map[string]any{"status": "draft", "version": cur.version + 1, "reason": reason}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit revise")
	}
	r.Status = "draft"
	r.CurrentVersion = cur.version + 1
	return &r, nil
}

// ListRosters returns all rosters of one employer.
func ListRosters(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Roster, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, programme_id, week_starting::text, status, legal_validation_status, current_version
		 FROM roster WHERE employer_id = $1 ORDER BY week_starting`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list rosters")
	}
	defer rows.Close()
	out := []Roster{}
	for rows.Next() {
		var r Roster
		if err := rows.Scan(&r.ID, &r.EmployerID, &r.ProgrammeID, &r.WeekStarting, &r.Status,
			&r.LegalValidationStatus, &r.CurrentVersion); err != nil {
			return nil, apperr.FromDB(err, "scan roster")
		}
		out = append(out, r)
	}
	return out, apperr.FromDB(rows.Err(), "list rosters")
}
