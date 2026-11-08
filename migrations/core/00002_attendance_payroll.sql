-- WorkforceOps Lab baseline, part 2: attendance, timesheets, pay structure.
--
-- Lineage checks that are expressible in SQL live here as CHECK
-- constraints (exactly-one-source on intervals, mandatory keys on payroll
-- lineage). Temporal freezing lives in 00004_protection.sql.

-- +goose Up

CREATE TABLE attendance_interval (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    roster_assignment_id uuid,
    interval_start TIMESTAMPTZ NOT NULL,
    interval_end TIMESTAMPTZ NOT NULL CHECK (interval_end > interval_start),
    timezone TEXT NOT NULL,
    local_date DATE NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('work', 'break_paid', 'break_unpaid')),
    source TEXT NOT NULL CHECK (source IN ('clock', 'correction', 'assumption')),
    source_refs JSONB NOT NULL,
    shift_identity uuid,
    supersedes_id uuid,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    FOREIGN KEY (employer_id, employee_id, roster_assignment_id)
        REFERENCES roster_assignment (employer_id, employee_id, id),
    FOREIGN KEY (employer_id, supersedes_id) REFERENCES attendance_interval (employer_id, id),
    CHECK (
        (CASE WHEN jsonb_typeof(source_refs -> 'clock_event_ids') = 'array'
                   AND jsonb_array_length(source_refs -> 'clock_event_ids') > 0
              THEN 1 ELSE 0 END)
        + (CASE WHEN source_refs ->> 'correction_id' IS NOT NULL
              THEN 1 ELSE 0 END)
        + (CASE WHEN source_refs ? 'assumption' THEN 1 ELSE 0 END) = 1
    )
);

CREATE TABLE attendance_record (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    business_date DATE NOT NULL,
    roster_assignment_id uuid,
    scheduled_min NUMERIC(8, 2) CHECK (scheduled_min IS NULL OR scheduled_min >= 0),
    recorded_min NUMERIC(8, 2) CHECK (recorded_min IS NULL OR recorded_min >= 0),
    actual_min NUMERIC(8, 2) CHECK (actual_min IS NULL OR actual_min >= 0),
    paid_min NUMERIC(8, 2) CHECK (paid_min IS NULL OR paid_min >= 0),
    status TEXT NOT NULL DEFAULT 'reviewed',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, employee_id, business_date),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    FOREIGN KEY (employer_id, employee_id, roster_assignment_id)
        REFERENCES roster_assignment (employer_id, employee_id, id)
);

CREATE TABLE attendance_correction (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    attendance_id uuid NOT NULL,
    field TEXT NOT NULL,
    old_value TEXT,
    new_value TEXT NOT NULL,
    reason TEXT NOT NULL,
    corrected_by TEXT NOT NULL,
    corrected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, attendance_id) REFERENCES attendance_record (employer_id, id)
);

CREATE TABLE pay_code (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('earning', 'leave', 'info')),
    default_treatment TEXT NOT NULL CHECK (default_treatment IN ('OW', 'AW', 'NON_CPF_WAGE'))
);

CREATE TABLE holiday_calendar (
    date DATE PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE pay_period (
    id uuid NOT NULL,
    employer_id uuid NOT NULL REFERENCES employer (id),
    code TEXT NOT NULL,
    starts_on DATE NOT NULL,
    ends_on DATE NOT NULL CHECK (ends_on >= starts_on),
    pay_date DATE NOT NULL,
    salary_deadline DATE NOT NULL,
    ot_deadline DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'processing', 'approved', 'locked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, code)
);

CREATE TABLE timesheet (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    pay_period_id uuid NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'submitted', 'approved', 'rejected')),
    submitted_at TIMESTAMPTZ,
    approved_by TEXT,
    approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, employee_id, pay_period_id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    FOREIGN KEY (employer_id, pay_period_id) REFERENCES pay_period (employer_id, id)
);

CREATE TABLE timesheet_line (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    timesheet_id uuid NOT NULL,
    pay_code TEXT NOT NULL REFERENCES pay_code (code),
    hours NUMERIC(10, 2) NOT NULL CHECK (hours >= 0),
    pay_period_id uuid NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, timesheet_id) REFERENCES timesheet (employer_id, id),
    FOREIGN KEY (employer_id, pay_period_id) REFERENCES pay_period (employer_id, id)
);

CREATE TABLE timesheet_line_interval (
    employer_id uuid NOT NULL,
    line_id uuid NOT NULL,
    interval_id uuid NOT NULL,
    PRIMARY KEY (employer_id, line_id, interval_id),
    FOREIGN KEY (employer_id, line_id) REFERENCES timesheet_line (employer_id, id),
    FOREIGN KEY (employer_id, interval_id) REFERENCES attendance_interval (employer_id, id)
);

CREATE TABLE shg_instruction (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    fund TEXT NOT NULL CHECK (fund IN ('CDAC', 'MBMF', 'SINDA', 'ECF')),
    status TEXT NOT NULL CHECK (status IN ('active', 'opt_out', 'verified_none')),
    amount_or_basis TEXT,
    source_declaration TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    verified_by TEXT,
    verification_basis TEXT,
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    CHECK (
        status <> 'verified_none'
        OR (verification_basis IS NOT NULL AND verified_at IS NOT NULL)
    )
);

CREATE TABLE payroll_run (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    pay_period_id uuid NOT NULL,
    kind TEXT NOT NULL DEFAULT 'regular' CHECK (kind IN ('regular', 'correction')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'validated', 'approved')),
    inputs_hash TEXT NOT NULL,
    rule_versions JSONB NOT NULL,
    calc_version TEXT NOT NULL,
    rounding_conventions JSONB NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    approved_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, pay_period_id) REFERENCES pay_period (employer_id, id)
);

-- One approved regular run per period. Draft and validated runs stay
-- replaceable; correction runs are out of scope for now.
CREATE UNIQUE INDEX payroll_run_one_approved_regular
    ON payroll_run (employer_id, pay_period_id)
    WHERE kind = 'regular' AND status = 'approved';

CREATE TABLE payroll_run_exclusion (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    payroll_run_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    reason TEXT NOT NULL CHECK (char_length(reason) > 0),
    actor TEXT NOT NULL,
    excluded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, payroll_run_id, employee_id),
    FOREIGN KEY (employer_id, payroll_run_id) REFERENCES payroll_run (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    CHECK (reason IS NOT NULL AND actor IS NOT NULL)
);

CREATE TABLE payroll_line (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    run_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    component TEXT NOT NULL,
    pay_code TEXT NOT NULL REFERENCES pay_code (code),
    hours NUMERIC(10, 2),
    amount NUMERIC(12, 2) NOT NULL,
    cpf_treatment TEXT NOT NULL CHECK (cpf_treatment IN ('OW', 'AW', 'NON_CPF_WAGE')),
    work_period TEXT NOT NULL CHECK (work_period ~ '^\d{4}-(0[1-9]|1[0-2])$'),
    payable_date DATE NOT NULL,
    contribution_period TEXT NOT NULL CHECK (contribution_period ~ '^\d{4}-(0[1-9]|1[0-2])$'),
    source_refs JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, run_id, employee_id, component, pay_code),
    FOREIGN KEY (employer_id, run_id) REFERENCES payroll_run (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    CHECK (
        source_refs ? 'timesheet_line_ids'
        AND source_refs ? 'terms_id'
        AND source_refs ? 'rule_pack_versions'
        AND source_refs ? 'calculation'
    )
);

CREATE TABLE payslip (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    run_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    schema_version TEXT NOT NULL DEFAULT 'payslip/v0.1',
    payload JSONB NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    issued_at TIMESTAMPTZ,
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, run_id, employee_id),
    FOREIGN KEY (employer_id, run_id) REFERENCES payroll_run (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

CREATE TABLE statutory_summary (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    run_id uuid NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('cpf', 'sdl', 'shg')),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, run_id, kind),
    FOREIGN KEY (employer_id, run_id) REFERENCES payroll_run (employer_id, id)
);

CREATE TABLE payroll_correction (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    run_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    reason TEXT NOT NULL,
    adjustment JSONB NOT NULL,
    created_by TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'requested',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, run_id) REFERENCES payroll_run (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

-- +goose Down

DROP TABLE IF EXISTS payroll_correction;
DROP TABLE IF EXISTS statutory_summary;
DROP TABLE IF EXISTS payslip;
DROP TABLE IF EXISTS payroll_line;
DROP TABLE IF EXISTS payroll_run_exclusion;
DROP INDEX IF EXISTS payroll_run_one_approved_regular;
DROP TABLE IF EXISTS payroll_run;
DROP TABLE IF EXISTS shg_instruction;
DROP TABLE IF EXISTS timesheet_line_interval;
DROP TABLE IF EXISTS timesheet_line;
DROP TABLE IF EXISTS timesheet;
DROP TABLE IF EXISTS pay_period;
DROP TABLE IF EXISTS holiday_calendar;
DROP TABLE IF EXISTS pay_code;
DROP TABLE IF EXISTS attendance_correction;
DROP TABLE IF EXISTS attendance_record;
DROP TABLE IF EXISTS attendance_interval;
