package payroll

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/compbasis"
	"github.com/PithomLabs/a1/internal/employer"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// BasePacks are required for every included employee: contributions,
// levies, helper-board instructions and the payslip record itself.
var BasePacks = []string{"SG-CPF-2026", "SG-SDL", "SG-SHG", "SG-MOM-KET"}

// PackStatus is one pack's activation state for the employer.
type PackStatus struct {
	Pack    string `json:"pack"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// BlockedDetail names one employee blocked on one pack.
type BlockedDetail struct {
	EmployeeNo string `json:"employee_no"`
	Pack       string `json:"pack"`
	Status     string `json:"status"`
}

// MissingDetail names one employee missing required input.
type MissingDetail struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
}

// ExcludedEmployee is one explicitly excluded employee, always visible.
type ExcludedEmployee struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
	Actor      string `json:"actor"`
}

// TermConflict names one employee whose terms changed after their
// sheet was approved: the approval no longer reflects the record.
type TermConflict struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
}

// AttendanceConflict names one employee with attendance evidence
// created after their sheet was approved: corrected intervals or
// unpaired clock events the approval does not cover. A newer approval
// built from the current evidence clears it.
type AttendanceConflict struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
}

// CompConflict names one employee with compensation effective in the
// period but created after their sheet was approved: the approval's
// pay basis is stale. A newer approval clears it.
type CompConflict struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
}

// SHGConflict names one employee with an SHG instruction effective in
// the period but created after their sheet was approved: a correction
// or verification the approval does not cover. A newer approval
// built from the current instructions clears it.
type SHGConflict struct {
	EmployeeNo string `json:"employee_no"`
	Reason     string `json:"reason"`
}

// Readiness is the payroll-readiness assessment. It reports facts and a
// verdict; it computes no money and approves nothing.
type Readiness struct {
	EmployerID          string               `json:"employer_id"`
	PayPeriodID         string               `json:"pay_period_id"`
	RunID               *string              `json:"run_id"`
	RunStatus           *string              `json:"run_status,omitempty"`
	Population          int                  `json:"population"`
	Expected            []string             `json:"expected"`
	WithApproved        []string             `json:"with_approved"`
	Included            []string             `json:"included"`
	Excluded            []ExcludedEmployee   `json:"excluded"`
	TermConflicts       []TermConflict       `json:"term_conflicts"`
	AttendanceConflicts []AttendanceConflict `json:"attendance_conflicts"`
	CompConflicts       []CompConflict       `json:"comp_conflicts"`
	SHGConflicts        []SHGConflict        `json:"shg_conflicts"`
	Packs               []PackStatus         `json:"packs"`
	Blocked             []BlockedDetail      `json:"blocked"`
	MissingInfo         []MissingDetail      `json:"missing_info"`
	Ready               bool                 `json:"ready"`
	Verdict             string               `json:"verdict"`
}

// Assess evaluates payroll readiness for the period. When runID is given,
// the run must exist for this employer and period, and its explicit
// exclusions apply and are reported visibly.
//
// The expected population is effective-dated: employees whose terms
// overlap the period, union anyone holding an approved sheet for it
// (terminated staff with approved history stay visible). Approvals
// resolve to the effective version of each sheet chain; empty or
// zero-hour approvals do not count as complete.
func Assess(ctx context.Context, pool *pgxpool.Pool, employerID, periodCodeOrID string, runID *string) (*Readiness, error) {
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
	out := &Readiness{
		EmployerID: employerID, PayPeriodID: period.id, RunID: runID,
		Expected: []string{}, WithApproved: []string{}, Included: []string{},
		Excluded: []ExcludedEmployee{}, TermConflicts: []TermConflict{},
		AttendanceConflicts: []AttendanceConflict{}, CompConflicts: []CompConflict{},
		SHGConflicts: []SHGConflict{},
		Packs:        []PackStatus{}, Blocked: []BlockedDetail{}, MissingInfo: []MissingDetail{},
	}
	activations, err := loadActivations(ctx, pool, employerID, out)
	if err != nil {
		return nil, err
	}
	exclusions, excludedIDs, err := loadExclusions(ctx, pool, employerID, period.id, runID, out)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT id, employee_no FROM employee WHERE employer_id = $1 AND (
		   EXISTS (SELECT 1 FROM employment_terms t
		     WHERE t.employer_id = employee.employer_id AND t.employee_id = employee.id
		       AND t.effective_from <= $3::date AND (t.effective_to IS NULL OR t.effective_to >= $2::date))
		   OR EXISTS (SELECT 1 FROM timesheet t
		     WHERE t.employer_id = employee.employer_id AND t.employee_id = employee.id
		       AND t.pay_period_id = $4 AND t.status = 'approved'))
		 ORDER BY employee_no`,
		employerID, period.start, period.end, period.id)
	if err != nil {
		return nil, apperr.FromDB(err, "list expected employees")
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
		out.Expected = append(out.Expected, e.no)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "list expected employees")
	}
	out.Population = len(emps)
	req := ApprovalRequest{Exclusions: exclusions, Activations: activations}
	for _, e := range emps {
		if excludedIDs[e.id] {
			continue
		}
		sheetID, err := timesheet.EffectiveApproved(ctx, pool, employerID, e.id, period.id)
		if err != nil {
			return nil, err
		}
		if sheetID == "" {
			out.MissingInfo = append(out.MissingInfo, MissingDetail{EmployeeNo: e.no, Reason: "no approved timesheet for period"})
			continue
		}
		out.WithApproved = append(out.WithApproved, e.no)
		missing, err := checkEmployee(ctx, pool, employerID, e.id, period.id, sheetID, period.start, period.end)
		if err != nil {
			return nil, err
		}
		if missing != "" {
			out.MissingInfo = append(out.MissingInfo, MissingDetail{EmployeeNo: e.no, Reason: missing})
			continue
		}
		if conflict, err := checkTermConflict(ctx, pool, employerID, e.id, period.id, sheetID); err != nil {
			return nil, err
		} else if conflict != "" {
			out.TermConflicts = append(out.TermConflicts, TermConflict{EmployeeNo: e.no, Reason: conflict})
			continue
		}
		if conflict, err := checkAttendanceConflict(ctx, pool, employerID, e.id, period.id, sheetID); err != nil {
			return nil, err
		} else if conflict != "" {
			out.AttendanceConflicts = append(out.AttendanceConflicts, AttendanceConflict{EmployeeNo: e.no, Reason: conflict})
			continue
		}
		if conflict, err := checkCompConflict(ctx, pool, employerID, e.id, period.id, sheetID); err != nil {
			return nil, err
		} else if conflict != "" {
			out.CompConflicts = append(out.CompConflicts, CompConflict{EmployeeNo: e.no, Reason: conflict})
			continue
		}
		if conflict, err := checkSHGConflict(ctx, pool, employerID, e.id, period.id, sheetID); err != nil {
			return nil, err
		} else if conflict != "" {
			out.SHGConflicts = append(out.SHGConflicts, SHGConflict{EmployeeNo: e.no, Reason: conflict})
			continue
		}
		// P3: the compensation basis resolves through the single
		// shared resolver; an ambiguous or invalid chain blocks
		// with a machine-readable conflict instead of guessing.
		if _, err := compbasis.ResolveEffectiveCompensation(ctx, pool, employerID, e.id, period.start, period.end); err != nil {
			var bc *compbasis.BasisConflict
			if errors.As(err, &bc) {
				out.CompConflicts = append(out.CompConflicts, CompConflict{EmployeeNo: e.no, Reason: bc.Error()})
				continue
			}
			return nil, err
		}
		packs, err := requiredPacks(ctx, pool, employerID, sheetID)
		if err != nil {
			return nil, err
		}
		out.Included = append(out.Included, e.no)
		req.Included = append(req.Included, EmployeeRequirement{EmployeeID: e.id, RequiredPacks: packs})
	}
	if err := AuthorizeApproval(req); err != nil {
		var blocked *BlockedError
		if !asBlocked(err, &blocked) {
			return nil, err
		}
		byID := map[string]string{}
		for _, e := range emps {
			byID[e.id] = e.no
		}
		for _, b := range blocked.Blocked {
			status := b.Status
			if status == "" {
				status = "not activated"
			}
			no := byID[b.EmployeeID]
			if no == "" {
				no = b.Pack
			}
			out.Blocked = append(out.Blocked, BlockedDetail{EmployeeNo: no, Pack: b.Pack, Status: status})
		}
	}
	switch {
	case len(out.Expected) == 0:
		out.Verdict = "not ready: empty population"
	case len(out.Included) == 0:
		out.Verdict = "not ready: no included employees with complete input"
	case len(out.Blocked) > 0 || len(out.MissingInfo) > 0 || len(out.TermConflicts) > 0 || len(out.AttendanceConflicts) > 0 || len(out.CompConflicts) > 0 || len(out.SHGConflicts) > 0:
		out.Verdict = fmt.Sprintf("not ready: %d blocked, %d missing input, %d term conflicts, %d attendance conflicts, %d comp conflicts, %d shg conflicts",
			len(out.Blocked), len(out.MissingInfo), len(out.TermConflicts), len(out.AttendanceConflicts), len(out.CompConflicts), len(out.SHGConflicts))
	default:
		out.Ready = true
		out.Verdict = "ready: all expected employees resolve all required packs"
	}
	return out, nil
}

func asBlocked(err error, target **BlockedError) bool {
	if b, ok := err.(*BlockedError); ok {
		*target = b
		return true
	}
	return false
}

func loadActivations(ctx context.Context, pool *pgxpool.Pool, employerID string, out *Readiness) (map[string]Activation, error) {
	rows, err := pool.Query(ctx,
		`SELECT pack, version, status FROM rule_pack_activation WHERE employer_id = $1 ORDER BY pack`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "load activations")
	}
	defer rows.Close()
	acts := map[string]Activation{}
	for rows.Next() {
		var a Activation
		if err := rows.Scan(&a.Pack, &a.Version, &a.Status); err != nil {
			return nil, apperr.FromDB(err, "scan activation")
		}
		acts[a.Pack] = a
		out.Packs = append(out.Packs, PackStatus{Pack: a.Pack, Version: a.Version, Status: a.Status})
	}
	return acts, apperr.FromDB(rows.Err(), "load activations")
}

func loadExclusions(ctx context.Context, pool *pgxpool.Pool, employerID, periodID string, runID *string, out *Readiness) ([]Exclusion, map[string]bool, error) {
	excludedIDs := map[string]bool{}
	if runID == nil {
		return nil, excludedIDs, nil
	}
	// The run must exist for this employer and period; anything else is a
	// caller error, never an empty exclusion set.
	var status string
	err := pool.QueryRow(ctx,
		`SELECT status FROM payroll_run WHERE employer_id = $1 AND id = $2 AND pay_period_id = $3`,
		employerID, *runID, periodID).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, apperr.New(apperr.CodeNotFound, "payroll run %s not found for this employer and period", *runID)
		}
		return nil, nil, apperr.FromDB(err, "resolve payroll run")
	}
	out.RunStatus = &status
	rows, err := pool.Query(ctx,
		`SELECT x.employee_id, e.employee_no, x.reason, x.actor
		 FROM payroll_run_exclusion x JOIN employee e ON e.id = x.employee_id
		 WHERE x.employer_id = $1 AND x.payroll_run_id = $2 ORDER BY e.employee_no`,
		employerID, *runID)
	if err != nil {
		return nil, nil, apperr.FromDB(err, "load exclusions")
	}
	defer rows.Close()
	var out2 []Exclusion
	for rows.Next() {
		var id, no, reason, actor string
		if err := rows.Scan(&id, &no, &reason, &actor); err != nil {
			return nil, nil, apperr.FromDB(err, "scan exclusion")
		}
		out2 = append(out2, Exclusion{EmployeeID: id, Reason: reason, Actor: actor})
		out.Excluded = append(out.Excluded, ExcludedEmployee{EmployeeNo: no, Reason: reason, Actor: actor})
		excludedIDs[id] = true
	}
	return out2, excludedIDs, apperr.FromDB(rows.Err(), "load exclusions")
}

// checkEmployee returns the missing-input reason, or "" when the
// effective sheet carries payable lines and the employee has terms and
// a covering SHG chain tip for the period.
func checkEmployee(ctx context.Context, q qtx, employerID, employeeID, periodID, sheetID, start, end string) (string, error) {
	var lines int
	var hours float64
	err := q.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(hours), 0) FROM timesheet_line
		 WHERE employer_id = $1 AND timesheet_id = $2`,
		employerID, sheetID).Scan(&lines, &hours)
	if err != nil {
		return "", apperr.FromDB(err, "check sheet lines")
	}
	if lines == 0 || hours <= 0 {
		return "approved timesheet has no payable lines", nil
	}
	var terms bool
	err = q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM employment_terms t
		 WHERE t.employer_id = $1 AND t.employee_id = $2
		   AND t.effective_from <= $4::date AND (t.effective_to IS NULL OR t.effective_to >= $3::date))`,
		employerID, employeeID, start, end).Scan(&terms)
	if err != nil {
		return "", apperr.FromDB(err, "check current terms")
	}
	if !terms {
		return "no effective employment terms for period", nil
	}
	return checkSHGCoverage(ctx, q, employerID, employeeID, start, end)
}

// checkSHGCoverage evaluates the employee's SHG chain tips covering the
// period: only successorless rows count, so superseded history never
// blocks. It returns "" when covered, else a machine-readable coded
// blocker: a corrupt verified_none tip first, then coverage by any
// active or fully-evidenced verified_none tip, else the pending or
// opt-out state of the live tips.
func checkSHGCoverage(ctx context.Context, q qtx, employerID, employeeID, start, end string) (string, error) {
	rows, err := q.Query(ctx,
		`SELECT s.fund, s.status,
		  (s.verified_at IS NOT NULL AND s.verified_by IS NOT NULL AND s.verification_basis IS NOT NULL) AS tripled
		 FROM shg_instruction s
		 WHERE s.employer_id = $1 AND s.employee_id = $2
		   AND s.effective_from <= $4::date AND (s.effective_to IS NULL OR s.effective_to >= $3::date)
		   AND NOT EXISTS (SELECT 1 FROM shg_instruction t WHERE t.supersedes_id = s.id)
		 ORDER BY s.fund`,
		employerID, employeeID, start, end)
	if err != nil {
		return "", apperr.FromDB(err, "check SHG instruction")
	}
	defer rows.Close()
	type tip struct {
		fund, status string
		tripled      bool
	}
	var tips []tip
	for rows.Next() {
		var tp tip
		if err := rows.Scan(&tp.fund, &tp.status, &tp.tripled); err != nil {
			return "", apperr.FromDB(err, "scan SHG instruction")
		}
		tips = append(tips, tp)
	}
	if err := rows.Err(); err != nil {
		return "", apperr.FromDB(err, "check SHG instruction")
	}
	if len(tips) == 0 {
		return "SHG_INSTRUCTION_MISSING: no SHG instruction for period", nil
	}
	for _, tp := range tips {
		if tp.status == "verified_none" && !tp.tripled {
			return "SHG_CORRUPT: " + tp.fund + " verified_none instruction lacks its evidence triple", nil
		}
	}
	for _, tp := range tips {
		if tp.status == "active" || tp.status == "verified_none" {
			return "", nil
		}
	}
	for _, tp := range tips {
		if tp.status == "pending" {
			return "SHG_PENDING: " + tp.fund + " instruction recorded but unverified", nil
		}
	}
	return "SHG_OPT_OUT: " + tips[0].fund + " instruction opted out", nil
}

// checkSHGConflict mirrors checkTermConflict for SHG evidence: an
// instruction overlapping the period but created after the effective
// approval (a correction, a verification, or a late declaration) means
// the approval predates the record. Either leg proves it: a newer
// lock-serialized basis version or a newer creation timestamp.
func checkSHGConflict(ctx context.Context, q qtx, employerID, employeeID, periodID, sheetID string) (string, error) {
	var approvedAt *time.Time
	var approvalSeq int64
	err := q.QueryRow(ctx,
		`SELECT approved_at, basis_seq FROM timesheet WHERE employer_id = $1 AND id = $2`,
		employerID, sheetID).Scan(&approvedAt, &approvalSeq)
	if err != nil {
		return "", apperr.FromDB(err, "load approval time")
	}
	if approvedAt == nil {
		return "approved sheet has no approval timestamp", nil
	}
	var clash string
	err = q.QueryRow(ctx,
		`SELECT s.fund FROM shg_instruction s JOIN pay_period p ON p.id = $3
		 WHERE s.employer_id = $1 AND s.employee_id = $2
		   AND s.effective_from <= p.ends_on AND (s.effective_to IS NULL OR s.effective_to >= p.starts_on)
		   AND (s.created_at > $4 OR s.basis_seq > $5)
		 ORDER BY s.fund LIMIT 1`,
		employerID, employeeID, periodID, *approvedAt, approvalSeq).Scan(&clash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", apperr.FromDB(err, "check SHG conflicts")
	}
	return "SHG instruction " + clash + " created after approval", nil
}

// checkTermConflict reports when a terms row covering the period was
// created after the sheet was approved: the approval predates the
// record it would pay against. Either leg proves it: a newer
// lock-serialized basis version (exact under the barrier) or a newer
// creation timestamp (preserves pre-migration behavior for legacy
// rows backfilled at seq 0).
func checkTermConflict(ctx context.Context, q qtx, employerID, employeeID, periodID, sheetID string) (string, error) {
	var approvedAt *time.Time
	var approvalSeq int64
	err := q.QueryRow(ctx,
		`SELECT approved_at, basis_seq FROM timesheet WHERE employer_id = $1 AND id = $2`,
		employerID, sheetID).Scan(&approvedAt, &approvalSeq)
	if err != nil {
		return "", apperr.FromDB(err, "load approval time")
	}
	if approvedAt == nil {
		return "approved sheet has no approval timestamp", nil
	}
	var clash string
	err = q.QueryRow(ctx,
		`SELECT t.effective_from::text FROM employment_terms t JOIN pay_period p ON p.id = $3
		 WHERE t.employer_id = $1 AND t.employee_id = $2
		   AND t.effective_from <= p.ends_on AND (t.effective_to IS NULL OR t.effective_to >= p.starts_on)
		   AND (t.created_at > $4 OR t.basis_seq > $5)
		 ORDER BY t.effective_from LIMIT 1`,
		employerID, employeeID, periodID, *approvedAt, approvalSeq).Scan(&clash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", apperr.FromDB(err, "check term conflicts")
	}
	return "terms effective " + clash + " created after approval", nil
}

// checkAttendanceConflict mirrors checkTermConflict for attendance
// evidence: intervals created after the effective approval (corrections
// and late-paired spans land here) and clock events still neither
// consumed nor confirmed duplicate. Event dates attribute in the
// employment timezone, never the vendor's. Handled evidence —
// pre-approval rows and dispositioned duplicates — never conflicts.
func checkAttendanceConflict(ctx context.Context, q qtx, employerID, employeeID, periodID, sheetID string) (string, error) {
	var approvedAt *time.Time
	var approvalSeq int64
	err := q.QueryRow(ctx,
		`SELECT approved_at, basis_seq FROM timesheet WHERE employer_id = $1 AND id = $2`,
		employerID, sheetID).Scan(&approvedAt, &approvalSeq)
	if err != nil {
		return "", apperr.FromDB(err, "load approval time")
	}
	if approvedAt == nil {
		return "approved sheet has no approval timestamp", nil
	}
	var clash string
	err = q.QueryRow(ctx,
		`SELECT i.local_date::text FROM attendance_interval i JOIN pay_period p ON p.id = $3
		 WHERE i.employer_id = $1 AND i.employee_id = $2
		   AND i.local_date BETWEEN p.starts_on AND p.ends_on
		   AND (i.created_at > $4 OR i.basis_seq > $5)
		 ORDER BY i.local_date LIMIT 1`,
		employerID, employeeID, periodID, *approvedAt, approvalSeq).Scan(&clash)
	if err != nil && err != pgx.ErrNoRows {
		return "", apperr.FromDB(err, "check attendance conflicts")
	}
	if err == nil {
		return "attendance evidence for " + clash + " created after approval; correction not yet approved", nil
	}
	loc, err := employer.EmploymentLocation(ctx, q, employerID)
	if err != nil {
		return "", err
	}
	var pending string
	err = q.QueryRow(ctx,
		`SELECT (e.occurred_at AT TIME ZONE $5)::date::text FROM clock_event e JOIN pay_period p ON p.id = $3
		 WHERE e.employer_id = $1 AND e.employee_id = $2
		   AND (e.occurred_at AT TIME ZONE $5)::date BETWEEN p.starts_on AND p.ends_on
		   AND (e.created_at > $4 OR e.basis_seq > $6)
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)
		   AND NOT EXISTS (SELECT 1 FROM clock_event_disposition d WHERE d.event_id = e.id)
		 ORDER BY e.occurred_at LIMIT 1`,
		employerID, employeeID, periodID, *approvedAt, loc.String(), approvalSeq).Scan(&pending)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", apperr.FromDB(err, "check pending clock evidence")
	}
	return "unpaired clock evidence for " + pending + " created after approval", nil
}

// checkCompConflict mirrors checkTermConflict for compensation: a line
// effective in the period but created after the approval means the
// approval's pay basis is stale. Compensation created_at is
// trigger-normalized, so backdating the timestamp cannot dodge this;
// the basis_seq leg additionally closes the D-RACE window where a
// transaction that started first commits second.
func checkCompConflict(ctx context.Context, q qtx, employerID, employeeID, periodID, sheetID string) (string, error) {
	var approvedAt *time.Time
	var approvalSeq int64
	err := q.QueryRow(ctx,
		`SELECT approved_at, basis_seq FROM timesheet WHERE employer_id = $1 AND id = $2`,
		employerID, sheetID).Scan(&approvedAt, &approvalSeq)
	if err != nil {
		return "", apperr.FromDB(err, "load approval time")
	}
	if approvedAt == nil {
		return "approved sheet has no approval timestamp", nil
	}
	var code, clash string
	var corrected bool
	err = q.QueryRow(ctx,
		`SELECT c.code, c.effective_from::text, c.supersedes_id IS NOT NULL FROM compensation_line c
		 JOIN employment_terms t ON t.employer_id = c.employer_id AND t.id = c.terms_id
		 JOIN pay_period p ON p.id = $3
		 WHERE c.employer_id = $1 AND t.employee_id = $2
		   AND c.effective_from <= p.ends_on AND (c.effective_to IS NULL OR c.effective_to >= p.starts_on)
		   AND (c.created_at > $4 OR c.basis_seq > $5)
		 ORDER BY c.effective_from LIMIT 1`,
		employerID, employeeID, periodID, *approvedAt, approvalSeq).Scan(&code, &clash, &corrected)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", apperr.FromDB(err, "check compensation conflicts")
	}
	if corrected {
		return "compensation " + code + " effective " + clash + " created after approval (correction; reissue required)", nil
	}
	return "compensation " + code + " effective " + clash + " created after approval", nil
}

// requiredPacks maps the effective sheet's lines to statutory packs:
// the base set always, plus leave, holiday, and overtime packs when
// those pay codes appear.
func requiredPacks(ctx context.Context, q qtx, employerID, sheetID string) ([]string, error) {
	packs := append([]string{}, BasePacks...)
	rows, err := q.Query(ctx,
		`SELECT DISTINCT pay_code FROM timesheet_line WHERE employer_id = $1 AND timesheet_id = $2`,
		employerID, sheetID)
	if err != nil {
		return nil, apperr.FromDB(err, "load line pay codes")
	}
	defer rows.Close()
	seen := map[string]bool{}
	for _, p := range packs {
		seen[p] = true
	}
	add := func(pack string) {
		if !seen[pack] {
			seen[pack] = true
			packs = append(packs, pack)
		}
	}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, apperr.FromDB(err, "scan pay code")
		}
		switch code {
		case "OT15", "OT20":
			add("SG-MOM-OT")
		case "LEAVE", "SICK":
			add("SG-MOM-LEAVE")
		case "PH":
			add("SG-MOM-PH")
		}
	}
	return packs, apperr.FromDB(rows.Err(), "load line pay codes")
}
