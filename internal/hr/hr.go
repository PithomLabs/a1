// Package hr implements employer administration: employees, status
// changes, effective-dated employment terms and compensation lines.
// Synthetic reference data only; no NRIC handling.
package hr

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// Employee statuses permitted by the schema.
const (
	StatusActive     = "active"
	StatusSuspended  = "suspended"
	StatusResigned   = "resigned"
	StatusTerminated = "terminated"
)

// Employee is one row with its employer scope.
type Employee struct {
	ID                  string `json:"id"`
	EmployerID          string `json:"employer_id"`
	EmployeeNo          string `json:"employee_no"`
	LegalName           string `json:"legal_name"`
	Status              string `json:"status"`
	StatusEffectiveDate string `json:"status_effective_date"`
}

// EmployeeInput creates an employee. All identity fields are required;
// synthetic values are expected in demo data.
type EmployeeInput struct {
	EmployeeNo          string `json:"employee_no"`
	LegalName           string `json:"legal_name"`
	Address             string `json:"address"`
	Gender              string `json:"gender"`
	DateOfBirth         string `json:"date_of_birth"`
	Citizenship         string `json:"citizenship"`
	Status              string `json:"status"`
	StatusEffectiveDate string `json:"status_effective_date"`
}

// CreateEmployee inserts one employee under the employer together with
// its coherent initial status-history row.
func CreateEmployee(ctx context.Context, pool *pgxpool.Pool, employerID string, in EmployeeInput, operator string) (*Employee, error) {
	in.EmployeeNo = strings.TrimSpace(in.EmployeeNo)
	in.LegalName = strings.TrimSpace(in.LegalName)
	if in.EmployeeNo == "" || in.LegalName == "" || in.Address == "" || in.Gender == "" ||
		in.DateOfBirth == "" || in.Citizenship == "" {
		return nil, apperr.New(apperr.CodeValidation, "employee_no, legal_name, address, gender, date_of_birth and citizenship are required")
	}
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	switch in.Status {
	case StatusActive, StatusSuspended, StatusResigned, StatusTerminated:
	default:
		return nil, apperr.New(apperr.CodeValidation, "unknown status %q", in.Status)
	}
	if in.StatusEffectiveDate == "" {
		in.StatusEffectiveDate = time.Now().UTC().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", in.StatusEffectiveDate); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "status_effective_date must be YYYY-MM-DD")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin create employee")
	}
	defer tx.Rollback(ctx)
	// Population first: a new employee must not enter the expected
	// set mid-capture. The INSERT trigger re-locks for direct SQL.
	if err := coredatabase.LockPayrollPopulation(ctx, tx, employerID); err != nil {
		return nil, err
	}
	var e Employee
	err = tx.QueryRow(ctx,
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::date)
		 RETURNING id, employer_id, employee_no, legal_name, status, status_effective_date::text`,
		employerID, in.EmployeeNo, in.LegalName, in.Address, in.Gender, in.DateOfBirth,
		in.Citizenship, in.Status, in.StatusEffectiveDate).Scan(
		&e.ID, &e.EmployerID, &e.EmployeeNo, &e.LegalName, &e.Status, &e.StatusEffectiveDate)
	if err != nil {
		return nil, apperr.FromDB(err, "create employee")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO employee_status_history (employer_id, employee_id, status, effective_from, reason)
		 VALUES ($1, $2, $3, $4::date, 'initial status')`,
		employerID, e.ID, e.Status, e.StatusEffectiveDate); err != nil {
		return nil, apperr.FromDB(err, "initial status history")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "hr", "employee.create", "employee",
		&e.ID, nil, map[string]string{
			"employee_no": e.EmployeeNo, "legal_name": e.LegalName,
			"status": e.Status, "status_effective_date": e.StatusEffectiveDate,
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit create employee")
	}
	return &e, nil
}

// ListEmployees returns all employees of one employer.
func ListEmployees(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Employee, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, employee_no, legal_name, status, status_effective_date::text
		 FROM employee WHERE employer_id = $1 ORDER BY employee_no`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list employees")
	}
	defer rows.Close()
	out := []Employee{}
	for rows.Next() {
		var e Employee
		if err := rows.Scan(&e.ID, &e.EmployerID, &e.EmployeeNo, &e.LegalName, &e.Status, &e.StatusEffectiveDate); err != nil {
			return nil, apperr.FromDB(err, "scan employee")
		}
		out = append(out, e)
	}
	return out, apperr.FromDB(rows.Err(), "list employees")
}

// GetEmployee resolves employee number or UUID within the employer.
func GetEmployee(ctx context.Context, pool *pgxpool.Pool, employerID, noOrID string) (*Employee, error) {
	var e Employee
	err := pool.QueryRow(ctx,
		`SELECT id, employer_id, employee_no, legal_name, status, status_effective_date::text
		 FROM employee WHERE employer_id = $1 AND (employee_no = $2 OR id::text = $2)`,
		employerID, noOrID).Scan(&e.ID, &e.EmployerID, &e.EmployeeNo, &e.LegalName, &e.Status, &e.StatusEffectiveDate)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employee %s not found", noOrID)
		}
		return nil, apperr.FromDB(err, "get employee")
	}
	return &e, nil
}

// allowedTransitions is the status lifecycle. Resigned and terminated
// are terminal: nobody comes back from them in this model.
var allowedTransitions = map[string][]string{
	StatusActive:     {StatusSuspended, StatusResigned, StatusTerminated},
	StatusSuspended:  {StatusActive, StatusResigned, StatusTerminated},
	StatusResigned:   {},
	StatusTerminated: {},
}

// SetStatus changes status, closes the prior history row, appends the
// successor and audits the change atomically. Ranges are inclusive, so
// the prior row closes the day before the new row opens.
func SetStatus(ctx context.Context, pool *pgxpool.Pool, employerID, employeeID, status, effectiveDate, reason, operator string) (*Employee, error) {
	switch status {
	case StatusActive, StatusSuspended, StatusResigned, StatusTerminated:
	default:
		return nil, apperr.New(apperr.CodeValidation, "unknown status %q", status)
	}
	if effectiveDate == "" {
		effectiveDate = time.Now().UTC().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", effectiveDate); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "effective date must be YYYY-MM-DD")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, apperr.New(apperr.CodeValidation, "status change requires a reason")
	}
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin status change")
	}
	defer tx.Rollback(ctx)
	// Global order population → employee: status changes move
	// population membership. The controlled function re-locks the
	// population key for direct-SQL callers.
	if err := coredatabase.LockPayrollPopulation(ctx, tx, employerID); err != nil {
		return nil, err
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, employeeID); err != nil {
		return nil, err
	}
	var current struct {
		status string
		from   string
	}
	// No FOR UPDATE here: the app role cannot lock rows it may not
	// UPDATE. This read is advisory; the controlled function below
	// re-validates under its own lock, which is the serialization
	// point for concurrent transitions.
	err = tx.QueryRow(ctx,
		`SELECT status, status_effective_date::text FROM employee
		 WHERE employer_id = $1 AND id = $2`,
		employerID, employeeID).Scan(&current.status, &current.from)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "employee not found")
		}
		return nil, apperr.FromDB(err, "load current status")
	}
	if current.status == status {
		return nil, apperr.New(apperr.CodeInvalidState, "employee is already %s", status)
	}
	allowed := false
	for _, next := range allowedTransitions[current.status] {
		if next == status {
			allowed = true
		}
	}
	if !allowed {
		return nil, apperr.New(apperr.CodeInvalidState,
			"status transition %s -> %s is not allowed", current.status, status)
	}
	var priorFrom string
	if err := tx.QueryRow(ctx,
		`SELECT effective_from::text FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 AND effective_to IS NULL`,
		employerID, employeeID).Scan(&priorFrom); err != nil && err != pgx.ErrNoRows {
		return nil, apperr.FromDB(err, "load prior status")
	}
	if priorFrom != "" && effectiveDate <= priorFrom {
		return nil, apperr.New(apperr.CodeInvalidState,
			"effective date %s must be after the current row's %s", effectiveDate, priorFrom)
	}
	// The app role cannot UPDATE employee or status history directly:
	// the controlled function validates the transition, closes the
	// open row, appends the successor, moves the employee pointer and
	// audits, atomically. The Go checks above stay so service callers
	// keep their exact validation errors.
	if _, err := tx.Exec(ctx, `SELECT transition_employee_status($1::uuid, $2::uuid, $3, $4::date, $5, $6)`,
		employerID, employeeID, status, effectiveDate, reason, operator); err != nil {
		return nil, apperr.FromDB(err, "transition employee status")
	}
	var e Employee
	err = tx.QueryRow(ctx,
		`SELECT id, employer_id, employee_no, legal_name, status, status_effective_date::text
		 FROM employee WHERE employer_id = $1 AND id = $2`,
		employerID, employeeID).Scan(
		&e.ID, &e.EmployerID, &e.EmployeeNo, &e.LegalName, &e.Status, &e.StatusEffectiveDate)
	if err != nil {
		return nil, apperr.FromDB(err, "read employee status")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit status change")
	}
	return &e, nil
}

// Terms holds the employment-terms row relevant to later slices.
type Terms struct {
	ID                 string  `json:"id"`
	EmployerID         string  `json:"employer_id"`
	EmployeeID         string  `json:"employee_id"`
	EffectiveFrom      string  `json:"effective_from"`
	EffectiveTo        *string `json:"effective_to"`
	EmploymentCategory string  `json:"employment_category"`
	PartIVEligible     bool    `json:"part_iv_eligible"`
	MonthlyBasic       string  `json:"monthly_basic"`
}

// TermsInput creates one effective-dated terms row. Category and Part IV
// eligibility are explicitly recorded data, never inferred. MonthlyBasic
// is an exact decimal string, never float64.
type TermsInput struct {
	EffectiveFrom      string  `json:"effective_from"`
	EffectiveTo        *string `json:"effective_to"`
	JobTitle           string  `json:"job_title"`
	JobDuties          string  `json:"job_duties"`
	EmploymentCategory string  `json:"employment_category"`
	PartIVEligible     bool    `json:"part_iv_eligible"`
	EligibilityBasis   string  `json:"eligibility_basis"`
	MonthlyBasic       string  `json:"monthly_basic"`
	WorkingArrangement string  `json:"working_arrangement"`
	NormalHoursWeekly  float64 `json:"normal_hours_weekly"`
	WorkingDays        string  `json:"working_days"`
	DailyHours         float64 `json:"daily_hours"`
	RestDay            string  `json:"rest_day"`
	StartDate          string  `json:"start_date"`
	EndDate            *string `json:"end_date"`
	ContractType       string  `json:"contract_type"`
	PlaceOfWork        string  `json:"place_of_work"`
	SalaryPeriod       string  `json:"salary_period"`
}

// moneyPattern is the exact money-input shape: non-negative decimal
// with at most two fraction digits. Money never passes through float64.
var moneyPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)

// validateMoney rejects anything NUMERIC(12,2) cannot hold exactly.
func validateMoney(field, v string) error {
	v = strings.TrimSpace(v)
	if !moneyPattern.MatchString(v) {
		return apperr.New(apperr.CodeValidation, "%s must be a non-negative decimal with at most 2 places", field)
	}
	intPart := v
	if i := strings.IndexByte(v, '.'); i >= 0 {
		intPart = v[:i]
	}
	intPart = strings.TrimLeft(intPart, "0")
	if len(intPart) > 10 {
		return apperr.New(apperr.CodeValidation, "%s exceeds the maximum payable amount", field)
	}
	return nil
}

// SetTerms closes the open predecessor (when the new row starts after
// it) and inserts the successor atomically, then audits the change.
// Backdated rows overlapping closed history are rejected by the
// exclusion constraint. A new range intersecting an approved
// timesheet's period is rejected loudly: approved results must never
// change silently under a terms edit.
// ValidRestDays is the canonical rest-day representation: full English
// weekday names matching time.Weekday.String, which the roster
// rest-day check compares against. Anything else cannot be stored.
var ValidRestDays = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func SetTerms(ctx context.Context, pool *pgxpool.Pool, employerID, employeeID string, in TermsInput, operator string) (*Terms, error) {
	switch in.EmploymentCategory {
	case "workman", "non_workman", "manager_executive":
	default:
		return nil, apperr.New(apperr.CodeValidation, "unknown employment_category %q", in.EmploymentCategory)
	}
	validRest := false
	for _, d := range ValidRestDays {
		if in.RestDay == d {
			validRest = true
		}
	}
	if !validRest {
		return nil, apperr.New(apperr.CodeValidation, "unknown rest_day %q: use a full weekday name", in.RestDay)
	}
	if in.EffectiveFrom == "" || in.JobTitle == "" || in.EligibilityBasis == "" || in.StartDate == "" ||
		in.ContractType == "" || in.PlaceOfWork == "" || in.SalaryPeriod == "" || in.WorkingArrangement == "" {
		return nil, apperr.New(apperr.CodeValidation, "effective_from, job_title, eligibility_basis, start_date, contract_type, place_of_work, salary_period and working_arrangement are required")
	}
	if _, err := time.Parse("2006-01-02", in.EffectiveFrom); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "effective_from must be YYYY-MM-DD")
	}
	if in.EffectiveTo != nil {
		if _, err := time.Parse("2006-01-02", *in.EffectiveTo); err != nil {
			return nil, apperr.New(apperr.CodeValidation, "effective_to must be YYYY-MM-DD")
		}
		if *in.EffectiveTo < in.EffectiveFrom {
			return nil, apperr.New(apperr.CodeValidation, "effective_to must not precede effective_from")
		}
	}
	if err := validateMoney("monthly_basic", in.MonthlyBasic); err != nil {
		return nil, err
	}
	if in.NormalHoursWeekly < 0 || in.DailyHours < 0 {
		return nil, apperr.New(apperr.CodeValidation, "hours must be non-negative")
	}
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin set terms")
	}
	defer tx.Rollback(ctx)
	// Global order population → employee: terms changes move
	// population membership, and the approved-period check below must
	// not race an approval.
	if err := coredatabase.LockPayrollPopulation(ctx, tx, employerID); err != nil {
		return nil, err
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, employeeID); err != nil {
		return nil, err
	}
	// Close the open predecessor when the successor starts after it.
	// No FOR UPDATE: the app role cannot lock terms rows. This read
	// is advisory; close_employment_terms re-validates under lock.
	var priorFrom string
	err = tx.QueryRow(ctx,
		`SELECT effective_from::text FROM employment_terms
		 WHERE employer_id = $1 AND employee_id = $2 AND effective_to IS NULL`,
		employerID, employeeID).Scan(&priorFrom)
	if err != nil && err != pgx.ErrNoRows {
		return nil, apperr.FromDB(err, "load open terms")
	}
	if priorFrom != "" {
		if in.EffectiveFrom <= priorFrom {
			return nil, apperr.New(apperr.CodeInvalidState,
				"effective_from %s must be after the open row's %s", in.EffectiveFrom, priorFrom)
		}
		// The app role cannot UPDATE terms history directly: the
		// controlled function locks, validates, closes and audits
		// the predecessor in one step.
		if _, err := tx.Exec(ctx, `SELECT close_employment_terms($1::uuid, $2::uuid, $3::date, $4)`,
			employerID, employeeID, in.EffectiveFrom, operator); err != nil {
			return nil, apperr.FromDB(err, "close prior terms")
		}
	}
	// Retroactive block: the new range must not intersect an approved
	// sheet's period.
	var clashPeriod string
	err = tx.QueryRow(ctx,
		`SELECT p.code FROM timesheet t JOIN pay_period p ON p.id = t.pay_period_id
		 WHERE t.employer_id = $1 AND t.employee_id = $2 AND t.status = 'approved'
		   AND p.starts_on <= COALESCE($4::date, '9999-12-31') AND p.ends_on >= $3::date
		 LIMIT 1`,
		employerID, employeeID, in.EffectiveFrom, nullStr(in.EffectiveTo)).Scan(&clashPeriod)
	if err != nil && err != pgx.ErrNoRows {
		return nil, apperr.FromDB(err, "check approved periods")
	}
	if clashPeriod != "" {
		return nil, apperr.New(apperr.CodeConflict,
			"terms range intersects approved period %s; use a prospective date or reissue the sheet", clashPeriod)
	}
	var t Terms
	err = tx.QueryRow(ctx,
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, effective_to, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, end_date,
		  contract_type, place_of_work, salary_period)
		 VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::date, $17, $18, $19, $20)
		 RETURNING id, employer_id, employee_id, effective_from::text, effective_to::text,
		  employment_category, part_iv_eligible, monthly_basic::text`,
		employerID, employeeID, in.EffectiveFrom, nullStr(in.EffectiveTo), in.JobTitle, in.JobDuties,
		in.EmploymentCategory, in.PartIVEligible, in.EligibilityBasis, strings.TrimSpace(in.MonthlyBasic), in.WorkingArrangement,
		in.NormalHoursWeekly, in.WorkingDays, in.DailyHours, in.RestDay, in.StartDate, nullStr(in.EndDate),
		in.ContractType, in.PlaceOfWork, in.SalaryPeriod).Scan(
		&t.ID, &t.EmployerID, &t.EmployeeID, &t.EffectiveFrom, &t.EffectiveTo,
		&t.EmploymentCategory, &t.PartIVEligible, &t.MonthlyBasic)
	if err != nil {
		return nil, apperr.FromDB(err, "set employment terms")
	}
	if err := audit.Write(ctx, tx, employerID, operator, "hr", "terms.set", "employment_terms",
		&t.ID, nil, map[string]string{"from": t.EffectiveFrom, "monthly_basic": t.MonthlyBasic}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit set terms")
	}
	return &t, nil
}

// ListTerms returns all terms rows for one employee, newest first.
func ListTerms(ctx context.Context, pool *pgxpool.Pool, employerID, employeeID string) ([]Terms, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, employee_id, effective_from::text, effective_to::text,
		  employment_category, part_iv_eligible, monthly_basic::text
		 FROM employment_terms WHERE employer_id = $1 AND employee_id = $2
		 ORDER BY effective_from DESC`, employerID, employeeID)
	if err != nil {
		return nil, apperr.FromDB(err, "list terms")
	}
	defer rows.Close()
	out := []Terms{}
	for rows.Next() {
		var t Terms
		if err := rows.Scan(&t.ID, &t.EmployerID, &t.EmployeeID, &t.EffectiveFrom, &t.EffectiveTo,
			&t.EmploymentCategory, &t.PartIVEligible, &t.MonthlyBasic); err != nil {
			return nil, apperr.FromDB(err, "scan terms")
		}
		out = append(out, t)
	}
	return out, apperr.FromDB(rows.Err(), "list terms")
}

// CompensationInput adds one allowance or deduction line to a terms row.
type CompensationInput struct {
	Kind          string  `json:"kind"`
	Code          string  `json:"code"`
	AmountMonthly string  `json:"amount_monthly"`
	EffectiveFrom string  `json:"effective_from"`
	EffectiveTo   *string `json:"effective_to"`
	Reason        string  `json:"reason"`
}

// CorrectionInput corrects one compensation line: the successor keeps
// the original's classification and links back to it.
type CorrectionInput struct {
	AmountMonthly string  `json:"amount_monthly"`
	EffectiveFrom string  `json:"effective_from"`
	EffectiveTo   *string `json:"effective_to"`
	Reason        string  `json:"reason"`
}

// Compensation is one allowance or deduction line with its exact amount.
type Compensation struct {
	ID            string  `json:"id"`
	EmployerID    string  `json:"employer_id"`
	EmployeeID    string  `json:"employee_id"`
	TermsID       string  `json:"terms_id"`
	Kind          string  `json:"kind"`
	Code          string  `json:"code"`
	AmountMonthly string  `json:"amount_monthly"`
	EffectiveFrom string  `json:"effective_from"`
	EffectiveTo   *string `json:"effective_to"`
	SupersedesID  *string `json:"supersedes_id,omitempty"`
}

// validateCompMoneyAndDates is the shared shape check for the add and
// correct paths: exact cents, ISO dates, ordered range. Containment,
// overlap, and succession live in the database, which re-validates.
func validateCompMoneyAndDates(amount, from string, to *string) error {
	if _, err := time.Parse("2006-01-02", from); err != nil {
		return apperr.New(apperr.CodeValidation, "effective_from must be YYYY-MM-DD")
	}
	if to != nil {
		if _, err := time.Parse("2006-01-02", *to); err != nil {
			return apperr.New(apperr.CodeValidation, "effective_to must be YYYY-MM-DD")
		}
		if *to < from {
			return apperr.New(apperr.CodeValidation, "effective_to must not precede effective_from")
		}
	}
	return validateMoney("amount_monthly", amount)
}

// AddCompensation inserts one compensation line under the terms row
// through the controlled add_compensation function, which owns the
// barrier lock, containment, overlap, and the canonical audit row.
func AddCompensation(ctx context.Context, pool *pgxpool.Pool, employerID, termsID string, in CompensationInput, operator string) (string, error) {
	switch in.Kind {
	case "allowance", "deduction":
	default:
		return "", apperr.New(apperr.CodeValidation, "unknown compensation kind %q", in.Kind)
	}
	if in.Code == "" || in.EffectiveFrom == "" {
		return "", apperr.New(apperr.CodeValidation, "code and effective_from are required")
	}
	if err := validateCompMoneyAndDates(in.AmountMonthly, in.EffectiveFrom, in.EffectiveTo); err != nil {
		return "", err
	}
	if strings.TrimSpace(operator) == "" {
		return "", apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return "", apperr.New(apperr.CodeValidation, "a reason is required")
	}
	var compEmployee string
	if err := pool.QueryRow(ctx,
		`SELECT employee_id FROM employment_terms WHERE employer_id = $1 AND id = $2`,
		employerID, termsID).Scan(&compEmployee); err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "terms %s not found", termsID)
		}
		return "", apperr.FromDB(err, "resolve terms employee")
	}
	var id string
	err := pool.QueryRow(ctx,
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::numeric, $7::date, $8::date, $9, $10)::text`,
		employerID, compEmployee, termsID, in.Kind, in.Code, strings.TrimSpace(in.AmountMonthly),
		in.EffectiveFrom, nullStr(in.EffectiveTo), operator, in.Reason).Scan(&id)
	if err != nil {
		return "", apperr.FromDB(err, "add compensation line")
	}
	return id, nil
}

// CorrectCompensation mints the audited successor of one compensation
// line. The original row is never mutated; a second successor of the
// same original is rejected.
func CorrectCompensation(ctx context.Context, pool *pgxpool.Pool, employerID, originalID string, in CorrectionInput, operator string) (string, error) {
	if in.EffectiveFrom == "" {
		return "", apperr.New(apperr.CodeValidation, "effective_from is required")
	}
	if err := validateCompMoneyAndDates(in.AmountMonthly, in.EffectiveFrom, in.EffectiveTo); err != nil {
		return "", err
	}
	if strings.TrimSpace(operator) == "" {
		return "", apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return "", apperr.New(apperr.CodeValidation, "a reason is required")
	}
	var compEmployee string
	if err := pool.QueryRow(ctx,
		`SELECT employee_id FROM compensation_line WHERE employer_id = $1 AND id = $2`,
		employerID, originalID).Scan(&compEmployee); err != nil {
		if err == pgx.ErrNoRows {
			return "", apperr.New(apperr.CodeNotFound, "compensation %s not found", originalID)
		}
		return "", apperr.FromDB(err, "resolve compensation employee")
	}
	var id string
	err := pool.QueryRow(ctx,
		`SELECT correct_compensation($1::uuid, $2::uuid, $3::uuid, $4::numeric, $5::date, $6::date, $7, $8)::text`,
		employerID, compEmployee, originalID, strings.TrimSpace(in.AmountMonthly),
		in.EffectiveFrom, nullStr(in.EffectiveTo), operator, in.Reason).Scan(&id)
	if err != nil {
		return "", apperr.FromDB(err, "correct compensation line")
	}
	return id, nil
}

// ListCompensation returns all compensation lines under one terms row.
func ListCompensation(ctx context.Context, pool *pgxpool.Pool, employerID, termsID string) ([]Compensation, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, employer_id, employee_id, terms_id, kind, code, amount_monthly::text,
		 effective_from::text, effective_to::text, supersedes_id::text
		 FROM compensation_line WHERE employer_id = $1 AND terms_id = $2 ORDER BY code`,
		employerID, termsID)
	if err != nil {
		return nil, apperr.FromDB(err, "list compensation")
	}
	defer rows.Close()
	out := []Compensation{}
	for rows.Next() {
		var c Compensation
		if err := rows.Scan(&c.ID, &c.EmployerID, &c.EmployeeID, &c.TermsID, &c.Kind, &c.Code,
			&c.AmountMonthly, &c.EffectiveFrom, &c.EffectiveTo, &c.SupersedesID); err != nil {
			return nil, apperr.FromDB(err, "scan compensation")
		}
		out = append(out, c)
	}
	return out, apperr.FromDB(rows.Err(), "list compensation")
}

func nullStr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}
