// EXPERIMENTAL — Gate 1 candidate for the M2.1 D1 admission boundary.
//
// This file implements the session-barrier + XID-ticket admission
// protocol behind review6.1 §A–D. It is NOT wired into
// CaptureSnapshot or ValidateRun: full capture/validation
// implementation is explicitly unauthorized until Gate 1 approves
// the mechanism. The candidate is proven by
// core/database/admission_test.go against experimental SQL that
// lives in the test file, never in migrations/.
//
// Protocol (one dedicated physical connection per admission):
//
//  1. Acquire a dedicated connection from the pool.
//  2. Take the employer population session barrier (wrapper-only).
//  3. Enumerate the expected lock set (READ COMMITTED, post-barrier).
//  4. Take employee + pack session barriers in canonical order.
//  5. Issue the admission ticket (separate committed RC operation).
//  6. BEGIN REPEATABLE READ on the same connection.
//  7. Verify the ticket inside the RR snapshot (visibility, pid,
//     employer, expiry, single-use, holds still present).
//  8. Re-derive the lock set inside RR; mismatch aborts (bounded
//     retry), because a barrier-nonmember writer may have committed
//     between enumeration and the snapshot.
//  9. Run caller work; commit or roll back.
//  10. Release session barriers in reverse order; release the
//     connection, or discard it on any uncertainty.
//
// Session locks and transaction locks conflict in the same advisory
// namespace, so readers (session scope) and writers (transaction
// scope, same keys) mutually exclude. Ticket freshness is proven by
// XID visibility (pg_visible_in_snapshot), never by xmin
// comparison and never by pg_locks inspection: granted session and
// transaction locks are byte-identical in pg_locks on PG16.
package database

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxAdmissionAttempts bounds the enumerate→ticket→revalidate retry
// loop. A persistent mismatch means a barrier-nonmember writer keeps
// committing between enumeration and the snapshot: after three
// attempts the caller gets the mismatch, not a silent admission.
const maxAdmissionAttempts = 3

// LockSet is the barrier set one admission holds: the employer
// population barrier (implicit) plus explicit employee and pack
// barriers. Do sorts and dedupes both lists into canonical order.
type LockSet struct {
	Employees []string
	Packs     []string
}

// Outcome reports how an admission bracket resolved. Exactly one of
// Released or Discarded is true whenever the bracket ran cleanup;
// Discarded means the connection was destroyed rather than returned
// to the pool because its session-lock state could not be proven
// clean.
type Outcome struct {
	TicketID  string
	Attempts  int
	Released  bool
	Discarded bool
}

// MismatchError reports a pre/post-BEGIN lock-set divergence: the
// set derived inside the RR snapshot differs from the set the
// ticket was issued for. Do retries these boundedly; the error
// surfaces only after exhaustion.
type MismatchError struct {
	Attempt int
	Detail  string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("admission lock set changed during admission (attempt %d): %s", e.Attempt, e.Detail)
}

// RowQuerier is satisfied by *pgxpool.Conn (READ COMMITTED
// enumeration) and pgx.Tx (in-snapshot revalidation).
type RowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Do runs work inside one admission bracket on a dedicated
// connection. enumerate derives the employee/pack barrier set; it
// runs once post-population-barrier (READ COMMITTED) and again
// inside the RR snapshot for the match check. work runs inside the
// verified RR transaction; a nil return commits, any error rolls
// back. Panics from enumerate or work propagate after cleanup.
func Do(ctx context.Context, pool *pgxpool.Pool, employerID string,
	enumerate func(context.Context, RowQuerier) (LockSet, error),
	work func(context.Context, pgx.Tx) error) (Outcome, error) {
	var out Outcome
	for attempt := 1; attempt <= maxAdmissionAttempts; attempt++ {
		out.Attempts = attempt
		res := runAttempt(ctx, pool, employerID, enumerate, work)
		out.TicketID = res.ticket
		out.Released, out.Discarded = res.released, res.discarded
		if res.err == nil {
			return out, nil
		}
		var mm *MismatchError
		if res.retry && errors.As(res.err, &mm) && attempt < maxAdmissionAttempts {
			continue
		}
		return out, res.err
	}
	return out, nil
}

// attemptResult carries one attempt's ticket, release state, retry
// flag and error. Exactly one of released/discarded is true whenever
// cleanup ran (all paths except acquire failure and propagated
// panics, where no outcome is returned at all).
type attemptResult struct {
	ticket    string
	released  bool
	discarded bool
	retry     bool
	err       error
}

// runAttempt runs one admission on a fresh dedicated connection.
func runAttempt(ctx context.Context, pool *pgxpool.Pool, employerID string,
	enumerate func(context.Context, RowQuerier) (LockSet, error),
	work func(context.Context, pgx.Tx) error) (res attemptResult) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		res.err = fmt.Errorf("acquire admission connection: %w", err)
		return res
	}
	// Cleanup contract: every path below funnels through finish,
	// which releases barriers in reverse order and either releases
	// a proven-clean connection or discards it. Panics propagate
	// after cleanup completes (no outcome is returned then; tests
	// assert the side effects).
	healthy := true
	var locked []barrierRef
	finish := func() {
		for i := len(locked) - 1; i >= 0; i-- {
			ok, uerr := unlockBarrier(ctx, conn.Conn(), locked[i])
			if uerr != nil || !ok {
				healthy = false
			}
		}
		if healthy && !conn.Conn().PgConn().IsClosed() && ctx.Err() == nil {
			conn.Release()
			res.released = true
		} else {
			// Destroy-then-release: close the underlying
			// connection first so no later acquirer can see
			// the tainted backend, then release the
			// resource so the pool drops (not reuses) it.
			// Releasing without the close would recycle
			// uncertainty; closing without the release
			// would leak the pool slot.
			_ = conn.Conn().Close(ctx)
			conn.Release()
			res.discarded = true
		}
	}
	var ticketID string
	run := func() (retry bool, rerr error) {
		defer func() {
			p := recover()
			finish()
			// Cleanup already ran: re-raise the original
			// panic, never swallow it.
			if p != nil {
				panic(p)
			}
		}()
		// Step 1: population session barrier.
		pop := barrierRef{kind: "population", employer: employerID}
		if err := lockBarrier(ctx, conn.Conn(), pop); err != nil {
			return false, err
		}
		locked = append(locked, pop)
		// Steps 2-4: enumerate post-barrier, then take the
		// employee and pack barriers in canonical order.
		pre, err := enumerate(ctx, conn.Conn())
		if err != nil {
			return false, fmt.Errorf("enumerate lock set: %w", err)
		}
		pre = canonicalLockSet(pre)
		for _, e := range pre.Employees {
			b := barrierRef{kind: "input", employer: employerID, employee: e}
			if err := lockBarrier(ctx, conn.Conn(), b); err != nil {
				return false, err
			}
			locked = append(locked, b)
		}
		for _, p := range pre.Packs {
			b := barrierRef{kind: "pack", employer: employerID, pack: p}
			if err := lockBarrier(ctx, conn.Conn(), b); err != nil {
				return false, err
			}
			locked = append(locked, b)
		}
		// Step 5: issue the ticket as a separate committed RC
		// operation on this same connection.
		ticketID, err = issueTicket(ctx, conn.Conn(), employerID, pre)
		if err != nil {
			return false, err
		}
		// Step 6-7: RR snapshot, then verify inside it.
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			return false, fmt.Errorf("begin admission snapshot: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback(ctx)
			}
		}()
		if err := verifyTicket(ctx, tx, ticketID, employerID); err != nil {
			_ = tx.Rollback(ctx)
			// Best effort outside the aborted snapshot; expiry
			// backstops a lost release.
			_ = releaseTicket(ctx, conn.Conn(), ticketID)
			return false, err
		}
		// Step 8: re-derive inside the snapshot; any divergence
		// aborts for a bounded retry.
		post, err := enumerate(ctx, tx)
		if err != nil {
			return false, fmt.Errorf("revalidate lock set: %w", err)
		}
		if d := diffLockSets(pre, canonicalLockSet(post)); d != "" {
			return true, &MismatchError{Detail: d}
		}
		// Step 9: caller work, then commit.
		if err := work(ctx, tx); err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit admission transaction: %w", err)
		}
		committed = true
		return false, nil
	}
	retry, err := run()
	// finish() ran inside run's defer and recorded the release
	// state on res.
	res.ticket = ticketID
	res.retry, res.err = retry, err
	return res
}

// barrierRef names one session barrier held by an admission.
type barrierRef struct {
	kind     string
	employer string
	employee string
	pack     string
}

func canonicalLockSet(s LockSet) LockSet {
	out := LockSet{
		Employees: append([]string(nil), s.Employees...),
		Packs:     append([]string(nil), s.Packs...),
	}
	sort.Strings(out.Employees)
	sort.Strings(out.Packs)
	out.Employees = dedupeStrings(out.Employees)
	out.Packs = dedupeStrings(out.Packs)
	return out
}

func dedupeStrings(in []string) []string {
	out := in[:0]
	var prev string
	for i, s := range in {
		if i == 0 || s != prev {
			out = append(out, s)
			prev = s
		}
	}
	return out
}

func diffLockSets(a, b LockSet) string {
	if len(a.Employees) != len(b.Employees) || len(a.Packs) != len(b.Packs) {
		return fmt.Sprintf("size %+v vs %+v", a, b)
	}
	for i := range a.Employees {
		if a.Employees[i] != b.Employees[i] {
			return fmt.Sprintf("employees %+v vs %+v", a.Employees, b.Employees)
		}
	}
	for i := range a.Packs {
		if a.Packs[i] != b.Packs[i] {
			return fmt.Sprintf("packs %+v vs %+v", a.Packs, b.Packs)
		}
	}
	return ""
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func lockBarrier(ctx context.Context, c *pgx.Conn, b barrierRef) error {
	if _, err := c.Exec(ctx, `SELECT exp_admission_lock($1, $2::uuid, $3::uuid, $4)`,
		b.kind, b.employer, nullStr(b.employee), nullStr(b.pack)); err != nil {
		return fmt.Errorf("take %s admission barrier: %w", b.kind, err)
	}
	return nil
}

func unlockBarrier(ctx context.Context, c *pgx.Conn, b barrierRef) (bool, error) {
	var ok bool
	if err := c.QueryRow(ctx, `SELECT exp_admission_unlock($1, $2::uuid, $3::uuid, $4)`,
		b.kind, b.employer, nullStr(b.employee), nullStr(b.pack)).Scan(&ok); err != nil {
		return false, fmt.Errorf("release %s admission barrier: %w", b.kind, err)
	}
	return ok, nil
}

func issueTicket(ctx context.Context, c *pgx.Conn, employerID string, set LockSet) (string, error) {
	var id string
	if err := c.QueryRow(ctx, `SELECT exp_issue_ticket($1::uuid, $2::uuid[], $3::text[])`,
		employerID, set.Employees, set.Packs).Scan(&id); err != nil {
		return "", fmt.Errorf("issue admission ticket: %w", err)
	}
	return id, nil
}

func verifyTicket(ctx context.Context, tx pgx.Tx, ticketID, employerID string) error {
	if _, err := tx.Exec(ctx, `SELECT exp_verify_ticket($1::uuid, $2::uuid)`, ticketID, employerID); err != nil {
		return fmt.Errorf("verify admission ticket: %w", err)
	}
	return nil
}

func releaseTicket(ctx context.Context, c *pgx.Conn, ticketID string) error {
	_, err := c.Exec(ctx, `SELECT exp_release_ticket($1::uuid)`, ticketID)
	return err
}
