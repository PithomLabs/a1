// Package timesheet generates draft timesheets from reviewed attendance,
// and moves them through submit, approve and reject. Generation links
// every line to its source intervals; it computes no pay.
package timesheet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
	"github.com/PithomLabs/a1/internal/duration"
	"github.com/PithomLabs/a1/internal/employer"
)

// dbtx is the query surface shared by *pgxpool.Pool and pgx.Tx.
type dbtx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// RegularPayCode carries reviewed work minutes into timesheets.
const RegularPayCode = "REG"

// Timesheet is one employee period timesheet with its lines.
// SupersedesID links a correction version to the sheet it corrects;
// originals carry nil.
type Timesheet struct {
	ID           string  `json:"id"`
	EmployerID   string  `json:"employer_id"`
	EmployeeID   string  `json:"employee_id"`
	EmployeeNo   string  `json:"employee_no"`
	PayPeriodID  string  `json:"pay_period_id"`
	Status       string  `json:"status"`
	SupersedesID *string `json:"supersedes_id,omitempty"`
	Lines        []Line  `json:"lines"`
}

// Line is one pay-code line with its source intervals. HoursUs is the
// authoritative exact total; Hours is the explicitly quantized display.
type Line struct {
	ID          string   `json:"id"`
	PayCode     string   `json:"pay_code"`
	Hours       float64  `json:"hours"`
	HoursUs     int64    `json:"hours_us"`
	IntervalIDs []string `json:"interval_ids"`
}

// Generation summarizes one generate run. Employees with unpaired events
// or exception-flagged days are skipped explicitly, never guessed.
type Generation struct {
	PayPeriodID string           `json:"pay_period_id"`
	Generated   []GeneratedSheet `json:"generated"`
	Skipped     []Skip           `json:"skipped"`
}

// GeneratedSheet names one created timesheet.
type GeneratedSheet struct {
	EmployeeNo  string `json:"employee_no"`
	TimesheetID string `json:"timesheet_id"`
}

// Skip records one employee excluded from generation with the reason.
type Skip struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
}

// Generate builds draft timesheets with a single REG line for every
// employee with current attendance evidence in one pay period, whatever
// their current status: final-period work for terminated staff must
// still generate. Only evidence-clean employees qualify (no unpaired
// events); others are skipped explicitly, never guessed.
//
// Partial-commit semantics: each employee generates in its own
// transaction, so a hard error aborts the remaining employees while
// committed sheets stand. The caller sees the error, not a partial
// report; rerunning is safe (existing sheets skip, never duplicate).
func Generate(ctx context.Context, pool *pgxpool.Pool, employerID, periodCodeOrID, operator string) (*Generation, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	var period struct {
		id    string
		start string
		end   string
	}
	err := pool.QueryRow(ctx,
		`SELECT id, starts_on::text, ends_on::text FROM pay_period WHERE employer_id = $1 AND (code = $2 OR id::text = $2)`,
		employerID, periodCodeOrID).Scan(&period.id, &period.start, &period.end)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "pay period %s not found", periodCodeOrID)
		}
		return nil, apperr.FromDB(err, "resolve pay period")
	}
	gen := &Generation{PayPeriodID: period.id, Generated: []GeneratedSheet{}, Skipped: []Skip{}}
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT e.id, e.employee_no FROM employee e
		 JOIN attendance_interval i ON i.employer_id = e.employer_id AND i.employee_id = e.id
		 JOIN pay_period p ON p.employer_id = e.employer_id AND p.id = $2
		 WHERE e.employer_id = $1 AND i.is_current
		   AND i.local_date BETWEEN p.starts_on AND p.ends_on
		 ORDER BY e.employee_no`, employerID, period.id)
	if err != nil {
		return nil, apperr.FromDB(err, "list evidenced employees")
	}
	defer rows.Close()
	type emp struct{ id, no string }
	var emps []emp
	for rows.Next() {
		var e emp
		if err := rows.Scan(&e.id, &e.no); err != nil {
			return nil, apperr.FromDB(err, "scan employee")
		}
		emps = append(emps, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "list active employees")
	}
	for _, e := range emps {
		skip, err := checkEligible(ctx, pool, employerID, e.id, period.start, period.end)
		if err != nil {
			return nil, err
		}
		if skip != "" {
			gen.Skipped = append(gen.Skipped, Skip{EmployeeNo: e.no, Reason: skip})
			continue
		}
		sheetID, err := generateOne(ctx, pool, employerID, e.id, e.no, period.id, period.start, period.end)
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) && ae.Code == apperr.CodeConflict {
				gen.Skipped = append(gen.Skipped, Skip{EmployeeNo: e.no, Reason: "timesheet already exists"})
				continue
			}
			return nil, err
		}
		gen.Generated = append(gen.Generated, GeneratedSheet{EmployeeNo: e.no, TimesheetID: sheetID})
	}
	if err := audit.Write(ctx, pool, employerID, operator, "timesheet",
		"timesheet.generate", "pay_period", &period.id, nil, map[string]any{
			"generated": gen.Generated,
			"skipped":   gen.Skipped,
		}); err != nil {
		return nil, err
	}
	return gen, nil
}

// Period is one pay period.
type Period struct {
	ID            string `json:"id"`
	EmployerID    string `json:"employer_id"`
	Code          string `json:"code"`
	StartsOn      string `json:"starts_on"`
	EndsOn        string `json:"ends_on"`
	PayDate       string `json:"pay_date"`
	SalaryDeadine string `json:"salary_deadline"`
	OTDeadline    string `json:"ot_deadline"`
	Status        string `json:"status"`
}

// ListPeriods returns all pay periods of one employer.
func ListPeriods(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Period, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, code, starts_on::text, ends_on::text, pay_date::text,
		   salary_deadline::text, ot_deadline::text, status
		 FROM pay_period WHERE employer_id = $1 ORDER BY starts_on`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list pay periods")
	}
	defer rows.Close()
	out := []Period{}
	for rows.Next() {
		var p Period
		if err := rows.Scan(&p.ID, &p.EmployerID, &p.Code, &p.StartsOn, &p.EndsOn,
			&p.PayDate, &p.SalaryDeadine, &p.OTDeadline, &p.Status); err != nil {
			return nil, apperr.FromDB(err, "scan pay period")
		}
		out = append(out, p)
	}
	return out, apperr.FromDB(rows.Err(), "list pay periods")
}

// PeriodInput creates one pay period. Code is unique per employer;
// ranges must not overlap an existing period (the exclusion
// constraint rejects overlaps the service check races with).
type PeriodInput struct {
	Code           string `json:"code"`
	StartsOn       string `json:"starts_on"`
	EndsOn         string `json:"ends_on"`
	PayDate        string `json:"pay_date"`
	SalaryDeadline string `json:"salary_deadline"`
	OTDeadline     string `json:"ot_deadline"`
}

// CreatePeriod inserts one open pay period after machine-validating
// every date. Deadline ordering follows the payroll calendar:
// salary pays within 7 days of period end, overtime within 14.
func CreatePeriod(ctx context.Context, pool *pgxpool.Pool, employerID string, in PeriodInput, operator string) (*Period, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(in.Code) == "" {
		return nil, apperr.New(apperr.CodeValidation, "period code is required")
	}
	dates := map[string]string{
		"starts_on": in.StartsOn, "ends_on": in.EndsOn, "pay_date": in.PayDate,
		"salary_deadline": in.SalaryDeadline, "ot_deadline": in.OTDeadline,
	}
	parsed := map[string]string{}
	for field, v := range dates {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return nil, apperr.New(apperr.CodeValidation, "%s must be YYYY-MM-DD", field)
		}
		parsed[field] = t.Format("2006-01-02")
	}
	if parsed["ends_on"] < parsed["starts_on"] {
		return nil, apperr.New(apperr.CodeValidation, "ends_on must not precede starts_on")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin pay period")
	}
	defer tx.Rollback(ctx)
	var p Period
	err = tx.QueryRow(ctx,
		`INSERT INTO pay_period (id, employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES (gen_random_uuid(), $1, $2, $3::date, $4::date, $5::date, $6::date, $7::date)
		 RETURNING id, employer_id, code, starts_on::text, ends_on::text, pay_date::text,
		   salary_deadline::text, ot_deadline::text, status`,
		employerID, in.Code, parsed["starts_on"], parsed["ends_on"],
		parsed["pay_date"], parsed["salary_deadline"], parsed["ot_deadline"]).Scan(
		&p.ID, &p.EmployerID, &p.Code, &p.StartsOn, &p.EndsOn,
		&p.PayDate, &p.SalaryDeadine, &p.OTDeadline, &p.Status)
	if err != nil {
		return nil, apperr.FromDB(err, "create pay period")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "payroll",
		"pay_period.create", "pay_period", &p.ID, nil, p); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit pay period")
	}
	return &p, nil
}

// SheetSummary is one timesheet row for listing.
type SheetSummary struct {
	ID         string `json:"id"`
	EmployeeNo string `json:"employee_no"`
	PeriodCode string `json:"period_code"`
	Status     string `json:"status"`
}

// ListTimesheets returns timesheets filtered by optional period and
// employee codes or IDs.
func ListTimesheets(ctx context.Context, pool *pgxpool.Pool, employerID, period, employee string) ([]SheetSummary, error) {
	rows, err := pool.Query(ctx,
		`SELECT t.id, e.employee_no, p.code, t.status
		 FROM timesheet t JOIN employee e ON e.id = t.employee_id
		 JOIN pay_period p ON p.id = t.pay_period_id
		 WHERE t.employer_id = $1
		   AND ($2 = '' OR p.code = $2 OR p.id::text = $2)
		   AND ($3 = '' OR e.employee_no = $3 OR e.id::text = $3)
		 ORDER BY p.starts_on, e.employee_no`,
		employerID, period, employee)
	if err != nil {
		return nil, apperr.FromDB(err, "list timesheets")
	}
	defer rows.Close()
	out := []SheetSummary{}
	for rows.Next() {
		var s SheetSummary
		if err := rows.Scan(&s.ID, &s.EmployeeNo, &s.PeriodCode, &s.Status); err != nil {
			return nil, apperr.FromDB(err, "scan timesheet")
		}
		out = append(out, s)
	}
	return out, apperr.FromDB(rows.Err(), "list timesheets")
}

// checkEligible rejects employees with unpaired events, exception-flagged
// days, or no current intervals. The event-date comparison converts each
// event to the employment timezone: vendor provenance never attributes
// business dates, and neither session TimeZone nor host timezone can
// shift a midnight boundary.
func checkEligible(ctx context.Context, q dbtx, employerID, employeeID, start, end string) (string, error) {
	loc, err := employer.EmploymentLocation(ctx, q, employerID)
	if err != nil {
		return "", err
	}
	var unpaired int
	err = q.QueryRow(ctx,
		`SELECT count(*) FROM clock_event e
		 WHERE e.employer_id = $1 AND e.employee_id = $2
		   AND (e.occurred_at AT TIME ZONE $5)::date BETWEEN $3::date AND $4::date
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)
		   AND NOT EXISTS (SELECT 1 FROM clock_event_disposition d WHERE d.event_id = e.id)`,
		employerID, employeeID, start, end, loc.String()).Scan(&unpaired)
	if err != nil {
		return "", apperr.FromDB(err, "check unpaired events")
	}
	if unpaired > 0 {
		return fmt.Sprintf("%d unpaired clock events", unpaired), nil
	}
	var flagged int
	err = q.QueryRow(ctx,
		`SELECT count(*) FROM attendance_record
		 WHERE employer_id = $1 AND employee_id = $2 AND business_date BETWEEN $3::date AND $4::date
		   AND status = 'exception'`,
		employerID, employeeID, start, end).Scan(&flagged)
	if err != nil {
		return "", apperr.FromDB(err, "check exception days")
	}
	if flagged > 0 {
		return fmt.Sprintf("%d exception-flagged days", flagged), nil
	}
	var intervals int
	err = q.QueryRow(ctx,
		`SELECT count(*) FROM attendance_interval i
		 WHERE i.employer_id = $1 AND i.employee_id = $2 AND i.local_date BETWEEN $3::date AND $4::date
		   AND i.kind IN ('work', 'break_paid') AND i.is_current`,
		employerID, employeeID, start, end).Scan(&intervals)
	if err != nil {
		return "", apperr.FromDB(err, "count reviewed intervals")
	}
	if intervals == 0 {
		return "no reviewed intervals in period", nil
	}
	return "", nil
}

// generateOne builds one draft timesheet with a REG line over current
// work and paid-break intervals, linked to every source interval. The
// eligibility gate re-runs inside the transaction under the employee's
// advisory lock, so a concurrent import cannot slip unpaired events in
// between the check and the build.
func generateOne(ctx context.Context, pool *pgxpool.Pool, employerID, employeeID, employeeNo, periodID, start, end string) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", apperr.FromDB(err, "begin generation")
	}
	defer tx.Rollback(ctx)
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, employeeID); err != nil {
		return "", err
	}
	if skip, err := checkEligible(ctx, tx, employerID, employeeID, start, end); err != nil {
		return "", err
	} else if skip != "" {
		return "", apperr.New(apperr.CodeConflict, "timesheet for %s became ineligible: %s", employeeNo, skip)
	}
	var sheetID string
	err = tx.QueryRow(ctx,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id) VALUES ($1, $2, $3) RETURNING id`,
		employerID, employeeID, periodID).Scan(&sheetID)
	if err != nil {
		return "", apperr.FromDB(err, fmt.Sprintf("create timesheet for %s", employeeNo))
	}
	if err := buildRegLine(ctx, tx, employerID, employeeID, employeeNo, periodID, start, end, sheetID, false); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", apperr.FromDB(err, "commit timesheet")
	}
	return sheetID, nil
}

// buildRegLine writes the single REG line of sheetID over the current
// work and paid-break intervals of the period, linked to each source
// interval. Shared by generation and draft regeneration. Corrections
// and reissues may legitimately empty a period; allowEmpty lets those
// paths keep an honest lineless draft instead of failing.
func buildRegLine(ctx context.Context, tx pgx.Tx, employerID, employeeID, employeeNo, periodID, start, end, sheetID string, allowEmpty bool) error {
	rows, err := tx.Query(ctx,
		`SELECT i.id, i.interval_start, i.interval_end
		 FROM attendance_interval i
		 WHERE i.employer_id = $1 AND i.employee_id = $2 AND i.local_date BETWEEN $3::date AND $4::date
		   AND i.kind IN ('work', 'break_paid') AND i.is_current
		 ORDER BY i.interval_start`,
		employerID, employeeID, start, end)
	if err != nil {
		return apperr.FromDB(err, "load reviewed intervals")
	}
	var ids []string
	var total int64
	for rows.Next() {
		var id string
		var istart, iend time.Time
		if err := rows.Scan(&id, &istart, &iend); err != nil {
			rows.Close()
			return apperr.FromDB(err, "scan interval")
		}
		ids = append(ids, id)
		total += duration.MicrosOf(istart, iend)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return apperr.FromDB(err, "load reviewed intervals")
	}
	if len(ids) == 0 {
		if allowEmpty {
			return nil
		}
		return apperr.New(apperr.CodeInvalidState, "timesheet for %s has no reviewed intervals", employeeNo)
	}
	hours := duration.Quantize2(duration.HoursOf(total))
	var lineID string
	err = tx.QueryRow(ctx,
		`INSERT INTO timesheet_line (employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		employerID, sheetID, RegularPayCode, hours, total, periodID).Scan(&lineID)
	if err != nil {
		return apperr.FromDB(err, "create timesheet line")
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx,
			`INSERT INTO timesheet_line_interval (employer_id, line_id, interval_id) VALUES ($1, $2, $3)`,
			employerID, lineID, id); err != nil {
			return apperr.FromDB(err, "link line interval")
		}
	}
	return nil
}

// RegenerateDraft rebuilds the REG line of a draft sheet from the current
// intervals inside the caller's transaction. Corrections call it so no
// draft keeps stale hours. Non-draft sheets are rejected: they reissue,
// never mutate.
func RegenerateDraft(ctx context.Context, tx pgx.Tx, employerID, sheetID string) error {
	var sheet struct {
		employeeID string
		employeeNo string
		periodID   string
		start      string
		end        string
		status     string
	}
	err := tx.QueryRow(ctx,
		`SELECT t.employee_id, e.employee_no, t.pay_period_id, p.starts_on::text, p.ends_on::text, t.status
		 FROM timesheet t JOIN employee e ON e.id = t.employee_id
		 JOIN pay_period p ON p.id = t.pay_period_id
		 WHERE t.employer_id = $1 AND t.id = $2 FOR UPDATE OF t`,
		employerID, sheetID).Scan(&sheet.employeeID, &sheet.employeeNo,
		&sheet.periodID, &sheet.start, &sheet.end, &sheet.status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperr.New(apperr.CodeNotFound, "timesheet %s not found", sheetID)
		}
		return apperr.FromDB(err, "load draft sheet")
	}
	if sheet.status != "draft" {
		return apperr.New(apperr.CodeInvalidState, "timesheet %s is %s, not draft", sheetID, sheet.status)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM timesheet_line_interval WHERE employer_id = $1 AND line_id IN
		 (SELECT id FROM timesheet_line WHERE employer_id = $1 AND timesheet_id = $2)`,
		employerID, sheetID); err != nil {
		return apperr.FromDB(err, "clear draft links")
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM timesheet_line WHERE employer_id = $1 AND timesheet_id = $2`,
		employerID, sheetID); err != nil {
		return apperr.FromDB(err, "clear draft lines")
	}
	return buildRegLine(ctx, tx, employerID, sheet.employeeID, sheet.employeeNo,
		sheet.periodID, sheet.start, sheet.end, sheetID, true)
}

// Reissue opens a correction version of an approved or rejected sheet.
// The original is never mutated: the new draft links to the terminal
// sheet of the chain via supersedes_id and builds its REG line from the
// current intervals. Drafts correct in place (no reissue); submitted
// sheets must be decided first. Only one open correction may exist per
// chain: a draft or submitted successor blocks until it is decided.
func Reissue(ctx context.Context, pool *pgxpool.Pool, employerID, sheetID, operator string) (*Timesheet, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin reissue")
	}
	defer tx.Rollback(ctx)
	var orig struct {
		id         string
		employeeID string
		employeeNo string
		periodID   string
		start      string
		end        string
		status     string
	}
	err = tx.QueryRow(ctx,
		`SELECT t.id, t.employee_id, e.employee_no, t.pay_period_id, p.starts_on::text, p.ends_on::text, t.status
		 FROM timesheet t JOIN employee e ON e.id = t.employee_id
		 JOIN pay_period p ON p.id = t.pay_period_id
		 WHERE t.employer_id = $1 AND t.id = $2 FOR UPDATE OF t`,
		employerID, sheetID).Scan(&orig.id, &orig.employeeID, &orig.employeeNo,
		&orig.periodID, &orig.start, &orig.end, &orig.status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "timesheet %s not found", sheetID)
		}
		return nil, apperr.FromDB(err, "load sheet")
	}
	switch orig.status {
	case "approved", "rejected":
	case "draft":
		return nil, apperr.New(apperr.CodeInvalidState, "timesheet %s is already a draft; correct it in place", sheetID)
	default:
		return nil, apperr.New(apperr.CodeInvalidState, "timesheet %s is %s; decide it before reissuing", sheetID, orig.status)
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, orig.employeeID); err != nil {
		return nil, err
	}
	// Walk to the terminal sheet; an open correction blocks a second one.
	terminal := orig.id
	for {
		var next struct {
			id     string
			status string
		}
		err := tx.QueryRow(ctx,
			`SELECT id, status FROM timesheet WHERE employer_id = $1 AND supersedes_id = $2 FOR UPDATE`,
			employerID, terminal).Scan(&next.id, &next.status)
		if err != nil {
			if err == pgx.ErrNoRows {
				break
			}
			return nil, apperr.FromDB(err, "walk correction chain")
		}
		if next.status == "draft" || next.status == "submitted" {
			return nil, apperr.New(apperr.CodeInvalidState,
				"correction %s is already open; decide it before reissuing", next.id)
		}
		terminal = next.id
	}
	var newID string
	err = tx.QueryRow(ctx,
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, supersedes_id)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		employerID, orig.employeeID, orig.periodID, terminal).Scan(&newID)
	if err != nil {
		return nil, apperr.FromDB(err, "create correction sheet")
	}
	if err := buildRegLine(ctx, tx, employerID, orig.employeeID, orig.employeeNo,
		orig.periodID, orig.start, orig.end, newID, true); err != nil {
		return nil, err
	}
	if err := audit.Write(ctx, tx, employerID, operator, "timesheet",
		"timesheet.reissue", "timesheet", &newID,
		map[string]any{"sheet_id": terminal, "status": orig.status},
		map[string]any{"sheet_id": newID, "status": "draft", "supersedes_id": terminal}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit reissue")
	}
	return Get(ctx, pool, employerID, newID)
}

// EffectiveApproved returns the approved sheet that counts for payroll:
// the deepest approval walking down the employee's correction chain
// from its single original. A draft correction never displaces the
// standing approval; a rejected correction restores it; an approval
// further down the same chain supersedes it. An empty string means
// no approval exists on the chain.
//
// The walk validates the structure it traverses instead of picking
// an arbitrary row: multiple originals, branches, links leaving the
// employee or pay period, chains without an original, and walks past
// the shared chain cap are all refused loudly.
func EffectiveApproved(ctx context.Context, q dbtx, employerID, employeeID, periodID string) (string, error) {
	rows, err := q.Query(ctx,
		`WITH RECURSIVE c(id, status, employee_id, pay_period_id, depth) AS (
		    SELECT t.id, t.status, t.employee_id, t.pay_period_id, 0
		    FROM timesheet t
		    WHERE t.employer_id = $1 AND t.employee_id = $2 AND t.pay_period_id = $3
		      AND t.supersedes_id IS NULL
		    UNION ALL
		    SELECT t.id, t.status, t.employee_id, t.pay_period_id, c.depth + 1
		    FROM timesheet t JOIN c ON t.supersedes_id = c.id AND t.employer_id = $1
		    WHERE c.depth < $4
		)
		SELECT id, status, employee_id, pay_period_id, depth FROM c ORDER BY depth`,
		employerID, employeeID, periodID, chainWalkLimit)
	if err != nil {
		return "", apperr.FromDB(err, "resolve effective approval")
	}
	defer rows.Close()
	type link struct {
		id, status, employee, period string
		depth                        int
	}
	var chain []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.id, &l.status, &l.employee, &l.period, &l.depth); err != nil {
			return "", apperr.FromDB(err, "scan effective approval")
		}
		chain = append(chain, l)
	}
	if err := rows.Err(); err != nil {
		return "", apperr.FromDB(err, "resolve effective approval")
	}
	if len(chain) == 0 {
		var any bool
		if err := q.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM timesheet
			 WHERE employer_id = $1 AND employee_id = $2 AND pay_period_id = $3)`,
			employerID, employeeID, periodID).Scan(&any); err != nil {
			return "", apperr.FromDB(err, "check chain existence")
		}
		if any {
			return "", apperr.New(apperr.CodeInvalidState,
				"timesheet chain for period %s has sheets but no original", periodID)
		}
		return "", nil
	}
	effective := ""
	for i, l := range chain {
		if l.depth != i {
			return "", apperr.New(apperr.CodeInvalidState,
				"timesheet chain for period %s branches or gaps at depth %d", periodID, l.depth)
		}
		if l.depth >= chainWalkLimit {
			return "", apperr.New(apperr.CodeInvalidState,
				"timesheet chain exceeds %d versions", chainWalkLimit)
		}
		if l.employee != employeeID || l.period != periodID {
			return "", apperr.New(apperr.CodeInvalidState,
				"timesheet chain for period %s leaves its employee or period at %s", periodID, l.id)
		}
		if l.status == "approved" {
			effective = l.id
		}
	}
	return effective, nil
}

// Get returns one timesheet with lines and source intervals.
func Get(ctx context.Context, pool *pgxpool.Pool, employerID, sheetID string) (*Timesheet, error) {
	var t Timesheet
	err := pool.QueryRow(ctx,
		`SELECT t.id, t.employer_id, t.employee_id, e.employee_no, t.pay_period_id, t.status, t.supersedes_id
		 FROM timesheet t JOIN employee e ON e.id = t.employee_id
		 WHERE t.employer_id = $1 AND t.id = $2`,
		employerID, sheetID).Scan(&t.ID, &t.EmployerID, &t.EmployeeID, &t.EmployeeNo, &t.PayPeriodID, &t.Status, &t.SupersedesID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "timesheet not found")
		}
		return nil, apperr.FromDB(err, "get timesheet")
	}
	rows, err := pool.Query(ctx,
		`SELECT id, pay_code, hours, hours_us FROM timesheet_line WHERE employer_id = $1 AND timesheet_id = $2 ORDER BY pay_code`,
		employerID, sheetID)
	if err != nil {
		return nil, apperr.FromDB(err, "list lines")
	}
	defer rows.Close()
	t.Lines = []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.PayCode, &l.Hours, &l.HoursUs); err != nil {
			return nil, apperr.FromDB(err, "scan line")
		}
		l.IntervalIDs, err = lineIntervals(ctx, pool, employerID, l.ID)
		if err != nil {
			return nil, err
		}
		t.Lines = append(t.Lines, l)
	}
	return &t, apperr.FromDB(rows.Err(), "list lines")
}

func lineIntervals(ctx context.Context, pool *pgxpool.Pool, employerID, lineID string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT interval_id FROM timesheet_line_interval WHERE employer_id = $1 AND line_id = $2 ORDER BY interval_id`,
		employerID, lineID)
	if err != nil {
		return nil, apperr.FromDB(err, "list line intervals")
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.FromDB(err, "scan line interval")
		}
		out = append(out, id)
	}
	return out, apperr.FromDB(rows.Err(), "list line intervals")
}

// Submit moves a draft timesheet to submitted.
func Submit(ctx context.Context, pool *pgxpool.Pool, employerID, sheetID, operator string) (*Timesheet, error) {
	return transition(ctx, pool, employerID, sheetID, "draft", "submitted", operator, false)
}

// Approve approves a submitted timesheet. Approving an already-approved
// sheet is idempotent and returns the current state.
func Approve(ctx context.Context, pool *pgxpool.Pool, employerID, sheetID, operator string) (*Timesheet, error) {
	t, err := Get(ctx, pool, employerID, sheetID)
	if err != nil {
		return nil, err
	}
	if t.Status == "approved" {
		return t, nil
	}
	return transition(ctx, pool, employerID, sheetID, "submitted", "approved", operator, true)
}

// Reject rejects a submitted timesheet with the deciding operator.
func Reject(ctx context.Context, pool *pgxpool.Pool, employerID, sheetID, operator string) (*Timesheet, error) {
	return transition(ctx, pool, employerID, sheetID, "submitted", "rejected", operator, true)
}

func transition(ctx context.Context, pool *pgxpool.Pool, employerID, sheetID, from, to, operator string, stamp bool) (*Timesheet, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin timesheet transition")
	}
	defer tx.Rollback(ctx)
	// Amendment A: transitions join the payroll-input barrier. The
	// employee key read is safe pre-lock (sheet identity is
	// trigger-frozen); the status UPDATE below re-validates under
	// the lock, so a race loser sees the winner's committed state.
	var transitionEmployee string
	if err := tx.QueryRow(ctx,
		`SELECT employee_id FROM timesheet WHERE employer_id = $1 AND id = $2`,
		employerID, sheetID).Scan(&transitionEmployee); err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeInvalidState, "timesheet not found or not %s", from)
		}
		return nil, apperr.FromDB(err, "resolve timesheet employee")
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, transitionEmployee); err != nil {
		return nil, err
	}
	set := `status = $3`
	args := []any{employerID, sheetID, to, from}
	if stamp {
		set = `status = $3, approved_by = $5, approved_at = now()`
		args = append(args, operator)
	} else if to == "submitted" {
		set = `status = $3, submitted_at = now()`
	}
	var id string
	err = tx.QueryRow(ctx,
		`UPDATE timesheet SET `+set+` WHERE employer_id = $1 AND id = $2 AND status = $4 RETURNING id`,
		args...).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeInvalidState, "timesheet not found or not %s", from)
		}
		return nil, apperr.FromDB(err, "transition timesheet")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "timesheet",
		"timesheet."+to, "timesheet", &id,
		map[string]any{"status": from}, map[string]any{"status": to}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit timesheet transition")
	}
	return Get(ctx, pool, employerID, id)
}
