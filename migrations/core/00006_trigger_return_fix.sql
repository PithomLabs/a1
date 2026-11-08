-- WorkforceOps Lab baseline, part 6: trigger RETURN-ROW fix.
--
-- BEFORE UPDATE row triggers must RETURN NEW: the part-4 functions returned
-- OLD on every non-INSERT operation, which silently discarded allowed
-- updates (state never advanced, no error raised). Forbidden writes still
-- raised correctly, which is why only state transitions were affected.
-- CREATE OR REPLACE redefines the bodies; trigger bindings are unchanged.

-- +goose Up

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
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
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
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

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
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

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
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
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
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_if_leave_decided() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('approved', 'rejected') THEN
        RAISE EXCEPTION 'frozen: leave request % already decided', OLD.id;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- No-op: the part-4 bodies had the same signatures; downgrading the
-- bodies would reintroduce silent update loss. Schema downgrades stop here.
SELECT 1;
