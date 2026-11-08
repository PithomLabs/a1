-- WorkforceOps Lab M1.2 (prompt11): payroll input integrity closure.
--
-- Single append-only migration for the milestone: B1 duplicate-event
-- disposition first, then Stage C privilege hardening. Sections are
-- independent and ordered; later sections append below with their own
-- Down blocks in reverse order.

-- +goose Up

-- Section B1. Duplicate vendor deliveries confirmed by an operator.
--
-- A duplicate work span delivered under fresh source event IDs raises
-- overlapping_span and its events stay unpaired, blocking timesheet
-- generation indefinitely. The operator inspects the original and the
-- overlapping events (review surfaces both with exact event IDs) and
-- confirms a duplicate endpoint against its canonical event. Both raw
-- events are preserved; only the duplicate is excluded from pairing
-- and eligibility; the exception row is retained and resolved once
-- every cause endpoint settles.
CREATE TABLE clock_event_disposition (
    employer_id uuid NOT NULL REFERENCES employer (id),
    event_id uuid NOT NULL,
    disposition TEXT NOT NULL CHECK (disposition = 'duplicate'),
    duplicate_of_event_id uuid NOT NULL REFERENCES clock_event (id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (char_length(reason) > 0),
    decided_by TEXT NOT NULL CHECK (char_length(decided_by) > 0),
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id),
    FOREIGN KEY (employer_id, event_id) REFERENCES clock_event (employer_id, id) ON DELETE RESTRICT,
    CHECK (event_id <> duplicate_of_event_id)
);

-- The matching rule lives in the database so writers that bypass the
-- service cannot silently discard genuine work: same employer, same
-- employee, same kind, exact same instant, and a canonical event that
-- is not itself dispositioned (chains and cycles are impossible
-- because every edge lands on an undispositioned event).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_clock_event_disposition() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    d_ee uuid; d_kind TEXT; d_at TIMESTAMPTZ;
    c_em uuid; c_ee uuid; c_kind TEXT; c_at TIMESTAMPTZ;
BEGIN
    SELECT e.employee_id, e.kind, e.occurred_at
      INTO d_ee, d_kind, d_at
      FROM public.clock_event e WHERE e.id = NEW.event_id;
    SELECT e.employer_id, e.employee_id, e.kind, e.occurred_at
      INTO c_em, c_ee, c_kind, c_at
      FROM public.clock_event e WHERE e.id = NEW.duplicate_of_event_id;
    IF d_ee IS NULL OR c_em IS NULL THEN
        RAISE EXCEPTION 'disposition references a missing clock event';
    END IF;
    IF NEW.employer_id <> c_em THEN
        RAISE EXCEPTION 'duplicate and canonical events belong to different employers';
    END IF;
    IF d_ee <> c_ee THEN
        RAISE EXCEPTION 'duplicate and canonical events belong to different employees';
    END IF;
    IF d_kind <> c_kind THEN
        RAISE EXCEPTION 'duplicate kind % differs from canonical kind %', d_kind, c_kind;
    END IF;
    IF d_at <> c_at THEN
        RAISE EXCEPTION 'duplicate instant % differs from canonical instant %', d_at, c_at;
    END IF;
    IF EXISTS (SELECT 1 FROM public.clock_event_disposition d
               WHERE d.event_id = NEW.duplicate_of_event_id) THEN
        RAISE EXCEPTION 'canonical event % is itself dispositioned as a duplicate', NEW.duplicate_of_event_id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER clock_event_disposition_match
    BEFORE INSERT OR UPDATE ON clock_event_disposition
    FOR EACH ROW EXECUTE FUNCTION check_clock_event_disposition();
-- +goose StatementEnd

-- Disposition history is insert-only for the app role: decisions are
-- never rewritten or deleted through the runtime role.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        REVOKE UPDATE, DELETE ON clock_event_disposition FROM app;
    END IF;
END
$$;
-- +goose StatementEnd

-- Section B6. Correction groups: one original interval is superseded
-- by one correction group holding one dated segment per business date.
-- The single-successor invariant moves from the interval row to the
-- group: UNIQUE(predecessor_interval_id) rejects a second group even
-- under concurrency, while sibling segments share one group.
CREATE TABLE attendance_correction_group (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES employer (id),
    employee_id uuid NOT NULL,
    predecessor_interval_id uuid NOT NULL UNIQUE REFERENCES attendance_interval (id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (char_length(reason) > 0),
    corrected_by TEXT NOT NULL CHECK (char_length(corrected_by) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employer_id, employee_id, id),
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id)
);

ALTER TABLE attendance_interval ADD COLUMN correction_group_id uuid NULL
    REFERENCES attendance_correction_group (id) ON DELETE RESTRICT;
-- Group membership is employer/employee consistent: a segment cannot
-- smuggle another employee's time into a group. (The NULL group on
-- clock-source rows skips the MATCH SIMPLE check.)
ALTER TABLE attendance_interval ADD CONSTRAINT attendance_interval_correction_group_member_fk
    FOREIGN KEY (employer_id, employee_id, correction_group_id)
    REFERENCES attendance_correction_group (employer_id, employee_id, id);
-- Every correction-source row belongs to exactly one group.
ALTER TABLE attendance_interval ADD CONSTRAINT attendance_interval_correction_group_present
    CHECK (source <> 'correction' OR correction_group_id IS NOT NULL);
-- The old one-row-per-predecessor index cannot survive sibling
-- segments: the single-successor invariant now lives on the group
-- table's UNIQUE(predecessor_interval_id), and the live-no-overlap
-- exclusion still bars overlapping siblings.
DROP INDEX IF EXISTS attendance_interval_single_correction_idx;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        REVOKE UPDATE, DELETE ON attendance_correction_group FROM app;
    END IF;
END
$$;
-- +goose StatementEnd

-- Section E1. Canonical rest-day values: full weekday names only.
-- Free-text values never equal time.Weekday.String, so a garbage value
-- would silently bypass the roster rest-day check. The service
-- validates first; this CHECK holds for direct writers.
ALTER TABLE employment_terms ADD CONSTRAINT employment_terms_rest_day_canonical
    CHECK (rest_day IN ('Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'));

-- Section B4. Compensation retro protection: history is insert-only
-- for the app role, and created_at is trigger-normalized so a
-- backdated timestamp cannot dodge after-approval conflict detection.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_compensation_created_at() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    NEW.created_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER compensation_created_at_normalize
    BEFORE INSERT OR UPDATE ON compensation_line
    FOR EACH ROW EXECUTE FUNCTION normalize_compensation_created_at();
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        REVOKE UPDATE, DELETE ON compensation_line FROM app;
    END IF;
END
$$;
-- +goose StatementEnd

-- Section E3. Exact microsecond duration totals: measured elapsed
-- time is integer microseconds computed in Go from interval endpoints.
-- Legacy decimal columns stay as explicitly quantized display only;
-- the database never implicitly rounds a business duration again.
ALTER TABLE timesheet_line ADD COLUMN hours_us BIGINT NOT NULL DEFAULT 0 CHECK (hours_us >= 0);
ALTER TABLE attendance_record ADD COLUMN actual_us BIGINT NOT NULL DEFAULT 0 CHECK (actual_us >= 0);
ALTER TABLE attendance_record ADD COLUMN paid_us BIGINT NOT NULL DEFAULT 0 CHECK (paid_us >= 0);

-- Backfill from the evidence each row already cites: a line sums its
-- linked intervals whatever their currency; a record sums its date's
-- current intervals, matching the writers' semantics.
-- +goose StatementBegin
DO $$
BEGIN
    UPDATE timesheet_line l SET hours_us = s.us FROM (
        SELECT li.line_id, COALESCE(sum(
            (EXTRACT(EPOCH FROM i.interval_end) - EXTRACT(EPOCH FROM i.interval_start)) * 1000000)::bigint, 0) AS us
        FROM timesheet_line_interval li
        JOIN attendance_interval i ON i.employer_id = li.employer_id AND i.id = li.interval_id
        GROUP BY li.line_id
    ) s WHERE s.line_id = l.id;
    UPDATE attendance_record r SET
        actual_us = s.actual, paid_us = s.paid FROM (
        SELECT i.employer_id, i.employee_id, i.local_date,
            COALESCE(sum(((EXTRACT(EPOCH FROM i.interval_end) - EXTRACT(EPOCH FROM i.interval_start)) * 1000000)::bigint), 0) AS actual,
            COALESCE(sum(CASE WHEN i.kind = 'work' OR i.kind = 'break_paid'
                THEN ((EXTRACT(EPOCH FROM i.interval_end) - EXTRACT(EPOCH FROM i.interval_start)) * 1000000)::bigint ELSE 0 END), 0) AS paid
        FROM attendance_interval i WHERE i.is_current
        GROUP BY i.employer_id, i.employee_id, i.local_date
    ) s WHERE s.employer_id = r.employer_id AND s.employee_id = r.employee_id AND s.local_date = r.business_date;
END
$$;
-- +goose StatementEnd

-- Section C1/C2. Interval currency hardening: the maintenance
-- trigger unconditionally establishes the current row, and the
-- SECURITY DEFINER function cannot be redirected through pg_temp.
--
-- search_path=public alone does NOT exclude the session temp schema:
-- pg_temp is implicitly searched first for sessions that own temp
-- tables. The function therefore uses a deliberately restricted
-- search_path (pg_catalog only: no name resolves implicitly) plus
-- schema-qualified references, and TEMPORARY is revoked from both the
-- app role and PUBLIC on this dedicated database.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maintain_interval_currency() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    NEW.is_current := true;
    IF NEW.supersedes_id IS NOT NULL THEN
        UPDATE public.attendance_interval SET is_current = false
        WHERE employer_id = NEW.employer_id AND id = NEW.supersedes_id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS attendance_interval_currency ON attendance_interval;
-- +goose StatementBegin
CREATE TRIGGER attendance_interval_currency
    BEFORE INSERT ON attendance_interval
    FOR EACH ROW EXECUTE FUNCTION maintain_interval_currency();
-- +goose StatementEnd

ALTER FUNCTION maintain_interval_currency() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION maintain_interval_currency() FROM PUBLIC;

-- TEMPORARY on this database is revoked from both app and PUBLIC at
-- provisioning time (superuser: scripts/db/init-roles.sh; tests mirror
-- it in dbutil SetupDB). It cannot live here: the migration role owns
-- the schema, not the database, so it may not alter the database ACL.
-- TEMP rides the default PUBLIC grant, so revoking from app alone
-- would leave the effective permission. This migration verifies the
-- revoke loudly instead of silently depending on it.
--
-- Verify loudly: owner, SECURITY DEFINER, restricted search path,
-- schema-qualified maintenance, and no residual TEMPORARY.
-- +goose StatementBegin
DO $$
DECLARE
    fn_owner TEXT;
    fn_definer BOOLEAN;
    fn_path TEXT[];
    fn_def TEXT;
    db_acl TEXT;
    bad INT;
BEGIN
    SELECT u.rolname, p.prosecdef, p.proconfig, pg_get_functiondef(p.oid)
      INTO fn_owner, fn_definer, fn_path, fn_def
      FROM pg_proc p JOIN pg_roles u ON u.oid = p.proowner
     WHERE p.proname = 'maintain_interval_currency';
    IF fn_owner <> 'migration_admin' THEN
        RAISE EXCEPTION 'maintain_interval_currency owned by %, want migration_admin', fn_owner;
    END IF;
    IF NOT fn_definer THEN
        RAISE EXCEPTION 'maintain_interval_currency is not SECURITY DEFINER';
    END IF;
    IF fn_path IS DISTINCT FROM ARRAY['search_path=pg_catalog'] THEN
        RAISE EXCEPTION 'maintain_interval_currency search_path is %, want pg_catalog only', fn_path;
    END IF;
    IF strpos(fn_def, 'UPDATE public.attendance_interval') = 0 THEN
        RAISE EXCEPTION 'maintain_interval_currency does not schema-qualify its maintenance UPDATE';
    END IF;
    IF fn_def LIKE '%UPDATE attendance_interval%' THEN
        RAISE EXCEPTION 'maintain_interval_currency keeps an unqualified table reference';
    END IF;
    SELECT datacl::text INTO db_acl FROM pg_database WHERE datname = current_database();
    IF db_acl IS NULL THEN
        RAISE EXCEPTION 'database % has default ACL: PUBLIC keeps TEMPORARY', current_database();
    END IF;
    SELECT count(*) INTO bad
      FROM aclexplode((SELECT datacl FROM pg_database WHERE datname = current_database()))
           AS a(grantor, grantee, priv, grantable)
     WHERE a.grantee IN (0, (SELECT oid FROM pg_roles WHERE rolname = 'app'))
       AND a.priv = 'TEMPORARY';
    IF bad > 0 THEN
        RAISE EXCEPTION 'TEMPORARY still granted on database %', current_database();
    END IF;
END
$$;
-- +goose StatementEnd

-- Section C3. Supersession identity: a successor belongs to the
-- same employer and employee as its predecessor, never to itself, and
-- never into a lineage cycle. Same-employer is also a foreign key;
-- the trigger reports the violation before the FK with an explicit
-- message, and adds the employee, self-loop and cycle rules the keys
-- cannot express.
--
-- One-successor-per-original lives where each lineage needs it:
-- operator corrections serialize on the correction-group UNIQUE
-- (predecessor_interval_id); clock-source pairing splits legitimately
-- supersede one work interval with several rows and serialize on the
-- per-employee advisory lock instead. A blanket UNIQUE here would
-- forbid the splits, so there is none.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_interval_supersession() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    pred_ee uuid;
    hop uuid;
    depth INT := 0;
BEGIN
    IF NEW.id = NEW.supersedes_id THEN
        RAISE EXCEPTION 'interval % cannot supersede itself', NEW.id;
    END IF;
    SELECT i.employee_id INTO pred_ee
      FROM public.attendance_interval i
     WHERE i.employer_id = NEW.employer_id AND i.id = NEW.supersedes_id;
    IF pred_ee IS NULL THEN
        RAISE EXCEPTION 'superseded interval % does not exist in this employer', NEW.supersedes_id;
    END IF;
    IF pred_ee <> NEW.employee_id THEN
        RAISE EXCEPTION 'interval % cannot supersede interval % of another employee',
            NEW.id, NEW.supersedes_id;
    END IF;
    hop := NEW.supersedes_id;
    LOOP
        IF hop = NEW.id THEN
            RAISE EXCEPTION 'supersession cycle through interval %', NEW.id;
        END IF;
        SELECT i.supersedes_id INTO hop
          FROM public.attendance_interval i
         WHERE i.employer_id = NEW.employer_id AND i.id = hop;
        EXIT WHEN hop IS NULL;
        depth := depth + 1;
        IF depth > 32 THEN
            RAISE EXCEPTION 'supersession chain exceeds 32 hops at interval %', NEW.id;
        END IF;
    END LOOP;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER attendance_interval_supersession
    BEFORE INSERT OR UPDATE ON attendance_interval
    FOR EACH ROW WHEN (NEW.supersedes_id IS NOT NULL)
    EXECUTE FUNCTION check_interval_supersession();
-- +goose StatementEnd

ALTER FUNCTION check_interval_supersession() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_interval_supersession() FROM PUBLIC;

-- Section C4. The single-chain guard fires on UPDATE as well as
-- INSERT: re-pointing an allocation to another employee's or period's
-- chain is the same double-counting the INSERT path rejects. The
-- function reads only NEW, so widening the trigger event is the whole
-- change; employer/employee/period scope continues to ride the
-- composite foreign keys plus the employee-match trigger, which
-- already covers UPDATE.
DROP TRIGGER IF EXISTS timesheet_line_interval_single_chain ON timesheet_line_interval;
-- +goose StatementBegin
CREATE TRIGGER timesheet_line_interval_single_chain
    BEFORE INSERT OR UPDATE ON timesheet_line_interval
    FOR EACH ROW EXECUTE FUNCTION enforce_interval_single_chain();
-- +goose StatementEnd

-- Section C5. Timesheet line identity: once interval links exist,
-- the line's parent identity (sheet, employer, pay period) is frozen.
-- Draft regeneration deletes and re-inserts lines instead of moving
-- them, so no legitimate workflow re-parents a linked line. (This
-- trigger sorts before the freeze trigger; a decided-sheet transplant
-- reports the identity violation, which is the more specific fault.)
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_timesheet_line_identity() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.timesheet_id IS DISTINCT FROM OLD.timesheet_id
        OR NEW.employer_id IS DISTINCT FROM OLD.employer_id
        OR NEW.pay_period_id IS DISTINCT FROM OLD.pay_period_id THEN
        IF EXISTS (SELECT 1 FROM public.timesheet_line_interval li
                   WHERE li.employer_id = OLD.employer_id AND li.line_id = OLD.id) THEN
            RAISE EXCEPTION 'timesheet line % has interval links: parent identity is frozen', OLD.id;
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER timesheet_line_identity
    BEFORE UPDATE ON timesheet_line
    FOR EACH ROW EXECUTE FUNCTION check_timesheet_line_identity();
-- +goose StatementEnd

ALTER FUNCTION check_timesheet_line_identity() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_timesheet_line_identity() FROM PUBLIC;

-- Section C6. Timesheet identity: once lines link to a sheet, its
-- employer, employee and pay-period identity cannot change, in any
-- status, on any chain version. Status transitions never touch these
-- columns, so the freeze workflow is unaffected.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_timesheet_identity() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.employer_id IS DISTINCT FROM OLD.employer_id
        OR NEW.employee_id IS DISTINCT FROM OLD.employee_id
        OR NEW.pay_period_id IS DISTINCT FROM OLD.pay_period_id THEN
        IF EXISTS (SELECT 1 FROM public.timesheet_line l
                   WHERE l.employer_id = OLD.employer_id AND l.timesheet_id = OLD.id) THEN
            RAISE EXCEPTION 'timesheet % has linked lines: identity is frozen', OLD.id;
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER timesheet_identity
    BEFORE UPDATE ON timesheet
    FOR EACH ROW EXECUTE FUNCTION check_timesheet_identity();
-- +goose StatementEnd

ALTER FUNCTION check_timesheet_identity() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_timesheet_identity() FROM PUBLIC;

-- Section C7. Published-roster immutability at the parent row.
-- Publication metadata, once written, is history: only a fresh
-- publication re-stamps it, and a published roster returns to draft
-- exclusively through the revise shape (pointer N -> N+1 onto an
-- existing revision). The assignment freeze trigger keeps guarding
-- the child rows; this guard keeps the parent from silently
-- demoting them back into mutability.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_roster_transition() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    have_rev BOOLEAN;
BEGIN
    IF NEW.employer_id IS DISTINCT FROM OLD.employer_id
        OR NEW.programme_id IS DISTINCT FROM OLD.programme_id
        OR NEW.week_starting IS DISTINCT FROM OLD.week_starting THEN
        RAISE EXCEPTION 'roster % identity (employer, programme, week) is immutable', OLD.id;
    END IF;
    IF NEW.current_version < OLD.current_version THEN
        RAISE EXCEPTION 'roster % pointer cannot move backwards from version %', OLD.id, OLD.current_version;
    END IF;
    IF OLD.status = 'draft' AND NEW.status = 'published' THEN
        IF NEW.current_version <> OLD.current_version THEN
            RAISE EXCEPTION 'roster % cannot move the revision pointer while publishing', OLD.id;
        END IF;
        IF OLD.published_at IS NOT NULL AND NEW.published_at IS NULL THEN
            RAISE EXCEPTION 'roster % cannot clear its publication timestamp while publishing', OLD.id;
        END IF;
        IF OLD.published_by IS NOT NULL AND NEW.published_by IS NULL THEN
            RAISE EXCEPTION 'roster % cannot clear its publisher while publishing', OLD.id;
        END IF;
    ELSIF OLD.status = 'published' AND NEW.status = 'draft' THEN
        IF NEW.current_version <> OLD.current_version + 1 THEN
            RAISE EXCEPTION 'roster % cannot reopen: a new draft revision must advance the pointer by one', OLD.id;
        END IF;
        SELECT EXISTS (SELECT 1 FROM public.roster_revision rev
                       WHERE rev.employer_id = NEW.employer_id AND rev.roster_id = NEW.id
                         AND rev.version = NEW.current_version) INTO have_rev;
        IF NOT have_rev THEN
            RAISE EXCEPTION 'roster % cannot reopen to missing revision %', OLD.id, NEW.current_version;
        END IF;
        IF NEW.published_at IS DISTINCT FROM OLD.published_at
            OR NEW.published_by IS DISTINCT FROM OLD.published_by THEN
            RAISE EXCEPTION 'roster % cannot rewrite its publication stamps while revising', OLD.id;
        END IF;
    ELSIF OLD.status = NEW.status THEN
        IF NEW.status = 'published' THEN
            RAISE EXCEPTION 'roster % is published: the parent row is frozen', OLD.id;
        END IF;
        IF NEW.current_version <> OLD.current_version THEN
            RAISE EXCEPTION 'roster % cannot move the revision pointer on a draft', OLD.id;
        END IF;
        IF OLD.published_at IS NOT NULL AND NEW.published_at IS DISTINCT FROM OLD.published_at THEN
            RAISE EXCEPTION 'roster % publication timestamp is immutable', OLD.id;
        END IF;
        IF OLD.published_by IS NOT NULL AND NEW.published_by IS DISTINCT FROM OLD.published_by THEN
            RAISE EXCEPTION 'roster % publisher is immutable', OLD.id;
        END IF;
    ELSE
        RAISE EXCEPTION 'roster % has unknown status transition % -> %', OLD.id, OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER roster_transition
    BEFORE UPDATE ON roster
    FOR EACH ROW EXECUTE FUNCTION check_roster_transition();
-- +goose StatementEnd

ALTER FUNCTION check_roster_transition() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_roster_transition() FROM PUBLIC;

-- Section C8. Employment history is insert-only for the app role.
-- The two legitimate predecessor closures (open terms row, open
-- status row) run through narrowly scoped SECURITY DEFINER functions
-- that lock, validate, close and audit in one transaction. Direct
-- UPDATE/DELETE of employees, terms, status history, memberships and
-- skills is revoked: history can only advance, never be rewritten.
-- Compensation was already insert-only (B4). Programme skill
-- requirements keep UPDATE: they are current-state config (upserted
-- by INSERT ... ON CONFLICT), not effective-dated history.
--
-- created_at on employment_terms is trigger-normalized like
-- compensation: readiness detects post-approval changes by comparing
-- created_at to approved_at, so a backdated timestamp would dodge
-- the conflict detector.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_terms_created_at() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    NEW.created_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER employment_terms_created_at_normalize
    BEFORE INSERT OR UPDATE ON employment_terms
    FOR EACH ROW EXECUTE FUNCTION normalize_terms_created_at();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION close_employment_terms(
    p_employer uuid, p_employee uuid, p_successor_from date, p_actor text
) RETURNS date
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    prior_id uuid;
    prior_from date;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'close_employment_terms requires an actor';
    END IF;
    SELECT t.id, t.effective_from INTO prior_id, prior_from
      FROM public.employment_terms t
     WHERE t.employer_id = p_employer AND t.employee_id = p_employee
       AND t.effective_to IS NULL
     FOR UPDATE;
    IF prior_id IS NULL THEN
        RAISE EXCEPTION 'no open employment terms for employee %', p_employee;
    END IF;
    IF p_successor_from <= prior_from THEN
        RAISE EXCEPTION 'successor date % must be after the open row''s %', p_successor_from, prior_from;
    END IF;
    UPDATE public.employment_terms SET effective_to = p_successor_from - 1
     WHERE id = prior_id;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'terms.close', 'employment_terms', prior_id,
             jsonb_build_object('effective_to', NULL),
             jsonb_build_object('effective_to', (p_successor_from - 1)::text));
    RETURN p_successor_from - 1;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION transition_employee_status(
    p_employer uuid, p_employee uuid, p_status text, p_effective date, p_reason text, p_actor text
) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    cur_status text;
    cur_from date;
    prior_from date;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'transition_employee_status requires an actor';
    END IF;
    IF p_reason IS NULL OR length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'status change requires a reason';
    END IF;
    IF p_status NOT IN ('active', 'suspended', 'resigned', 'terminated') THEN
        RAISE EXCEPTION 'unknown status %', p_status;
    END IF;
    SELECT e.status, e.status_effective_date INTO cur_status, cur_from
      FROM public.employee e
     WHERE e.employer_id = p_employer AND e.id = p_employee
     FOR UPDATE;
    IF cur_status IS NULL THEN
        RAISE EXCEPTION 'employee % not found', p_employee;
    END IF;
    IF cur_status = p_status THEN
        RAISE EXCEPTION 'employee is already %', p_status;
    END IF;
    IF NOT ((cur_status = 'active' AND p_status IN ('suspended', 'resigned', 'terminated'))
         OR (cur_status = 'suspended' AND p_status IN ('active', 'resigned', 'terminated'))) THEN
        RAISE EXCEPTION 'status transition % -> % is not allowed', cur_status, p_status;
    END IF;
    SELECT h.effective_from INTO prior_from
      FROM public.employee_status_history h
     WHERE h.employer_id = p_employer AND h.employee_id = p_employee
       AND h.effective_to IS NULL
     FOR UPDATE;
    IF prior_from IS NOT NULL AND p_effective <= prior_from THEN
        RAISE EXCEPTION 'effective date % must be after the current row''s %', p_effective, prior_from;
    END IF;
    UPDATE public.employee SET status = p_status, status_effective_date = p_effective
     WHERE employer_id = p_employer AND id = p_employee;
    UPDATE public.employee_status_history SET effective_to = p_effective - 1
     WHERE employer_id = p_employer AND employee_id = p_employee AND effective_to IS NULL;
    INSERT INTO public.employee_status_history (employer_id, employee_id, status, effective_from, reason)
     VALUES (p_employer, p_employee, p_status, p_effective, p_reason);
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'employee.status', 'employee', p_employee,
             jsonb_build_object('status', cur_status, 'from', cur_from::text),
             jsonb_build_object('status', p_status, 'from', p_effective::text, 'reason', p_reason));
END
$$;
-- +goose StatementEnd

ALTER FUNCTION normalize_terms_created_at() OWNER TO migration_admin;
ALTER FUNCTION close_employment_terms(uuid, uuid, date, text) OWNER TO migration_admin;
ALTER FUNCTION transition_employee_status(uuid, uuid, text, date, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION normalize_terms_created_at() FROM PUBLIC;
REVOKE ALL ON FUNCTION close_employment_terms(uuid, uuid, date, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION transition_employee_status(uuid, uuid, text, date, text, text) FROM PUBLIC;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        REVOKE UPDATE, DELETE ON employee, employment_terms, employee_status_history,
            team_membership, employee_skill FROM app;
        GRANT EXECUTE ON FUNCTION close_employment_terms(uuid, uuid, date, text) TO app;
        GRANT EXECUTE ON FUNCTION transition_employee_status(uuid, uuid, text, date, text, text) TO app;
    END IF;
END
$$;
-- +goose StatementEnd

-- Verify loudly: ownership, SECURITY DEFINER, restricted search
-- path, qualification, execute grants, and effective revokes.
-- +goose StatementBegin
DO $$
DECLARE
    fn TEXT;
    fn_owner TEXT;
    fn_definer BOOLEAN;
    fn_path TEXT[];
    fn_def TEXT;
BEGIN
    FOREACH fn IN ARRAY ARRAY['close_employment_terms', 'transition_employee_status'] LOOP
        SELECT u.rolname, p.prosecdef, p.proconfig, pg_get_functiondef(p.oid)
          INTO fn_owner, fn_definer, fn_path, fn_def
          FROM pg_proc p JOIN pg_roles u ON u.oid = p.proowner
         WHERE p.proname = fn;
        IF fn_owner <> 'migration_admin' THEN
            RAISE EXCEPTION '% owned by %, want migration_admin', fn, fn_owner;
        END IF;
        IF NOT fn_definer THEN
            RAISE EXCEPTION '% is not SECURITY DEFINER', fn;
        END IF;
        IF fn_path IS DISTINCT FROM ARRAY['search_path=pg_catalog'] THEN
            RAISE EXCEPTION '% search_path is %, want pg_catalog only', fn, fn_path;
        END IF;
        IF fn_def LIKE '%FROM employment_terms %' OR fn_def LIKE '%FROM employee %'
            OR fn_def LIKE '%UPDATE employment_terms %' OR fn_def LIKE '%UPDATE employee %'
            OR fn_def LIKE '%INTO audit_event %' OR fn_def LIKE '%INTO employee_status_history %' THEN
            RAISE EXCEPTION '% keeps an unqualified table reference', fn;
        END IF;
        IF NOT has_function_privilege('app', fn || '(' ||
            CASE WHEN fn = 'close_employment_terms' THEN 'uuid,uuid,date,text'
                 ELSE 'uuid,uuid,text,date,text,text' END || ')', 'EXECUTE') THEN
            RAISE EXCEPTION 'app lacks EXECUTE on %', fn;
        END IF;
    END LOOP;
    IF EXISTS (SELECT 1 FROM information_schema.role_table_grants
               WHERE grantee = 'app' AND privilege_type IN ('UPDATE', 'DELETE')
               AND table_name IN ('employee', 'employment_terms', 'employee_status_history',
                   'team_membership', 'employee_skill')) THEN
        RAISE EXCEPTION 'C8 insert-only revokes did not take effect for role app';
    END IF;
END
$$;
-- +goose StatementEnd

-- Section C9. Uniform EXECUTE hygiene: the B1/B4 trigger functions
-- predate the hardening sections and still carry the default PUBLIC
-- EXECUTE grant. Trigger firing does not check EXECUTE, so revoking
-- it from PUBLIC changes nothing for the triggers while closing
-- direct invocation by arbitrary roles.
REVOKE ALL ON FUNCTION check_clock_event_disposition() FROM PUBLIC;
REVOKE ALL ON FUNCTION normalize_compensation_created_at() FROM PUBLIC;

-- +goose Down

-- C9 rollback restores the default PUBLIC EXECUTE on the two B-stage
-- trigger functions above (the C1-C8 function revokes ride with
-- their own dropped functions and need no restore).
GRANT EXECUTE ON FUNCTION check_clock_event_disposition() TO PUBLIC;
GRANT EXECUTE ON FUNCTION normalize_compensation_created_at() TO PUBLIC;

-- C8 rollback: the controlled functions go away and direct
-- UPDATE/DELETE returns to the app role.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        GRANT UPDATE, DELETE ON employee, employment_terms, employee_status_history,
            team_membership, employee_skill TO app;
    END IF;
END
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS employment_terms_created_at_normalize ON employment_terms;
DROP FUNCTION IF EXISTS normalize_terms_created_at();
DROP FUNCTION IF EXISTS transition_employee_status(uuid, uuid, text, date, text, text);
DROP FUNCTION IF EXISTS close_employment_terms(uuid, uuid, date, text);
DROP TRIGGER IF EXISTS roster_transition ON roster;
DROP FUNCTION IF EXISTS check_roster_transition();
DROP TRIGGER IF EXISTS timesheet_identity ON timesheet;
DROP FUNCTION IF EXISTS check_timesheet_identity();
DROP TRIGGER IF EXISTS timesheet_line_identity ON timesheet_line;
DROP FUNCTION IF EXISTS check_timesheet_line_identity();
DROP TRIGGER IF EXISTS timesheet_line_interval_single_chain ON timesheet_line_interval;
-- +goose StatementBegin
CREATE TRIGGER timesheet_line_interval_single_chain
    BEFORE INSERT ON timesheet_line_interval
    FOR EACH ROW EXECUTE FUNCTION enforce_interval_single_chain();
-- +goose StatementEnd
DROP TRIGGER IF EXISTS attendance_interval_supersession ON attendance_interval;
DROP FUNCTION IF EXISTS check_interval_supersession();

-- C1/C2 rollback restores the 00009 maintenance behavior:
-- conditional trigger and public search path. (TEMPORARY stays
-- revoked: provisioning owns the database ACL, and re-granting it on
-- rollback would silently reopen the pg_temp hole.)
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
DROP TRIGGER IF EXISTS attendance_interval_currency ON attendance_interval;
-- +goose StatementBegin
CREATE TRIGGER attendance_interval_currency
    BEFORE INSERT ON attendance_interval
    FOR EACH ROW WHEN (NEW.supersedes_id IS NOT NULL)
    EXECUTE FUNCTION maintain_interval_currency();
-- +goose StatementEnd

ALTER TABLE attendance_record DROP COLUMN IF EXISTS paid_us;
ALTER TABLE attendance_record DROP COLUMN IF EXISTS actual_us;
ALTER TABLE timesheet_line DROP COLUMN IF EXISTS hours_us;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        GRANT UPDATE, DELETE ON compensation_line TO app;
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS compensation_created_at_normalize ON compensation_line;
DROP FUNCTION IF EXISTS normalize_compensation_created_at();

ALTER TABLE employment_terms DROP CONSTRAINT IF EXISTS employment_terms_rest_day_canonical;

-- Rolling back with multi-segment groups present fails loudly here
-- instead of silently discarding siblings: resolve the groups first.
CREATE UNIQUE INDEX attendance_interval_single_correction_idx ON attendance_interval (supersedes_id)
    WHERE source = 'correction' AND supersedes_id IS NOT NULL;
ALTER TABLE attendance_interval DROP CONSTRAINT IF EXISTS attendance_interval_correction_group_present;
ALTER TABLE attendance_interval DROP CONSTRAINT IF EXISTS attendance_interval_correction_group_member_fk;
ALTER TABLE attendance_interval DROP COLUMN IF EXISTS correction_group_id;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        GRANT UPDATE, DELETE ON attendance_correction_group TO app;
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE IF EXISTS attendance_correction_group;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        GRANT UPDATE, DELETE ON clock_event_disposition TO app;
    END IF;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS clock_event_disposition_match ON clock_event_disposition;
DROP FUNCTION IF EXISTS check_clock_event_disposition();
DROP TABLE IF EXISTS clock_event_disposition;
