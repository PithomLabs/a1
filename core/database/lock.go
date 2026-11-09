package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LockPayrollInput serializes payroll-input mutations per employee.
//
// Convention (all operations must match): transaction-scoped advisory
// lock, key `pay-input:<employer-uuid>:<employee-uuid>`, acquired in
// UUID-sorted order whenever several employees lock in one
// transaction. The key is calculated exactly once, inside the
// acquire_payroll_input_lock SQL helper this calls: Go services,
// triggers and controlled functions must never compute their own
// key. Collisions only serialize more; correctness never depends on
// uniqueness.
//
// Barrier members (Amendment A): compensation writes, employment-term
// changes, status transitions, SHG instructions, clock-event imports,
// late-break attachment, dispositions, attendance corrections, and
// timesheet transitions. Whoever holds the lock commits before the
// waiter proceeds, so an input mutation and the approval freezing
// its interpretation cannot cross unnoticed; pay_basis_seq versions
// the resulting commit order per employee.
func LockPayrollInput(ctx context.Context, tx pgx.Tx, employerID, employeeID string) error {
	_, err := tx.Exec(ctx, `SELECT acquire_payroll_input_lock($1::uuid, $2::uuid)`, employerID, employeeID)
	if err != nil {
		return fmt.Errorf("lock payroll input: %w", err)
	}
	return nil
}

// Global lock order (M2 snapshot capture widens the surface):
// population → employee (UUID-sorted) → rule-pack key. Every holder
// acquires in this order and never inverts it: population writers
// take the population lock first, capture takes population, then
// employees sorted, then packs. Advisory xact locks release at
// commit; re-taking a held key in the same transaction is a no-op.

// LockPayrollPopulation serializes expected-population changes per
// employer. Capture holds it for the whole snapshot transaction;
// employee creation, term changes and status transitions take it
// first, before the employee lock.
func LockPayrollPopulation(ctx context.Context, tx pgx.Tx, employerID string) error {
	_, err := tx.Exec(ctx, `SELECT acquire_payroll_population_lock($1::uuid)`, employerID)
	if err != nil {
		return fmt.Errorf("lock payroll population: %w", err)
	}
	return nil
}

// LockRulePack serializes activation of one employer pack. Capture
// pins required packs through this in sorted order; the activation
// function takes the same key.
func LockRulePack(ctx context.Context, tx pgx.Tx, employerID, pack string) error {
	_, err := tx.Exec(ctx, `SELECT acquire_rule_pack_lock($1::uuid, $2)`, employerID, pack)
	if err != nil {
		return fmt.Errorf("lock rule pack: %w", err)
	}
	return nil
}

// BeginSnapshotTx opens the capture transaction at REPEATABLE READ so
// every resolution query sees one committed view. Advisory locks
// alone cannot give that: under READ COMMITTED each statement would
// see a newer prefix. The snapshot boundary is the transaction's
// first read; the population, employee and pack locks then freeze
// every writer that could commit past it, so the captured set is one
// coherent committed prefix.
//
// No serialization-retry loop is needed: every statement that could
// conflict with a concurrent writer is covered by an advisory lock
// both sides take, and the snapshot rows themselves are fresh keys
// (the one-valid partial unique turns a duplicate capture into a
// plain 23505, not a 40001). A REPEATABLE READ transaction whose
// entire read/write footprint is advisory-serialized cannot hit a
// serialization failure.
func BeginSnapshotTx(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin snapshot transaction: %w", err)
	}
	return tx, nil
}
