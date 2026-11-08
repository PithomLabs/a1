-- WorkforceOps Lab baseline, part 1: employer, employee, BPO, roster foundations.
--
-- Prerequisites (superuser bootstrap, see scripts/db/init-roles.sh and the
-- integrity-test setup): extensions pgcrypto and btree_gist installed, roles
-- migration_admin and app created. Roles are best-effort referenced in a
-- later migration with existence checks; extensions are required before this
-- file runs and failure to install them fails loudly here.
--
-- Conventions: single public schema; uuid PKs; per-employer uniqueness;
-- composite (employer_id, id) keys on every employer-owned parent so child
-- rows cannot cross employers; btree uniques here, gist exclusion
-- constraints in 00004_protection.sql.

-- +goose Up

CREATE TABLE employer (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    uen TEXT NOT NULL,
    address TEXT NOT NULL,
    jurisdiction TEXT NOT NULL DEFAULT 'SG',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (code),
    UNIQUE (jurisdiction, uen)
);

CREATE TABLE employee (
    id uuid NOT NULL,
    employer_id uuid NOT NULL REFERENCES employer (id),
    employee_no TEXT NOT NULL,
    legal_name TEXT NOT NULL,
    address TEXT NOT NULL,
    gender TEXT NOT NULL,
    date_of_birth DATE NOT NULL,
    citizenship TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'resigned', 'terminated')),
    status_effective_date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, employee_no)
);

CREATE TABLE employee_status_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'resigned', 'terminated')),
    effective_from DATE NOT NULL,
    effective_to DATE,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

CREATE TABLE employment_terms (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    effective_from DATE NOT NULL,
    effective_to DATE,
    job_title TEXT NOT NULL,
    job_duties TEXT NOT NULL,
    employment_category TEXT NOT NULL CHECK (employment_category IN ('workman', 'non_workman', 'manager_executive')),
    part_iv_eligible BOOLEAN NOT NULL,
    eligibility_basis TEXT NOT NULL,
    monthly_basic NUMERIC(12, 2) NOT NULL CHECK (monthly_basic >= 0),
    working_arrangement TEXT NOT NULL,
    normal_hours_weekly NUMERIC(6, 2) NOT NULL CHECK (normal_hours_weekly >= 0),
    working_days TEXT NOT NULL,
    daily_hours NUMERIC(5, 2) NOT NULL CHECK (daily_hours >= 0),
    rest_day TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE,
    contract_type TEXT NOT NULL,
    fixed_term_duration TEXT,
    place_of_work TEXT NOT NULL,
    salary_period TEXT NOT NULL,
    overtime_period TEXT,
    notice_period TEXT,
    probation TEXT,
    medical_benefits TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

CREATE TABLE compensation_line (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    terms_id uuid NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('allowance', 'deduction')),
    code TEXT NOT NULL,
    amount_monthly NUMERIC(12, 2) NOT NULL CHECK (amount_monthly >= 0),
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, terms_id) REFERENCES employment_terms (employer_id, id)
);

CREATE TABLE ket_record (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    terms_id uuid NOT NULL,
    schema_version TEXT NOT NULL DEFAULT 'ket/v0.1',
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload JSONB NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, terms_id) REFERENCES employment_terms (employer_id, id)
);

CREATE TABLE leave_type (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE leave_request (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    type TEXT NOT NULL,
    from_date DATE NOT NULL,
    to_date DATE NOT NULL CHECK (to_date >= from_date),
    days NUMERIC(6, 2) NOT NULL CHECK (days > 0),
    status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'rejected')),
    reason TEXT,
    approved_by TEXT,
    approved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

CREATE TABLE client (
    id uuid NOT NULL,
    employer_id uuid NOT NULL REFERENCES employer (id),
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, code)
);

CREATE TABLE programme (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    client_id uuid NOT NULL,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    service_window JSONB,
    timezone TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, code),
    FOREIGN KEY (employer_id, client_id) REFERENCES client (employer_id, id)
);

CREATE TABLE skill (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('language', 'product', 'process'))
);

CREATE TABLE programme_required_skill (
    employer_id uuid NOT NULL,
    programme_id uuid NOT NULL,
    skill_id uuid NOT NULL REFERENCES skill (id),
    minimum_level INT NOT NULL CHECK (minimum_level > 0),
    PRIMARY KEY (employer_id, programme_id, skill_id),
    FOREIGN KEY (employer_id, programme_id) REFERENCES programme (employer_id, id)
);

CREATE TABLE programme_staffing_requirement (
    employer_id uuid NOT NULL,
    programme_id uuid NOT NULL,
    shift_code TEXT NOT NULL,
    weekday INT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    min_headcount INT NOT NULL CHECK (min_headcount >= 0),
    PRIMARY KEY (employer_id, programme_id, shift_code, weekday),
    FOREIGN KEY (employer_id, programme_id) REFERENCES programme (employer_id, id)
);

CREATE TABLE team (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    programme_id uuid NOT NULL,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, code),
    FOREIGN KEY (employer_id, programme_id) REFERENCES programme (employer_id, id)
);

CREATE TABLE team_membership (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    team_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('agent', 'lead', 'supervisor')),
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, team_id) REFERENCES team (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

CREATE TABLE employee_skill (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    skill_id uuid NOT NULL REFERENCES skill (id),
    level INT NOT NULL CHECK (level > 0),
    valid_from DATE NOT NULL,
    valid_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

CREATE TABLE shift_template (
    id uuid NOT NULL,
    employer_id uuid NOT NULL REFERENCES employer (id),
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    start_local TIME NOT NULL,
    end_local TIME NOT NULL,
    cross_midnight BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, code)
);

CREATE TABLE shift_segment (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    template_id uuid NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('work', 'break_paid', 'break_unpaid')),
    start_offset_min INT NOT NULL CHECK (start_offset_min >= 0),
    duration_min INT NOT NULL CHECK (duration_min > 0),
    paid BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, template_id) REFERENCES shift_template (employer_id, id)
);

CREATE TABLE rotation_pattern (
    id uuid NOT NULL,
    employer_id uuid NOT NULL REFERENCES employer (id),
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    cycle_days INT NOT NULL CHECK (cycle_days > 0),
    pattern JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, code)
);

CREATE TABLE rotation_assignment (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    pattern_id uuid NOT NULL,
    employee_id uuid,
    team_id uuid,
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, pattern_id) REFERENCES rotation_pattern (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    FOREIGN KEY (employer_id, team_id) REFERENCES team (employer_id, id),
    CHECK (
        (employee_id IS NULL AND team_id IS NOT NULL)
        OR (employee_id IS NOT NULL AND team_id IS NULL)
    )
);

CREATE TABLE roster (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    programme_id uuid NOT NULL,
    week_starting DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    legal_validation_status TEXT NOT NULL DEFAULT 'unavailable' CHECK (legal_validation_status IN ('verified', 'unavailable')),
    legal_validation_pack_version TEXT,
    legal_validation_checked_at TIMESTAMPTZ,
    legal_validation_result TEXT,
    current_version INT NOT NULL DEFAULT 1 CHECK (current_version > 0),
    published_at TIMESTAMPTZ,
    published_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, programme_id, week_starting),
    FOREIGN KEY (employer_id, programme_id) REFERENCES programme (employer_id, id)
);

CREATE TABLE roster_revision (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    version INT NOT NULL CHECK (version > 0),
    published_at TIMESTAMPTZ,
    published_by TEXT,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, roster_id, version),
    FOREIGN KEY (employer_id, roster_id) REFERENCES roster (employer_id, id)
);

CREATE TABLE roster_assignment (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    business_date DATE NOT NULL,
    template_id uuid,
    rest_day BOOLEAN NOT NULL,
    status TEXT NOT NULL DEFAULT 'scheduled',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, employee_id, id),
    UNIQUE (employer_id, revision_id, employee_id, business_date),
    FOREIGN KEY (employer_id, revision_id) REFERENCES roster_revision (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id),
    FOREIGN KEY (employer_id, template_id) REFERENCES shift_template (employer_id, id),
    CHECK (
        (template_id IS NULL AND rest_day)
        OR (template_id IS NOT NULL AND NOT rest_day)
    )
);

CREATE TABLE clock_event (
    id uuid NOT NULL,
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    timezone TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('in', 'out')),
    source TEXT NOT NULL,
    source_event_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (employer_id, id),
    UNIQUE (employer_id, source, source_event_id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

-- +goose Down

DROP TABLE IF EXISTS clock_event;
DROP TABLE IF EXISTS roster_assignment;
DROP TABLE IF EXISTS roster_revision;
DROP TABLE IF EXISTS roster;
DROP TABLE IF EXISTS rotation_assignment;
DROP TABLE IF EXISTS rotation_pattern;
DROP TABLE IF EXISTS shift_segment;
DROP TABLE IF EXISTS shift_template;
DROP TABLE IF EXISTS employee_skill;
DROP TABLE IF EXISTS team_membership;
DROP TABLE IF EXISTS team;
DROP TABLE IF EXISTS programme_staffing_requirement;
DROP TABLE IF EXISTS programme_required_skill;
DROP TABLE IF EXISTS skill;
DROP TABLE IF EXISTS programme;
DROP TABLE IF EXISTS client;
DROP TABLE IF EXISTS leave_request;
DROP TABLE IF EXISTS leave_type;
DROP TABLE IF EXISTS ket_record;
DROP TABLE IF EXISTS compensation_line;
DROP TABLE IF EXISTS employment_terms;
DROP TABLE IF EXISTS employee_status_history;
DROP TABLE IF EXISTS employee;
DROP TABLE IF EXISTS employer;
