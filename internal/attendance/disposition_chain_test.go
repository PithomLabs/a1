package attendance

import (
	"testing"

	"github.com/PithomLabs/a1/internal/timesheet"
)

// Stage C service parity: a canonical event cannot itself be
// confirmed as a duplicate (B-CYC-01 chain direction). The failed
// call leaves no disposition and no audit residue.
func TestDispositionCanonicalBecomesDuplicateRejected(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DC")
	importCSV(t, pool, emp, spanCSV(no, "dc-1", "dc-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "dc-3", "dc-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "dc-5", "dc-6",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	origIn := eventID(t, pool, emp, "dc-1")
	dupIn := eventID(t, pool, emp, "dc-3")
	lateIn := eventID(t, pool, emp, "dc-5")
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: dupIn, CanonicalID: lateIn, Reason: "dup delivery",
	}, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("first edge: %v", err)
	}
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: lateIn, CanonicalID: origIn, Reason: "chain attempt",
	}, "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatal("expected the canonical-becomes-duplicate edge to fail")
	}
	var edges int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event_disposition WHERE employer_id = $1`,
		emp).Scan(&edges); err != nil || edges != 1 {
		t.Fatalf("edges=%d err=%v want only the first", edges, err)
	}
	var dupRows int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event_disposition WHERE event_id = $1::uuid`,
		lateIn).Scan(&dupRows); err != nil || dupRows != 0 {
		t.Fatalf("dupRows=%d err=%v want the canonical still usable", dupRows, err)
	}
	var audits int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'clock_event.disposition'`,
		emp).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v want only the committed decision", audits, err)
	}
}
