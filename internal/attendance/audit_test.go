package attendance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/timesheet"
)

func lastAudit(t *testing.T, pool *pgxpool.Pool, emp, action string) (actor, service, entity, entityID, before, after string) {
	t.Helper()
	var eid, bef, aft *string
	if err := pool.QueryRow(ctx(t),
		`SELECT actor, service, entity, entity_id::text, before_state::text, after_state::text
		 FROM audit_event WHERE employer_id = $1 AND action = $2
		 ORDER BY occurred_at DESC LIMIT 1`, emp, action).Scan(
		&actor, &service, &entity, &eid, &bef, &aft); err != nil {
		t.Fatalf("audit %s: %v", action, err)
	}
	if eid != nil {
		entityID = *eid
	}
	if bef != nil {
		before = *bef
	}
	if aft != nil {
		after = *aft
	}
	return actor, service, entity, entityID, before, after
}

// A disposition excludes vendor evidence from pairing. The audit row
// names the excluded event, the canonical it was folded into, and the
// operator who confirmed the duplicate.
func TestDispositionAudited(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DA")
	importCSV(t, pool, emp, spanCSV(no, "da-1", "da-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "da-3", "da-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	dup, orig := eventID(t, pool, emp, "da-3"), eventID(t, pool, emp, "da-1")
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: dup, CanonicalID: orig, Reason: "vendor resend",
	}, "op-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("disposition: %v", err)
	}
	actor, service, entity, eid, _, after := lastAudit(t, pool, emp, "clock_event.disposition")
	if actor != "op-1" || service != "attendance" || entity != "clock_event" || eid != dup {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	if !strings.Contains(after, orig) || !strings.Contains(after, "vendor resend") {
		t.Fatalf("after=%s want canonical and reason", after)
	}
}

// Correct supersedes evidence against an immutable predecessor. The
// audit row carries the group lineage plus the old and new bounds.
func TestCorrectAudited(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "CA")
	importCSV(t, pool, emp, spanCSV(no, "ca-1", "ca-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	rep, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T09:00:00+08:00", End: "2026-08-03T17:00:00+08:00",
		Kind: "work", Reason: "early close",
	}, "op-2", timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	actor, service, entity, eid, before, after := lastAudit(t, pool, emp, "interval.correct")
	if actor != "op-2" || service != "attendance" || entity != "attendance_interval" || eid != rep.SegmentIDs[0] {
		t.Fatalf("audit=%s/%s/%s/%s", actor, service, entity, eid)
	}
	// Bounds are instants: compare as time, never as wall-clock text,
	// so the assertion holds under any process timezone.
	at := func(s string) time.Time {
		t.Helper()
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return ts
	}
	var bef struct {
		IntervalID string `json:"interval_id"`
		End        string `json:"end"`
	}
	if err := json.Unmarshal([]byte(before), &bef); err != nil {
		t.Fatalf("before json: %v", err)
	}
	if bef.IntervalID != orig || !at(bef.End).Equal(at("2026-08-03T18:00:00+08:00")) {
		t.Fatalf("before=%s want predecessor and old end", before)
	}
	var aft struct {
		GroupID  string `json:"correction_group_id"`
		Reason   string `json:"reason"`
		Segments []struct {
			End string `json:"end"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(after), &aft); err != nil {
		t.Fatalf("after json: %v", err)
	}
	if aft.GroupID != rep.GroupID || aft.Reason != "early close" || len(aft.Segments) != 1 ||
		!at(aft.Segments[0].End).Equal(at("2026-08-03T17:00:00+08:00")) {
		t.Fatalf("after=%s want group, reason, and new end", after)
	}
}
