-- WorkforceOps Lab M1.3: final payroll-input integrity closure.
--
-- Single forward migration for Prompt 13 (Milestone 1.3) as amended by
-- plan_prompt13_review.md (Amendments A-D). Sections land in execution
-- order: the shared payroll-input barrier first (Amendment A / E1),
-- then correction groups (A), compensation lifecycle (E2-E5),
-- disposition concurrency (C), evidence timestamps (D) and privilege
-- hygiene (G/H6). The Down section is guarded forward-only (Stage F):
-- it fails before destructive changes whenever populated history
-- depends on this migration's objects.

-- +goose Up

-- Preconditions mirror 00009: migrate as migration_admin with the
-- application role present, so every GRANT below takes effect loudly.
-- +goose StatementBegin
DO $$
BEGIN
    IF current_user <> 'migration_admin' THEN
        RAISE EXCEPTION 'migrations must run as migration_admin, not %', current_user;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        RAISE EXCEPTION 'role app is missing: provision roles before migrating';
    END IF;
END
$$;
-- +goose StatementEnd

-- Section E1/A. Shared payroll-input barrier (Amendment A, D-RACE).
--
-- Transaction-start timestamps (now()) cannot order concurrent
-- transactions: a mutation that starts before approval but commits
-- after it carries an earlier created_at and evades every
-- created_at > approved_at conflict check. The barrier has two parts:
--
-- 1. A single transaction-scoped advisory lock per employee, taken
--    through exactly one SQL helper by Go services, triggers and
--    controlled functions alike. Whoever holds the lock commits
--    before the waiter proceeds. (Rejected alternative: comparing
--    post-lock clock_timestamp() values — a transaction that started
--    first but commits second still compares wrong, and ties, while
--    implausible across a lock handoff, are not impossible.)
-- 2. A per-employee lock-serialized basis version (pay_basis_seq):
--    evidence writes and approval transitions take nextval only
--    after acquiring the lock, so sequence order equals commit order
--    within one employee. The sequence object is global for
--    mechanics, but values are only ever compared within a single
--    employee: it is NOT a claim about global commit order, and
--    gaps from rolled-back transactions are meaningless and fine.
--
-- Readiness treats a row as post-approval when EITHER its basis_seq
-- or its created_at is newer than the approval's. The created_at leg
-- preserves behavior for pre-migration rows (backfilled seq 0: real
-- historical ordering is unknown and never fabricated); the seq leg
-- is exact for everything written under the barrier.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION acquire_payroll_input_lock(p_employer uuid, p_employee uuid)
RETURNS void
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pay-input:' || p_employer::text || ':' || p_employee::text));
END
$$;
-- +goose StatementEnd

ALTER FUNCTION acquire_payroll_input_lock(uuid, uuid) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION acquire_payroll_input_lock(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION acquire_payroll_input_lock(uuid, uuid) TO app;

CREATE SEQUENCE pay_basis_seq AS bigint START 1;
ALTER SEQUENCE pay_basis_seq OWNER TO migration_admin;
REVOKE ALL ON SEQUENCE pay_basis_seq FROM PUBLIC;
GRANT USAGE, SELECT ON SEQUENCE pay_basis_seq TO app;

-- Legacy rows keep seq 0 (unknown historical order, never fabricated).
ALTER TABLE timesheet ADD COLUMN basis_seq BIGINT NOT NULL DEFAULT 0 CHECK (basis_seq >= 0);
ALTER TABLE compensation_line ADD COLUMN basis_seq BIGINT NOT NULL DEFAULT 0 CHECK (basis_seq >= 0);
ALTER TABLE employment_terms ADD COLUMN basis_seq BIGINT NOT NULL DEFAULT 0 CHECK (basis_seq >= 0);
ALTER TABLE attendance_interval ADD COLUMN basis_seq BIGINT NOT NULL DEFAULT 0 CHECK (basis_seq >= 0);
ALTER TABLE clock_event ADD COLUMN basis_seq BIGINT NOT NULL DEFAULT 0 CHECK (basis_seq >= 0);

-- Evidence rows take the barrier lock and a basis version at insert,
-- whether written by a service or by direct app SQL. basis_seq is
-- system-assigned: callers omit it (reject, don't coerce) and no
-- UPDATE may change it afterwards.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assign_pay_basis_seq() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_employee uuid;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.basis_seq IS DISTINCT FROM OLD.basis_seq THEN
            RAISE EXCEPTION '% basis_seq is system-assigned and immutable', TG_TABLE_NAME;
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.basis_seq IS DISTINCT FROM 0 THEN
        RAISE EXCEPTION '% basis_seq is system-assigned: omit it on INSERT', TG_TABLE_NAME;
    END IF;
    -- compensation_line keys its employee through the parent terms
    -- (E2 denormalizes this; the lock key is identical either way).
    IF TG_TABLE_NAME = 'compensation_line' THEN
        SELECT t.employee_id INTO v_employee
          FROM public.employment_terms t
         WHERE t.employer_id = NEW.employer_id AND t.id = NEW.terms_id;
        IF v_employee IS NULL THEN
            RAISE EXCEPTION 'compensation parent terms % missing in this employer', NEW.terms_id;
        END IF;
    ELSE
        v_employee := NEW.employee_id;
    END IF;
    PERFORM public.acquire_payroll_input_lock(NEW.employer_id, v_employee);
    NEW.basis_seq := nextval('public.pay_basis_seq');
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER compensation_line_basis_seq
    BEFORE INSERT OR UPDATE ON compensation_line
    FOR EACH ROW EXECUTE FUNCTION assign_pay_basis_seq();
CREATE TRIGGER employment_terms_basis_seq
    BEFORE INSERT OR UPDATE ON employment_terms
    FOR EACH ROW EXECUTE FUNCTION assign_pay_basis_seq();
CREATE TRIGGER attendance_interval_basis_seq
    BEFORE INSERT OR UPDATE ON attendance_interval
    FOR EACH ROW EXECUTE FUNCTION assign_pay_basis_seq();
CREATE TRIGGER clock_event_basis_seq
    BEFORE INSERT OR UPDATE ON clock_event
    FOR EACH ROW EXECUTE FUNCTION assign_pay_basis_seq();

-- Approval assigns its own basis version under the same lock, so a
-- direct-SQL approval cannot bypass the Go service's locking step.
-- Approving from any prior status (the service requires submitted;
-- the barrier does not reimplement the state machine) orders against
-- concurrent evidence writes all the same.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_timesheet_approval_barrier() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status = 'approved' THEN
            PERFORM public.acquire_payroll_input_lock(NEW.employer_id, NEW.employee_id);
            NEW.basis_seq := nextval('public.pay_basis_seq');
        ELSIF NEW.basis_seq IS DISTINCT FROM 0 THEN
            RAISE EXCEPTION 'timesheet basis_seq is assigned by approval only';
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.status IS DISTINCT FROM 'approved' AND NEW.status = 'approved' THEN
        PERFORM public.acquire_payroll_input_lock(NEW.employer_id, NEW.employee_id);
        NEW.basis_seq := nextval('public.pay_basis_seq');
        RETURN NEW;
    END IF;
    IF NEW.basis_seq IS DISTINCT FROM OLD.basis_seq THEN
        RAISE EXCEPTION 'timesheet basis_seq is assigned by approval only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timesheet_approval_barrier
    BEFORE INSERT OR UPDATE ON timesheet
    FOR EACH ROW EXECUTE FUNCTION check_timesheet_approval_barrier();

ALTER FUNCTION assign_pay_basis_seq() OWNER TO migration_admin;
ALTER FUNCTION check_timesheet_approval_barrier() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION assign_pay_basis_seq() FROM PUBLIC;
REVOKE ALL ON FUNCTION check_timesheet_approval_barrier() FROM PUBLIC;

-- Verify loudly: ownership, invoker status, restricted search path,
-- schema-qualified references, execute grants, and sequence ACL.
-- +goose StatementBegin
DO $$
DECLARE
    fn TEXT;
    fn_owner TEXT;
    fn_definer BOOLEAN;
    fn_path TEXT[];
    fn_def TEXT;
    app_exec BOOLEAN;
BEGIN
    FOREACH fn IN ARRAY ARRAY['acquire_payroll_input_lock', 'assign_pay_basis_seq', 'check_timesheet_approval_barrier'] LOOP
        SELECT u.rolname, p.prosecdef, p.proconfig, pg_get_functiondef(p.oid)
          INTO fn_owner, fn_definer, fn_path, fn_def
          FROM pg_proc p JOIN pg_roles u ON u.oid = p.proowner
         WHERE p.proname = fn;
        IF fn_owner <> 'migration_admin' THEN
            RAISE EXCEPTION '% owned by %, want migration_admin', fn, fn_owner;
        END IF;
        IF fn_definer THEN
            RAISE EXCEPTION '% must be SECURITY INVOKER (lock + nextval need no privilege)', fn;
        END IF;
        IF fn_path IS DISTINCT FROM ARRAY['search_path=pg_catalog'] THEN
            RAISE EXCEPTION '% search_path is %, want pg_catalog only', fn, fn_path;
        END IF;
        IF fn <> 'acquire_payroll_input_lock' THEN
            IF strpos(fn_def, 'public.acquire_payroll_input_lock') = 0
               OR strpos(fn_def, 'public.pay_basis_seq') = 0 THEN
                RAISE EXCEPTION '% does not schema-qualify the lock helper and sequence', fn;
            END IF;
        END IF;
        SELECT has_function_privilege('app', p.oid, 'EXECUTE') INTO app_exec
          FROM pg_proc p WHERE p.proname = fn;
        IF fn = 'acquire_payroll_input_lock' AND NOT app_exec THEN
            RAISE EXCEPTION 'app cannot EXECUTE the lock helper';
        END IF;
        IF fn <> 'acquire_payroll_input_lock' AND app_exec THEN
            RAISE EXCEPTION 'app must not EXECUTE trigger-only % directly', fn;
        END IF;
    END LOOP;
    IF NOT has_sequence_privilege('app', 'public.pay_basis_seq', 'USAGE') THEN
        RAISE EXCEPTION 'app lacks USAGE on pay_basis_seq';
    END IF;
END
$$;
-- +goose StatementEnd

-- Employee-status changes join the barrier: a population change and
-- an approval must not cross unnoticed. Body is the 00010 definition
-- with the shared lock acquired before the first row lock (advisory
-- first is the deadlock-free order used everywhere). Status rows
-- carry no basis_seq: no readiness comparison consumes them —
-- population is enforced at generation and gating points, and the
-- lock alone establishes the ordering. (close_employment_terms gains
-- its lock together with the E2 child-bounds check below.)
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
    PERFORM public.acquire_payroll_input_lock(p_employer, p_employee);
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

-- Section A. Correction-group lineage (A1/A2/A3, Amendment B).
--
-- A correction group supersedes a SET of predecessor intervals: one
-- for first corrections, the complete current sibling set for
-- re-corrections. The single predecessor_interval_id column cannot
-- represent the second case, so predecessors move to a link table
-- (backfilled, then the column drops with its id-only FK and UNIQUE).
-- Uniqueness is group-level — UNIQUE(predecessor_interval_id) on the
-- link table — never per-interval on supersedes_id: sibling segments
-- legitimately share one predecessor, and clock pairing legitimately
-- supersedes one work interval with several rows. The (employer, id)
-- key already holds by primary key; the explicit UNIQUE exists as the
-- link table's foreign-key target.
ALTER TABLE attendance_correction_group
    ADD CONSTRAINT attendance_correction_group_employer_id_key UNIQUE (employer_id, id);
CREATE TABLE attendance_correction_group_predecessor (
    employer_id uuid NOT NULL,
    group_id uuid NOT NULL,
    predecessor_interval_id uuid NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, predecessor_interval_id),
    FOREIGN KEY (employer_id, group_id)
        REFERENCES attendance_correction_group (employer_id, id),
    FOREIGN KEY (employer_id, predecessor_interval_id)
        REFERENCES attendance_interval (employer_id, id),
    UNIQUE (predecessor_interval_id)
);
ALTER TABLE attendance_correction_group_predecessor OWNER TO migration_admin;

-- A1-F4: committed groups and their bindings are frozen. INSERT stays
-- for creation; UPDATE/DELETE are revoked (segment rows were already
-- frozen, which the matrix re-proves).
REVOKE UPDATE, DELETE ON attendance_correction_group FROM app;
REVOKE UPDATE, DELETE ON attendance_correction_group_predecessor FROM app;

INSERT INTO attendance_correction_group_predecessor (employer_id, group_id, predecessor_interval_id)
SELECT employer_id, id, predecessor_interval_id FROM attendance_correction_group;
ALTER TABLE attendance_correction_group DROP COLUMN predecessor_interval_id;

-- BEFORE: links and segments bind only within the group's creating
-- transaction (same-xid rule). No group is ever appended to after
-- its lifecycle closes, whatever the statement order inside the
-- creating transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_correction_group_open() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_group uuid;
    v_xmin text;
BEGIN
    IF TG_TABLE_NAME = 'attendance_correction_group_predecessor' THEN
        v_group := NEW.group_id;
    ELSE
        v_group := NEW.correction_group_id;
    END IF;
    -- xmin is xid while pg_current_xact_id() is xid8: compare the
    -- canonical text forms (no xid = xid8 operator exists).
    SELECT g.xmin::text INTO v_xmin
      FROM public.attendance_correction_group g
     WHERE g.employer_id = NEW.employer_id AND g.id = v_group;
    -- Missing group: the foreign key reports; nothing to check here.
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF v_xmin IS DISTINCT FROM pg_current_xact_id()::text THEN
        RAISE EXCEPTION 'correction group % is closed to new rows: open a new correction instead', v_group;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER correction_group_predecessor_open
    BEFORE INSERT ON attendance_correction_group_predecessor
    FOR EACH ROW EXECUTE FUNCTION check_correction_group_open();
CREATE TRIGGER correction_group_segment_open
    BEFORE INSERT ON attendance_interval
    FOR EACH ROW WHEN (NEW.source = 'correction' AND NEW.correction_group_id IS NOT NULL)
    EXECUTE FUNCTION check_correction_group_open();

-- DEFERRED: every group created in the transaction is complete and
-- coherent at commit, regardless of insert order. Checks: non-empty
-- predecessor set and segment set; uniform segment kind; binding in
-- both directions (every segment supersedes a member, every member
-- is superseded); employer/employee scope; no self-membership; every
-- predecessor current at insert (nothing outside this group
-- supersedes it — the check that also rejects concurrent double
-- creation alongside the UNIQUE); sibling completeness for
-- re-corrections (a predecessor that is itself a group segment
-- requires the whole current sibling set).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_correction_group_complete() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_pred int;
    v_seg int;
    v_kinds int;
    v_stray int;
    v_orph int;
    v_scope int;
    v_self int;
    v_hist int;
    v_sib int;
BEGIN
    SELECT count(*) INTO v_pred
      FROM public.attendance_correction_group_predecessor l WHERE l.group_id = NEW.id;
    SELECT count(*) INTO v_seg
      FROM public.attendance_interval i WHERE i.correction_group_id = NEW.id;
    IF v_pred = 0 OR v_seg = 0 THEN
        RAISE EXCEPTION 'correction group % incomplete: % predecessors, % segments (want both >= 1)',
            NEW.id, v_pred, v_seg;
    END IF;
    SELECT count(*) INTO v_kinds
      FROM (SELECT DISTINCT kind FROM public.attendance_interval WHERE correction_group_id = NEW.id) k;
    IF v_kinds <> 1 THEN
        RAISE EXCEPTION 'correction group % mixes % segment kinds (want exactly 1)', NEW.id, v_kinds;
    END IF;
    SELECT count(*) INTO v_stray
      FROM public.attendance_interval i
     WHERE i.correction_group_id = NEW.id
       AND NOT EXISTS (SELECT 1 FROM public.attendance_correction_group_predecessor l
                        WHERE l.group_id = NEW.id AND l.predecessor_interval_id = i.supersedes_id);
    IF v_stray > 0 THEN
        RAISE EXCEPTION 'correction group % has % segments superseding outside its predecessor set', NEW.id, v_stray;
    END IF;
    SELECT count(*) INTO v_orph
      FROM public.attendance_correction_group_predecessor l
     WHERE l.group_id = NEW.id
       AND NOT EXISTS (SELECT 1 FROM public.attendance_interval i
                        WHERE i.correction_group_id = NEW.id AND i.supersedes_id = l.predecessor_interval_id);
    IF v_orph > 0 THEN
        RAISE EXCEPTION 'correction group % leaves % predecessors without a successor segment', NEW.id, v_orph;
    END IF;
    SELECT count(*) INTO v_scope
      FROM public.attendance_correction_group_predecessor l
      JOIN public.attendance_interval v ON v.id = l.predecessor_interval_id
     WHERE l.group_id = NEW.id
       AND (v.employer_id IS DISTINCT FROM NEW.employer_id OR v.employee_id IS DISTINCT FROM NEW.employee_id);
    SELECT count(*) INTO v_self
      FROM public.attendance_interval i
     WHERE i.correction_group_id = NEW.id
       AND (i.employer_id IS DISTINCT FROM NEW.employer_id OR i.employee_id IS DISTINCT FROM NEW.employee_id);
    IF v_scope > 0 OR v_self > 0 THEN
        RAISE EXCEPTION 'correction group % mixes employers or employees', NEW.id;
    END IF;
    SELECT count(*) INTO v_self
      FROM public.attendance_correction_group_predecessor l
     WHERE l.group_id = NEW.id
       AND EXISTS (SELECT 1 FROM public.attendance_interval i
                    WHERE i.correction_group_id = NEW.id AND i.id = l.predecessor_interval_id);
    IF v_self > 0 THEN
        RAISE EXCEPTION 'correction group % lists its own segment as a predecessor', NEW.id;
    END IF;
    SELECT count(*) INTO v_hist
      FROM public.attendance_interval s
     WHERE EXISTS (SELECT 1 FROM public.attendance_correction_group_predecessor l
                    WHERE l.group_id = NEW.id AND l.predecessor_interval_id = s.supersedes_id)
       AND s.correction_group_id IS DISTINCT FROM NEW.id;
    IF v_hist > 0 THEN
        RAISE EXCEPTION 'correction group % targets a predecessor already superseded outside the group', NEW.id;
    END IF;
    SELECT count(*) INTO v_sib
      FROM public.attendance_correction_group_predecessor l
      JOIN public.attendance_interval p ON p.id = l.predecessor_interval_id
      JOIN public.attendance_interval s ON s.correction_group_id = p.correction_group_id
     WHERE l.group_id = NEW.id
       AND p.correction_group_id IS NOT NULL
       AND s.is_current
       AND NOT EXISTS (SELECT 1 FROM public.attendance_correction_group_predecessor l2
                        WHERE l2.group_id = NEW.id AND l2.predecessor_interval_id = s.id);
    IF v_sib > 0 THEN
        RAISE EXCEPTION 'correction group % is a partial re-correction: % current siblings missing', NEW.id, v_sib;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER correction_group_complete
    AFTER INSERT ON attendance_correction_group
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_correction_group_complete();

ALTER FUNCTION check_correction_group_open() OWNER TO migration_admin;
ALTER FUNCTION check_correction_group_complete() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_correction_group_open() FROM PUBLIC;
REVOKE ALL ON FUNCTION check_correction_group_complete() FROM PUBLIC;

-- A3: business-date attribution is employment-local and
-- trigger-pinned. The zone maps from the EMPLOYER's jurisdiction,
-- never from the vendor-supplied row timezone; unknown jurisdictions
-- fail loudly instead of guessing. Rule mirrors splitRun exactly:
-- local_date is the employment date of interval_start, and the end
-- is same-date or exactly local midnight (the full-day and
-- midnight-boundary shapes the service emits).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION employment_tz(p_jurisdiction text) RETURNS text
    LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog AS $$
BEGIN
    CASE p_jurisdiction
        WHEN 'SG' THEN RETURN 'Asia/Singapore';
        ELSE RAISE EXCEPTION 'unknown employment jurisdiction %: register its zone explicitly', p_jurisdiction;
    END CASE;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION employment_tz(text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION employment_tz(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION employment_tz(text) TO app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_interval_local_date() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_jur text;
    v_tz text;
    v_start date;
    v_end date;
BEGIN
    SELECT e.jurisdiction INTO v_jur FROM public.employer e WHERE e.id = NEW.employer_id;
    -- Missing employer: the foreign key reports; nothing to check here.
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    v_tz := public.employment_tz(v_jur);
    v_start := (NEW.interval_start AT TIME ZONE v_tz)::date;
    v_end := (NEW.interval_end AT TIME ZONE v_tz)::date;
    IF v_start IS DISTINCT FROM NEW.local_date THEN
        RAISE EXCEPTION 'interval local_date % does not match employment-local start date %', NEW.local_date, v_start;
    END IF;
    IF NOT (v_end = NEW.local_date
            OR (v_end = NEW.local_date + 1 AND (NEW.interval_end AT TIME ZONE v_tz)::time = time '00:00:00')) THEN
        RAISE EXCEPTION 'interval end % is neither same-date nor exact employment-local midnight', NEW.interval_end;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER attendance_interval_local_date
    BEFORE INSERT OR UPDATE ON attendance_interval
    FOR EACH ROW EXECUTE FUNCTION check_interval_local_date();

ALTER FUNCTION check_interval_local_date() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_interval_local_date() FROM PUBLIC;

-- Verify loudly: link table shape and frozen ACLs, group frozen
-- ACLs, old column gone, function ownership/paths/grants.
-- +goose StatementBegin
DO $$
DECLARE
    fn TEXT;
    fn_path TEXT[];
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
                WHERE table_schema = 'public' AND table_name = 'attendance_correction_group'
                  AND column_name = 'predecessor_interval_id') THEN
        RAISE EXCEPTION 'predecessor_interval_id column still present';
    END IF;
    IF has_table_privilege('app', 'public.attendance_correction_group', 'UPDATE')
       OR has_table_privilege('app', 'public.attendance_correction_group', 'DELETE') THEN
        RAISE EXCEPTION 'app can still mutate correction groups';
    END IF;
    IF NOT has_table_privilege('app', 'public.attendance_correction_group', 'INSERT') THEN
        RAISE EXCEPTION 'app lost INSERT on correction groups';
    END IF;
    IF NOT has_table_privilege('app', 'public.attendance_correction_group_predecessor', 'INSERT')
       OR has_table_privilege('app', 'public.attendance_correction_group_predecessor', 'UPDATE')
       OR has_table_privilege('app', 'public.attendance_correction_group_predecessor', 'DELETE') THEN
        RAISE EXCEPTION 'link table ACL is not insert-only for app';
    END IF;
    FOR fn IN SELECT 'check_correction_group_open' UNION ALL SELECT 'check_correction_group_complete'
               UNION ALL SELECT 'check_interval_local_date' UNION ALL SELECT 'employment_tz' LOOP
        SELECT p.proconfig INTO fn_path FROM pg_proc p WHERE p.proname = fn;
        IF fn_path IS DISTINCT FROM ARRAY['search_path=pg_catalog'] THEN
            RAISE EXCEPTION '% search_path is %, want pg_catalog only', fn, fn_path;
        END IF;
    END LOOP;
    IF NOT has_function_privilege('app', 'public.employment_tz(text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE employment_tz';
    END IF;
END
$$;
-- +goose StatementEnd

-- Stage E2–E5: compensation lifecycle (D-CONTAIN, D-ROUND, D-AUDIT,
-- D-NOCORRECT). All writes route through two SECURITY DEFINER
-- functions; raw INSERT is revoked from app so no unaudited,
-- uncontained, or silently rounded row can reach the table.

-- E3: exact cents, rejected rather than rounded. NUMERIC(12,2) would
-- round 199.999 to 200.00 before any CHECK sees it; unconstrained
-- NUMERIC plus an exact-scale CHECK keeps the input honest. The
-- 10-integer-digit cap matches the service's validateMoney.
ALTER TABLE compensation_line ALTER COLUMN amount_monthly TYPE NUMERIC;
ALTER TABLE compensation_line ADD CONSTRAINT compensation_line_amount_exact
    CHECK (amount_monthly >= 0
           AND amount_monthly = trunc(amount_monthly, 2)
           AND amount_monthly < 10000000000);
ALTER TABLE employment_terms ALTER COLUMN monthly_basic TYPE NUMERIC;
ALTER TABLE employment_terms ADD CONSTRAINT employment_terms_basic_exact
    CHECK (monthly_basic >= 0
           AND monthly_basic = trunc(monthly_basic, 2)
           AND monthly_basic < 10000000000);

-- E2: the employee key denormalized (backfilled from the parent
-- terms), the successor link, one successor per original.
ALTER TABLE compensation_line ADD COLUMN employee_id uuid;
UPDATE compensation_line c SET employee_id = t.employee_id
  FROM employment_terms t
 WHERE t.employer_id = c.employer_id AND t.id = c.terms_id;
ALTER TABLE compensation_line ALTER COLUMN employee_id SET NOT NULL;
ALTER TABLE compensation_line ADD CONSTRAINT compensation_line_employee_fk
    FOREIGN KEY (employer_id, employee_id) REFERENCES employee (employer_id, id);
ALTER TABLE compensation_line ADD COLUMN supersedes_id uuid;
ALTER TABLE compensation_line ADD CONSTRAINT compensation_line_successor_fk
    FOREIGN KEY (employer_id, supersedes_id) REFERENCES compensation_line (employer_id, id);
ALTER TABLE compensation_line ADD CONSTRAINT compensation_line_single_successor
    UNIQUE (supersedes_id);

-- E2: same-code overlap is an employee-level invariant, not a
-- per-terms one — the 00004 exclusion is dropped, not narrowed. An
-- exclusion cannot express liveness: a superseded original keeps
-- supersedes_id NULL, so any exclusion still covering it would
-- reject legitimate later rows in a narrowed-away range. Overlap is
-- validated by trigger instead, for originals and successors alike,
-- serialized by the employee lock both functions hold (raw SQL is
-- revoked, so every writer takes it).
ALTER TABLE compensation_line DROP CONSTRAINT compensation_line_no_overlap;

-- BEFORE: containment within the parent terms, the successor
-- contract (classification preserved), and live-basis overlap (no
-- collision with any not-yet-superseded line but the direct
-- predecessor). Fires on INSERT only: the table is insert-only for
-- every writer, services included.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_compensation_line() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_terms_employee uuid;
    v_terms_from date;
    v_terms_to date;
    o_employer uuid;
    o_employee uuid;
    o_terms uuid;
    o_kind text;
    o_code text;
    v_clash uuid;
BEGIN
    SELECT t.employee_id, t.effective_from, t.effective_to
      INTO v_terms_employee, v_terms_from, v_terms_to
      FROM public.employment_terms t
     WHERE t.employer_id = NEW.employer_id AND t.id = NEW.terms_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'compensation parent terms % missing in this employer', NEW.terms_id;
    END IF;
    IF NEW.employee_id IS DISTINCT FROM v_terms_employee THEN
        RAISE EXCEPTION 'compensation employee % does not match its terms employee %',
            NEW.employee_id, v_terms_employee;
    END IF;
    IF NEW.effective_from < v_terms_from THEN
        RAISE EXCEPTION 'compensation containment: effective % predates its terms %',
            NEW.effective_from, v_terms_from;
    END IF;
    IF v_terms_to IS NOT NULL
       AND (NEW.effective_from > v_terms_to
            OR NEW.effective_to IS NULL
            OR NEW.effective_to > v_terms_to) THEN
        RAISE EXCEPTION 'compensation containment: line outlives its terms ending %', v_terms_to;
    END IF;
    IF NEW.supersedes_id IS NOT NULL THEN
        SELECT c.employer_id, c.employee_id, c.terms_id, c.kind, c.code
          INTO o_employer, o_employee, o_terms, o_kind, o_code
          FROM public.compensation_line c
         WHERE c.id = NEW.supersedes_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'successor original % does not exist', NEW.supersedes_id;
        END IF;
        IF NEW.employer_id IS DISTINCT FROM o_employer
           OR NEW.employee_id IS DISTINCT FROM o_employee
           OR NEW.terms_id IS DISTINCT FROM o_terms
           OR NEW.kind IS DISTINCT FROM o_kind
           OR NEW.code IS DISTINCT FROM o_code THEN
            RAISE EXCEPTION 'successor must preserve employer, employee, terms and classification';
        END IF;
    END IF;
    -- Overlap against the live basis only: a new row must not collide
    -- with any not-yet-superseded line but its own direct predecessor
    -- (which is live until this insert lands). Superseded history is
    -- dead basis and never collides, so narrowing corrections and
    -- chained successors stay possible.
    SELECT c.id INTO v_clash
      FROM public.compensation_line c
     WHERE c.employer_id = NEW.employer_id AND c.employee_id = NEW.employee_id
       AND c.kind = NEW.kind AND c.code = NEW.code
       AND c.id IS DISTINCT FROM NEW.supersedes_id
       AND NOT EXISTS (SELECT 1 FROM public.compensation_line s WHERE s.supersedes_id = c.id)
       AND daterange(c.effective_from, COALESCE(c.effective_to, 'infinity'::date), '[]')
         && daterange(NEW.effective_from, COALESCE(NEW.effective_to, 'infinity'::date), '[]')
     LIMIT 1;
    IF FOUND THEN
        IF NEW.supersedes_id IS NOT NULL THEN
            RAISE EXCEPTION 'successor overlaps live compensation % that is not its direct predecessor', v_clash;
        ELSE
            RAISE EXCEPTION 'compensation overlaps live compensation % for the same code', v_clash;
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER compensation_line_containment
    BEFORE INSERT ON compensation_line
    FOR EACH ROW EXECUTE FUNCTION check_compensation_line();

ALTER FUNCTION check_compensation_line() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_compensation_line() FROM PUBLIC;

-- E4/E5: the only write path. Each function takes the employee
-- barrier lock first (serializing validation reads), inserts the
-- row, and writes the one canonical audit event atomically — a
-- failed write leaves no audit shadow. Amounts canonicalize to
-- cents (100.00, never 100).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION add_compensation(
    p_employer uuid, p_employee uuid, p_terms uuid, p_kind text, p_code text,
    p_amount numeric, p_from date, p_to date, p_actor text, p_reason text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_id uuid;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'add_compensation requires an actor';
    END IF;
    IF p_reason IS NULL OR length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'compensation add requires a reason';
    END IF;
    IF p_kind NOT IN ('allowance', 'deduction') THEN
        RAISE EXCEPTION 'unknown compensation kind %', p_kind;
    END IF;
    IF p_code IS NULL OR length(trim(p_code)) = 0 THEN
        RAISE EXCEPTION 'compensation code is required';
    END IF;
    IF p_to IS NOT NULL AND p_to < p_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    PERFORM public.acquire_payroll_input_lock(p_employer, p_employee);
    INSERT INTO public.compensation_line
        (id, employer_id, employee_id, terms_id, kind, code, amount_monthly, effective_from, effective_to)
     VALUES (public.gen_random_uuid(), p_employer, p_employee, p_terms, p_kind, trim(p_code),
             p_amount, p_from, p_to)
     RETURNING id INTO v_id;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'compensation.add', 'compensation_line', v_id, NULL,
             jsonb_build_object('kind', p_kind, 'code', trim(p_code),
                 'amount', to_char(p_amount, 'FM9999999999.00'),
                 'effective_from', p_from::text, 'effective_to', p_to::text,
                 'terms_id', p_terms::text, 'reason', trim(p_reason)));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION correct_compensation(
    p_employer uuid, p_employee uuid, p_original uuid, p_amount numeric,
    p_from date, p_to date, p_actor text, p_reason text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    o_terms uuid;
    o_kind text;
    o_code text;
    o_employee uuid;
    o_amount numeric;
    o_from date;
    o_to date;
    v_id uuid;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'correct_compensation requires an actor';
    END IF;
    IF p_reason IS NULL OR length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'compensation correction requires a reason';
    END IF;
    IF p_to IS NOT NULL AND p_to < p_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    PERFORM public.acquire_payroll_input_lock(p_employer, p_employee);
    SELECT c.terms_id, c.kind, c.code, c.employee_id, c.amount_monthly, c.effective_from, c.effective_to
      INTO o_terms, o_kind, o_code, o_employee, o_amount, o_from, o_to
      FROM public.compensation_line c
     WHERE c.id = p_original AND c.employer_id = p_employer;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'original compensation % not found in this employer', p_original;
    END IF;
    IF o_employee IS DISTINCT FROM p_employee THEN
        RAISE EXCEPTION 'correction employee % does not match the original''s employee %',
            p_employee, o_employee;
    END IF;
    -- One successor per original, reported consistently: without this
    -- the overlap trigger would fire first for same-range retries
    -- (P0001) while the UNIQUE would fire for disjoint ones (23505).
    -- The UNIQUE stays as the race backstop.
    IF EXISTS (SELECT 1 FROM public.compensation_line s WHERE s.supersedes_id = p_original) THEN
        RAISE EXCEPTION 'compensation % already has a successor; correct the successor instead', p_original;
    END IF;
    INSERT INTO public.compensation_line
        (id, employer_id, employee_id, terms_id, kind, code, amount_monthly,
         effective_from, effective_to, supersedes_id)
     VALUES (public.gen_random_uuid(), p_employer, p_employee, o_terms, o_kind, o_code,
             p_amount, p_from, p_to, p_original)
     RETURNING id INTO v_id;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'compensation.correct', 'compensation_line', v_id,
             jsonb_build_object('amount', to_char(o_amount, 'FM9999999999.00'),
                 'effective_from', o_from::text, 'effective_to', o_to::text),
             jsonb_build_object('kind', o_kind, 'code', o_code,
                 'amount', to_char(p_amount, 'FM9999999999.00'),
                 'effective_from', p_from::text, 'effective_to', p_to::text,
                 'supersedes_id', p_original::text, 'reason', trim(p_reason)));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION add_compensation(uuid, uuid, uuid, text, text, numeric, date, date, text, text) OWNER TO migration_admin;
ALTER FUNCTION correct_compensation(uuid, uuid, uuid, numeric, date, date, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION add_compensation(uuid, uuid, uuid, text, text, numeric, date, date, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION correct_compensation(uuid, uuid, uuid, numeric, date, date, text, text) FROM PUBLIC;

-- E2: closing terms must not strand a live line past the close. The
-- controlled path corrects (caps) the line first; superseded history
-- never blocks. OR REPLACE keeps 00010's file untouched.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION close_employment_terms(
    p_employer uuid, p_employee uuid, p_successor_from date, p_actor text
) RETURNS date
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    prior_id uuid;
    prior_from date;
    v_outlive int;
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
    SELECT count(*) INTO v_outlive
      FROM public.compensation_line c
     WHERE c.employer_id = p_employer AND c.terms_id = prior_id
       AND (c.effective_to IS NULL OR c.effective_to > p_successor_from - 1)
       AND NOT EXISTS (SELECT 1 FROM public.compensation_line s WHERE s.supersedes_id = c.id);
    IF v_outlive > 0 THEN
        RAISE EXCEPTION '% compensation lines under these terms outlive the close date %; correct them first',
            v_outlive, p_successor_from - 1;
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
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
        REVOKE INSERT ON compensation_line FROM app;
        GRANT EXECUTE ON FUNCTION add_compensation(uuid, uuid, uuid, text, text, numeric, date, date, text, text) TO app;
        GRANT EXECUTE ON FUNCTION correct_compensation(uuid, uuid, uuid, numeric, date, date, text, text) TO app;
    END IF;
END
$$;
-- +goose StatementEnd

-- Verify loudly: raw INSERT gone, functions executable by app,
-- ownership/paths locked, the containment trigger present,
-- exactness CHECKs present, old per-terms exclusion gone.
-- +goose StatementBegin
DO $$
DECLARE
    fn TEXT;
    fn_path TEXT[];
BEGIN
    IF has_table_privilege('app', 'public.compensation_line', 'INSERT') THEN
        RAISE EXCEPTION 'app can still INSERT compensation directly';
    END IF;
    IF NOT has_function_privilege('app',
            'public.add_compensation(uuid, uuid, uuid, text, text, numeric, date, date, text, text)', 'EXECUTE')
       OR NOT has_function_privilege('app',
            'public.correct_compensation(uuid, uuid, uuid, numeric, date, date, text, text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE the compensation functions';
    END IF;
    FOR fn IN SELECT 'add_compensation' UNION ALL SELECT 'correct_compensation'
               UNION ALL SELECT 'check_compensation_line' UNION ALL SELECT 'close_employment_terms' LOOP
        SELECT p.proconfig INTO fn_path FROM pg_proc p WHERE p.proname = fn;
        IF fn_path IS DISTINCT FROM ARRAY['search_path=pg_catalog'] THEN
            RAISE EXCEPTION '% search_path is %, want pg_catalog only', fn, fn_path;
        END IF;
    END LOOP;
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'compensation_line_no_overlap') THEN
        RAISE EXCEPTION 'old per-terms overlap exclusion still present';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'compensation_line_containment') THEN
        RAISE EXCEPTION 'compensation containment trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'compensation_line_amount_exact') THEN
        RAISE EXCEPTION 'compensation exactness CHECK missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'employment_terms_basic_exact') THEN
        RAISE EXCEPTION 'monthly_basic exactness CHECK missing';
    END IF;
END
$$;
-- +goose StatementEnd

-- Stage C: duplicate-disposition cycles (B-CYC-01). The 00010 trigger
-- checked only one direction (a duplicate cannot become canonical)
-- with no concurrency protection: concurrent A→B plus B→A both
-- passed, and sequential A→B then B→C chained. The canonical-event
-- policy enforced here:
--
-- - A canonical event may collect many duplicates.
-- - A duplicate cannot become the canonical of another edge.
-- - A canonical cannot itself be classified as a duplicate.
-- - No chains, no cycles; raw events are never modified.
--
-- Concurrency: a transaction-scoped advisory lock on the
-- deterministically ordered endpoint pair, then a post-lock recheck
-- of both directions. Reversed pairs share one key, so they
-- serialize; distinct pairs use distinct keys. The service already
-- holds the employee barrier lock above this, and direct SQL takes
-- only the pair lock, so no lock cycle can form.
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
    -- Order-independent pair key: reversed edges serialize on it.
    PERFORM pg_advisory_xact_lock(hashtext('disposition:' ||
        LEAST(NEW.event_id, NEW.duplicate_of_event_id)::text || ':' ||
        GREATEST(NEW.event_id, NEW.duplicate_of_event_id)::text));
    -- Post-lock recheck, both directions: neither a duplicate
    -- becoming canonical nor a canonical becoming a duplicate.
    IF EXISTS (SELECT 1 FROM public.clock_event_disposition d
               WHERE d.event_id = NEW.duplicate_of_event_id) THEN
        RAISE EXCEPTION 'canonical event % is itself dispositioned as a duplicate', NEW.duplicate_of_event_id;
    END IF;
    IF EXISTS (SELECT 1 FROM public.clock_event_disposition d
               WHERE d.duplicate_of_event_id = NEW.event_id) THEN
        RAISE EXCEPTION 'event % is already canonical for another disposition', NEW.event_id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Verify loudly: the trigger still guards the table and the
-- recheck survives the replace.
-- +goose StatementBegin
DO $$
DECLARE
    v_def text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'clock_event_disposition_match') THEN
        RAISE EXCEPTION 'disposition match trigger missing';
    END IF;
    SELECT pg_get_functiondef(p.oid) INTO v_def FROM pg_proc p
     WHERE p.proname = 'check_clock_event_disposition';
    IF v_def NOT LIKE '%disposition:'' ||%' OR v_def NOT LIKE '%already canonical%' THEN
        RAISE EXCEPTION 'disposition trigger lacks the pair lock or the reverse check';
    END IF;
END
$$;
-- +goose StatementEnd

-- Stage D: backdated attendance evidence (A1-F5). Readiness compares
-- created_at to approved_at, so caller-controlled creation stamps
-- would dodge conflict detection. Normalize on write, mirroring the
-- compensation/terms precedent: event time (occurred_at, interval
-- bounds, source offset, business date) is never touched — only the
-- server-side record-creation metadata. On UPDATE only a CHANGED
-- stamp normalizes: the supersession flip (which touches is_current
-- alone) must not falsify the predecessor's creation time. This does
-- NOT fix D-RACE (transaction-start stamps still predate later
-- approvals); the basis_seq leg covers that window.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_clock_event_created_at() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        NEW.created_at := now();
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER clock_event_created_at_normalize
    BEFORE INSERT OR UPDATE ON clock_event
    FOR EACH ROW EXECUTE FUNCTION normalize_clock_event_created_at();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_interval_created_at() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        NEW.created_at := now();
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER attendance_interval_created_at_normalize
    BEFORE INSERT OR UPDATE ON attendance_interval
    FOR EACH ROW EXECUTE FUNCTION normalize_interval_created_at();
-- +goose StatementEnd

ALTER FUNCTION normalize_clock_event_created_at() OWNER TO migration_admin;
ALTER FUNCTION normalize_interval_created_at() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION normalize_clock_event_created_at() FROM PUBLIC;
REVOKE ALL ON FUNCTION normalize_interval_created_at() FROM PUBLIC;

-- Verify loudly: both normalize triggers guard their tables.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'clock_event_created_at_normalize') THEN
        RAISE EXCEPTION 'clock_event normalize trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'attendance_interval_created_at_normalize') THEN
        RAISE EXCEPTION 'interval normalize trigger missing';
    END IF;
END
$$;
-- +goose StatementEnd

-- H6 (S7-P3-LEGACY-EXEC): legacy trigger-only functions carried the
-- default PUBLIC EXECUTE grant, so the runtime role could invoke
-- them directly. Revoke it: triggers fire without any EXECUTE check
-- (the maintain_interval_currency precedent already runs this way),
-- and no service calls these functions directly.
REVOKE ALL ON FUNCTION forbid_if_timesheet_decided() FROM PUBLIC;
REVOKE ALL ON FUNCTION forbid_unless_roster_draft_revision() FROM PUBLIC;
REVOKE ALL ON FUNCTION forbid_if_run_approved() FROM PUBLIC;
REVOKE ALL ON FUNCTION forbid_unless_run_draft() FROM PUBLIC;
REVOKE ALL ON FUNCTION forbid_if_leave_decided() FROM PUBLIC;
REVOKE ALL ON FUNCTION enforce_interval_single_chain() FROM PUBLIC;
REVOKE ALL ON FUNCTION enforce_line_interval_employee() FROM PUBLIC;

-- Verify loudly: app holds EXECUTE on none of the seven, and every
-- one still backs a live trigger.
-- +goose StatementBegin
DO $$
DECLARE
    fn TEXT;
BEGIN
    FOR fn IN SELECT 'forbid_if_timesheet_decided' UNION ALL SELECT 'forbid_unless_roster_draft_revision'
               UNION ALL SELECT 'forbid_if_run_approved' UNION ALL SELECT 'forbid_unless_run_draft'
               UNION ALL SELECT 'forbid_if_leave_decided' UNION ALL SELECT 'enforce_interval_single_chain'
               UNION ALL SELECT 'enforce_line_interval_employee' LOOP
        IF has_function_privilege('app', 'public.' || fn || '()', 'EXECUTE') THEN
            RAISE EXCEPTION 'app keeps EXECUTE on legacy trigger function %', fn;
        END IF;
        IF NOT EXISTS (SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid = t.tgfoid
                        WHERE p.proname = fn AND NOT t.tgisinternal) THEN
            RAISE EXCEPTION 'legacy trigger function % backs no trigger', fn;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- H4 (C-TZ-05): programme timezones are accepted operational
-- configuration, so validate the IANA name at the boundary (service)
-- and in the database (this probe): an unknown zone fails the write
-- instead of detonating a later AT TIME ZONE conversion.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_programme_timezone() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    PERFORM now() AT TIME ZONE NEW.timezone;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER programme_timezone_valid
    BEFORE INSERT OR UPDATE ON programme
    FOR EACH ROW EXECUTE FUNCTION check_programme_timezone();

ALTER FUNCTION check_programme_timezone() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_programme_timezone() FROM PUBLIC;

-- +goose Down

-- Stage F: guarded forward-only rollback. M1.2 and later integrity
-- migrations are forward-only once dependent business data exists:
-- inspect first, fail before anything destructive, and never
-- silently drop correction, disposition, or compensation history.
-- Populated recovery means a verified backup plus a controlled
-- restore/rebuild — never Down. The reversal below runs only when
-- every dependent table is empty and no basis version was assigned
-- (freshly migrated development/test databases).
-- +goose StatementBegin
DO $$
DECLARE
    v_comp int;
    v_terms int;
    v_groups int;
    v_links int;
    v_seq int;
BEGIN
    SELECT count(*) INTO v_comp FROM public.compensation_line;
    IF v_comp > 0 THEN
        RAISE EXCEPTION '00011 Down refused: % compensation rows depend on this migration (forward-only; restore from backup)', v_comp;
    END IF;
    SELECT count(*) INTO v_terms FROM public.employment_terms;
    IF v_terms > 0 THEN
        RAISE EXCEPTION '00011 Down refused: % employment-terms rows depend on this migration (forward-only; restore from backup)', v_terms;
    END IF;
    SELECT count(*) INTO v_groups FROM public.attendance_correction_group;
    IF v_groups > 0 THEN
        RAISE EXCEPTION '00011 Down refused: % correction groups depend on this migration (forward-only; restore from backup)', v_groups;
    END IF;
    SELECT count(*) INTO v_links FROM public.attendance_correction_group_predecessor;
    IF v_links > 0 THEN
        RAISE EXCEPTION '00011 Down refused: % correction-group links depend on this migration (forward-only; restore from backup)', v_links;
    END IF;
    SELECT count(*) INTO v_seq FROM (
        SELECT basis_seq FROM public.compensation_line WHERE basis_seq <> 0
        UNION ALL SELECT basis_seq FROM public.timesheet WHERE basis_seq <> 0
        UNION ALL SELECT basis_seq FROM public.employment_terms WHERE basis_seq <> 0
        UNION ALL SELECT basis_seq FROM public.attendance_interval WHERE basis_seq <> 0
        UNION ALL SELECT basis_seq FROM public.clock_event WHERE basis_seq <> 0
    ) s;
    IF v_seq > 0 THEN
        RAISE EXCEPTION '00011 Down refused: % rows carry assigned basis versions (forward-only; restore from backup)', v_seq;
    END IF;
END
$$;
-- +goose StatementEnd

-- H4 reversal: drop the programme timezone probe.
DROP TRIGGER programme_timezone_valid ON programme;
DROP FUNCTION check_programme_timezone();

-- H6 reversal: restore the default PUBLIC EXECUTE the legacy
-- trigger functions carried before this migration.
GRANT EXECUTE ON FUNCTION forbid_if_timesheet_decided() TO PUBLIC;
GRANT EXECUTE ON FUNCTION forbid_unless_roster_draft_revision() TO PUBLIC;
GRANT EXECUTE ON FUNCTION forbid_if_run_approved() TO PUBLIC;
GRANT EXECUTE ON FUNCTION forbid_unless_run_draft() TO PUBLIC;
GRANT EXECUTE ON FUNCTION forbid_if_leave_decided() TO PUBLIC;
GRANT EXECUTE ON FUNCTION enforce_interval_single_chain() TO PUBLIC;
GRANT EXECUTE ON FUNCTION enforce_line_interval_employee() TO PUBLIC;

-- Stage D reversal: drop the evidence normalize triggers.
DROP TRIGGER attendance_interval_created_at_normalize ON attendance_interval;
DROP FUNCTION normalize_interval_created_at();
DROP TRIGGER clock_event_created_at_normalize ON clock_event;
DROP FUNCTION normalize_clock_event_created_at();

-- Stage C reversal: restore the 00010 disposition trigger body
-- verbatim (00010's file stays untouched; this copy must not drift).
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

-- E2–E5 reversal: drop the controlled functions and the containment
-- trigger, restore the 00004 per-terms exclusion, the 00010 close
-- body, exact NUMERIC(12,2) types, and the app INSERT grant.
DROP TRIGGER compensation_line_containment ON compensation_line;
DROP FUNCTION check_compensation_line();
DROP FUNCTION correct_compensation(uuid, uuid, uuid, numeric, date, date, text, text);
DROP FUNCTION add_compensation(uuid, uuid, uuid, text, text, numeric, date, date, text, text);
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
ALTER TABLE compensation_line DROP CONSTRAINT compensation_line_single_successor;
ALTER TABLE compensation_line DROP CONSTRAINT compensation_line_successor_fk;
ALTER TABLE compensation_line DROP COLUMN supersedes_id;
ALTER TABLE compensation_line DROP CONSTRAINT compensation_line_employee_fk;
ALTER TABLE compensation_line DROP COLUMN employee_id;
ALTER TABLE compensation_line DROP CONSTRAINT compensation_line_amount_exact;
ALTER TABLE compensation_line ALTER COLUMN amount_monthly TYPE NUMERIC(12, 2);
ALTER TABLE employment_terms DROP CONSTRAINT employment_terms_basic_exact;
ALTER TABLE employment_terms ALTER COLUMN monthly_basic TYPE NUMERIC(12, 2);
ALTER TABLE compensation_line ADD CONSTRAINT compensation_line_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        terms_id WITH =,
        kind WITH =,
        code WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );
GRANT INSERT ON compensation_line TO app;

-- Stage A reversal: drop the group machinery, restore the 00010
-- single-predecessor column (the table is empty: the guard proved
-- no group or link rows exist, so nothing is lost).
DROP TRIGGER attendance_interval_local_date ON attendance_interval;
DROP FUNCTION check_interval_local_date();
DROP FUNCTION employment_tz(text);
DROP TRIGGER correction_group_complete ON attendance_correction_group;
DROP FUNCTION check_correction_group_complete();
DROP TRIGGER correction_group_segment_open ON attendance_interval;
DROP TRIGGER correction_group_predecessor_open ON attendance_correction_group_predecessor;
DROP FUNCTION check_correction_group_open();
DROP TABLE attendance_correction_group_predecessor;
ALTER TABLE attendance_correction_group ADD COLUMN predecessor_interval_id uuid NOT NULL UNIQUE REFERENCES attendance_interval (id) ON DELETE RESTRICT;
ALTER TABLE attendance_correction_group DROP CONSTRAINT attendance_correction_group_employer_id_key;

-- Barrier reversal: drop the approval and evidence triggers, the
-- basis columns, the sequence, and the lock helper.
DROP TRIGGER timesheet_approval_barrier ON timesheet;
DROP TRIGGER compensation_line_basis_seq ON compensation_line;
DROP TRIGGER employment_terms_basis_seq ON employment_terms;
DROP TRIGGER attendance_interval_basis_seq ON attendance_interval;
DROP TRIGGER clock_event_basis_seq ON clock_event;
DROP FUNCTION check_timesheet_approval_barrier();
DROP FUNCTION assign_pay_basis_seq();
ALTER TABLE timesheet DROP COLUMN basis_seq;
ALTER TABLE compensation_line DROP COLUMN basis_seq;
ALTER TABLE employment_terms DROP COLUMN basis_seq;
ALTER TABLE attendance_interval DROP COLUMN basis_seq;
ALTER TABLE clock_event DROP COLUMN basis_seq;
DROP SEQUENCE pay_basis_seq;
DROP FUNCTION acquire_payroll_input_lock(uuid, uuid);
