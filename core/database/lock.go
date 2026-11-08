package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
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
