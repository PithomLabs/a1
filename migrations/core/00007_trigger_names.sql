-- WorkforceOps Lab baseline, part 7: trigger names match the design spec.
--
-- database-design.md §3 names five logical triggers; the part-4 migration
-- created them under working names. Trigger names share a namespace only
-- with their own table, so the three timesheet triggers and the two
-- payroll-run triggers take their shared spec names. Bodies (part 6) and
-- bindings are unchanged.

-- +goose Up

ALTER TRIGGER timesheet_freeze ON timesheet RENAME TO trg_timesheet_frozen;
ALTER TRIGGER timesheet_line_freeze ON timesheet_line RENAME TO trg_timesheet_frozen;
ALTER TRIGGER timesheet_line_interval_freeze ON timesheet_line_interval RENAME TO trg_timesheet_frozen;
ALTER TRIGGER roster_assignment_draft_only ON roster_assignment RENAME TO trg_roster_published_frozen;
ALTER TRIGGER payroll_run_freeze ON payroll_run RENAME TO trg_payroll_run_locked;
ALTER TRIGGER payroll_line_freeze ON payroll_line RENAME TO trg_payroll_run_locked;
ALTER TRIGGER payroll_run_exclusion_draft_only ON payroll_run_exclusion RENAME TO trg_payroll_exclusion_locked;
ALTER TRIGGER leave_request_freeze ON leave_request RENAME TO trg_leave_decided_frozen;

-- +goose Down

ALTER TRIGGER trg_timesheet_frozen ON timesheet RENAME TO timesheet_freeze;
ALTER TRIGGER trg_timesheet_frozen ON timesheet_line RENAME TO timesheet_line_freeze;
ALTER TRIGGER trg_timesheet_frozen ON timesheet_line_interval RENAME TO timesheet_line_interval_freeze;
ALTER TRIGGER trg_roster_published_frozen ON roster_assignment RENAME TO roster_assignment_draft_only;
ALTER TRIGGER trg_payroll_run_locked ON payroll_run RENAME TO payroll_run_freeze;
ALTER TRIGGER trg_payroll_run_locked ON payroll_line RENAME TO payroll_line_freeze;
ALTER TRIGGER trg_payroll_exclusion_locked ON payroll_run_exclusion RENAME TO payroll_run_exclusion_draft_only;
ALTER TRIGGER trg_leave_decided_frozen ON leave_request RENAME TO leave_request_freeze;
