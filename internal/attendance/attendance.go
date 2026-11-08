// Package attendance implements deterministic clock-event import,
// roster-anchored in/out pairing with explicit break evidence, attendance
// review with persisted exceptions, and auditable corrections. Raw events
// are never modified; pairing never invents events; scheduled segments
// never pose as observed breaks; implausible spans stay unpaired with
// explicit exceptions, never become payable time.
package attendance

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
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

// Event kinds accepted by the import contract.
const (
	KindIn         = "in"
	KindOut        = "out"
	KindBreakStart = "break_start"
	KindBreakEnd   = "break_end"
)

// Exception codes for unpairable or ambiguous input.
const (
	ExcMissingPunch         = "missing_punch"
	ExcUnmatchedEvent       = "unmatched_event"
	ExcDanglingBreak        = "dangling_break"
	ExcMissingBreak         = "missing_break_evidence"
	ExcAmbiguousBreak       = "ambiguous_break_classification"
	ExcNoRosterLink         = "no_roster_link"
	ExcSpanTooLong          = "span_too_long"
	ExcSpanDateMismatch     = "span_date_mismatch"
	ExcConflictingRedeliver = "conflicting_redelivery"
	ExcOverlappingSpan      = "overlapping_span"
	ExcEvidenceConflict     = "evidence_conflict"
)

// maxSpan bounds one in/out pairing defensively. It is a ceiling, not a
// matching rule: the roster anchor decides attribution; anything beyond
// the ceiling stays unpaired with an exception. Actual events are never
// truncated or discarded.
const maxSpan = 24 * time.Hour

// dbtx is the query surface shared by *pgxpool.Pool and pgx.Tx so pairing,
// correction and aggregation helpers run inside or outside transactions.
type dbtx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// clockRow is one parsed CSV row.
type clockRow struct {
	Source        string
	SourceEventID string
	EmployeeNo    string
	OccurredAt    time.Time
	Timezone      string
	Kind          string
	Line          int
}

// Report summarizes one import run. Replayed rows return their original
// outcome: duplicates are reported, never re-inserted. Pairing always
// reconsiders every unpaired event of every employee in the file, so a
// replay after an interrupted run repairs pending work instead of
// reporting silence.
type Report struct {
	EventsReceived     int         `json:"events_received"`
	EventsStored       int         `json:"events_stored"`
	Duplicates         int         `json:"duplicates"`
	IntervalsBuilt     int         `json:"intervals_built"`
	Exceptions         []Exception `json:"exceptions"`
	ExceptionsResolved int         `json:"exceptions_resolved"`
}

// Exception is one explicit pairing or validation failure. EventIDs names
// the clock events that must be consumed before the exception resolves.
type Exception struct {
	Code       string   `json:"code"`
	EmployeeNo string   `json:"employee_no"`
	Date       string   `json:"date"`
	Detail     string   `json:"detail"`
	EventIDs   []string `json:"event_ids,omitempty"`
}

// Import parses CSV, stores new clock events with dedup, and pairs spans
// into attendance intervals in one transaction. Conflicting redelivery
// (same source event ID, different payload) surfaces as an exception and
// never overwrites the stored event. Late breaks split live work
// intervals through the linkage gate: covers feeding a decided chain
// without an open correction draft hold the break with an explicit
// evidence conflict, while draft terminals regenerate atomically.
func Import(ctx context.Context, pool *pgxpool.Pool, employerID string, r io.Reader, operator string, regen RegenerateFunc, chain ChainFunc) (*Report, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if regen == nil || chain == nil {
		return nil, apperr.New(apperr.CodeValidation, "draft regeneration and chain resolution are required")
	}
	rows, err := parseCSV(r)
	if err != nil {
		return nil, err
	}
	// Employment timezone is the sole business-date authority for this
	// import: vendor timezones resolve instants, never dates.
	loc, err := employer.EmploymentLocation(ctx, pool, employerID)
	if err != nil {
		return nil, err
	}
	rep := &Report{EventsReceived: len(rows), Exceptions: []Exception{}}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin import")
	}
	defer tx.Rollback(ctx)
	// Resolve every row's employee first (unknown rows report and skip),
	// then lock the touched employees in sorted order.
	type resolved struct {
		row        clockRow
		employeeID string
	}
	var resolvedRows []resolved
	empSet := map[string]string{}
	for _, row := range rows {
		var employeeID string
		err := tx.QueryRow(ctx,
			`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = $2`,
			employerID, row.EmployeeNo).Scan(&employeeID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rep.Exceptions = append(rep.Exceptions, Exception{
					Code: ExcUnmatchedEvent, EmployeeNo: row.EmployeeNo,
					Date:   bizDate(row.OccurredAt, loc),
					Detail: fmt.Sprintf("line %d: unknown employee_no", row.Line),
				})
				continue
			}
			return nil, apperr.FromDB(err, "resolve employee")
		}
		resolvedRows = append(resolvedRows, resolved{row: row, employeeID: employeeID})
		empSet[employeeID] = row.EmployeeNo
	}
	empIDs := make([]string, 0, len(empSet))
	for id := range empSet {
		empIDs = append(empIDs, id)
	}
	sort.Strings(empIDs)
	for _, id := range empIDs {
		if err := coredatabase.LockPayrollInput(ctx, tx, employerID, id); err != nil {
			return nil, err
		}
	}
	var conflicts []Exception
	for _, rr := range resolvedRows {
		stored, err := storeEvent(ctx, tx, employerID, rr.employeeID, rr.row)
		if err != nil {
			return nil, err
		}
		switch stored {
		case storeInserted:
			rep.EventsStored++
		case storeDuplicate:
			rep.Duplicates++
		case storeConflict:
			rep.Duplicates++
			e, err := conflictException(ctx, tx, employerID, rr.employeeID, rr.row, loc)
			if err != nil {
				return nil, err
			}
			rep.Exceptions = append(rep.Exceptions, e)
			conflicts = append(conflicts, e)
		}
	}
	for _, id := range empIDs {
		n, excs, resolved, err := pairEmployee(ctx, tx, employerID, id, empSet[id], loc, regen, chain)
		if err != nil {
			return nil, err
		}
		rep.IntervalsBuilt += n
		rep.ExceptionsResolved += resolved
		rep.Exceptions = append(rep.Exceptions, excs...)
	}
	for _, c := range conflicts {
		if err := audit.Write(ctx, tx, employerID, operator, "attendance",
			"clock_event.conflict", "clock_event", nil, nil, c); err != nil {
			return nil, err
		}
	}
	if err := audit.Write(ctx, tx, employerID, operator, "attendance",
		"clock_event.import", "clock_event", nil, nil, map[string]any{
			"events_received":   rep.EventsReceived,
			"events_stored":     rep.EventsStored,
			"duplicates":        rep.Duplicates,
			"conflicts":         len(conflicts),
			"intervals_built":   rep.IntervalsBuilt,
			"exceptions_raised": len(rep.Exceptions),
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit import")
	}
	return rep, nil
}

type storeOutcome int

const (
	storeInserted storeOutcome = iota
	storeDuplicate
	storeConflict
)

// storeEvent inserts one clock event. An identical replay is a duplicate;
// a different payload under the same source event ID is a conflict: the
// original row is preserved and the caller must surface the conflict.
func storeEvent(ctx context.Context, tx pgx.Tx, employerID, employeeID string, row clockRow) (storeOutcome, error) {
	var id string
	err := tx.QueryRow(ctx,
		`INSERT INTO clock_event (employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (employer_id, source, source_event_id) DO NOTHING
		 RETURNING id`,
		employerID, employeeID, row.OccurredAt, row.Timezone, row.Kind, row.Source, row.SourceEventID).Scan(&id)
	if err != nil {
		if err != pgx.ErrNoRows {
			return 0, apperr.FromDB(err, "store clock event")
		}
		var stored struct {
			employeeID string
			kind       string
			at         time.Time
		}
		if err := tx.QueryRow(ctx,
			`SELECT employee_id, kind, occurred_at FROM clock_event
			 WHERE employer_id = $1 AND source = $2 AND source_event_id = $3`,
			employerID, row.Source, row.SourceEventID).
			Scan(&stored.employeeID, &stored.kind, &stored.at); err != nil {
			return 0, apperr.FromDB(err, "load conflicting event")
		}
		if stored.employeeID == employeeID && stored.kind == row.Kind &&
			stored.at.UnixMicro() == row.OccurredAt.UnixMicro() {
			return storeDuplicate, nil
		}
		return storeConflict, nil
	}
	return storeInserted, nil
}

// conflictException builds the redelivery-conflict exception against the
// stored event's date and persists it (non-blocking: the stored evidence
// was already processed normally).
func conflictException(ctx context.Context, tx pgx.Tx, employerID, employeeID string, row clockRow, loc *time.Location) (Exception, error) {
	var storedID string
	var storedAt time.Time
	_ = tx.QueryRow(ctx,
		`SELECT id::text, occurred_at FROM clock_event
		 WHERE employer_id = $1 AND source = $2 AND source_event_id = $3`,
		employerID, row.Source, row.SourceEventID).Scan(&storedID, &storedAt)
	storedDate := bizDate(storedAt, loc)
	e := Exception{
		Code: ExcConflictingRedeliver, EmployeeNo: row.EmployeeNo,
		Date:     storedDate,
		Detail:   fmt.Sprintf("line %d: source event %s/%s redelivered with a different payload; original preserved", row.Line, row.Source, row.SourceEventID),
		EventIDs: []string{storedID},
	}
	if err := persistExceptions(ctx, tx, employerID, employeeID, []Exception{e}); err != nil {
		return e, err
	}
	return e, nil
}

type linkedSheet struct {
	id     string
	status string
}

// linkedSheets lists the timesheets citing one interval.
func linkedSheets(ctx context.Context, tx pgx.Tx, employerID, intervalID string) ([]linkedSheet, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT t.id, t.status FROM timesheet_line_interval li
		 JOIN timesheet_line l ON l.employer_id = li.employer_id AND l.id = li.line_id
		 JOIN timesheet t ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
		 WHERE li.employer_id = $1 AND li.interval_id = $2`,
		employerID, intervalID)
	if err != nil {
		return nil, apperr.FromDB(err, "load linked sheets")
	}
	defer rows.Close()
	var out []linkedSheet
	for rows.Next() {
		var s linkedSheet
		if err := rows.Scan(&s.id, &s.status); err != nil {
			return nil, apperr.FromDB(err, "scan linked sheets")
		}
		out = append(out, s)
	}
	return out, apperr.FromDB(rows.Err(), "load linked sheets")
}

// pairEmployee pairs one employee's full unpaired set, attaches late
// breaks, regenerates the draft terminals fed by split intervals,
// persists exceptions, resolves settled causes, and derives day
// statuses. It must run under the employee's advisory lock.
func pairEmployee(ctx context.Context, tx pgx.Tx, employerID, employeeID, empNo string, loc *time.Location, regen RegenerateFunc, chain ChainFunc) (int, []Exception, int, error) {
	evs, err := unpairedEvents(ctx, tx, employerID, employeeID)
	if err != nil {
		return 0, nil, 0, err
	}
	built, excs, err := pairSpans(ctx, tx, employerID, employeeID, empNo, evs, loc)
	if err != nil {
		return built, excs, 0, err
	}
	n2, excs2, drafts, err := attachLateBreaks(ctx, tx, employerID, employeeID, empNo, loc, chain)
	if err != nil {
		return built, excs, 0, err
	}
	built += n2
	excs = append(excs, excs2...)
	for _, sheetID := range drafts {
		if err := regen(ctx, tx, employerID, sheetID); err != nil {
			return built, excs, 0, err
		}
	}
	if err := persistExceptions(ctx, tx, employerID, employeeID, excs); err != nil {
		return built, excs, 0, err
	}
	resolved, err := resolveExceptions(ctx, tx, employerID, employeeID)
	if err != nil {
		return built, excs, 0, err
	}
	if err := deriveDayStatuses(ctx, tx, employerID, employeeID); err != nil {
		return built, excs, resolved, err
	}
	return built, excs, resolved, nil
}

// persistExceptions appends pairing exceptions, skipping causes already
// open. Callers must hold the employee advisory lock, which makes the
// check-then-insert race-free. Errors abort the caller: an exception
// must never be silently dropped.
func persistExceptions(ctx context.Context, tx pgx.Tx, employerID, employeeID string, excs []Exception) error {
	for _, e := range excs {
		if e.Date == "" {
			continue
		}
		var open bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM attendance_exception
			 WHERE employer_id = $1 AND employee_id = $2 AND business_date = $3::date
			   AND code = $4 AND detail = $5 AND NOT resolved)`,
			employerID, employeeID, e.Date, e.Code, e.Detail).Scan(&open); err != nil {
			return apperr.FromDB(err, "check open exception")
		}
		if open {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO attendance_exception (employer_id, employee_id, business_date, code, detail, event_ids)
			 VALUES ($1, $2, $3::date, $4, $5, $6::uuid[])`,
			employerID, employeeID, e.Date, e.Code, e.Detail, e.EventIDs); err != nil {
			return apperr.FromDB(err, "persist exception")
		}
	}
	return nil
}

// resolveExceptions clears exceptions whose causes the current evidence
// settles: a cause endpoint counts as settled when a live interval
// consumes it or a duplicate disposition excludes it. It returns the
// newly-resolved count. Conflicting redelivery never auto-resolves:
// the vendor must reconcile the duplicate source ID.
func resolveExceptions(ctx context.Context, tx pgx.Tx, employerID, employeeID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`WITH c AS (
		   UPDATE attendance_exception ex SET resolved = true, resolved_at = now()
		   WHERE ex.employer_id = $1 AND ex.employee_id = $2 AND NOT ex.resolved
		     AND ex.code IN ('missing_punch', 'unmatched_event', 'dangling_break',
		       'span_too_long', 'span_date_mismatch', 'ambiguous_break_classification',
		       'evidence_conflict')
		     AND cardinality(ex.event_ids) > 0
		     AND NOT EXISTS (
		       SELECT 1 FROM unnest(ex.event_ids) eid
		       WHERE NOT EXISTS (
		         SELECT 1 FROM attendance_interval i
		         WHERE i.employer_id = ex.employer_id
		           AND i.source_refs -> 'clock_event_ids' ? eid::text)
		         AND NOT EXISTS (
		           SELECT 1 FROM clock_event_disposition d WHERE d.event_id = eid))
		   RETURNING 1)
		 SELECT count(*) FROM c`, employerID, employeeID).Scan(&n)
	if err != nil {
		return 0, apperr.FromDB(err, "resolve consumed exceptions")
	}
	// An overlapping span settles when every cause endpoint is either
	// consumed into a live interval or confirmed as a duplicate: the
	// handled duplicate stops blocking generation, while a genuinely
	// overlapping span with unsettled endpoints stays open.
	var o int
	err = tx.QueryRow(ctx,
		`WITH c AS (
		   UPDATE attendance_exception ex SET resolved = true, resolved_at = now()
		   WHERE ex.employer_id = $1 AND ex.employee_id = $2 AND NOT ex.resolved
		     AND ex.code = 'overlapping_span'
		     AND cardinality(ex.event_ids) > 0
		     AND NOT EXISTS (
		       SELECT 1 FROM unnest(ex.event_ids) eid
		       WHERE NOT EXISTS (
		         SELECT 1 FROM attendance_interval i
		         WHERE i.employer_id = ex.employer_id
		           AND i.source_refs -> 'clock_event_ids' ? eid::text)
		         AND NOT EXISTS (
		           SELECT 1 FROM clock_event_disposition d WHERE d.event_id = eid))
		   RETURNING 1)
		 SELECT count(*) FROM c`, employerID, employeeID).Scan(&o)
	if err != nil {
		return 0, apperr.FromDB(err, "resolve settled overlaps")
	}
	n += o
	var m int
	err = tx.QueryRow(ctx,
		`WITH c AS (
		   UPDATE attendance_exception ex SET resolved = true, resolved_at = now()
		   WHERE ex.employer_id = $1 AND ex.employee_id = $2 AND NOT ex.resolved
		     AND ex.code = 'missing_break_evidence'
		     AND EXISTS (
		       SELECT 1 FROM attendance_interval i
		       WHERE i.employer_id = ex.employer_id AND i.employee_id = ex.employee_id
		         AND i.is_current AND i.kind IN ('break_paid', 'break_unpaid')
		         AND i.shift_identity = ANY(ex.event_ids))
		   RETURNING 1)
		 SELECT count(*) FROM c`, employerID, employeeID).Scan(&m)
	if err != nil {
		return 0, apperr.FromDB(err, "resolve missing breaks")
	}
	var k int
	err = tx.QueryRow(ctx,
		`WITH c AS (
		   UPDATE attendance_exception ex SET resolved = true, resolved_at = now()
		   WHERE ex.employer_id = $1 AND ex.employee_id = $2 AND NOT ex.resolved
		     AND ex.code = 'no_roster_link'
		     AND EXISTS (
		       SELECT 1 FROM roster_assignment a
		       JOIN roster_revision rev ON rev.employer_id = a.employer_id AND rev.id = a.revision_id
		       JOIN roster r ON r.employer_id = rev.employer_id AND r.id = rev.roster_id
		         AND r.current_version = rev.version
		       WHERE a.employer_id = ex.employer_id AND a.employee_id = ex.employee_id
		         AND a.business_date = ex.business_date)
		   RETURNING 1)
		 SELECT count(*) FROM c`, employerID, employeeID).Scan(&k)
	if err != nil {
		return 0, apperr.FromDB(err, "resolve roster links")
	}
	return n + m + k, nil
}

// blockingCodes are exceptions that flag a day. Conflicting redelivery
// is recorded and visible but never blocks: the stored evidence was
// already processed normally. overlapping_span blocks until every
// cause endpoint settles (H5): an unresolved overlap must not display
// as fully reviewed while it still blocks the employee.
const blockingCodes = `'missing_punch', 'unmatched_event', 'dangling_break', 'missing_break_evidence',
  'ambiguous_break_classification', 'no_roster_link', 'span_too_long', 'span_date_mismatch',
  'evidence_conflict', 'overlapping_span'`

// deriveDayStatuses recomputes every day record of the employee from the
// current unresolved blocking exceptions. A day is 'exception' exactly
// when such an exception exists; nothing else may set the flag.
func deriveDayStatuses(ctx context.Context, tx pgx.Tx, employerID, employeeID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE attendance_record r SET status = CASE WHEN EXISTS (
		   SELECT 1 FROM attendance_exception ex
		   WHERE ex.employer_id = r.employer_id AND ex.employee_id = r.employee_id
		     AND ex.business_date = r.business_date AND NOT ex.resolved
		     AND ex.code IN (`+blockingCodes+`)) THEN 'exception' ELSE 'reviewed' END
		 WHERE r.employer_id = $1 AND r.employee_id = $2`,
		employerID, employeeID)
	return apperr.FromDB(err, "derive day statuses")
}

func parseCSV(r io.Reader) ([]clockRow, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, apperr.New(apperr.CodeValidation, "csv: missing header row: %v", err)
	}
	want := []string{"source", "source_event_id", "employee_no", "occurred_at", "timezone", "kind"}
	if len(header) != len(want) {
		return nil, apperr.New(apperr.CodeValidation, "csv: header must be %s", strings.Join(want, ","))
	}
	for i := range want {
		if strings.ToLower(strings.TrimSpace(header[i])) != want[i] {
			return nil, apperr.New(apperr.CodeValidation, "csv: header must be %s", strings.Join(want, ","))
		}
	}
	var out []clockRow
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return nil, apperr.New(apperr.CodeValidation, "csv line %d: %v", line, err)
		}
		row := clockRow{
			Source: strings.TrimSpace(rec[0]), SourceEventID: strings.TrimSpace(rec[1]),
			EmployeeNo: strings.TrimSpace(rec[2]), Timezone: strings.TrimSpace(rec[4]),
			Kind: strings.TrimSpace(rec[5]), Line: line,
		}
		if row.Source == "" || row.SourceEventID == "" || row.EmployeeNo == "" {
			return nil, apperr.New(apperr.CodeValidation, "csv line %d: source, source_event_id and employee_no are required", line)
		}
		switch row.Kind {
		case KindIn, KindOut, KindBreakStart, KindBreakEnd:
		default:
			return nil, apperr.New(apperr.CodeValidation, "csv line %d: unknown kind %q", line, row.Kind)
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(rec[3]))
		if err != nil {
			return nil, apperr.New(apperr.CodeValidation, "csv line %d: occurred_at must be RFC 3339: %v", line, err)
		}
		row.OccurredAt = ts
		if row.Timezone == "" {
			return nil, apperr.New(apperr.CodeValidation, "csv line %d: timezone is required", line)
		}
		if _, err := time.LoadLocation(row.Timezone); err != nil {
			return nil, apperr.New(apperr.CodeValidation, "csv line %d: unknown timezone %q", line, row.Timezone)
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, apperr.New(apperr.CodeValidation, "csv: no data rows")
	}
	return out, nil
}

// event is a clock event with its database identity.
type event struct {
	id     string
	kind   string
	at     time.Time
	tz     string
	source string
}

// breakSpan is one paired break_start/break_end.
type breakSpan struct {
	start, end event
}

// unpairedEvents loads every event of the employee not cited by any
// interval, in deterministic order: timestamp, kind rank (in, break
// start/end, out), source, then source event ID. Identical replays pair
// identically.
func unpairedEvents(ctx context.Context, q dbtx, employerID, employeeID string) ([]event, error) {
	rows, err := q.Query(ctx,
		`SELECT e.id, e.kind, e.occurred_at, e.timezone, e.source FROM clock_event e
		 WHERE e.employer_id = $1 AND e.employee_id = $2
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)
		   AND NOT EXISTS (SELECT 1 FROM clock_event_disposition d WHERE d.event_id = e.id)
		 ORDER BY e.occurred_at,
		   CASE e.kind WHEN 'in' THEN 0 WHEN 'break_start' THEN 1 WHEN 'break_end' THEN 2 ELSE 3 END,
		   e.source, e.source_event_id`,
		employerID, employeeID)
	if err != nil {
		return nil, apperr.FromDB(err, "load unpaired events")
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.id, &e.kind, &e.at, &e.tz, &e.source); err != nil {
			return nil, apperr.FromDB(err, "scan event")
		}
		out = append(out, e)
	}
	return out, apperr.FromDB(rows.Err(), "load unpaired events")
}

func bizDate(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}

// pairSpans pairs one employee's unpaired events in order. Anything left
// unpaired becomes an explicit exception rather than a guess. Candidate
// spans must clear the roster anchor (attribution) and the 24h ceiling
// (defense); failures leave both endpoints unpaired with exceptions.
func pairSpans(ctx context.Context, tx pgx.Tx, employerID, employeeID, empNo string, ordered []event, loc *time.Location) (int, []Exception, error) {
	var excs []Exception
	built := 0
	i := 0
	for i < len(ordered) {
		if ordered[i].kind != KindIn {
			excs = append(excs, Exception{Code: ExcUnmatchedEvent, EmployeeNo: empNo,
				Date:     bizDate(ordered[i].at, loc),
				Detail:   fmt.Sprintf("%s without preceding in", ordered[i].kind),
				EventIDs: []string{ordered[i].id}})
			i++
			continue
		}
		in := ordered[i]
		i++
		var breaks []breakSpan
		var pending *event
		var out *event
		outIdx := -1
		closed := false
		for i < len(ordered) && !closed {
			switch ordered[i].kind {
			case KindBreakStart:
				if pending != nil {
					excs = append(excs, Exception{Code: ExcDanglingBreak, EmployeeNo: empNo,
						Date: bizDate(pending.at, loc), Detail: "break_start without break_end",
						EventIDs: []string{pending.id}})
				}
				e := ordered[i]
				pending = &e
				i++
			case KindBreakEnd:
				if pending == nil {
					excs = append(excs, Exception{Code: ExcDanglingBreak, EmployeeNo: empNo,
						Date: bizDate(ordered[i].at, loc), Detail: "break_end without break_start",
						EventIDs: []string{ordered[i].id}})
					i++
					continue
				}
				breaks = append(breaks, breakSpan{start: *pending, end: ordered[i]})
				pending = nil
				i++
			case KindOut:
				e := ordered[i]
				out = &e
				outIdx = i
				i++
				closed = true
			case KindIn:
				excs = append(excs, Exception{Code: ExcMissingPunch, EmployeeNo: empNo,
					Date: bizDate(in.at, loc), Detail: "in without matching out before next in",
					EventIDs: []string{in.id}})
				closed = true
			}
		}
		if out == nil {
			excs = append(excs, Exception{Code: ExcMissingPunch, EmployeeNo: empNo,
				Date: bizDate(in.at, loc), Detail: "in without matching out",
				EventIDs: []string{in.id}})
			if pending != nil {
				excs = append(excs, Exception{Code: ExcDanglingBreak, EmployeeNo: empNo,
					Date: bizDate(pending.at, loc), Detail: "break_start without break_end",
					EventIDs: []string{pending.id}})
			}
			continue
		}
		if pending != nil {
			excs = append(excs, Exception{Code: ExcDanglingBreak, EmployeeNo: empNo,
				Date: bizDate(pending.at, loc), Detail: "break_start without break_end",
				EventIDs: []string{pending.id}})
		}
		// Defensive ceiling first: implausible spans never pair.
		if out.at.Sub(in.at) > maxSpan {
			excs = append(excs, Exception{Code: ExcSpanTooLong, EmployeeNo: empNo,
				Date:     bizDate(in.at, loc),
				Detail:   fmt.Sprintf("span %.1f hours exceeds the 24h pairing ceiling; events preserved unpaired", out.at.Sub(in.at).Hours()),
				EventIDs: []string{in.id, out.id}})
			i = outIdx // reprocess the out on its own
			continue
		}
		// Roster anchor: the out must fall on the in-date, or the next
		// date for cross-midnight templates. Overtime stays legitimate:
		// the rule constrains dates, never hours.
		inDate := bizDate(in.at, loc)
		outDate := bizDate(out.at, loc)
		asg, err := resolveAssignment(ctx, tx, employerID, employeeID, inDate)
		if err != nil {
			return built, excs, err
		}
		if asg != nil {
			allowed := outDate == inDate
			if !allowed && asg.crossMidnight {
				allowed = outDate == nextDate(inDate)
			}
			if !allowed {
				excs = append(excs, Exception{Code: ExcSpanDateMismatch, EmployeeNo: empNo,
					Date:     inDate,
					Detail:   fmt.Sprintf("out on %s does not belong to the %s shift starting %s", outDate, inDate, in.at.UTC().Format(time.RFC3339)),
					EventIDs: []string{in.id, out.id}})
				i = outIdx
				continue
			}
		}
		// Overlap guard: a span re-describing already-covered time (for
		// example a vendor resend under fresh event IDs) stays unpaired
		// with an explicit exception. It must never double-pair, and it
		// must not fail the rest of the batch.
		var clash bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM attendance_interval
			 WHERE employer_id = $1 AND employee_id = $2 AND is_current
			   AND tstzrange(interval_start, interval_end) && tstzrange($3, $4))`,
			employerID, employeeID, in.at, out.at).Scan(&clash); err != nil {
			return built, excs, apperr.FromDB(err, "check span overlap")
		}
		if clash {
			// Both endpoints are consumed by this exception: they
			// describe covered time, so no phantom missing-punch
			// trail follows them.
			excs = append(excs, Exception{Code: ExcOverlappingSpan, EmployeeNo: empNo,
				Date:     inDate,
				Detail:   "span overlaps an existing interval; events preserved unpaired",
				EventIDs: []string{in.id, out.id}})
			continue
		}
		n, spanExcs, err := storeSpan(ctx, tx, employerID, employeeID, empNo, in, *out, breaks, asg, loc)
		if err != nil {
			return built, excs, err
		}
		built += n
		excs = append(excs, spanExcs...)
	}
	return built, excs, nil
}

func nextDate(ymd string) string {
	d, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		return ""
	}
	return d.AddDate(0, 0, 1).Format("2006-01-02")
}

// assignment is a current-revision roster assignment with its template's
// cross-midnight flag.
type assignment struct {
	id            string
	crossMidnight bool
}

// resolveAssignment finds the current-revision assignment for the
// employee and business date, or nil when the span is unrostered.
func resolveAssignment(ctx context.Context, q dbtx, employerID, employeeID, date string) (*assignment, error) {
	var a assignment
	err := q.QueryRow(ctx,
		`SELECT a.id, COALESCE(t.cross_midnight, false) FROM roster_assignment a
		 JOIN roster_revision rev ON rev.employer_id = a.employer_id AND rev.id = a.revision_id
		 JOIN roster r ON r.employer_id = rev.employer_id AND r.id = rev.roster_id AND r.current_version = rev.version
		 LEFT JOIN shift_template t ON t.employer_id = a.employer_id AND t.id = a.template_id
		 WHERE a.employer_id = $1 AND a.employee_id = $2 AND a.business_date = $3::date`,
		employerID, employeeID, date).Scan(&a.id, &a.crossMidnight)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, apperr.FromDB(err, "link roster assignment")
	}
	return &a, nil
}

// datedRun is one interval run with its own business date and the event
// IDs it actually spans.
type datedRun struct {
	start, end time.Time
	date       string
	eventIDs   []string
}

// breakPieceAid resolves the assignment for one dated break piece. A
// piece landing on a date without its own roster assignment keeps a NULL
// assignment and reports unlinked: silently inheriting the shift's
// assignment would misattribute evidence across a roster boundary.
func breakPieceAid(ctx context.Context, tx pgx.Tx, employerID, employeeID, spanDate, runDate string, fallback *string) (*string, bool, error) {
	if runDate == spanDate || fallback == nil {
		return assignmentIDFor(ctx, tx, employerID, employeeID, spanDate, runDate, fallback), false, nil
	}
	own, err := resolveAssignment(ctx, tx, employerID, employeeID, runDate)
	if err != nil {
		return nil, false, err
	}
	if own != nil {
		return &own.id, false, nil
	}
	return nil, true, nil
}

// splitRun cuts [start, end) at local midnights so cross-midnight work
// attributes to each calendar date while both halves cite the same
// boundary events and keep one shift identity.
func splitRun(start, end time.Time, loc *time.Location, eventIDs []string) []datedRun {
	var runs []datedRun
	cursor := start
	for {
		dayEnd := time.Date(cursor.In(loc).Year(), cursor.In(loc).Month(), cursor.In(loc).Day()+1, 0, 0, 0, 0, loc)
		if !dayEnd.Before(end) {
			runs = append(runs, datedRun{start: cursor, end: end,
				date: cursor.In(loc).Format("2006-01-02"), eventIDs: eventIDs})
			return runs
		}
		runs = append(runs, datedRun{start: cursor, end: dayEnd,
			date: cursor.In(loc).Format("2006-01-02"), eventIDs: eventIDs})
		cursor = dayEnd
	}
}

// storeSpan writes one in/out span with nested break intervals. Breaks
// that cannot be classified become exceptions with no interval and are
// never cited as consumed: their events stay unpaired. Unrostered spans
// are stored and flagged, never silently reviewed.
func storeSpan(ctx context.Context, tx pgx.Tx, employerID, employeeID, empNo string, in, out event, breaks []breakSpan, asg *assignment, loc *time.Location) (int, []Exception, error) {
	var excs []Exception
	var assignmentID *string
	if asg != nil {
		assignmentID = &asg.id
	}
	// Classify breaks before writing anything: only consumed breaks may
	// be cited by work intervals.
	type classified struct {
		span breakSpan
		kind string
	}
	var good []classified
	for _, b := range breaks {
		kind, reason := classifyBreak(ctx, tx, employerID, assignmentID, b.start.at, b.end.at, loc)
		if reason != "" {
			excs = append(excs, Exception{Code: ExcAmbiguousBreak, EmployeeNo: empNo,
				Date: bizDate(in.at, loc), Detail: reason,
				EventIDs: []string{b.start.id, b.end.id}})
			continue
		}
		good = append(good, classified{span: b, kind: kind})
	}
	// Work runs between classified breaks, each citing only the events
	// that bound it.
	type rawRun struct {
		start, end time.Time
		ids        []string
	}
	var raws []rawRun
	cursor := in.at
	cursorIDs := []string{in.id}
	for _, g := range good {
		if g.span.start.at.After(cursor) {
			raws = append(raws, rawRun{start: cursor, end: g.span.start.at,
				ids: append(append([]string{}, cursorIDs...), g.span.start.id)})
		}
		cursor = g.span.end.at
		cursorIDs = []string{g.span.end.id}
	}
	if out.at.After(cursor) {
		raws = append(raws, rawRun{start: cursor, end: out.at,
			ids: append(append([]string{}, cursorIDs...), out.id)})
	}
	n := 0
	dates := map[string]bool{}
	for _, r := range raws {
		for _, d := range splitRun(r.start, r.end, loc, r.ids) {
			aid := assignmentIDFor(ctx, tx, employerID, employeeID, bizDate(in.at, loc), d.date, assignmentID)
			if err := insertInterval(ctx, tx, employerID, employeeID, aid, d.start, d.end, loc.String(), d.date, "work", refsJSON(d.eventIDs), in.id, nil); err != nil {
				return n, excs, err
			}
			n++
			dates[d.date] = true
		}
	}
	for _, g := range good {
		ids := []string{g.span.start.id, g.span.end.id}
		for _, d := range splitRun(g.span.start.at, g.span.end.at, loc, ids) {
			aid, unlinked, err := breakPieceAid(ctx, tx, employerID, employeeID, bizDate(in.at, loc), d.date, assignmentID)
			if err != nil {
				return n, excs, err
			}
			if unlinked {
				excs = append(excs, Exception{Code: ExcNoRosterLink, EmployeeNo: empNo,
					Date: d.date, Detail: "break piece lands on a date with no roster assignment; stored unlinked",
					EventIDs: ids})
			}
			if err := insertInterval(ctx, tx, employerID, employeeID, aid, d.start, d.end, loc.String(), d.date, g.kind, refsJSON(d.eventIDs), in.id, nil); err != nil {
				return n, excs, err
			}
			n++
			dates[d.date] = true
		}
	}
	if len(breaks) == 0 && assignmentID != nil && hasScheduledBreaks(ctx, tx, employerID, *assignmentID) {
		excs = append(excs, Exception{Code: ExcMissingBreak, EmployeeNo: empNo,
			Date: bizDate(in.at, loc), Detail: "roster schedules a break but no break events observed",
			EventIDs: []string{in.id}})
	}
	if asg == nil {
		excs = append(excs, Exception{Code: ExcNoRosterLink, EmployeeNo: empNo,
			Date: bizDate(in.at, loc), Detail: "span has no roster assignment; intervals stored and flagged",
			EventIDs: []string{in.id, out.id}})
	}
	for d := range dates {
		aid := assignmentIDFor(ctx, tx, employerID, employeeID, bizDate(in.at, loc), d, assignmentID)
		if err := upsertRecord(ctx, tx, employerID, employeeID, d, aid); err != nil {
			return n, excs, err
		}
	}
	return n, excs, nil
}

// assignmentIDFor keeps the in-date assignment for the shift's own date
// and resolves the run date's assignment for split halves, falling back
// to the shift assignment so cross-midnight work keeps its shift link.
func assignmentIDFor(ctx context.Context, q dbtx, employerID, employeeID, inDate, runDate string, fallback *string) *string {
	if runDate == inDate || fallback == nil {
		return fallback
	}
	a, err := resolveAssignment(ctx, q, employerID, employeeID, runDate)
	if err != nil || a == nil {
		return fallback
	}
	return &a.id
}

// attachLateBreaks fits unpaired break pairs that arrived after their
// span into the covering live work interval, superseding it with split
// runs. Breaks that fit nowhere stay unpaired with their exceptions.
// attachLateBreaks pairs break events left unpaired by the span pass
// and splits the live work intervals they fall in. Splitting
// supersedes live evidence, so every touched interval clears a linkage
// gate first: covers feeding a decided chain without an open correction
// draft hold the whole break with an explicit evidence conflict instead
// of silently desyncing the approval. Draft terminals collected along
// the way come back for regeneration by the caller.
func attachLateBreaks(ctx context.Context, tx pgx.Tx, employerID, employeeID, empNo string, loc *time.Location, chain ChainFunc) (int, []Exception, []string, error) {
	evs, err := unpairedEvents(ctx, tx, employerID, employeeID)
	if err != nil {
		return 0, nil, nil, err
	}
	type pair struct {
		start, end event
	}
	var pairs []pair
	var pending *event
	for _, e := range evs {
		switch e.kind {
		case KindBreakStart:
			c := e
			pending = &c
		case KindBreakEnd:
			if pending != nil {
				pairs = append(pairs, pair{start: *pending, end: e})
				pending = nil
			}
		}
	}
	var excs []Exception
	var drafts []string
	seenDraft := map[string]bool{}
	built := 0
	for _, p := range pairs {
		// A late break may span the dated halves of one split span:
		// collect every live work interval it touches and require
		// them to tile contiguously across the break. Gaps or foreign
		// intervals mean the evidence does not support attachment.
		type cover struct {
			id         string
			start, end time.Time
			aid        *string
			shift      *string
		}
		rows, err := tx.Query(ctx,
			`SELECT id, interval_start, interval_end, roster_assignment_id, shift_identity
			 FROM attendance_interval
			 WHERE employer_id = $1 AND employee_id = $2 AND is_current AND kind = 'work'
			   AND interval_start < $4 AND interval_end > $3
			 ORDER BY interval_start`,
			employerID, employeeID, p.start.at, p.end.at)
		if err != nil {
			return built, excs, nil, apperr.FromDB(err, "find covering spans")
		}
		var covers []cover
		for rows.Next() {
			var c cover
			if err := rows.Scan(&c.id, &c.start, &c.end, &c.aid, &c.shift); err != nil {
				rows.Close()
				return built, excs, nil, apperr.FromDB(err, "scan covering span")
			}
			covers = append(covers, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return built, excs, nil, apperr.FromDB(err, "find covering spans")
		}
		tiles := len(covers) > 0 &&
			!covers[0].start.After(p.start.at) &&
			!covers[len(covers)-1].end.Before(p.end.at)
		for i := 1; tiles && i < len(covers); i++ {
			tiles = covers[i-1].end.Equal(covers[i].start)
		}
		if !tiles {
			continue // fits no live span; pairing already exceptioned it
		}
		// One shift pattern owns the break: halves rostered under
		// different templates mean the break straddles two shifts and
		// no single window set can classify it. Distinct assignment
		// rows sharing one template (a night shift's dated halves)
		// classify fine; each piece still resolves its own date's
		// assignment below.
		tmpls := map[string]bool{}
		for _, c := range covers {
			if c.aid == nil {
				continue
			}
			var tid string
			if err := tx.QueryRow(ctx,
				`SELECT template_id FROM roster_assignment WHERE employer_id = $1 AND id = $2`,
				employerID, *c.aid).Scan(&tid); err != nil {
				return built, excs, nil, apperr.FromDB(err, "resolve shift template")
			}
			tmpls[tid] = true
		}
		if len(tmpls) > 1 {
			excs = append(excs, Exception{Code: ExcAmbiguousBreak, EmployeeNo: empNo,
				Date: bizDate(p.start.at, loc), Detail: "late break spans intervals of two shifts; no single template owns it",
				EventIDs: []string{p.start.id, p.end.id}})
			continue
		}
		kind, reason := classifyBreak(ctx, tx, employerID, covers[0].aid, p.start.at, p.end.at, loc)
		if reason != "" {
			excs = append(excs, Exception{Code: ExcAmbiguousBreak, EmployeeNo: empNo,
				Date: bizDate(p.start.at, loc), Detail: reason,
				EventIDs: []string{p.start.id, p.end.id}})
			continue
		}
		// Linkage gate: the break splits whole, or waits whole. Any
		// touched interval feeding a decided chain without an open
		// correction draft holds the break with an explicit conflict;
		// draft terminals collect for regeneration after the split.
		held := ""
		var want []string
		for _, w := range covers {
			sheets, err := linkedSheets(ctx, tx, employerID, w.id)
			if err != nil {
				return built, excs, nil, err
			}
			for _, s := range sheets {
				if s.status == "rejected" {
					continue // inert history
				}
				terminal, tstatus, err := chain(ctx, tx, employerID, s.id)
				if err != nil {
					return built, excs, nil, err
				}
				if tstatus != "draft" {
					held = fmt.Sprintf("break evidence held: interval feeds %s timesheet with no open correction draft; reissue to attach", tstatus)
					break
				}
				want = append(want, terminal)
			}
			if held != "" {
				break
			}
		}
		if held != "" {
			excs = append(excs, Exception{Code: ExcEvidenceConflict, EmployeeNo: empNo,
				Date: bizDate(p.start.at, loc), Detail: held,
				EventIDs: []string{p.start.id, p.end.id}})
			continue
		}
		for _, d := range want {
			if !seenDraft[d] {
				seenDraft[d] = true
				drafts = append(drafts, d)
			}
		}
		type splitPart struct {
			kind       string
			start, end time.Time
			ids        []string
		}
		inDate := bizDate(p.start.at, loc)
		dates := map[string]bool{}
		for _, w := range covers {
			// Clip the break to this covered half and cut the
			// standard three parts, skipping empties.
			bs, be := p.start.at, p.end.at
			if bs.Before(w.start) {
				bs = w.start
			}
			if be.After(w.end) {
				be = w.end
			}
			var runs []splitPart
			if bs.After(w.start) {
				runs = append(runs, splitPart{kind: "work", start: w.start, end: bs, ids: []string{p.start.id}})
			}
			runs = append(runs, splitPart{kind: kind, start: bs, end: be, ids: []string{p.start.id, p.end.id}})
			if w.end.After(be) {
				runs = append(runs, splitPart{kind: "work", start: be, end: w.end, ids: []string{p.end.id}})
			}
			for _, r := range runs {
				for _, d := range splitRun(r.start, r.end, loc, r.ids) {
					aid := assignmentIDFor(ctx, tx, employerID, employeeID, inDate, d.date, w.aid)
					if r.kind != "work" {
						var unlinked bool
						aid, unlinked, err = breakPieceAid(ctx, tx, employerID, employeeID, inDate, d.date, w.aid)
						if err != nil {
							return built, excs, nil, err
						}
						if unlinked {
							excs = append(excs, Exception{Code: ExcNoRosterLink, EmployeeNo: empNo,
								Date: d.date, Detail: "break piece lands on a date with no roster assignment; stored unlinked",
								EventIDs: r.ids})
						}
					}
					if err := insertInterval(ctx, tx, employerID, employeeID, aid, d.start, d.end, loc.String(), d.date, r.kind, refsJSON(d.eventIDs), eventIDOrZero(w.shift), &w.id); err != nil {
						return built, excs, nil, err
					}
					built++
					dates[d.date] = true
				}
			}
		}
		for d := range dates {
			aid := assignmentIDFor(ctx, tx, employerID, employeeID, inDate, d, covers[0].aid)
			if err := upsertRecord(ctx, tx, employerID, employeeID, d, aid); err != nil {
				return built, excs, nil, err
			}
		}
	}
	return built, excs, drafts, nil
}

func eventIDOrZero(id *string) string {
	if id == nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	return *id
}

// classifyBreak maps one observed break to the roster's scheduled break
// segments by midpoint: the break takes the kind of the scheduled window
// containing its midpoint, anchored at the assignment's business date
// plus the template's local start. A break with no roster, no scheduled
// break, or a midpoint outside every window is ambiguous: the second
// return carries the exception detail.
func classifyBreak(ctx context.Context, q dbtx, employerID string, assignmentID *string, start, end time.Time, loc *time.Location) (string, string) {
	if assignmentID == nil {
		return "", "break observed without a roster assignment"
	}
	rows, err := q.Query(ctx,
		`SELECT s.kind, s.start_offset_min, s.duration_min, t.start_local, a.business_date::text, t.cross_midnight
		 FROM roster_assignment a
		 JOIN shift_template t ON t.employer_id = a.employer_id AND t.id = a.template_id
		 JOIN shift_segment s ON s.employer_id = t.employer_id AND s.template_id = t.id
		 WHERE a.employer_id = $1 AND a.id = $2 AND s.kind IN ('break_paid', 'break_unpaid')`,
		employerID, *assignmentID)
	if err != nil {
		return "", fmt.Sprintf("load scheduled breaks: %v", apperr.FromDB(err, "load segments"))
	}
	defer rows.Close()
	type seg struct {
		kind       string
		start      int
		length     int
		shiftStart time.Time
	}
	var segs []seg
	// Shift anchors resolve in the employment timezone: the decoded
	// instant's own location is the host process timezone and must
	// never position a comparison window.
	for rows.Next() {
		var s seg
		var startLocal, bizDate string
		var cross bool
		if err := rows.Scan(&s.kind, &s.start, &s.length, &startLocal, &bizDate, &cross); err != nil {
			return "", fmt.Sprintf("scan scheduled breaks: %v", apperr.FromDB(err, "scan segments"))
		}
		var anchor time.Time
		var perr error
		for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
			anchor, perr = time.ParseInLocation(layout, bizDate+" "+startLocal, loc)
			if perr == nil {
				break
			}
		}
		if perr != nil {
			return "", fmt.Sprintf("bad template start %q: %v", startLocal, perr)
		}
		s.shiftStart = anchor
		segs = append(segs, s)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Sprintf("load scheduled breaks: %v", apperr.FromDB(err, "load segments"))
	}
	if len(segs) == 0 {
		return "", "break observed but the roster schedules no break"
	}
	mid := start.Add(end.Sub(start) / 2)
	for _, s := range segs {
		winStart := s.shiftStart.Add(time.Duration(s.start) * time.Minute)
		winEnd := winStart.Add(time.Duration(s.length) * time.Minute)
		if !mid.Before(winStart) && mid.Before(winEnd) {
			return s.kind, ""
		}
	}
	return "", "break observed outside every scheduled break window"
}

func hasScheduledBreaks(ctx context.Context, q dbtx, employerID, assignmentID string) bool {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM roster_assignment a
		 JOIN shift_segment s ON s.employer_id = a.employer_id AND s.template_id = a.template_id
		 WHERE a.employer_id = $1 AND a.id = $2 AND s.kind IN ('break_paid', 'break_unpaid')`,
		employerID, assignmentID).Scan(&n)
	return err == nil && n > 0
}

func refsJSON(ids []string) string {
	var b strings.Builder
	b.WriteString(`{"clock_event_ids": [`)
	for i, id := range ids {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", id)
	}
	b.WriteString(`]}`)
	return b.String()
}

// insertInterval writes one interval row. supersedesID links a correction
// or pairing-split successor; nil for fresh spans.
func insertInterval(ctx context.Context, q dbtx, employerID, employeeID string, assignmentID *string, start, end time.Time, tz, date, kind, refs, shift string, supersedesID *string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO attendance_interval (employer_id, employee_id, roster_assignment_id, interval_start, interval_end, timezone, local_date, kind, source, source_refs, shift_identity, supersedes_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, 'clock', $9::jsonb, $10, $11)`,
		employerID, employeeID, assignmentID, start, end, tz, date, kind, refs, shift, supersedesID)
	return apperr.FromDB(err, "store interval")
}

// upsertRecord recomputes one day record from CURRENT intervals only.
// Status always recomputes to reviewed here; deriveDayStatuses flips days
// with unresolved blocking exceptions afterwards, in the same transaction.
func upsertRecord(ctx context.Context, q dbtx, employerID, employeeID, date string, assignmentID *string) error {
	rows, err := q.Query(ctx,
		`SELECT i.interval_start, i.interval_end, i.kind FROM attendance_interval i
		 WHERE i.employer_id = $1 AND i.employee_id = $2 AND i.local_date = $3::date AND i.is_current`,
		employerID, employeeID, date)
	if err != nil {
		return apperr.FromDB(err, "load day intervals")
	}
	defer rows.Close()
	var actualUs, paidUs int64
	for rows.Next() {
		var istart, iend time.Time
		var kind string
		if err := rows.Scan(&istart, &iend, &kind); err != nil {
			return apperr.FromDB(err, "scan day interval")
		}
		us := duration.MicrosOf(istart, iend)
		actualUs += us
		if kind == "work" || kind == "break_paid" {
			paidUs += us
		}
	}
	if err := rows.Err(); err != nil {
		return apperr.FromDB(err, "load day intervals")
	}
	actual := duration.Quantize2(duration.MinutesOf(actualUs))
	paid := duration.Quantize2(duration.MinutesOf(paidUs))
	var scheduled *float64
	if assignmentID != nil {
		var mins float64
		if err := q.QueryRow(ctx,
			`SELECT COALESCE(sum(s.duration_min), 0) FROM roster_assignment a
			 JOIN shift_segment s ON s.employer_id = a.employer_id AND s.template_id = a.template_id
			 WHERE a.employer_id = $1 AND a.id = $2`, employerID, *assignmentID).Scan(&mins); err == nil {
			scheduled = &mins
		}
	}
	_, err = q.Exec(ctx,
		`INSERT INTO attendance_record (employer_id, employee_id, business_date, roster_assignment_id, scheduled_min, recorded_min, actual_min, paid_min, actual_us, paid_us, status)
		 VALUES ($1, $2, $3::date, $4, $5, $6, $6, $7, $8, $9, 'reviewed')
		 ON CONFLICT (employer_id, employee_id, business_date)
		 DO UPDATE SET roster_assignment_id = EXCLUDED.roster_assignment_id,
		  scheduled_min = EXCLUDED.scheduled_min, recorded_min = EXCLUDED.recorded_min,
		  actual_min = EXCLUDED.actual_min, paid_min = EXCLUDED.paid_min,
		  actual_us = EXCLUDED.actual_us, paid_us = EXCLUDED.paid_us, status = 'reviewed'`,
		employerID, employeeID, date, assignmentID, scheduled, actual, paid, actualUs, paidUs)
	return apperr.FromDB(err, "upsert day record")
}
