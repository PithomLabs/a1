package integrity

import (
	"testing"
	"time"
)

// Stage C disposition concurrency (§14B): the canonical-event policy
// under sequential and concurrent direct-SQL writers, all as the app
// role with persisted-state re-reads. A canonical event may collect
// many duplicates, but edges never chain (B-CYC-01): a duplicate
// cannot become canonical, and a canonical cannot become a
// duplicate.

func mkClockEvent(t *testing.T, employer, employee, sourceID string) string {
	t.Helper()
	return queryID(t, appPool,
		`INSERT INTO clock_event (id, employer_id, employee_id, occurred_at, timezone, kind, source, source_event_id)
		 VALUES (gen_random_uuid(), $1, $2, '2026-08-03T09:00+08', 'Asia/Singapore', 'in', 'vendor-x', $3)
		 RETURNING id`,
		employer, employee, sourceID)
}

func tryDisposition(t *testing.T, employer, dup, canon string) error {
	t.Helper()
	_, err := appPool.Exec(ctx(t),
		`INSERT INTO clock_event_disposition
		   (employer_id, event_id, disposition, duplicate_of_event_id, reason, decided_by)
		 VALUES ($1, $2::uuid, 'duplicate', $3::uuid, 'matrix', 'op-1')`,
		employer, dup, canon)
	return err
}

func dispositionCount(t *testing.T, employer string) int {
	t.Helper()
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event_disposition WHERE employer_id = $1`, employer).Scan(&n)
	expectOK(t, err, "count dispositions")
	return n
}

func cycleCount(t *testing.T, employer string) int {
	t.Helper()
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM clock_event_disposition a
		 JOIN clock_event_disposition b ON b.event_id = a.duplicate_of_event_id
		  AND b.duplicate_of_event_id = a.event_id
		 WHERE a.employer_id = $1`, employer).Scan(&n)
	expectOK(t, err, "count 2-cycles")
	return n
}

// One canonical event collects many duplicates.
func TestDispositionMultiDuplicatePositive(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	c := mkClockEvent(t, a, emp, key(t, "c"))
	d1 := mkClockEvent(t, a, emp, key(t, "d1"))
	d2 := mkClockEvent(t, a, emp, key(t, "d2"))
	expectOK(t, tryDisposition(t, a, d1, c), "first duplicate")
	expectOK(t, tryDisposition(t, a, d2, c), "second duplicate")
	if n := dispositionCount(t, a); n != 2 {
		t.Fatalf("rows=%d want both duplicates", n)
	}
}

// An event already classified as a duplicate cannot become the
// canonical event of another disposition.
func TestDispositionDuplicateBecomesCanonicalRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	b := mkClockEvent(t, a, emp, key(t, "b"))
	ev := mkClockEvent(t, a, emp, key(t, "a"))
	c := mkClockEvent(t, a, emp, key(t, "c"))
	expectOK(t, tryDisposition(t, a, ev, b), "A dup of B")
	err := tryDisposition(t, a, c, ev)
	expectSQLError(t, err, "P0001", "itself dispositioned", "duplicate as canonical")
	if n := dispositionCount(t, a); n != 1 {
		t.Fatalf("rows=%d want only the first edge", n)
	}
}

// A canonical event cannot itself be classified as a duplicate:
// chains are prohibited in both directions.
func TestDispositionCanonicalBecomesDuplicateRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	b := mkClockEvent(t, a, emp, key(t, "b"))
	ev := mkClockEvent(t, a, emp, key(t, "a"))
	c := mkClockEvent(t, a, emp, key(t, "c"))
	expectOK(t, tryDisposition(t, a, ev, b), "A dup of B")
	err := tryDisposition(t, a, b, c)
	expectSQLError(t, err, "P0001", "already canonical", "canonical as duplicate")
	if n := dispositionCount(t, a); n != 1 {
		t.Fatalf("rows=%d want only the first edge", n)
	}
}

// A sequential A→B then B→A is a 2-cycle: the second edge fails.
func TestDispositionSequentialCycleRejected(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	ev := mkClockEvent(t, a, emp, key(t, "a"))
	b := mkClockEvent(t, a, emp, key(t, "b"))
	expectOK(t, tryDisposition(t, a, ev, b), "A dup of B")
	err := tryDisposition(t, a, b, ev)
	expectSQLError(t, err, "P0001", "itself dispositioned", "closing the cycle")
	if n := cycleCount(t, a); n != 0 {
		t.Fatalf("cycles=%d want none", n)
	}
}

// Concurrent reversed pair: at most one edge commits, in either
// start order. The winner's canonical stays usable, both raw events
// survive untouched, and the loser leaves no residue.
func TestDispositionConcurrentReversePair(t *testing.T) {
	for _, order := range []struct{ name, first, second string }{
		{"aFirst", "a", "b"},
		{"bFirst", "b", "a"},
	} {
		t.Run(order.name, func(t *testing.T) {
			a := mkEmployer(t, key(t, order.name), "UEN-"+key(t, order.name))
			emp := mkEmployee(t, a, "E001")
			events := map[string]string{
				"a": mkClockEvent(t, a, emp, key(t, order.name, "a")),
				"b": mkClockEvent(t, a, emp, key(t, order.name, "b")),
			}
			firstDup, firstCanon := events[order.first], events[order.second]
			c1, err := appPool.Acquire(ctx(t))
			expectOK(t, err, "acquire conn 1")
			defer c1.Release()
			c2, err := appPool.Acquire(ctx(t))
			expectOK(t, err, "acquire conn 2")
			defer c2.Release()
			tx1, err := c1.Begin(ctx(t))
			expectOK(t, err, "begin tx1")
			_, err = tx1.Exec(ctx(t),
				`INSERT INTO clock_event_disposition
				   (employer_id, event_id, disposition, duplicate_of_event_id, reason, decided_by)
				 VALUES ($1, $2::uuid, 'duplicate', $3::uuid, 'matrix', 'op-1')`,
				a, firstDup, firstCanon)
			expectOK(t, err, "tx1 edge holds the pair lock")
			done := make(chan error, 1)
			go func() {
				tx2, err := c2.Begin(ctx(t))
				if err != nil {
					done <- err
					return
				}
				defer tx2.Rollback(ctx(t))
				_, err = tx2.Exec(ctx(t),
					`INSERT INTO clock_event_disposition
					   (employer_id, event_id, disposition, duplicate_of_event_id, reason, decided_by)
					 VALUES ($1, $2::uuid, 'duplicate', $3::uuid, 'matrix', 'op-1')`,
					a, firstCanon, firstDup)
				if err == nil {
					err = tx2.Commit(ctx(t))
				}
				done <- err
			}()
			time.Sleep(300 * time.Millisecond)
			expectOK(t, tx1.Commit(ctx(t)), "commit tx1")
			select {
			case err := <-done:
				expectSQLError(t, err, "P0001", "dispositioned", "reversed loser")
			case <-time.After(15 * time.Second):
				t.Fatal("loser never returned after the winner committed")
			}
			if n := dispositionCount(t, a); n != 1 {
				t.Fatalf("rows=%d want exactly the winner", n)
			}
			if n := cycleCount(t, a); n != 0 {
				t.Fatalf("cycles=%d want none", n)
			}
			// The winner's canonical is not itself a duplicate:
			// the canonical pair stays usable.
			var canonDup bool
			err = appPool.QueryRow(ctx(t),
				`SELECT EXISTS (SELECT 1 FROM clock_event_disposition WHERE event_id = $1::uuid)`,
				firstCanon).Scan(&canonDup)
			expectOK(t, err, "canonical usability")
			if canonDup {
				t.Fatal("winner's canonical is dispositioned as a duplicate")
			}
			// Both raw events survive with their evidence intact.
			for _, id := range []string{firstDup, firstCanon} {
				var kind, at string
				err = appPool.QueryRow(ctx(t),
					`SELECT kind, occurred_at::text FROM clock_event WHERE id = $1::uuid`,
					id).Scan(&kind, &at)
				expectOK(t, err, "raw event "+id)
				if kind != "in" {
					t.Fatalf("kind=%s want the preserved event", kind)
				}
			}
		})
	}
}

// Disposition never rewrites raw evidence: both endpoints re-read
// byte-identical after the decision.
func TestDispositionEvidencePreserved(t *testing.T) {
	a := mkEmployer(t, key(t), "UEN-"+key(t))
	emp := mkEmployee(t, a, "E001")
	ev := mkClockEvent(t, a, emp, key(t, "a"))
	b := mkClockEvent(t, a, emp, key(t, "b"))
	type raw struct{ ee, at, tz, kind, src, sid string }
	read := func(id string) raw {
		var r raw
		err := appPool.QueryRow(ctx(t),
			`SELECT employee_id::text, occurred_at::text, timezone, kind, source, source_event_id
			 FROM clock_event WHERE id = $1::uuid`, id).
			Scan(&r.ee, &r.at, &r.tz, &r.kind, &r.src, &r.sid)
		expectOK(t, err, "read raw event")
		return r
	}
	beforeA, beforeB := read(ev), read(b)
	expectOK(t, tryDisposition(t, a, ev, b), "A dup of B")
	if got := read(ev); got != beforeA {
		t.Fatalf("dup endpoint changed: %+v was %+v", got, beforeA)
	}
	if got := read(b); got != beforeB {
		t.Fatalf("canonical endpoint changed: %+v was %+v", got, beforeB)
	}
}
