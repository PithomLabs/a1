-- WorkforceOps Lab baseline, part 3: statutory parameter packs (global),
-- per-employer activation, audit, idempotency.
--
-- Pack tables are jurisdiction-global. No parameter row is seeded here:
-- values enter only through a reviewed MOM pinning step. Rates take effect
-- in the contribution period named by effective_from, never retroactively.

-- +goose Up

CREATE TABLE mom_ot_params (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT NOT NULL,
    value JSONB NOT NULL,
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (key, effective_from)
);

CREATE TABLE mom_shift_params (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT NOT NULL,
    value JSONB NOT NULL,
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (key, effective_from)
);

CREATE TABLE cpf_rate_band (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    age_from INT NOT NULL CHECK (age_from >= 0),
    age_to INT CHECK (age_to IS NULL OR age_to >= age_from),
    employee_rate NUMERIC(5, 4) NOT NULL CHECK (employee_rate >= 0),
    employer_rate NUMERIC(5, 4) NOT NULL CHECK (employer_rate >= 0),
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (age_from, age_to, effective_from)
);

CREATE TABLE cpf_ceiling (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind TEXT NOT NULL CHECK (kind IN ('ow_monthly', 'aw_annual', 'total_annual')),
    amount NUMERIC(12, 2) NOT NULL CHECK (amount >= 0),
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (kind, effective_from)
);

CREATE TABLE sdl_params (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rate NUMERIC(6, 5) NOT NULL CHECK (rate >= 0),
    min_amount NUMERIC(12, 2) NOT NULL CHECK (min_amount >= 0),
    max_amount NUMERIC(12, 2) NOT NULL CHECK (max_amount >= min_amount),
    rounding TEXT NOT NULL,
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (effective_from)
);

CREATE TABLE shg_contribution (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fund TEXT NOT NULL CHECK (fund IN ('CDAC', 'MBMF', 'SINDA', 'ECF')),
    wage_from NUMERIC(12, 2) NOT NULL CHECK (wage_from >= 0),
    wage_to NUMERIC(12, 2) CHECK (wage_to IS NULL OR wage_to >= wage_from),
    amount NUMERIC(12, 2) NOT NULL CHECK (amount >= 0),
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (fund, wage_from, effective_from)
);

CREATE TABLE leave_entitlement_rule (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    leave_type TEXT NOT NULL,
    service_years_from INT NOT NULL CHECK (service_years_from >= 0),
    service_years_to INT CHECK (service_years_to IS NULL OR service_years_to >= service_years_from),
    days NUMERIC(6, 2) NOT NULL CHECK (days > 0),
    effective_from DATE NOT NULL,
    effective_to DATE,
    UNIQUE (leave_type, service_years_from, effective_from)
);

CREATE TABLE rule_pack_activation (
    employer_id uuid NOT NULL REFERENCES employer (id),
    pack TEXT NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('IMPLEMENTABLE', 'BLOCKED', 'SUPERSEDED')),
    activated_at TIMESTAMPTZ,
    PRIMARY KEY (employer_id, pack)
);

CREATE TABLE audit_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES employer (id),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor TEXT NOT NULL,
    service TEXT NOT NULL,
    action TEXT NOT NULL,
    entity TEXT NOT NULL,
    entity_id uuid,
    before_state JSONB,
    after_state JSONB
);

CREATE TABLE idempotency_key (
    employer_id uuid NOT NULL REFERENCES employer (id),
    key TEXT NOT NULL,
    owner TEXT NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    result_ref TEXT,
    PRIMARY KEY (employer_id, key)
);

-- +goose Down

DROP TABLE IF EXISTS idempotency_key;
DROP TABLE IF EXISTS audit_event;
DROP TABLE IF EXISTS rule_pack_activation;
DROP TABLE IF EXISTS leave_entitlement_rule;
DROP TABLE IF EXISTS shg_contribution;
DROP TABLE IF EXISTS sdl_params;
DROP TABLE IF EXISTS cpf_ceiling;
DROP TABLE IF EXISTS cpf_rate_band;
DROP TABLE IF EXISTS mom_shift_params;
DROP TABLE IF EXISTS mom_ot_params;
