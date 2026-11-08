-- WorkforceOps Lab baseline, part 5: server-generated row ids.
--
-- The foundation migrations declared bare `id uuid NOT NULL` columns;
-- inserts must not have to mint identifiers. Forward-only fix (no edit
-- of applied migrations): every row id defaults to gen_random_uuid().

-- +goose Up

ALTER TABLE employee ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE employment_terms ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE compensation_line ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE ket_record ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE leave_request ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE client ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE programme ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE team ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE team_membership ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE employee_skill ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE shift_template ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE shift_segment ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE rotation_pattern ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE rotation_assignment ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE roster ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE roster_revision ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE roster_assignment ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE clock_event ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE attendance_interval ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE attendance_record ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE attendance_correction ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE pay_period ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE timesheet ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE timesheet_line ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE shg_instruction ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE payroll_run ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE payroll_run_exclusion ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE payroll_line ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE payslip ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE statutory_summary ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE payroll_correction ALTER COLUMN id SET DEFAULT gen_random_uuid();

-- +goose Down

ALTER TABLE payroll_correction ALTER COLUMN id DROP DEFAULT;
ALTER TABLE statutory_summary ALTER COLUMN id DROP DEFAULT;
ALTER TABLE payslip ALTER COLUMN id DROP DEFAULT;
ALTER TABLE payroll_line ALTER COLUMN id DROP DEFAULT;
ALTER TABLE payroll_run_exclusion ALTER COLUMN id DROP DEFAULT;
ALTER TABLE payroll_run ALTER COLUMN id DROP DEFAULT;
ALTER TABLE shg_instruction ALTER COLUMN id DROP DEFAULT;
ALTER TABLE timesheet_line ALTER COLUMN id DROP DEFAULT;
ALTER TABLE timesheet ALTER COLUMN id DROP DEFAULT;
ALTER TABLE pay_period ALTER COLUMN id DROP DEFAULT;
ALTER TABLE attendance_correction ALTER COLUMN id DROP DEFAULT;
ALTER TABLE attendance_record ALTER COLUMN id DROP DEFAULT;
ALTER TABLE attendance_interval ALTER COLUMN id DROP DEFAULT;
ALTER TABLE clock_event ALTER COLUMN id DROP DEFAULT;
ALTER TABLE roster_assignment ALTER COLUMN id DROP DEFAULT;
ALTER TABLE roster_revision ALTER COLUMN id DROP DEFAULT;
ALTER TABLE roster ALTER COLUMN id DROP DEFAULT;
ALTER TABLE rotation_assignment ALTER COLUMN id DROP DEFAULT;
ALTER TABLE rotation_pattern ALTER COLUMN id DROP DEFAULT;
ALTER TABLE shift_segment ALTER COLUMN id DROP DEFAULT;
ALTER TABLE shift_template ALTER COLUMN id DROP DEFAULT;
ALTER TABLE employee_skill ALTER COLUMN id DROP DEFAULT;
ALTER TABLE team_membership ALTER COLUMN id DROP DEFAULT;
ALTER TABLE team ALTER COLUMN id DROP DEFAULT;
ALTER TABLE programme ALTER COLUMN id DROP DEFAULT;
ALTER TABLE client ALTER COLUMN id DROP DEFAULT;
ALTER TABLE leave_request ALTER COLUMN id DROP DEFAULT;
ALTER TABLE ket_record ALTER COLUMN id DROP DEFAULT;
ALTER TABLE compensation_line ALTER COLUMN id DROP DEFAULT;
ALTER TABLE employment_terms ALTER COLUMN id DROP DEFAULT;
ALTER TABLE employee ALTER COLUMN id DROP DEFAULT;
