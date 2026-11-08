// Package attendance review and correction workflows.
package attendance

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
	"github.com/PithomLabs/a1/internal/employer"
)

// RegenerateFunc rebuilds a draft sheet's lines inside the caller's
// transaction. Injected (implemented by the timesheet service) so the
// attendance service stays decoupled from timesheet internals.
type RegenerateFunc func(ctx context.Context, tx pgx.Tx, employerID, sheetID string) error

// DraftsFunc resolves the draft sheets a correction touching the given
// business dates must regenerate, rejecting when a decided period has
// no open correction draft. Injected alongside RegenerateFunc.
type DraftsFunc func(ctx context.Context, tx pgx.Tx, employerID, employeeID string, dates []string) ([]string, error)

// ChainFunc walks a timesheet supersedes chain to its terminal sheet.
// Injected (implemented by the timesheet service) so pairing-time
// linkage gates stay decoupled from timesheet internals.
type ChainFunc func(ctx context.Context, tx pgx.Tx, employerID, sheetID string) (terminalID, status string, err error)

// CorrectionInput supersedes one logical unit: either a lone interval
// or the complete current membership of one correction group. Bounds
// stay explicit: no other interval's bounds move as a side effect.
type CorrectionInput struct {
	IntervalIDs []string `json:"interval_ids"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	Kind        string   `json:"kind"`
	Reason      string   `json:"reason"`
}

// CorrectionReport names the correction group and its dated segments
// in interval-start order.
type CorrectionReport struct {
	GroupID    string   `json:"correction_group_id"`
	SegmentIDs []string `json:"interval_ids"`
}

// Correct supersedes one logical unit with operator-attributed
// bounds, split into one dated segment per business date inside a
// single correction group. The unit is either a lone interval or the
// complete current membership of one correction group —
// re-correcting a member resolves all current siblings, and a
// partial set is rejected with a machine-readable missing-sibling
// error instead of silently forking the group.
//
// Integrity rules: every predecessor must be live (not already
// superseded). Timesheet linkage is chain-aware: a linked submitted
// or approved sheet blocks unless its chain already carries an open
// correction draft to receive the corrected evidence — the operator
// reissues the decided sheet first, so decided history never changes
// under a correction. Drafts in every pay period the new bounds
// touch regenerate inside the same transaction, so no draft keeps
// stale hours. Rejected links are inert history and do not block:
// the rejected rows keep pointing at the old interval while the
// reissued draft rebuilds from the successor. New bounds must not
// overlap other live intervals of the employee; no coverage rule
// constrains them beyond that — explicit bounds are operator intent,
// and set-completeness (every predecessor superseded, no silent
// span loss) is the complete group-atomicity fix. Each segment
// resolves its own date's roster assignment and never inherits the
// old day's; an unassigned date stores NULL with an explicit
// exception. The group row is the unique successor of the
// predecessor set: concurrent corrections serialize on the employee
// lock and the loser finds the set already superseded. Everything —
// group, links, segments, audit rows, draft regeneration,
// aggregates, exception resolution — commits atomically.
func Correct(ctx context.Context, pool *pgxpool.Pool, employerID string, in CorrectionInput, operator string, regen RegenerateFunc, drafts DraftsFunc) (*CorrectionReport, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if regen == nil {
		return nil, apperr.New(apperr.CodeValidation, "draft regeneration is required")
	}
	if drafts == nil {
		return nil, apperr.New(apperr.CodeValidation, "draft resolution is required")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, apperr.New(apperr.CodeValidation, "correction reason is required")
	}
	ids := dedupeIDs(in.IntervalIDs)
	if len(ids) == 0 {
		return nil, apperr.New(apperr.CodeValidation, "at least one interval_id is required")
	}
	start, err := time.Parse(time.RFC3339, in.Start)
	if err != nil {
		return nil, apperr.New(apperr.CodeValidation, "start must be RFC 3339")
	}
	end, err := time.Parse(time.RFC3339, in.End)
	if err != nil {
		return nil, apperr.New(apperr.CodeValidation, "end must be RFC 3339")
	}
	if !end.After(start) {
		return nil, apperr.New(apperr.CodeValidation, "end must be after start")
	}
	loc, err := employer.EmploymentLocation(ctx, pool, employerID)
	if err != nil {
		return nil, err
	}
	switch in.Kind {
	case "work", "break_paid", "break_unpaid", "overtime", "rest":
	default:
		return nil, apperr.New(apperr.CodeValidation, "unknown kind %q", in.Kind)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin correction")
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx,
		`SELECT i.id, i.employee_id, e.employee_no, i.roster_assignment_id,
		   i.interval_start, i.interval_end, i.timezone, i.local_date::text,
		   i.kind, i.shift_identity, COALESCE(i.source_refs::text, '{}'), i.is_current,
		   i.correction_group_id
		 FROM attendance_interval i JOIN employee e ON e.id = i.employee_id
		 WHERE i.employer_id = $1 AND i.id = ANY($2::uuid[])
		 ORDER BY i.interval_start, i.id`,
		employerID, ids)
	if err != nil {
		return nil, apperr.FromDB(err, "load intervals")
	}
	var preds []correctionPred
	for rows.Next() {
		var p correctionPred
		if err := rows.Scan(&p.id, &p.employeeID, &p.empNo, &p.aid, &p.start, &p.end,
			&p.tz, &p.date, &p.kind, &p.shift, &p.refs, &p.live, &p.group); err != nil {
			rows.Close()
			return nil, apperr.FromDB(err, "scan interval")
		}
		preds = append(preds, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "load intervals")
	}
	if len(preds) != len(ids) {
		found := map[string]bool{}
		for _, p := range preds {
			found[p.id] = true
		}
		for _, id := range ids {
			if !found[id] {
				return nil, apperr.New(apperr.CodeNotFound, "interval %s not found", id)
			}
		}
	}
	for _, p := range preds[1:] {
		if p.employeeID != preds[0].employeeID {
			return nil, apperr.New(apperr.CodeValidation, "correct one employee's intervals at a time")
		}
	}
	// Group-atomic resolution: the request names one logical unit. A
	// member resolves its whole current sibling set; anything less is
	// a partial re-correction and fails before anything is written.
	groups := map[string]bool{}
	for _, p := range preds {
		if p.group != nil {
			groups[*p.group] = true
		}
	}
	if len(groups) > 1 {
		return nil, apperr.New(apperr.CodeValidation,
			"intervals belong to more than one correction group; correct one complete group at a time")
	}
	if len(groups) == 1 {
		var groupID string
		for g := range groups {
			groupID = g
		}
		sibs, err := tx.Query(ctx,
			`SELECT id::text FROM attendance_interval
			 WHERE employer_id = $1 AND correction_group_id = $2 AND is_current
			 ORDER BY interval_start, id`,
			employerID, groupID)
		if err != nil {
			return nil, apperr.FromDB(err, "resolve siblings")
		}
		var required []string
		for sibs.Next() {
			var id string
			if err := sibs.Scan(&id); err != nil {
				sibs.Close()
				return nil, apperr.FromDB(err, "scan siblings")
			}
			required = append(required, id)
		}
		sibs.Close()
		if err := sibs.Err(); err != nil {
			return nil, apperr.FromDB(err, "resolve siblings")
		}
		have := map[string]bool{}
		for _, p := range preds {
			have[p.id] = true
		}
		var missing []string
		for _, id := range required {
			if !have[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return nil, apperr.New(apperr.CodeValidation,
				"partial re-correction of group %s: requires intervals [%s]; missing [%s]",
				groupID, strings.Join(required, " "), strings.Join(missing, " "))
		}
	} else if len(preds) > 1 {
		return nil, apperr.New(apperr.CodeValidation,
			"correct one interval or one complete group at a time")
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, preds[0].employeeID); err != nil {
		return nil, err
	}
	// Re-check liveness under the lock: the pre-lock read is stale for
	// a race loser, and the fresh snapshot names the real outcome.
	for i := range preds {
		if err := tx.QueryRow(ctx,
			`SELECT is_current FROM attendance_interval WHERE employer_id = $1 AND id = $2`,
			employerID, preds[i].id).Scan(&preds[i].live); err != nil {
			return nil, apperr.FromDB(err, "recheck interval")
		}
		if !preds[i].live {
			return nil, apperr.New(apperr.CodeInvalidState, "interval already superseded; correct the current version")
		}
	}
	// Timesheet linkage is chain-aware: a linked submitted or approved
	// sheet blocks unless its chain already carries an open correction
	// draft to receive the corrected evidence. Every linked sheet's
	// period contains a predecessor date, so the drafts resolver below
	// walks each linked chain exactly once — no separate linked-sheet
	// gate may over-block a reissued chain.
	// New bounds must not overlap other live intervals of the employee.
	var clash bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM attendance_interval
		 WHERE employer_id = $1 AND employee_id = $2 AND is_current AND NOT (id = ANY($3::uuid[]))
		   AND tstzrange(interval_start, interval_end) && tstzrange($4, $5))`,
		employerID, preds[0].employeeID, ids, start, end).Scan(&clash); err != nil {
		return nil, apperr.FromDB(err, "check overlap")
	}
	if clash {
		return nil, apperr.New(apperr.CodeConflict, "corrected bounds overlap another live interval")
	}
	// Split the new bounds on the same canonical midnight boundary the
	// import uses: one segment per business date, each resolving its
	// own roster assignment. A segment landing on an unassigned date
	// stores NULL and raises an explicit exception — it never inherits
	// an old day's roster link.
	// Predecessors keep their own source references untouched: every
	// segment cites the correction group instead, and the chain
	// resolves through supersedes_id. The schema allows exactly one
	// evidence origin per row, so segments never duplicate the clock
	// events.
	employeeID, empNo := preds[0].employeeID, preds[0].empNo
	var origEvents []string
	seenEvents := map[string]bool{}
	for _, p := range preds {
		var refs map[string]any
		if err := json.Unmarshal([]byte(p.refs), &refs); err != nil {
			return nil, apperr.New(apperr.CodeInternal, "stored source references are not valid JSON")
		}
		if evtIDs, ok := refs["clock_event_ids"].([]any); ok {
			for _, id := range evtIDs {
				if s, ok := id.(string); ok && !seenEvents[s] {
					seenEvents[s] = true
					origEvents = append(origEvents, s)
				}
			}
		}
	}
	runs := splitRun(start, end, loc, origEvents)
	segAids := make([]*string, len(runs))
	var newExcs []Exception
	for i, r := range runs {
		a, err := resolveAssignment(ctx, tx, employerID, employeeID, r.date)
		if err != nil {
			return nil, err
		}
		if a == nil {
			newExcs = append(newExcs, Exception{Code: ExcNoRosterLink, EmployeeNo: empNo,
				Date: r.date, Detail: "corrected segment lands on a date with no roster assignment; stored unlinked",
				EventIDs: origEvents})
			continue
		}
		aid := a.id
		segAids[i] = &aid
	}
	if in.Kind == "break_paid" || in.Kind == "break_unpaid" {
		mid := start.Add(end.Sub(start) / 2)
		midDate := bizDate(mid, loc)
		var midAid *string
		for i, r := range runs {
			if r.date == midDate {
				midAid = segAids[i]
				break
			}
		}
		if _, reason := classifyBreak(ctx, tx, employerID, midAid, start, end, loc); reason != "" {
			return nil, apperr.New(apperr.CodeValidation, "corrected break unclassifiable: %s", reason)
		}
	}
	var groupID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO attendance_correction_group (employer_id, employee_id, reason, corrected_by)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		employerID, employeeID, in.Reason, operator).Scan(&groupID); err != nil {
		return nil, apperr.FromDB(err, "record correction group")
	}
	// Predecessors bind before segments: the deferred commit trigger
	// validates the complete set, and the same-transaction rule
	// keeps later appends out.
	for _, p := range preds {
		if _, err := tx.Exec(ctx,
			`INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
			 VALUES ($1, $2, $3)`,
			employerID, groupID, p.id); err != nil {
			return nil, apperr.FromDB(err, "bind correction predecessor")
		}
	}
	// Each segment supersedes the first predecessor (by start) it
	// overlaps, so the binding follows the evidence; segments outside
	// every predecessor span attach to the head. A repair pass then
	// steals the best-overlapping segment for any predecessor left
	// without one, satisfying the no-orphan rule by construction.
	segPred := make([]string, len(runs))
	covered := map[string]bool{}
	for i, r := range runs {
		segPred[i] = preds[0].id
		for _, p := range preds {
			if r.start.Before(p.end) && p.start.Before(r.end) {
				segPred[i] = p.id
				break
			}
		}
		covered[segPred[i]] = true
	}
	for _, p := range preds {
		if covered[p.id] {
			continue
		}
		best, bestUs := 0, int64(-1)
		for i, r := range runs {
			lo := r.start
			if p.start.After(lo) {
				lo = p.start
			}
			hi := r.end
			if p.end.Before(hi) {
				hi = p.end
			}
			if us := hi.Sub(lo).Microseconds(); us > bestUs {
				best, bestUs = i, us
			}
		}
		segPred[best] = p.id
		covered[p.id] = true
	}
	rep := &CorrectionReport{GroupID: groupID, SegmentIDs: []string{}}
	shift := eventIDOrZero(preds[0].shift)
	for i, r := range runs {
		refsJSON, err := json.Marshal(map[string]any{
			"correction_id":           segPred[i],
			"predecessor_interval_id": segPred[i],
			"correction_group_id":     groupID,
		})
		if err != nil {
			return nil, apperr.New(apperr.CodeInternal, "encode source references: %v", err)
		}
		var segID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO attendance_interval (employer_id, employee_id, roster_assignment_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, shift_identity, supersedes_id, correction_group_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, 'correction', $9::jsonb, $10, $11, $12) RETURNING id`,
			employerID, employeeID, segAids[i], r.start, r.end, loc.String(), r.date, in.Kind,
			string(refsJSON), shift, segPred[i], groupID).Scan(&segID); err != nil {
			return nil, apperr.FromDB(err, "store corrected interval")
		}
		rep.SegmentIDs = append(rep.SegmentIDs, segID)
	}
	// Day records re-derive first so every affected date — including a
	// previously empty new date — exists before its audit row lands.
	// Run dates carry their own resolved assignment; an old date keeps
	// its link when the correction moves fully away.
	type dayRef struct {
		date string
		aid  *string
	}
	var days []dayRef
	dayAid := map[string]*string{}
	for i, r := range runs {
		if _, ok := dayAid[r.date]; !ok {
			dayAid[r.date] = segAids[i]
			days = append(days, dayRef{r.date, segAids[i]})
		}
	}
	for _, p := range preds {
		if _, ok := dayAid[p.date]; !ok {
			dayAid[p.date] = p.aid
			days = append(days, dayRef{p.date, p.aid})
		}
	}
	for _, d := range days {
		if err := upsertRecord(ctx, tx, employerID, employeeID, d.date, d.aid); err != nil {
			return nil, err
		}
	}
	// One audit row per affected day record: the segment on that date,
	// or the group's head segment when the old date keeps no hours.
	// The old value names the predecessors on that date — the single
	// predecessor for classic corrections, NULL for a brand-new date
	// under a multi-predecessor re-correction.
	dateSeg := map[string]string{}
	for i, r := range runs {
		if _, ok := dateSeg[r.date]; !ok {
			dateSeg[r.date] = rep.SegmentIDs[i]
		}
	}
	datePreds := map[string][]string{}
	for _, p := range preds {
		datePreds[p.date] = append(datePreds[p.date], p.id)
	}
	for _, d := range days {
		var rid string
		if err := tx.QueryRow(ctx,
			`SELECT id FROM attendance_record WHERE employer_id = $1 AND employee_id = $2 AND business_date = $3::date`,
			employerID, employeeID, d.date).Scan(&rid); err != nil {
			return nil, apperr.FromDB(err, "load day record")
		}
		newVal := dateSeg[d.date]
		if newVal == "" {
			newVal = rep.SegmentIDs[0]
		}
		var oldVal any
		if len(preds) == 1 {
			oldVal = preds[0].id
		} else if idsOnDate := datePreds[d.date]; len(idsOnDate) > 0 {
			oldVal = strings.Join(idsOnDate, ",")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO attendance_correction (employer_id, attendance_id, field, old_value, new_value, reason, corrected_by)
			 VALUES ($1, $2, 'interval', $3, $4, $5, $6)`,
			employerID, rid, oldVal, newVal, in.Reason, operator); err != nil {
			return nil, apperr.FromDB(err, "audit correction")
		}
	}
	if err := persistExceptions(ctx, tx, employerID, employeeID, newExcs); err != nil {
		return nil, err
	}
	// Drafts in every touched period regenerate from current intervals:
	// no draft keeps stale hours after a correction. A touched decided
	// chain without an open correction draft rejects the move — this is
	// the downstream-path proof: the correction lands only where a draft
	// exists to receive it.
	var touchDates []string
	for _, d := range days {
		touchDates = append(touchDates, d.date)
	}
	periodDrafts, err := drafts(ctx, tx, employerID, employeeID, touchDates)
	if err != nil {
		return nil, err
	}
	for _, sheetID := range periodDrafts {
		if err := regen(ctx, tx, employerID, sheetID); err != nil {
			return nil, err
		}
	}
	if _, err := resolveExceptions(ctx, tx, employerID, employeeID); err != nil {
		return nil, err
	}
	if err := deriveDayStatuses(ctx, tx, employerID, employeeID); err != nil {
		return nil, err
	}
	segs := make([]map[string]any, 0, len(runs))
	for i, r := range runs {
		segs = append(segs, map[string]any{
			"interval_id": rep.SegmentIDs[i], "date": r.date,
			"start": r.start.UTC().Format(time.RFC3339), "end": r.end.UTC().Format(time.RFC3339),
		})
	}
	// Audit instants render in UTC (H2): the same instant produces
	// the same stable text on every host, while business dates stay
	// employment-local alongside them.
	before := map[string]any{
		"interval_ids": ids, "start": preds[0].start.UTC().Format(time.RFC3339),
		"end":  preds[len(preds)-1].end.UTC().Format(time.RFC3339),
		"kind": preds[0].kind, "dates": predDates(preds),
	}
	if len(preds) == 1 {
		before["interval_id"] = preds[0].id
	}
	if err := audit.Write(ctx, tx, employerID, operator, "attendance",
		"interval.correct", "attendance_interval", &rep.SegmentIDs[0],
		before,
		map[string]any{
			"correction_group_id": groupID, "segments": segs,
			"kind": in.Kind, "reason": in.Reason,
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit correction")
	}
	return rep, nil
}

// correctionPred is one loaded predecessor interval awaiting
// supersession by a correction group.
type correctionPred struct {
	id         string
	employeeID string
	empNo      string
	aid        *string
	start      time.Time
	end        time.Time
	tz         string
	date       string
	kind       string
	shift      *string
	refs       string
	live       bool
	group      *string
}

// dedupeIDs drops repeat interval IDs, keeping first-seen order: the
// predecessor set is what matters, not how often it was named.
func dedupeIDs(ids []string) []string {
	seen := map[string]bool{}
	out := ids[:0]
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func predDates(preds []correctionPred) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range preds {
		if !seen[p.date] {
			seen[p.date] = true
			out = append(out, p.date)
		}
	}
	return out
}

// DayRecord is one reviewed attendance day. ActualUs and PaidUs are the
// authoritative exact totals; the minute displays quantize explicitly.
type DayRecord struct {
	ID           string   `json:"id"`
	EmployeeID   string   `json:"employee_id"`
	EmployeeNo   string   `json:"employee_no"`
	BusinessDate string   `json:"business_date"`
	ScheduledMin *float64 `json:"scheduled_min"`
	RecordedMin  *float64 `json:"recorded_min"`
	ActualMin    *float64 `json:"actual_min"`
	PaidMin      *float64 `json:"paid_min"`
	ActualUs     int64    `json:"actual_us"`
	PaidUs       int64    `json:"paid_us"`
	Status       string   `json:"status"`
}

// UnpairedEvent is one clock event cited by no interval.
type UnpairedEvent struct {
	ID            string `json:"id"`
	EmployeeNo    string `json:"employee_no"`
	OccurredAt    string `json:"occurred_at"`
	Timezone      string `json:"timezone"`
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	SourceEventID string `json:"source_event_id"`
}

// PersistedException is one stored attendance exception with its state.
type PersistedException struct {
	ID           string `json:"id"`
	EmployeeNo   string `json:"employee_no"`
	BusinessDate string `json:"business_date"`
	Code         string `json:"code"`
	Detail       string `json:"detail"`
	Resolved     bool   `json:"resolved"`
}

// MonthlyExcess carries the independently visible 72-hour compliance
// signal. It never gates timesheet generation: verified work over the
// limit stays payable, flagged for review.
type MonthlyExcess struct {
	EmployeeID   string  `json:"employee_id"`
	EmployeeNo   string  `json:"employee_no"`
	Month        string  `json:"month"`
	ExcessHours  float64 `json:"excess_hours"`
	NeedsReview  bool    `json:"needs_review"`
	ReviewReason string  `json:"review_reason"`
}

// Review is one attendance review window: day records, still-unpaired
// events, persisted exceptions, and the monthly excess signal.
type Review struct {
	Records        []DayRecord          `json:"records"`
	UnpairedEvents []UnpairedEvent      `json:"unpaired_events"`
	Exceptions     []PersistedException `json:"exceptions"`
	MonthlyExcess  []MonthlyExcess      `json:"monthly_excess"`
}

// ReviewAttendance lists records, unpaired events, persisted exceptions
// and monthly excess for the window. Date comparisons use the employment
// timezone: neither vendor declarations nor session TimeZone can shift a
// midnight boundary.
func ReviewAttendance(ctx context.Context, pool *pgxpool.Pool, employerID, from, to string) (*Review, error) {
	if _, err := time.Parse("2006-01-02", from); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "from must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", to); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "to must be YYYY-MM-DD")
	}
	loc, err := employer.EmploymentLocation(ctx, pool, employerID)
	if err != nil {
		return nil, err
	}
	rev := &Review{Records: []DayRecord{}, UnpairedEvents: []UnpairedEvent{}, Exceptions: []PersistedException{}, MonthlyExcess: []MonthlyExcess{}}
	rows, err := pool.Query(ctx,
		`SELECT r.id, r.employee_id, e.employee_no, r.business_date::text, r.scheduled_min, r.recorded_min, r.actual_min, r.paid_min, r.actual_us, r.paid_us, r.status
		 FROM attendance_record r JOIN employee e ON e.id = r.employee_id
		 WHERE r.employer_id = $1 AND r.business_date BETWEEN $2::date AND $3::date
		 ORDER BY e.employee_no, r.business_date`,
		employerID, from, to)
	if err != nil {
		return nil, apperr.FromDB(err, "list records")
	}
	for rows.Next() {
		var d DayRecord
		if err := rows.Scan(&d.ID, &d.EmployeeID, &d.EmployeeNo, &d.BusinessDate,
			&d.ScheduledMin, &d.RecordedMin, &d.ActualMin, &d.PaidMin, &d.ActualUs, &d.PaidUs, &d.Status); err != nil {
			rows.Close()
			return nil, apperr.FromDB(err, "scan record")
		}
		rev.Records = append(rev.Records, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "list records")
	}
	uerows, err := pool.Query(ctx,
		`SELECT e.id, emp.employee_no, e.occurred_at::text, e.timezone, e.kind, e.source, e.source_event_id
		 FROM clock_event e JOIN employee emp ON emp.id = e.employee_id
		 WHERE e.employer_id = $1
		   AND (e.occurred_at AT TIME ZONE $4)::date BETWEEN $2::date AND $3::date
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)
		   AND NOT EXISTS (SELECT 1 FROM clock_event_disposition d WHERE d.event_id = e.id)
		 ORDER BY e.occurred_at`,
		employerID, from, to, loc.String())
	if err != nil {
		return nil, apperr.FromDB(err, "list unpaired events")
	}
	for uerows.Next() {
		var u UnpairedEvent
		if err := uerows.Scan(&u.ID, &u.EmployeeNo, &u.OccurredAt, &u.Timezone, &u.Kind, &u.Source, &u.SourceEventID); err != nil {
			uerows.Close()
			return nil, apperr.FromDB(err, "scan unpaired event")
		}
		rev.UnpairedEvents = append(rev.UnpairedEvents, u)
	}
	uerows.Close()
	if err := uerows.Err(); err != nil {
		return nil, apperr.FromDB(err, "list unpaired events")
	}
	exrows, err := pool.Query(ctx,
		`SELECT ex.id, emp.employee_no, ex.business_date::text, ex.code, ex.detail, ex.resolved
		 FROM attendance_exception ex JOIN employee emp ON emp.id = ex.employee_id
		 WHERE ex.employer_id = $1 AND ex.business_date BETWEEN $2::date AND $3::date
		 ORDER BY emp.employee_no, ex.business_date, ex.code`,
		employerID, from, to)
	if err != nil {
		return nil, apperr.FromDB(err, "list exceptions")
	}
	for exrows.Next() {
		var p PersistedException
		if err := exrows.Scan(&p.ID, &p.EmployeeNo, &p.BusinessDate, &p.Code, &p.Detail, &p.Resolved); err != nil {
			exrows.Close()
			return nil, apperr.FromDB(err, "scan exception")
		}
		rev.Exceptions = append(rev.Exceptions, p)
	}
	exrows.Close()
	if err := exrows.Err(); err != nil {
		return nil, apperr.FromDB(err, "list exceptions")
	}
	mrows, err := pool.Query(ctx,
		`SELECT r.employee_id, e.employee_no, to_char(r.business_date, 'YYYY-MM'),
		   COALESCE(sum(GREATEST(COALESCE(r.actual_min, 0) - COALESCE(r.scheduled_min, 0), 0)) / 60, 0)
		 FROM attendance_record r JOIN employee e ON e.id = r.employee_id
		 WHERE r.employer_id = $1 AND r.business_date BETWEEN $2::date AND $3::date
		 GROUP BY r.employee_id, e.employee_no, to_char(r.business_date, 'YYYY-MM')`,
		employerID, from, to)
	if err != nil {
		return nil, apperr.FromDB(err, "monthly excess")
	}
	for mrows.Next() {
		var m MonthlyExcess
		if err := mrows.Scan(&m.EmployeeID, &m.EmployeeNo, &m.Month, &m.ExcessHours); err != nil {
			mrows.Close()
			return nil, apperr.FromDB(err, "scan monthly excess")
		}
		if m.ExcessHours > 72 {
			m.NeedsReview = true
			m.ReviewReason = "overtime exceeds 72 hours in the calendar month"
		}
		rev.MonthlyExcess = append(rev.MonthlyExcess, m)
	}
	mrows.Close()
	return rev, apperr.FromDB(mrows.Err(), "monthly excess")
}

// LinkedSheet names one timesheet fed by an interval.
type LinkedSheet struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// IntervalDetail is one attendance interval with everything an
// operator needs to decide a correction without SQL: bounds, lineage,
// source evidence, linked sheets (which decide whether correction is
// permitted), and the date's open exceptions.
type IntervalDetail struct {
	ID                string        `json:"id"`
	EmployeeNo        string        `json:"employee_no"`
	BusinessDate      string        `json:"business_date"`
	Start             string        `json:"start"`
	End               string        `json:"end"`
	Timezone          string        `json:"timezone"`
	Kind              string        `json:"kind"`
	Source            string        `json:"source"`
	IsCurrent         bool          `json:"is_current"`
	SupersedesID      *string       `json:"supersedes_id"`
	CorrectionGroupID *string       `json:"correction_group_id"`
	SourceRefs        any           `json:"source_refs"`
	LinkedSheets      []LinkedSheet `json:"linked_sheets"`
	Exceptions        []string      `json:"exceptions"`
}

// ListIntervals returns intervals in the window, newest lineage first
// per date. employeeNoOrID narrows to one employee when non-empty.
func ListIntervals(ctx context.Context, pool *pgxpool.Pool, employerID, employeeNoOrID, from, to string) ([]IntervalDetail, error) {
	if _, err := time.Parse("2006-01-02", from); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "from must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", to); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "to must be YYYY-MM-DD")
	}
	rows, err := pool.Query(ctx,
		`SELECT i.id, e.employee_no, i.local_date::text,
		   i.interval_start::text, i.interval_end::text, i.timezone,
		   i.kind, i.source, i.is_current, i.supersedes_id, i.correction_group_id, i.source_refs
		 FROM attendance_interval i JOIN employee e ON e.id = i.employee_id
		 WHERE i.employer_id = $1 AND i.local_date BETWEEN $2::date AND $3::date
		   AND ($4 = '' OR e.employee_no = $4 OR e.id::text = $4)
		 ORDER BY i.local_date, i.interval_start, i.created_at`,
		employerID, from, to, employeeNoOrID)
	if err != nil {
		return nil, apperr.FromDB(err, "list intervals")
	}
	defer rows.Close()
	out := []IntervalDetail{}
	for rows.Next() {
		var d IntervalDetail
		var refs []byte
		if err := rows.Scan(&d.ID, &d.EmployeeNo, &d.BusinessDate, &d.Start, &d.End,
			&d.Timezone, &d.Kind, &d.Source, &d.IsCurrent, &d.SupersedesID, &d.CorrectionGroupID, &refs); err != nil {
			return nil, apperr.FromDB(err, "scan interval")
		}
		var v any
		if err := json.Unmarshal(refs, &v); err != nil {
			return nil, apperr.FromDB(err, "decode source refs")
		}
		d.SourceRefs = v
		d.LinkedSheets = []LinkedSheet{}
		d.Exceptions = []string{}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "list intervals")
	}
	for i := range out {
		lrows, err := pool.Query(ctx,
			`SELECT DISTINCT t.id, t.status FROM timesheet_line_interval li
			 JOIN timesheet_line l ON l.employer_id = li.employer_id AND l.id = li.line_id
			 JOIN timesheet t ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
			 WHERE li.employer_id = $1 AND li.interval_id = $2 ORDER BY t.id`,
			employerID, out[i].ID)
		if err != nil {
			return nil, apperr.FromDB(err, "list linked sheets")
		}
		for lrows.Next() {
			var s LinkedSheet
			if err := lrows.Scan(&s.ID, &s.Status); err != nil {
				lrows.Close()
				return nil, apperr.FromDB(err, "scan linked sheet")
			}
			out[i].LinkedSheets = append(out[i].LinkedSheets, s)
		}
		lrows.Close()
		if err := lrows.Err(); err != nil {
			return nil, apperr.FromDB(err, "list linked sheets")
		}
		erows, err := pool.Query(ctx,
			`SELECT ex.code FROM attendance_exception ex JOIN employee e ON e.id = ex.employee_id
			 WHERE ex.employer_id = $1 AND e.employee_no = $2 AND ex.business_date = $3::date
			   AND NOT ex.resolved ORDER BY ex.code`,
			employerID, out[i].EmployeeNo, out[i].BusinessDate)
		if err != nil {
			return nil, apperr.FromDB(err, "list interval exceptions")
		}
		for erows.Next() {
			var code string
			if err := erows.Scan(&code); err != nil {
				erows.Close()
				return nil, apperr.FromDB(err, "scan interval exception")
			}
			out[i].Exceptions = append(out[i].Exceptions, code)
		}
		erows.Close()
		if err := erows.Err(); err != nil {
			return nil, apperr.FromDB(err, "list interval exceptions")
		}
	}
	return out, nil
}
