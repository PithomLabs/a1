-- WorkforceOps Lab baseline, part 4: protection layer.
--
-- Least privilege for insert-only history tables, temporal freezing
-- triggers, and gist exclusion constraints for effective dating. The
-- exclusion constraints require btree_gist from the superuser bootstrap;
-- they live here (not in the table definitions) so the failure, if the
-- bootstrap was skipped, names the prerequisite instead of the table.

-- +goose Up

-- Least privilege first: the app role may only insert into history tables.
-- Conditional so migrations also run in environments where roles were not
-- pre-created; the integrity suite asserts the revokes took effect.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        REVOKE UPDATE, DELETE ON
            clock_event,
            attendance_interval,
            attendance_correction,
            roster_revision,
            ket_record,
            payslip,
            audit_event
        FROM app;
    END IF;
END
$$;
-- +goose StatementEnd

-- 1. Timesheets freeze for resubmission rejection, not recomputation.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_if_timesheet_decided() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    ts_id UUID;
    ts_status TEXT;
BEGIN
    IF TG_TABLE_NAME = 'timesheet' THEN
        IF OLD.status IN ('approved', 'rejected') THEN
            RAISE EXCEPTION 'frozen: timesheet % already decided', OLD.id;
        END IF;
        RETURN OLD;
    ELSIF TG_TABLE_NAME = 'timesheet_line' THEN
        IF TG_OP = 'INSERT' THEN
            ts_id := NEW.timesheet_id;
        ELSE
            ts_id := OLD.timesheet_id;
        END IF;
        SELECT status INTO ts_status FROM timesheet WHERE id = ts_id;
    ELSE -- timesheet_line_interval
        IF TG_OP = 'INSERT' THEN
            ts_id := NEW.line_id;
        ELSE
            ts_id := OLD.line_id;
        END IF;
        SELECT t.status INTO ts_status
        FROM timesheet_line l JOIN timesheet t ON t.id = l.timesheet_id
        WHERE l.id = ts_id;
    END IF;
    IF ts_status IN ('approved', 'rejected') THEN
        RAISE EXCEPTION 'frozen: timesheet % already decided', ts_id;
    END IF;
    IF TG_OP = 'INSERT' THEN RETURN NEW; ELSE RETURN OLD; END IF;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timesheet_freeze
    BEFORE UPDATE OR DELETE ON timesheet
    FOR EACH ROW EXECUTE FUNCTION forbid_if_timesheet_decided();

CREATE TRIGGER timesheet_line_freeze
    BEFORE INSERT OR UPDATE OR DELETE ON timesheet_line
    FOR EACH ROW EXECUTE FUNCTION forbid_if_timesheet_decided();

CREATE TRIGGER timesheet_line_interval_freeze
    BEFORE INSERT OR UPDATE OR DELETE ON timesheet_line_interval
    FOR EACH ROW EXECUTE FUNCTION forbid_if_timesheet_decided();

-- 2. A roster revision keeps its lineage while its roster is a draft; once
-- published, it is immutable and corrections arrive as a new revision.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_unless_roster_draft_revision() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    rev_employer UUID;
    rev_id UUID;
    roster_status TEXT;
    roster_current INT;
    rev_version INT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        rev_employer := NEW.employer_id;
        rev_id := NEW.revision_id;
    ELSE
        rev_employer := OLD.employer_id;
        rev_id := OLD.revision_id;
    END IF;
    SELECT r.status, r.current_version, rev.version
    INTO roster_status, roster_current, rev_version
    FROM roster_revision rev JOIN roster r
        ON r.employer_id = rev.employer_id AND r.id = rev.roster_id
    WHERE rev.employer_id = rev_employer AND rev.id = rev_id;
    IF roster_status <> 'draft' OR rev_version <> roster_current THEN
        RAISE EXCEPTION 'frozen: roster revision % not the current draft', rev_id;
    END IF;
    IF TG_OP = 'INSERT' THEN RETURN NEW; ELSE RETURN OLD; END IF;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER roster_assignment_draft_only
    BEFORE INSERT OR UPDATE OR DELETE ON roster_assignment
    FOR EACH ROW EXECUTE FUNCTION forbid_unless_roster_draft_revision();

-- 3. Approved payroll runs (run row and lines) are immutable.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_if_run_approved() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    run_id UUID;
    run_status TEXT;
BEGIN
    IF TG_TABLE_NAME = 'payroll_run' THEN
        IF OLD.status = 'approved' THEN
            RAISE EXCEPTION 'frozen: payroll run % already approved', OLD.id;
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'INSERT' THEN
        run_id := NEW.run_id;
    ELSE
        run_id := OLD.run_id;
    END IF;
    SELECT status INTO run_status FROM payroll_run WHERE id = run_id;
    IF run_status = 'approved' THEN
        RAISE EXCEPTION 'frozen: payroll run % already approved', run_id;
    END IF;
    IF TG_OP = 'INSERT' THEN RETURN NEW; ELSE RETURN OLD; END IF;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER payroll_run_freeze
    BEFORE UPDATE OR DELETE ON payroll_run
    FOR EACH ROW EXECUTE FUNCTION forbid_if_run_approved();

CREATE TRIGGER payroll_line_freeze
    BEFORE INSERT OR UPDATE OR DELETE ON payroll_line
    FOR EACH ROW EXECUTE FUNCTION forbid_if_run_approved();

-- 4. Exclusions may only be recorded while the parent run is a draft.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_unless_run_draft() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    run_status TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT status INTO run_status FROM payroll_run WHERE id = NEW.payroll_run_id;
        IF run_status <> 'draft' THEN
            RAISE EXCEPTION 'frozen: exclusions only while run % is a draft', NEW.payroll_run_id;
        END IF;
        RETURN NEW;
    END IF;
    SELECT status INTO run_status FROM payroll_run WHERE id = OLD.payroll_run_id;
    IF run_status <> 'draft' THEN
        RAISE EXCEPTION 'frozen: exclusions only while run % is a draft', OLD.payroll_run_id;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.payroll_run_id <> OLD.payroll_run_id THEN
        SELECT status INTO run_status FROM payroll_run WHERE id = NEW.payroll_run_id;
        IF run_status <> 'draft' THEN
            RAISE EXCEPTION 'frozen: exclusions only while run % is a draft', NEW.payroll_run_id;
        END IF;
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER payroll_run_exclusion_draft_only
    BEFORE INSERT OR UPDATE OR DELETE ON payroll_run_exclusion
    FOR EACH ROW EXECUTE FUNCTION forbid_unless_run_draft();

-- 5. Decided leave requests are immutable; correction is a new request.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_if_leave_decided() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('approved', 'rejected') THEN
        RAISE EXCEPTION 'frozen: leave request % already decided', OLD.id;
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER leave_request_freeze
    BEFORE UPDATE OR DELETE ON leave_request
    FOR EACH ROW EXECUTE FUNCTION forbid_if_leave_decided();

-- Temporal integrity: at most one covering row per effective-dated key.
ALTER TABLE employment_terms ADD CONSTRAINT employment_terms_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );

ALTER TABLE employee_status_history ADD CONSTRAINT employee_status_history_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );

ALTER TABLE team_membership ADD CONSTRAINT team_membership_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        team_id WITH =,
        employee_id WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );

ALTER TABLE employee_skill ADD CONSTRAINT employee_skill_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        skill_id WITH =,
        daterange(valid_from, COALESCE(valid_to, 'infinity'::date), '[]') WITH &&
    );

ALTER TABLE rotation_assignment ADD CONSTRAINT rotation_assignment_employee_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    ) WHERE (employee_id IS NOT NULL);

ALTER TABLE rotation_assignment ADD CONSTRAINT rotation_assignment_team_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        team_id WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    ) WHERE (team_id IS NOT NULL);

ALTER TABLE compensation_line ADD CONSTRAINT compensation_line_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        terms_id WITH =,
        kind WITH =,
        code WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );

ALTER TABLE leave_request ADD CONSTRAINT leave_request_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        daterange(from_date, to_date, '[]') WITH &&
    );

ALTER TABLE shg_instruction ADD CONSTRAINT shg_instruction_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        fund WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );

-- +goose Down

ALTER TABLE shg_instruction DROP CONSTRAINT IF EXISTS shg_instruction_no_overlap;
ALTER TABLE leave_request DROP CONSTRAINT IF EXISTS leave_request_no_overlap;
ALTER TABLE compensation_line DROP CONSTRAINT IF EXISTS compensation_line_no_overlap;
ALTER TABLE rotation_assignment DROP CONSTRAINT IF EXISTS rotation_assignment_team_no_overlap;
ALTER TABLE rotation_assignment DROP CONSTRAINT IF EXISTS rotation_assignment_employee_no_overlap;
ALTER TABLE employee_skill DROP CONSTRAINT IF EXISTS employee_skill_no_overlap;
ALTER TABLE team_membership DROP CONSTRAINT IF EXISTS team_membership_no_overlap;
ALTER TABLE employee_status_history DROP CONSTRAINT IF EXISTS employee_status_history_no_overlap;
ALTER TABLE employment_terms DROP CONSTRAINT IF EXISTS employment_terms_no_overlap;

DROP TRIGGER IF EXISTS leave_request_freeze ON leave_request;
DROP TRIGGER IF EXISTS payroll_run_exclusion_draft_only ON payroll_run_exclusion;
DROP TRIGGER IF EXISTS payroll_line_freeze ON payroll_line;
DROP TRIGGER IF EXISTS payroll_run_freeze ON payroll_run;
DROP TRIGGER IF EXISTS roster_assignment_draft_only ON roster_assignment;
DROP TRIGGER IF EXISTS timesheet_line_interval_freeze ON timesheet_line_interval;
DROP TRIGGER IF EXISTS timesheet_line_freeze ON timesheet_line;
DROP TRIGGER IF EXISTS timesheet_freeze ON timesheet;

DROP FUNCTION IF EXISTS forbid_if_leave_decided();
DROP FUNCTION IF EXISTS forbid_unless_run_draft();
DROP FUNCTION IF EXISTS forbid_if_run_approved();
DROP FUNCTION IF EXISTS forbid_unless_roster_draft_revision();
DROP FUNCTION IF EXISTS forbid_if_timesheet_decided();

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        GRANT UPDATE, DELETE ON
            clock_event,
            attendance_interval,
            attendance_correction,
            roster_revision,
            ket_record,
            payslip,
            audit_event
        TO app;
    END IF;
END
$$;
-- +goose StatementEnd
