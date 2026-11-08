-- WorkforceOps Lab M1.1, part 9: integrity guards.
--
-- Remediates the adversarial-review blockers that belong in the schema:
-- overlapping pay periods, double allocation of intervals, cross-employee
-- links, concurrent supersede, overlapping live intervals, freeze
-- re-parenting, unscoped trigger lookups, and silent role degradation.
--
-- Two role facts are enforced loudly: migrations run as migration_admin,
-- and the app role must exist with the documented revokes in place.

-- +goose Up

-- 0. Preconditions: fail loudly instead of silently degrading (SYN-P1-7).
-- +goose StatementBegin
DO $$
DECLARE
    t RECORD;
    missing TEXT := '';
BEGIN
    IF current_user <> 'migration_admin' THEN
        RAISE EXCEPTION 'migrations must run as migration_admin, not %', current_user;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        RAISE EXCEPTION 'role app is missing: provision roles before migrating';
    END IF;
    FOR t IN SELECT tablename FROM pg_tables
             WHERE schemaname = 'public' AND tablename NOT LIKE 'goose\_%' LOOP
        IF (SELECT tableowner FROM pg_tables
            WHERE schemaname = 'public' AND tablename = t.tablename) <> 'migration_admin' THEN
            missing := missing || ' ' || t.tablename;
        END IF;
    END LOOP;
    IF missing <> '' THEN
        RAISE EXCEPTION 'tables not owned by migration_admin:% (migrate as migration_admin on a fresh database)', missing;
    END IF;
END
$$;
-- +goose StatementEnd

-- Roles are guaranteed now: apply the insert-only revokes unconditionally
-- (00004 skipped them silently when roles were absent) and verify.
REVOKE UPDATE, DELETE ON
    clock_event, attendance_interval, attendance_correction,
    roster_revision, ket_record, payslip, audit_event
FROM app;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.role_table_grants
               WHERE grantee = 'app' AND privilege_type IN ('UPDATE', 'DELETE')
               AND table_name IN ('clock_event', 'attendance_interval',
                   'attendance_correction', 'roster_revision', 'ket_record',
                   'payslip', 'audit_event')) THEN
        RAISE EXCEPTION 'insert-only revokes did not take effect for role app';
    END IF;
END
$$;
-- +goose StatementEnd

-- 1. No overlapping pay periods per employer (review §3A / R1).
ALTER TABLE pay_period ADD CONSTRAINT pay_period_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        daterange(starts_on, ends_on, '[]') WITH &&
    );

-- 2. One interval feeds at most one sheet chain (R1 double allocation).
-- A plain UNIQUE would forbid correction versions from re-linking the
-- same intervals, so the guard is a trigger: every existing linker must
-- belong to the same employee+period chain as the new one. Cross-period
-- double counting (the R1 failure) is rejected; correction versions of
-- one chain share evidence while only the effective approval counts.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_interval_single_chain() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    new_emp UUID;
    new_period UUID;
    bad UUID;
BEGIN
    SELECT t.employee_id, t.pay_period_id INTO new_emp, new_period
    FROM timesheet_line l JOIN timesheet t
      ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
    WHERE l.employer_id = NEW.employer_id AND l.id = NEW.line_id;
    SELECT t.id INTO bad
    FROM timesheet_line_interval li
    JOIN timesheet_line l ON l.employer_id = li.employer_id AND l.id = li.line_id
    JOIN timesheet t ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
    WHERE li.employer_id = NEW.employer_id AND li.interval_id = NEW.interval_id
      AND (t.employee_id <> new_emp OR t.pay_period_id <> new_period)
    LIMIT 1;
    IF bad IS NOT NULL THEN
        RAISE EXCEPTION 'interval % already feeds sheet % of another employee or period', NEW.interval_id, bad;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timesheet_line_interval_single_chain
    BEFORE INSERT ON timesheet_line_interval
    FOR EACH ROW EXECUTE FUNCTION enforce_interval_single_chain();

-- 3. Line and interval must belong to the same employee (SYN-P2-23).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_line_interval_employee() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    line_emp UUID;
    iv_emp UUID;
BEGIN
    SELECT t.employee_id INTO line_emp
    FROM timesheet_line l JOIN timesheet t
      ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
    WHERE l.employer_id = NEW.employer_id AND l.id = NEW.line_id;
    SELECT i.employee_id INTO iv_emp FROM attendance_interval i
    WHERE i.employer_id = NEW.employer_id AND i.id = NEW.interval_id;
    IF line_emp IS NULL OR iv_emp IS NULL OR line_emp <> iv_emp THEN
        RAISE EXCEPTION 'line % and interval % belong to different employees', NEW.line_id, NEW.interval_id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timesheet_line_interval_employee_match
    BEFORE INSERT OR UPDATE ON timesheet_line_interval
    FOR EACH ROW EXECUTE FUNCTION enforce_line_interval_employee();

-- 4. Explicit current-interval predicate (review amendment G).
-- is_current is maintained by the schema, never by callers: inserting a
-- successor flips the predecessor in a BEFORE trigger so the partial
-- exclusion below never sees two live overlapping rows. The function is
-- SECURITY DEFINER owned by migration_admin because the app role cannot
-- UPDATE this table; direct UPDATE stays revoked.
ALTER TABLE attendance_interval ADD COLUMN is_current BOOLEAN NOT NULL DEFAULT true;
UPDATE attendance_interval o SET is_current = false
WHERE EXISTS (SELECT 1 FROM attendance_interval n
              WHERE n.employer_id = o.employer_id AND n.supersedes_id = o.id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maintain_interval_currency() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
BEGIN
    IF NEW.supersedes_id IS NOT NULL THEN
        UPDATE attendance_interval SET is_current = false
        WHERE employer_id = NEW.employer_id AND id = NEW.supersedes_id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER attendance_interval_currency
    BEFORE INSERT ON attendance_interval
    FOR EACH ROW WHEN (NEW.supersedes_id IS NOT NULL)
    EXECUTE FUNCTION maintain_interval_currency();

-- One successor per interval for operator corrections (review §3D / R4).
-- Partial: late-break pairing splits legitimately supersede one work
-- interval with several clock-source rows; those serialize on the
-- per-employee advisory lock instead.
CREATE UNIQUE INDEX attendance_interval_single_correction_idx ON attendance_interval (supersedes_id)
    WHERE source = 'correction' AND supersedes_id IS NOT NULL;

-- Live intervals of one employee never overlap (SYN-P1-2/P1-21).
-- tstzrange bounds are '[)': adjacent runs are fine. Historical
-- (superseded) rows are excluded by the predicate, never blocked.
ALTER TABLE attendance_interval ADD CONSTRAINT attendance_interval_live_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        tstzrange(interval_start, interval_end) WITH &&
    ) WHERE (is_current);

-- 5. Append-only timesheet corrections (review amendment A).
-- Decided sheets stay immutable; a correction is a new draft row linked
-- via supersedes_id. The original per-employee-period uniqueness becomes
-- partial so exactly one ORIGINAL sheet exists; successors chain on.
ALTER TABLE timesheet ADD COLUMN supersedes_id uuid;
ALTER TABLE timesheet ADD CONSTRAINT timesheet_supersedes_fk
    FOREIGN KEY (employer_id, supersedes_id) REFERENCES timesheet (employer_id, id);
ALTER TABLE timesheet ADD CONSTRAINT timesheet_single_successor
    UNIQUE (supersedes_id);
ALTER TABLE timesheet DROP CONSTRAINT timesheet_employer_id_employee_id_pay_period_id_key;
-- Partial unique: exactly one ORIGINAL sheet per employee+period;
-- correction versions (supersedes_id set) chain via timesheet_single_successor.
CREATE UNIQUE INDEX timesheet_single_original_idx ON timesheet (employer_id, employee_id, pay_period_id)
    WHERE supersedes_id IS NULL;

-- 6. Freeze triggers: UPDATE validates the NEW parent too (SYN-P1-1) and
-- every parent lookup is employer-scoped (SYN-P3-3). Bindings unchanged.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_if_timesheet_decided() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    old_ts UUID;
    new_ts UUID;
    ts_status TEXT;
    line_ts UUID;
BEGIN
    IF TG_TABLE_NAME = 'timesheet' THEN
        IF OLD.status IN ('approved', 'rejected') THEN
            RAISE EXCEPTION 'frozen: timesheet % already decided', OLD.id;
        END IF;
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
    ELSIF TG_TABLE_NAME = 'timesheet_line' THEN
        IF TG_OP = 'INSERT' THEN
            SELECT status INTO ts_status FROM timesheet
            WHERE employer_id = NEW.employer_id AND id = NEW.timesheet_id;
            IF ts_status IN ('approved', 'rejected') THEN
                RAISE EXCEPTION 'frozen: timesheet % already decided', NEW.timesheet_id;
            END IF;
            RETURN NEW;
        ELSIF TG_OP = 'DELETE' THEN
            old_ts := OLD.timesheet_id;
        ELSE
            old_ts := OLD.timesheet_id;
            new_ts := NEW.timesheet_id;
        END IF;
        SELECT status INTO ts_status FROM timesheet
        WHERE employer_id = OLD.employer_id AND id = old_ts;
        IF ts_status IN ('approved', 'rejected') THEN
            RAISE EXCEPTION 'frozen: timesheet % already decided', old_ts;
        END IF;
        IF TG_OP = 'UPDATE' AND new_ts IS DISTINCT FROM old_ts THEN
            SELECT status INTO ts_status FROM timesheet
            WHERE employer_id = NEW.employer_id AND id = new_ts;
            IF ts_status IN ('approved', 'rejected') THEN
                RAISE EXCEPTION 'frozen: timesheet % already decided', new_ts;
            END IF;
        END IF;
    ELSE -- timesheet_line_interval
        IF TG_OP = 'INSERT' THEN
            line_ts := NEW.line_id;
        ELSIF TG_OP = 'DELETE' THEN
            line_ts := OLD.line_id;
        ELSIF NEW.line_id IS DISTINCT FROM OLD.line_id THEN
            SELECT t.status INTO ts_status
            FROM timesheet_line l JOIN timesheet t
              ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
            WHERE l.employer_id = NEW.employer_id AND l.id = NEW.line_id;
            IF ts_status IN ('approved', 'rejected') THEN
                RAISE EXCEPTION 'frozen: timesheet line % already decided', NEW.line_id;
            END IF;
            line_ts := OLD.line_id;
        ELSE
            line_ts := OLD.line_id;
        END IF;
        SELECT t.status INTO ts_status
        FROM timesheet_line l JOIN timesheet t
          ON t.employer_id = l.employer_id AND t.id = l.timesheet_id
        WHERE l.employer_id = COALESCE(OLD.employer_id, NEW.employer_id) AND l.id = line_ts;
        IF ts_status IN ('approved', 'rejected') THEN
            RAISE EXCEPTION 'frozen: timesheet line % already decided', line_ts;
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_unless_roster_draft_revision() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    roster_status TEXT;
    roster_current INT;
    rev_version INT;
BEGIN
    SELECT r.status, r.current_version, rev.version
    INTO roster_status, roster_current, rev_version
    FROM roster_revision rev JOIN roster r
        ON r.employer_id = rev.employer_id AND r.id = rev.roster_id
    WHERE rev.employer_id = (CASE WHEN TG_OP = 'INSERT' THEN NEW.employer_id ELSE OLD.employer_id END)
      AND rev.id = (CASE WHEN TG_OP = 'INSERT' THEN NEW.revision_id ELSE OLD.revision_id END);
    IF roster_status <> 'draft' OR rev_version <> roster_current THEN
        RAISE EXCEPTION 'frozen: roster revision % not the current draft',
            (CASE WHEN TG_OP = 'INSERT' THEN NEW.revision_id ELSE OLD.revision_id END);
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.revision_id IS DISTINCT FROM OLD.revision_id THEN
        SELECT r.status, r.current_version, rev.version
        INTO roster_status, roster_current, rev_version
        FROM roster_revision rev JOIN roster r
            ON r.employer_id = rev.employer_id AND r.id = rev.roster_id
        WHERE rev.employer_id = NEW.employer_id AND rev.id = NEW.revision_id;
        IF roster_status <> 'draft' OR rev_version <> roster_current THEN
            RAISE EXCEPTION 'frozen: roster revision % not the current draft', NEW.revision_id;
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_if_run_approved() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    run_status TEXT;
BEGIN
    IF TG_TABLE_NAME = 'payroll_run' THEN
        IF OLD.status = 'approved' THEN
            RAISE EXCEPTION 'frozen: payroll run % already approved', OLD.id;
        END IF;
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
    END IF;
    IF TG_OP = 'INSERT' THEN
        SELECT status INTO run_status FROM payroll_run
        WHERE employer_id = NEW.employer_id AND id = NEW.run_id;
    ELSE
        SELECT status INTO run_status FROM payroll_run
        WHERE employer_id = OLD.employer_id AND id = OLD.run_id;
    END IF;
    IF run_status = 'approved' THEN
        RAISE EXCEPTION 'frozen: payroll run % already approved',
            (CASE WHEN TG_OP = 'INSERT' THEN NEW.run_id ELSE OLD.run_id END);
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.run_id IS DISTINCT FROM OLD.run_id THEN
        SELECT status INTO run_status FROM payroll_run
        WHERE employer_id = NEW.employer_id AND id = NEW.run_id;
        IF run_status = 'approved' THEN
            RAISE EXCEPTION 'frozen: payroll run % already approved', NEW.run_id;
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_unless_run_draft() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    run_status TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT status INTO run_status FROM payroll_run
        WHERE employer_id = NEW.employer_id AND id = NEW.payroll_run_id;
        IF run_status <> 'draft' THEN
            RAISE EXCEPTION 'frozen: exclusions only while run % is a draft', NEW.payroll_run_id;
        END IF;
        RETURN NEW;
    END IF;
    SELECT status INTO run_status FROM payroll_run
    WHERE employer_id = OLD.employer_id AND id = OLD.payroll_run_id;
    IF run_status <> 'draft' THEN
        RAISE EXCEPTION 'frozen: exclusions only while run % is a draft', OLD.payroll_run_id;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.payroll_run_id <> OLD.payroll_run_id THEN
        SELECT status INTO run_status FROM payroll_run
        WHERE employer_id = NEW.employer_id AND id = NEW.payroll_run_id;
        IF run_status <> 'draft' THEN
            RAISE EXCEPTION 'frozen: exclusions only while run % is a draft', NEW.payroll_run_id;
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

-- 7. Persisted attendance exceptions (SYN-P1-6 / SYN-P2-17).
-- Append-only log: day status derives from unresolved rows, Import
-- appends new causes, validation flips (resolved, resolved_at). No
-- natural-key unique: re-raised causes check-then-insert under the
-- employee advisory lock instead of ON CONFLICT DO UPDATE (which would
-- need table-level UPDATE). The app role may INSERT and flip the two
-- resolution columns; anything else stays revoked.
CREATE TABLE attendance_exception (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL,
    employee_id uuid NOT NULL,
    business_date DATE NOT NULL,
    code TEXT NOT NULL,
    detail TEXT NOT NULL,
    event_ids uuid[] NOT NULL DEFAULT '{}',
    resolved BOOLEAN NOT NULL DEFAULT false,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employer_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);
CREATE INDEX attendance_exception_open_idx ON attendance_exception (employer_id, employee_id, business_date)
    WHERE NOT resolved;
REVOKE UPDATE, DELETE ON attendance_exception FROM app;
GRANT SELECT, INSERT ON attendance_exception TO app;
GRANT UPDATE (resolved, resolved_at) ON attendance_exception TO app;

-- 8. Backfill initial status history (SYN-P1-8): employees created before
-- history rows existed gain their coherent initial row.
INSERT INTO employee_status_history (employer_id, employee_id, status, effective_from, reason)
SELECT e.employer_id, e.id, e.status, e.status_effective_date, 'backfill: initial status at M1.1'
FROM employee e
WHERE NOT EXISTS (SELECT 1 FROM employee_status_history h
                  WHERE h.employer_id = e.employer_id AND h.employee_id = e.id);

-- +goose Down

DELETE FROM employee_status_history WHERE reason = 'backfill: initial status at M1.1';
DROP TABLE IF EXISTS attendance_exception;
-- NOTE: the redefined freeze-function bodies are intentionally NOT reverted:
-- restoring the 00006 text would reintroduce the re-parenting bypass. Down
-- removes the new triggers/constraints/columns only. (Same documented-skew
-- policy as 00006 Down.)
DROP TRIGGER IF EXISTS timesheet_line_interval_employee_match ON timesheet_line_interval;
DROP FUNCTION IF EXISTS enforce_line_interval_employee();
DROP TRIGGER IF EXISTS timesheet_line_interval_single_chain ON timesheet_line_interval;
DROP FUNCTION IF EXISTS enforce_interval_single_chain();
DROP TRIGGER IF EXISTS attendance_interval_currency ON attendance_interval;
DROP FUNCTION IF EXISTS maintain_interval_currency();
ALTER TABLE attendance_interval DROP CONSTRAINT IF EXISTS attendance_interval_live_no_overlap;
DROP INDEX IF EXISTS attendance_interval_single_correction_idx;
ALTER TABLE attendance_interval DROP COLUMN IF EXISTS is_current;
DROP INDEX IF EXISTS timesheet_single_original_idx;
ALTER TABLE timesheet DROP CONSTRAINT IF EXISTS timesheet_single_successor;
ALTER TABLE timesheet DROP CONSTRAINT IF EXISTS timesheet_supersedes_fk;
ALTER TABLE timesheet DROP COLUMN IF EXISTS supersedes_id;
ALTER TABLE timesheet ADD CONSTRAINT timesheet_employer_id_employee_id_pay_period_id_key
    UNIQUE (employer_id, employee_id, pay_period_id);
ALTER TABLE pay_period DROP CONSTRAINT IF EXISTS pay_period_no_overlap;
