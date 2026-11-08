package attendance

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
	"github.com/PithomLabs/a1/internal/employer"
)

// DispositionInput confirms one clock event as a duplicate delivery of
// a canonical event. Both IDs are clock_event UUIDs.
type DispositionInput struct {
	EventID     string `json:"event_id"`
	CanonicalID string `json:"canonical_event_id"`
	Reason      string `json:"reason"`
}

// DispositionReport is the outcome of one duplicate confirmation.
type DispositionReport struct {
	EventID         string      `json:"event_id"`
	CanonicalID     string      `json:"canonical_event_id"`
	AlreadyRecorded bool        `json:"already_recorded"`
	IntervalsBuilt  int         `json:"intervals_built"`
	Exceptions      []Exception `json:"exceptions"`
	Resolved        int         `json:"exceptions_resolved"`
}

type dispositionEvent struct {
	id         string
	employeeID string
	kind       string
	at         time.Time
}

// DispositionDuplicate records an operator decision that one clock
// event is a duplicate delivery of a canonical event, then re-pairs
// the employee so day eligibility recalculates: the duplicate stays
// preserved but excluded, the canonical evidence stays usable, and a
// settled overlapping_span resolves instead of blocking generation
// forever.
//
// The match is strict on purpose — same employee, same kind, exact
// same instant — so a genuinely overlapping second span can never be
// confirmed away. The database trigger re-enforces the rule for
// writers that bypass this service. Repeating the same decision is an
// idempotent success; pointing the event at a rival canonical event
// conflicts and the first decision stands.
func DispositionDuplicate(ctx context.Context, pool *pgxpool.Pool, employerID string, in DispositionInput, operator string, regen RegenerateFunc, chain ChainFunc) (*DispositionReport, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if regen == nil || chain == nil {
		return nil, apperr.New(apperr.CodeValidation, "draft regeneration and chain resolution are required")
	}
	if strings.TrimSpace(in.EventID) == "" || strings.TrimSpace(in.CanonicalID) == "" {
		return nil, apperr.New(apperr.CodeValidation, "event_id and canonical_event_id are required")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, apperr.New(apperr.CodeValidation, "a reason is required to confirm a duplicate")
	}
	if in.EventID == in.CanonicalID {
		return nil, apperr.New(apperr.CodeValidation, "an event cannot duplicate itself")
	}
	loc, err := employer.EmploymentLocation(ctx, pool, employerID)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, apperr.FromDB(err, "begin disposition")
	}
	defer tx.Rollback(ctx)

	dup, err := loadDispositionEvent(ctx, tx, employerID, in.EventID)
	if err != nil {
		return nil, err
	}
	canon, err := loadDispositionEvent(ctx, tx, employerID, in.CanonicalID)
	if err != nil {
		return nil, err
	}
	if dup.employeeID != canon.employeeID {
		return nil, apperr.New(apperr.CodeValidation, "duplicate and canonical events belong to different employees")
	}
	if dup.kind != canon.kind {
		return nil, apperr.New(apperr.CodeValidation,
			"duplicate kind %s differs from canonical kind %s", dup.kind, canon.kind)
	}
	if dup.at.UnixMicro() != canon.at.UnixMicro() {
		return nil, apperr.New(apperr.CodeValidation, "duplicate and canonical events are different instants, not a duplicate delivery")
	}
	if err := coredatabase.LockPayrollInput(ctx, tx, employerID, dup.employeeID); err != nil {
		return nil, err
	}
	// An allocated event already anchors evidence: only corrections
	// may supersede it, never a duplicate confirmation.
	var consumed bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM attendance_interval i
		 WHERE i.employer_id = $1 AND i.source_refs -> 'clock_event_ids' ? $2)`,
		employerID, dup.id).Scan(&consumed); err != nil {
		return nil, apperr.FromDB(err, "check event allocation")
	}
	if consumed {
		return nil, apperr.New(apperr.CodeInvalidState,
			"event %s is already allocated to an interval; correct the interval instead", dup.id)
	}
	var canonDup bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM clock_event_disposition WHERE event_id = $1::uuid)`,
		canon.id).Scan(&canonDup); err != nil {
		return nil, apperr.FromDB(err, "check canonical disposition")
	}
	if canonDup {
		return nil, apperr.New(apperr.CodeValidation,
			"canonical event %s is itself dispositioned as a duplicate", canon.id)
	}
	// The reverse direction: a canonical event cannot itself be
	// confirmed as a duplicate. Checked post-lock alongside the
	// trigger's own recheck, so chains fail the same way through
	// the service and through direct SQL.
	var alreadyCanon bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM clock_event_disposition WHERE duplicate_of_event_id = $1::uuid)`,
		dup.id).Scan(&alreadyCanon); err != nil {
		return nil, apperr.FromDB(err, "check duplicate canonical use")
	}
	if alreadyCanon {
		return nil, apperr.New(apperr.CodeValidation,
			"event %s is already canonical for another disposition", dup.id)
	}
	rep := &DispositionReport{EventID: dup.id, CanonicalID: canon.id, Exceptions: []Exception{}}
	var inserted bool
	if err := tx.QueryRow(ctx,
		`INSERT INTO clock_event_disposition
		   (employer_id, event_id, disposition, duplicate_of_event_id, reason, decided_by)
		 VALUES ($1, $2::uuid, 'duplicate', $3::uuid, $4, $5)
		 ON CONFLICT (event_id) DO NOTHING
		 RETURNING true`, employerID, dup.id, canon.id, in.Reason, operator).Scan(&inserted); err != nil {
		if err != pgx.ErrNoRows {
			return nil, apperr.FromDB(err, "record disposition")
		}
		var standing string
		if err := tx.QueryRow(ctx,
			`SELECT duplicate_of_event_id::text FROM clock_event_disposition WHERE event_id = $1::uuid`,
			dup.id).Scan(&standing); err != nil {
			return nil, apperr.FromDB(err, "load standing disposition")
		}
		if standing != canon.id {
			return nil, apperr.New(apperr.CodeConflict,
				"event %s is already confirmed as a duplicate of %s", dup.id, standing)
		}
		rep.AlreadyRecorded = true
	}
	var empNo string
	if err := tx.QueryRow(ctx,
		`SELECT employee_no FROM employee WHERE employer_id = $1 AND id = $2`,
		employerID, dup.employeeID).Scan(&empNo); err != nil {
		return nil, apperr.FromDB(err, "resolve employee")
	}
	built, excs, resolved, err := pairEmployee(ctx, tx, employerID, dup.employeeID, empNo, loc, regen, chain)
	if err != nil {
		return nil, err
	}
	rep.IntervalsBuilt, rep.Exceptions, rep.Resolved = built, excs, resolved
	if !rep.AlreadyRecorded {
		if err := audit.Write(ctx, tx, employerID, operator, "attendance",
			"clock_event.disposition", "clock_event", &dup.id, nil, map[string]any{
				"duplicate_of_event_id": canon.id,
				"reason":                in.Reason,
				"intervals_built":       built,
				"exceptions_resolved":   resolved,
			}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.FromDB(err, "commit disposition")
	}
	return rep, nil
}

func loadDispositionEvent(ctx context.Context, tx pgx.Tx, employerID, id string) (dispositionEvent, error) {
	var e dispositionEvent
	err := tx.QueryRow(ctx,
		`SELECT id::text, employee_id::text, kind, occurred_at FROM clock_event
		 WHERE employer_id = $1 AND id = $2::uuid`, employerID, id).Scan(
		&e.id, &e.employeeID, &e.kind, &e.at)
	if err != nil {
		if err == pgx.ErrNoRows {
			return e, apperr.New(apperr.CodeNotFound, "clock event %s not found", id)
		}
		return e, apperr.FromDB(err, "load clock event")
	}
	return e, nil
}
