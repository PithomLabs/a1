-- WorkforceOps Lab Milestone 2: run lifecycle, snapshots, compensation
-- basis, SHG lifecycle and rule-pack activation governance.
--
-- Single forward migration for Prompt 15 as amended by
-- plan_prompt15_review.md (Amendments A-F). Sections land in execution
-- order: SHG lifecycle (P4), rule-pack activation (P5), snapshot
-- capture (P2), run lifecycle (P1), then the contract tables (P6).
-- The Down section is guarded forward-only: it fails before
-- destructive changes whenever populated history depends on this
-- migration's objects.

-- +goose Up

-- Preconditions mirror 00011: migrate as migration_admin with the
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

-- Section SHG. SHG instruction lifecycle (Prompt 15 section 4, P4).
--
-- SHG instructions move from presence-checked declarations to a
-- governed chain: pending declarations are verified into
-- verified_none (with a full evidence triple) or confirmed into
-- active; opt_out stays a first-class terminal state; every change
-- is an audited successor, never a mutation. Readiness evaluates the
-- chain tip per fund and blocks on pending, opt_out, missing or
-- corrupt states with machine-readable codes.

-- The 00004 per-fund exclusion blocks correction chains (a successor
-- legitimately shares its predecessor's range), so it gives way to a
-- live-basis overlap trigger below.
ALTER TABLE shg_instruction DROP CONSTRAINT IF EXISTS shg_instruction_no_overlap;

-- The 00002 status CHECK (active/opt_out/verified_none) and the loose
-- verified-none CHECK give way to the lifecycle CHECKs below.
ALTER TABLE shg_instruction DROP CONSTRAINT IF EXISTS shg_instruction_status_check;
ALTER TABLE shg_instruction DROP CONSTRAINT IF EXISTS shg_instruction_check;

ALTER TABLE shg_instruction ADD COLUMN supersedes_id uuid;
ALTER TABLE shg_instruction ADD COLUMN basis_seq BIGINT NOT NULL DEFAULT 0 CHECK (basis_seq >= 0);

ALTER TABLE shg_instruction ADD CONSTRAINT shg_status_values
    CHECK (status IN ('pending', 'active', 'opt_out', 'verified_none'));
ALTER TABLE shg_instruction ADD CONSTRAINT shg_verified_consistency CHECK (
    (status = 'verified_none'
     AND verified_at IS NOT NULL AND verified_by IS NOT NULL AND verification_basis IS NOT NULL)
    OR (status IN ('pending', 'active', 'opt_out')
        AND verified_at IS NULL AND verified_by IS NULL AND verification_basis IS NULL)
);
ALTER TABLE shg_instruction ADD CONSTRAINT shg_effective_order
    CHECK (effective_to IS NULL OR effective_to >= effective_from);
ALTER TABLE shg_instruction ADD CONSTRAINT shg_instruction_supersedes_fk
    FOREIGN KEY (employer_id, supersedes_id) REFERENCES shg_instruction (employer_id, id);
ALTER TABLE shg_instruction ADD CONSTRAINT shg_instruction_single_successor
    UNIQUE (supersedes_id);

-- SHG instructions join the payroll-input barrier: every insert takes
-- the employee lock and a basis version from the shared sequence, so
-- readiness orders SHG evidence against approvals exactly like the
-- other inputs. The generic trigger keys the employee directly.
CREATE TRIGGER shg_instruction_basis_seq
    BEFORE INSERT OR UPDATE ON shg_instruction
    FOR EACH ROW EXECUTE FUNCTION assign_pay_basis_seq();

-- BEFORE INSERT: the successor contract (same employer, employee and
-- fund; predecessors are open-ended chain tips; no regression to
-- pending) and live-basis overlap (no collision with any
-- not-yet-superseded same-fund row but the direct predecessor, which
-- is live until this insert lands).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_shg_instruction() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    o_employer uuid;
    o_employee uuid;
    o_fund text;
    o_status text;
    o_to date;
    v_clash uuid;
BEGIN
    IF NEW.supersedes_id IS NOT NULL THEN
        SELECT s.employer_id, s.employee_id, s.fund, s.status, s.effective_to
          INTO o_employer, o_employee, o_fund, o_status, o_to
          FROM public.shg_instruction s
         WHERE s.id = NEW.supersedes_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'successor original % does not exist', NEW.supersedes_id;
        END IF;
        IF NEW.employer_id IS DISTINCT FROM o_employer
           OR NEW.employee_id IS DISTINCT FROM o_employee
           OR NEW.fund IS DISTINCT FROM o_fund THEN
            RAISE EXCEPTION 'successor must preserve employer, employee and fund';
        END IF;
        IF o_to IS NOT NULL THEN
            RAISE EXCEPTION 'only open-ended instructions can be superseded';
        END IF;
        IF NEW.status = 'pending' THEN
            RAISE EXCEPTION 'successor must not regress its chain to pending';
        END IF;
    END IF;
    SELECT s.id INTO v_clash
      FROM public.shg_instruction s
     WHERE s.employer_id = NEW.employer_id AND s.employee_id = NEW.employee_id
       AND s.fund = NEW.fund
       AND s.id IS DISTINCT FROM NEW.supersedes_id
       AND NOT EXISTS (SELECT 1 FROM public.shg_instruction t WHERE t.supersedes_id = s.id)
       AND daterange(s.effective_from, COALESCE(s.effective_to, 'infinity'::date), '[]')
         && daterange(NEW.effective_from, COALESCE(NEW.effective_to, 'infinity'::date), '[]')
     LIMIT 1;
    IF FOUND THEN
        IF NEW.supersedes_id IS NOT NULL THEN
            RAISE EXCEPTION 'successor overlaps a live % instruction % that is not its direct predecessor', NEW.fund, v_clash;
        ELSE
            RAISE EXCEPTION 'SHG instruction overlaps a live % instruction %', NEW.fund, v_clash;
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER shg_instruction_lifecycle
    BEFORE INSERT ON shg_instruction
    FOR EACH ROW EXECUTE FUNCTION check_shg_instruction();

ALTER FUNCTION check_shg_instruction() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_shg_instruction() FROM PUBLIC;

-- Caller-controlled creation stamps would dodge the post-approval
-- conflict leg the same way backdated attendance evidence did, so
-- SHG creation stamps normalize on write: event data (the effective
-- range, the declaration, the verified triple) is never touched.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_shg_created_at() RETURNS trigger
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
CREATE TRIGGER shg_instruction_created_at_normalize
    BEFORE INSERT OR UPDATE ON shg_instruction
    FOR EACH ROW EXECUTE FUNCTION normalize_shg_created_at();
-- +goose StatementEnd

ALTER FUNCTION normalize_shg_created_at() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION normalize_shg_created_at() FROM PUBLIC;

-- The only write path. Each function takes the employee barrier lock
-- first (serializing validation reads), inserts the row, and writes
-- the one canonical audit event atomically. The source declaration
-- is the human justification, so it doubles as the audit reason and
-- no separate reason parameter exists.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION add_shg_instruction(
    p_employer uuid, p_employee uuid, p_fund text, p_status text, p_amount text,
    p_declaration text, p_from date, p_to date,
    p_verified_at timestamptz, p_verified_by text, p_verification_basis text,
    p_actor text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_id uuid;
    v_triple int;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'add_shg_instruction requires an actor';
    END IF;
    IF p_fund NOT IN ('CDAC', 'MBMF', 'SINDA', 'ECF') THEN
        RAISE EXCEPTION 'fund must be one of CDAC, MBMF, SINDA, ECF';
    END IF;
    IF p_status NOT IN ('pending', 'active', 'opt_out', 'verified_none') THEN
        RAISE EXCEPTION 'status must be pending, active, opt_out or verified_none';
    END IF;
    IF p_declaration IS NULL OR length(trim(p_declaration)) = 0 THEN
        RAISE EXCEPTION 'source_declaration is required';
    END IF;
    IF p_to IS NOT NULL AND p_to < p_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    v_triple := (p_verified_at IS NOT NULL)::int + (p_verified_by IS NOT NULL)::int
        + (p_verification_basis IS NOT NULL)::int;
    IF p_status = 'verified_none' AND v_triple <> 3 THEN
        RAISE EXCEPTION 'verified_none requires the full verified triple';
    END IF;
    IF p_status <> 'verified_none' AND v_triple <> 0 THEN
        RAISE EXCEPTION '% must be unverified', p_status;
    END IF;
    PERFORM public.acquire_payroll_input_lock(p_employer, p_employee);
    INSERT INTO public.shg_instruction
        (id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration,
         effective_from, effective_to, verified_at, verified_by, verification_basis)
     VALUES (public.gen_random_uuid(), p_employer, p_employee, p_fund, p_status,
             NULLIF(trim(p_amount), ''), trim(p_declaration), p_from, p_to,
             p_verified_at, NULLIF(trim(p_verified_by), ''), NULLIF(trim(p_verification_basis), ''))
     RETURNING id INTO v_id;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'shg.set', 'shg_instruction', v_id, NULL,
             jsonb_build_object('fund', p_fund, 'status', p_status,
                 'source_declaration', trim(p_declaration),
                 'effective_from', p_from::text, 'effective_to', p_to::text));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION verify_shg_instruction(
    p_employer uuid, p_employee uuid, p_predecessor uuid,
    p_declaration text, p_from date, p_to date,
    p_verified_at timestamptz, p_verified_by text, p_verification_basis text,
    p_actor text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    o_employee uuid;
    o_fund text;
    o_status text;
    o_from date;
    o_to date;
    v_id uuid;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'verify_shg_instruction requires an actor';
    END IF;
    IF p_declaration IS NULL OR length(trim(p_declaration)) = 0 THEN
        RAISE EXCEPTION 'source_declaration is required';
    END IF;
    IF p_to IS NOT NULL AND p_to < p_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    IF p_verified_at IS NULL OR p_verified_by IS NULL OR length(trim(p_verified_by)) = 0
       OR p_verification_basis IS NULL OR length(trim(p_verification_basis)) = 0 THEN
        RAISE EXCEPTION 'verification requires the full verified triple';
    END IF;
    PERFORM public.acquire_payroll_input_lock(p_employer, p_employee);
    SELECT s.employee_id, s.fund, s.status, s.effective_from, s.effective_to
      INTO o_employee, o_fund, o_status, o_from, o_to
      FROM public.shg_instruction s
     WHERE s.id = p_predecessor AND s.employer_id = p_employer;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'original SHG instruction % not found in this employer', p_predecessor;
    END IF;
    IF o_employee IS DISTINCT FROM p_employee THEN
        RAISE EXCEPTION 'verification employee % does not match the original''s employee %',
            p_employee, o_employee;
    END IF;
    IF o_status <> 'pending' THEN
        RAISE EXCEPTION 'only pending instructions can be verified (status is %)', o_status;
    END IF;
    IF o_to IS NOT NULL THEN
        RAISE EXCEPTION 'only open-ended instructions can be verified';
    END IF;
    IF EXISTS (SELECT 1 FROM public.shg_instruction s WHERE s.supersedes_id = p_predecessor) THEN
        RAISE EXCEPTION 'SHG instruction % already has a successor; correct the successor instead', p_predecessor;
    END IF;
    INSERT INTO public.shg_instruction
        (id, employer_id, employee_id, fund, status, source_declaration,
         effective_from, effective_to, verified_at, verified_by, verification_basis, supersedes_id)
     VALUES (public.gen_random_uuid(), p_employer, p_employee, o_fund, 'verified_none',
             trim(p_declaration), p_from, p_to,
             p_verified_at, trim(p_verified_by), trim(p_verification_basis), p_predecessor)
     RETURNING id INTO v_id;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'shg.verify', 'shg_instruction', v_id,
             jsonb_build_object('status', o_status,
                 'effective_from', o_from::text, 'effective_to', o_to::text),
             jsonb_build_object('fund', o_fund, 'status', 'verified_none',
                 'source_declaration', trim(p_declaration),
                 'effective_from', p_from::text, 'effective_to', p_to::text,
                 'verified_by', trim(p_verified_by),
                 'verification_basis', trim(p_verification_basis),
                 'supersedes_id', p_predecessor::text));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION correct_shg_instruction(
    p_employer uuid, p_employee uuid, p_predecessor uuid,
    p_status text, p_amount text, p_declaration text, p_from date, p_to date,
    p_verified_at timestamptz, p_verified_by text, p_verification_basis text,
    p_actor text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    o_employee uuid;
    o_fund text;
    o_status text;
    o_from date;
    o_to date;
    v_triple int;
    v_id uuid;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'correct_shg_instruction requires an actor';
    END IF;
    IF p_status NOT IN ('active', 'opt_out', 'verified_none') THEN
        RAISE EXCEPTION 'correction status must be active, opt_out or verified_none, never pending';
    END IF;
    IF p_declaration IS NULL OR length(trim(p_declaration)) = 0 THEN
        RAISE EXCEPTION 'source_declaration is required';
    END IF;
    IF p_to IS NOT NULL AND p_to < p_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    v_triple := (p_verified_at IS NOT NULL)::int + (p_verified_by IS NOT NULL)::int
        + (p_verification_basis IS NOT NULL)::int;
    IF p_status = 'verified_none' AND v_triple <> 3 THEN
        RAISE EXCEPTION 'verified_none requires the full verified triple';
    END IF;
    IF p_status <> 'verified_none' AND v_triple <> 0 THEN
        RAISE EXCEPTION '% must be unverified', p_status;
    END IF;
    PERFORM public.acquire_payroll_input_lock(p_employer, p_employee);
    SELECT s.employee_id, s.fund, s.status, s.effective_from, s.effective_to
      INTO o_employee, o_fund, o_status, o_from, o_to
      FROM public.shg_instruction s
     WHERE s.id = p_predecessor AND s.employer_id = p_employer;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'original SHG instruction % not found in this employer', p_predecessor;
    END IF;
    IF o_employee IS DISTINCT FROM p_employee THEN
        RAISE EXCEPTION 'correction employee % does not match the original''s employee %',
            p_employee, o_employee;
    END IF;
    IF o_to IS NOT NULL THEN
        RAISE EXCEPTION 'only open-ended instructions can be corrected';
    END IF;
    IF EXISTS (SELECT 1 FROM public.shg_instruction s WHERE s.supersedes_id = p_predecessor) THEN
        RAISE EXCEPTION 'SHG instruction % already has a successor; correct the successor instead', p_predecessor;
    END IF;
    INSERT INTO public.shg_instruction
        (id, employer_id, employee_id, fund, status, amount_or_basis, source_declaration,
         effective_from, effective_to, verified_at, verified_by, verification_basis, supersedes_id)
     VALUES (public.gen_random_uuid(), p_employer, p_employee, o_fund, p_status,
             NULLIF(trim(p_amount), ''), trim(p_declaration), p_from, p_to,
             p_verified_at, NULLIF(trim(p_verified_by), ''), NULLIF(trim(p_verification_basis), ''), p_predecessor)
     RETURNING id INTO v_id;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'hr', 'shg.correct', 'shg_instruction', v_id,
             jsonb_build_object('status', o_status,
                 'effective_from', o_from::text, 'effective_to', o_to::text),
             jsonb_build_object('fund', o_fund, 'status', p_status,
                 'source_declaration', trim(p_declaration),
                 'effective_from', p_from::text, 'effective_to', p_to::text,
                 'supersedes_id', p_predecessor::text));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION add_shg_instruction(uuid, uuid, text, text, text, text, date, date, timestamptz, text, text, text) OWNER TO migration_admin;
ALTER FUNCTION verify_shg_instruction(uuid, uuid, uuid, text, date, date, timestamptz, text, text, text) OWNER TO migration_admin;
ALTER FUNCTION correct_shg_instruction(uuid, uuid, uuid, text, text, text, date, date, timestamptz, text, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION add_shg_instruction(uuid, uuid, text, text, text, text, date, date, timestamptz, text, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION verify_shg_instruction(uuid, uuid, uuid, text, date, date, timestamptz, text, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION correct_shg_instruction(uuid, uuid, uuid, text, text, text, date, date, timestamptz, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION add_shg_instruction(uuid, uuid, text, text, text, text, date, date, timestamptz, text, text, text) TO app;
GRANT EXECUTE ON FUNCTION verify_shg_instruction(uuid, uuid, uuid, text, date, date, timestamptz, text, text, text) TO app;
GRANT EXECUTE ON FUNCTION correct_shg_instruction(uuid, uuid, uuid, text, text, text, date, date, timestamptz, text, text, text) TO app;

-- Direct writes close: the app role reads SHG instructions and writes
-- them only through the three lifecycle functions.
REVOKE ALL ON TABLE shg_instruction FROM app;
GRANT SELECT ON TABLE shg_instruction TO app;

-- Verify block: fail the migration loudly unless every SHG lifecycle
-- object below exists in its governed shape.
-- +goose StatementBegin
DO $$
DECLARE
    fn_path text[];
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_instruction_no_overlap') THEN
        RAISE EXCEPTION 'old per-fund overlap exclusion still present';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_status_values') THEN
        RAISE EXCEPTION 'SHG status CHECK missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_verified_consistency') THEN
        RAISE EXCEPTION 'SHG verified-triple CHECK missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_effective_order') THEN
        RAISE EXCEPTION 'SHG effective-order CHECK missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_instruction_supersedes_fk') THEN
        RAISE EXCEPTION 'SHG supersedes FK missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shg_instruction_single_successor') THEN
        RAISE EXCEPTION 'SHG single-successor UNIQUE missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'shg_instruction_lifecycle') THEN
        RAISE EXCEPTION 'SHG lifecycle trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'shg_instruction_basis_seq') THEN
        RAISE EXCEPTION 'SHG barrier trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'shg_instruction_created_at_normalize') THEN
        RAISE EXCEPTION 'SHG created_at trigger missing';
    END IF;
    IF NOT has_function_privilege('app',
            'public.add_shg_instruction(uuid, uuid, text, text, text, text, date, date, timestamptz, text, text, text)', 'EXECUTE')
       OR NOT has_function_privilege('app',
            'public.verify_shg_instruction(uuid, uuid, uuid, text, date, date, timestamptz, text, text, text)', 'EXECUTE')
       OR NOT has_function_privilege('app',
            'public.correct_shg_instruction(uuid, uuid, uuid, text, text, text, date, date, timestamptz, text, text, text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE the SHG lifecycle functions';
    END IF;
    IF has_table_privilege('app', 'public.shg_instruction', 'INSERT')
       OR has_table_privilege('app', 'public.shg_instruction', 'UPDATE')
       OR has_table_privilege('app', 'public.shg_instruction', 'DELETE') THEN
        RAISE EXCEPTION 'app still writes shg_instruction directly';
    END IF;
END
$$;
-- +goose StatementEnd

-- Section RULES. Rule-pack activation governance (Prompt 15 section 7, P5).
--
-- The adversarial review found app flipping packs to IMPLEMENTABLE
-- with invented versions. That ends here: activation records and
-- statutory parameter tables are read-only to app, and the only
-- activation path is a privileged function call carrying a
-- registry-pinned identity/version/digest triple plus recorded
-- evidence. BLOCKED stays the default; history is append-only and
-- supersession-preserving.

-- rule_pack is identity plus the reviewed pin, never status: a pack
-- the registry never heard of cannot activate, and a pack with no
-- reviewed pin has no version any caller may claim. New pins arrive
-- by migration as packs genuinely qualify; tests pin isolated
-- fixture packs, never real ones.
CREATE TABLE rule_pack (
    pack TEXT PRIMARY KEY,
    jurisdiction TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('contributions', 'levies', 'helper-boards', 'overtime',
        'hours', 'leave', 'holidays', 'pay-deadlines', 'records', 'test-fixture')),
    reviewed_version TEXT,
    manifest_digest TEXT,
    spec_version TEXT,
    verification_status TEXT,
    CHECK ((reviewed_version IS NULL) = (manifest_digest IS NULL))
);

-- Known pack identities, all unpinned: nothing here is activatable
-- until a reviewed version is pinned by a later migration.
INSERT INTO rule_pack (pack, jurisdiction, scope) VALUES
    ('SG-CPF-2026', 'SG', 'contributions'),
    ('SG-SDL', 'SG', 'levies'),
    ('SG-SHG', 'SG', 'helper-boards'),
    ('SG-MOM-KET', 'SG', 'records'),
    ('SG-MOM-OT', 'SG', 'overtime'),
    ('SG-MOM-OT-001', 'SG', 'overtime'),
    ('SG-MOM-SHIFT', 'SG', 'hours'),
    ('SG-MOM-LEAVE', 'SG', 'leave'),
    ('SG-MOM-PH', 'SG', 'holidays'),
    ('SG-MOM-PAYDEAD', 'SG', 'pay-deadlines');

-- Append-only activation history: every activation is a new row
-- linked to its predecessor, so superseding a version preserves the
-- original record and the previous applicable version stays
-- identifiable. No UPDATE or DELETE path exists for any role but the
-- schema owner.
CREATE TABLE rule_pack_activation_history (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL REFERENCES employer (id),
    pack TEXT NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('IMPLEMENTABLE', 'SUPERSEDED')),
    effective_from DATE,
    effective_to DATE,
    manifest_digest TEXT NOT NULL,
    evidence_refs JSONB NOT NULL,
    spec_version TEXT NOT NULL,
    review_ref TEXT NOT NULL,
    verification_status TEXT NOT NULL,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    supersedes_id uuid UNIQUE REFERENCES rule_pack_activation_history (id),
    activated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employer_id, pack, version),
    CHECK (effective_to IS NULL OR effective_from IS NULL OR effective_to >= effective_from)
);

-- The only activation path. The caller proves the pack against the
-- registry pin (identity, jurisdiction, scope, exact version,
-- manifest digest) and records its evidence; the function serializes
-- per employer pack, enforces the transition policy, appends history,
-- moves the current pointer and audits atomically.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION activate_rule_pack(
    p_employer uuid, p_pack text, p_version text, p_jurisdiction text, p_scope text,
    p_effective_from date, p_effective_to date, p_manifest_digest text, p_evidence_refs jsonb,
    p_spec_version text, p_review_ref text, p_verification_status text,
    p_actor text, p_reason text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    r_jurisdiction text;
    r_scope text;
    r_version text;
    r_digest text;
    c_version text;
    c_status text;
    v_tip uuid;
    v_id uuid;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'activate_rule_pack requires an actor';
    END IF;
    IF p_reason IS NULL OR length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'activation requires a reason';
    END IF;
    SELECT r.jurisdiction, r.scope, r.reviewed_version, r.manifest_digest
      INTO r_jurisdiction, r_scope, r_version, r_digest
      FROM public.rule_pack r
     WHERE r.pack = p_pack;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'unknown rule pack %', p_pack;
    END IF;
    IF p_jurisdiction IS DISTINCT FROM r_jurisdiction THEN
        RAISE EXCEPTION 'jurisdiction % does not match the registered jurisdiction % for pack %',
            p_jurisdiction, r_jurisdiction, p_pack;
    END IF;
    IF p_scope IS DISTINCT FROM r_scope THEN
        RAISE EXCEPTION 'scope % does not match the registered scope % for pack %',
            p_scope, r_scope, p_pack;
    END IF;
    IF r_version IS NULL THEN
        RAISE EXCEPTION 'rule pack % has no reviewed version pinned', p_pack;
    END IF;
    IF p_version IS DISTINCT FROM r_version THEN
        RAISE EXCEPTION 'version % is not the reviewed version % of pack %', p_version, r_version, p_pack;
    END IF;
    IF p_manifest_digest IS DISTINCT FROM r_digest THEN
        RAISE EXCEPTION 'manifest digest does not match the reviewed pin for pack % version %', p_pack, p_version;
    END IF;
    IF p_evidence_refs IS NULL OR p_evidence_refs = '{}'::jsonb THEN
        RAISE EXCEPTION 'activation requires recorded evidence references';
    END IF;
    IF p_spec_version IS NULL OR length(trim(p_spec_version)) = 0 THEN
        RAISE EXCEPTION 'activation requires a specification version';
    END IF;
    IF p_review_ref IS NULL OR length(trim(p_review_ref)) = 0 THEN
        RAISE EXCEPTION 'activation requires an independent review record';
    END IF;
    IF p_verification_status IS NULL OR length(trim(p_verification_status)) = 0 THEN
        RAISE EXCEPTION 'activation requires a verification status';
    END IF;
    IF p_effective_to IS NOT NULL AND p_effective_from IS NOT NULL AND p_effective_to < p_effective_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    -- One activation per employer pack at a time: concurrent attempts
    -- serialize here, so exactly one history row wins per version and
    -- the loser gets a deterministic already-active error.
    PERFORM pg_advisory_xact_lock(hashtext(p_employer::text || '|' || p_pack));
    SELECT a.version, a.status INTO c_version, c_status
      FROM public.rule_pack_activation a
     WHERE a.employer_id = p_employer AND a.pack = p_pack
     FOR UPDATE;
    IF FOUND AND c_status = 'IMPLEMENTABLE' AND c_version IS NOT DISTINCT FROM p_version THEN
        RAISE EXCEPTION 'pack % version % is already active', p_pack, p_version;
    END IF;
    IF FOUND AND c_status NOT IN ('BLOCKED', 'IMPLEMENTABLE') THEN
        RAISE EXCEPTION 'pack % activation is %, which no transition may leave', p_pack, c_status;
    END IF;
    IF FOUND AND c_status = 'IMPLEMENTABLE' THEN
        SELECT h.id INTO v_tip
          FROM public.rule_pack_activation_history h
         WHERE h.employer_id = p_employer AND h.pack = p_pack
         ORDER BY h.activated_at DESC LIMIT 1;
    END IF;
    INSERT INTO public.rule_pack_activation_history
        (id, employer_id, pack, version, status, effective_from, effective_to,
         manifest_digest, evidence_refs, spec_version, review_ref, verification_status,
         actor, reason, supersedes_id)
     VALUES (public.gen_random_uuid(), p_employer, p_pack, p_version, 'IMPLEMENTABLE',
             p_effective_from, p_effective_to, p_manifest_digest, p_evidence_refs,
             trim(p_spec_version), trim(p_review_ref), trim(p_verification_status),
             p_actor, trim(p_reason), v_tip)
     RETURNING id INTO v_id;
    INSERT INTO public.rule_pack_activation (employer_id, pack, version, status, activated_at)
     VALUES (p_employer, p_pack, p_version, 'IMPLEMENTABLE', now())
     ON CONFLICT (employer_id, pack)
     DO UPDATE SET version = EXCLUDED.version, status = 'IMPLEMENTABLE', activated_at = EXCLUDED.activated_at;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'rules', 'rule_pack.activate', 'rule_pack_activation_history', v_id,
             CASE WHEN c_version IS NULL THEN NULL
                  ELSE jsonb_build_object('version', c_version, 'status', c_status) END,
             jsonb_build_object('pack', p_pack, 'version', p_version, 'status', 'IMPLEMENTABLE',
                 'manifest_digest', p_manifest_digest, 'spec_version', trim(p_spec_version),
                 'review_ref', trim(p_review_ref), 'verification_status', trim(p_verification_status),
                 'supersedes_id', v_tip::text, 'reason', trim(p_reason)));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

-- Noting the BLOCKED default is the one activation-table write the
-- app role keeps, through this narrow function: it inserts a BLOCKED
-- row for a known pack and never touches an existing row, so it can
-- neither invent packs nor downgrade a live activation.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_pack_blocked(p_employer uuid, p_pack text) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.rule_pack r WHERE r.pack = p_pack) THEN
        RAISE EXCEPTION 'unknown rule pack %', p_pack;
    END IF;
    INSERT INTO public.rule_pack_activation (employer_id, pack, version, status)
     VALUES (p_employer, p_pack, 'v0', 'BLOCKED')
     ON CONFLICT (employer_id, pack) DO NOTHING;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION activate_rule_pack(uuid, text, text, text, text, date, date, text, jsonb, text, text, text, text, text) OWNER TO migration_admin;
ALTER FUNCTION ensure_pack_blocked(uuid, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION activate_rule_pack(uuid, text, text, text, text, date, date, text, jsonb, text, text, text, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION ensure_pack_blocked(uuid, text) FROM PUBLIC;
-- activate_rule_pack stays admin-only: no GRANT below is the point.
-- The app role may only note the BLOCKED default.
GRANT EXECUTE ON FUNCTION ensure_pack_blocked(uuid, text) TO app;

-- Direct writes close: activation records, the registry, the history
-- and the statutory parameter tables are read-only to app. The
-- registry is fully dark to app (defaults would otherwise grant it).
REVOKE ALL ON TABLE rule_pack FROM app;
REVOKE ALL ON TABLE rule_pack_activation FROM app;
GRANT SELECT ON TABLE rule_pack_activation TO app;
REVOKE ALL ON TABLE rule_pack_activation_history FROM app;
GRANT SELECT ON TABLE rule_pack_activation_history TO app;
REVOKE ALL ON TABLE mom_ot_params FROM app;
GRANT SELECT ON TABLE mom_ot_params TO app;
REVOKE ALL ON TABLE mom_shift_params FROM app;
GRANT SELECT ON TABLE mom_shift_params TO app;
REVOKE ALL ON TABLE cpf_rate_band FROM app;
GRANT SELECT ON TABLE cpf_rate_band TO app;
REVOKE ALL ON TABLE cpf_ceiling FROM app;
GRANT SELECT ON TABLE cpf_ceiling TO app;
REVOKE ALL ON TABLE sdl_params FROM app;
GRANT SELECT ON TABLE sdl_params TO app;
REVOKE ALL ON TABLE shg_contribution FROM app;
GRANT SELECT ON TABLE shg_contribution TO app;

-- Verify block: the registry holds the ten known identities, the
-- history and functions exist, and the privilege boundary holds.
-- +goose StatementBegin
DO $$
DECLARE
    v_packs int;
BEGIN
    SELECT count(*) INTO v_packs FROM public.rule_pack;
    IF v_packs <> 10 THEN
        RAISE EXCEPTION 'rule_pack registry holds % identities, want 10', v_packs;
    END IF;
    IF EXISTS (SELECT 1 FROM public.rule_pack WHERE reviewed_version IS NOT NULL) THEN
        RAISE EXCEPTION 'rule_pack registry pins a reviewed version: nothing may qualify at migration time';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'rule_pack_activation_history_employer_id_pack_version_key') THEN
        RAISE EXCEPTION 'activation history version UNIQUE missing';
    END IF;
    IF NOT has_function_privilege('app', 'public.ensure_pack_blocked(uuid, text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE ensure_pack_blocked';
    END IF;
    IF has_function_privilege('app',
            'public.activate_rule_pack(uuid, text, text, text, text, date, date, text, jsonb, text, text, text, text, text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app can EXECUTE activate_rule_pack';
    END IF;
    IF has_table_privilege('app', 'public.rule_pack_activation', 'INSERT')
       OR has_table_privilege('app', 'public.rule_pack_activation', 'UPDATE')
       OR has_table_privilege('app', 'public.rule_pack_activation', 'DELETE')
       OR has_table_privilege('app', 'public.rule_pack_activation_history', 'INSERT')
       OR has_table_privilege('app', 'public.rule_pack', 'SELECT') THEN
        RAISE EXCEPTION 'app keeps activation-table writes or registry reads';
    END IF;
END
$$;
-- +goose StatementEnd

-- Section SNAPSHOT. Immutable per-run input snapshots (Prompt 15
-- section 8, P2).
--
-- Capture runs one REPEATABLE READ transaction: the employer
-- population lock first, then employee pay-basis locks in UUID
-- order, then the required rule-pack keys, then resolution and a
-- content-bound SHA-256 over the canonical form. The population
-- lock is the boundary a concurrent new employment record cannot
-- cross mid-capture; the triggers below pull direct SQL into the
-- same convention.

-- Employer-level population barrier. Key calculated exactly once,
-- here: Go services and triggers call this helper, never their own
-- formula.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION acquire_payroll_population_lock(p_employer uuid)
RETURNS void
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pay-population:' || p_employer::text));
END
$$;
-- +goose StatementEnd

-- Per-pack activation key: capture pins required packs through this,
-- and the activation function takes the same key, so a supersession
-- cannot land mid-capture.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION acquire_rule_pack_lock(p_employer uuid, p_pack text)
RETURNS void
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('pay-pack:' || p_employer::text || ':' || p_pack));
END
$$;
-- +goose StatementEnd

ALTER FUNCTION acquire_payroll_population_lock(uuid) OWNER TO migration_admin;
ALTER FUNCTION acquire_rule_pack_lock(uuid, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION acquire_payroll_population_lock(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION acquire_rule_pack_lock(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION acquire_payroll_population_lock(uuid) TO app;
GRANT EXECUTE ON FUNCTION acquire_rule_pack_lock(uuid, text) TO app;

-- Direct-SQL population stability: employee and employment-term
-- writes take the population barrier in-trigger, so hand-rolled SQL
-- cannot slip a new employment record past a capture. INSERT and
-- UPDATE both qualify (a date change moves population membership);
-- DELETEs stay drift-detected, never silently mixed, because the
-- capture reads under REPEATABLE READ.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION take_payroll_population_lock() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    PERFORM public.acquire_payroll_population_lock(NEW.employer_id);
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER employee_population_barrier
    BEFORE INSERT OR UPDATE ON employee
    FOR EACH ROW EXECUTE FUNCTION take_payroll_population_lock();
CREATE TRIGGER employment_terms_population_barrier
    BEFORE INSERT OR UPDATE ON employment_terms
    FOR EACH ROW EXECUTE FUNCTION take_payroll_population_lock();

ALTER FUNCTION take_payroll_population_lock() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION take_payroll_population_lock() FROM PUBLIC;

-- Status transitions join the population barrier (OR REPLACE keeps
-- 00011's file untouched): the 00011 body with the population lock
-- ahead of its employee lock, preserving the global
-- population → employee order. The service already holds both above
-- this; the locks here serialize direct-SQL callers, and re-taking
-- held xact locks is a no-op.
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
    PERFORM public.acquire_payroll_population_lock(p_employer);
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

-- Term closes join the barrier the same way (OR REPLACE keeps
-- 00011's file untouched): a close can drop an employee out of the
-- expected population.
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
    PERFORM public.acquire_payroll_population_lock(p_employer);
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

-- The activation function takes the named pack key instead of its
-- inline formula (OR REPLACE keeps the RULES block above untouched):
-- capture and activation now serialize on one documented key.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION activate_rule_pack(
    p_employer uuid, p_pack text, p_version text, p_jurisdiction text, p_scope text,
    p_effective_from date, p_effective_to date, p_manifest_digest text, p_evidence_refs jsonb,
    p_spec_version text, p_review_ref text, p_verification_status text,
    p_actor text, p_reason text
) RETURNS uuid
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    r_jurisdiction text;
    r_scope text;
    r_version text;
    r_digest text;
    c_version text;
    c_status text;
    v_tip uuid;
    v_id uuid;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'activate_rule_pack requires an actor';
    END IF;
    IF p_reason IS NULL OR length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'activation requires a reason';
    END IF;
    SELECT r.jurisdiction, r.scope, r.reviewed_version, r.manifest_digest
      INTO r_jurisdiction, r_scope, r_version, r_digest
      FROM public.rule_pack r
     WHERE r.pack = p_pack;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'unknown rule pack %', p_pack;
    END IF;
    IF p_jurisdiction IS DISTINCT FROM r_jurisdiction THEN
        RAISE EXCEPTION 'jurisdiction % does not match the registered jurisdiction % for pack %',
            p_jurisdiction, r_jurisdiction, p_pack;
    END IF;
    IF p_scope IS DISTINCT FROM r_scope THEN
        RAISE EXCEPTION 'scope % does not match the registered scope % for pack %',
            p_scope, r_scope, p_pack;
    END IF;
    IF r_version IS NULL THEN
        RAISE EXCEPTION 'rule pack % has no reviewed version pinned', p_pack;
    END IF;
    IF p_version IS DISTINCT FROM r_version THEN
        RAISE EXCEPTION 'version % is not the reviewed version % of pack %', p_version, r_version, p_pack;
    END IF;
    IF p_manifest_digest IS DISTINCT FROM r_digest THEN
        RAISE EXCEPTION 'manifest digest does not match the reviewed pin for pack % version %', p_pack, p_version;
    END IF;
    IF p_evidence_refs IS NULL OR p_evidence_refs = '{}'::jsonb THEN
        RAISE EXCEPTION 'activation requires recorded evidence references';
    END IF;
    IF p_spec_version IS NULL OR length(trim(p_spec_version)) = 0 THEN
        RAISE EXCEPTION 'activation requires a specification version';
    END IF;
    IF p_review_ref IS NULL OR length(trim(p_review_ref)) = 0 THEN
        RAISE EXCEPTION 'activation requires an independent review record';
    END IF;
    IF p_verification_status IS NULL OR length(trim(p_verification_status)) = 0 THEN
        RAISE EXCEPTION 'activation requires a verification status';
    END IF;
    IF p_effective_to IS NOT NULL AND p_effective_from IS NOT NULL AND p_effective_to < p_effective_from THEN
        RAISE EXCEPTION 'effective_to must not precede effective_from';
    END IF;
    PERFORM public.acquire_rule_pack_lock(p_employer, p_pack);
    SELECT a.version, a.status INTO c_version, c_status
      FROM public.rule_pack_activation a
     WHERE a.employer_id = p_employer AND a.pack = p_pack
     FOR UPDATE;
    IF FOUND AND c_status = 'IMPLEMENTABLE' AND c_version IS NOT DISTINCT FROM p_version THEN
        RAISE EXCEPTION 'pack % version % is already active', p_pack, p_version;
    END IF;
    IF FOUND AND c_status NOT IN ('BLOCKED', 'IMPLEMENTABLE') THEN
        RAISE EXCEPTION 'pack % activation is %, which no transition may leave', p_pack, c_status;
    END IF;
    IF FOUND AND c_status = 'IMPLEMENTABLE' THEN
        SELECT h.id INTO v_tip
          FROM public.rule_pack_activation_history h
         WHERE h.employer_id = p_employer AND h.pack = p_pack
         ORDER BY h.activated_at DESC LIMIT 1;
    END IF;
    INSERT INTO public.rule_pack_activation_history
        (id, employer_id, pack, version, status, effective_from, effective_to,
         manifest_digest, evidence_refs, spec_version, review_ref, verification_status,
         actor, reason, supersedes_id)
     VALUES (public.gen_random_uuid(), p_employer, p_pack, p_version, 'IMPLEMENTABLE',
             p_effective_from, p_effective_to, p_manifest_digest, p_evidence_refs,
             trim(p_spec_version), trim(p_review_ref), trim(p_verification_status),
             p_actor, trim(p_reason), v_tip)
     RETURNING id INTO v_id;
    INSERT INTO public.rule_pack_activation (employer_id, pack, version, status, activated_at)
     VALUES (p_employer, p_pack, p_version, 'IMPLEMENTABLE', now())
     ON CONFLICT (employer_id, pack)
     DO UPDATE SET version = EXCLUDED.version, status = 'IMPLEMENTABLE', activated_at = EXCLUDED.activated_at;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (p_employer, p_actor, 'rules', 'rule_pack.activate', 'rule_pack_activation_history', v_id,
             CASE WHEN c_version IS NULL THEN NULL
                  ELSE jsonb_build_object('version', c_version, 'status', c_status) END,
             jsonb_build_object('pack', p_pack, 'version', p_version, 'status', 'IMPLEMENTABLE',
                 'manifest_digest', p_manifest_digest, 'spec_version', trim(p_spec_version),
                 'review_ref', trim(p_review_ref), 'verification_status', trim(p_verification_status),
                 'supersedes_id', v_tip::text, 'reason', trim(p_reason)));
    RETURN v_id;
END
$$;
-- +goose StatementEnd

-- Snapshot header: one row per capture version. content_hash is the
-- canonical SHA-256 over the inputs; calc_engine names the
-- unimplemented calculator honestly. Exactly one valid version per
-- run (partial unique below); drift invalidates, never rewrites.
CREATE TABLE payroll_snapshot (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employer_id uuid NOT NULL,
    payroll_run_id uuid NOT NULL,
    pay_period_id uuid NOT NULL,
    version INT NOT NULL CHECK (version >= 1),
    status TEXT NOT NULL CHECK (status IN ('valid', 'invalidated')),
    schema_version TEXT NOT NULL DEFAULT 'm2/v1',
    content_hash TEXT NOT NULL,
    calc_engine TEXT NOT NULL DEFAULT 'unimplemented',
    jurisdiction TEXT NOT NULL,
    currency TEXT NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    captured_by TEXT NOT NULL,
    invalidated_at TIMESTAMPTZ,
    invalidated_reason TEXT,
    UNIQUE (employer_id, id),
    UNIQUE (payroll_run_id, version),
    FOREIGN KEY (employer_id, payroll_run_id) REFERENCES payroll_run (employer_id, id),
    FOREIGN KEY (employer_id, pay_period_id) REFERENCES pay_period (employer_id, id)
);

CREATE UNIQUE INDEX payroll_snapshot_one_valid
    ON payroll_snapshot (payroll_run_id) WHERE status = 'valid';

-- Per-employee captured inputs: resolved references plus the
-- canonical inputs document and its row hash. inputs pins terms,
-- status, compensation segments, the approved sheet with its
-- attendance evidence, and the SHG tips; row_hash binds them.
CREATE TABLE payroll_snapshot_employee (
    snapshot_id uuid NOT NULL REFERENCES payroll_snapshot (id),
    employee_id uuid NOT NULL,
    employment_terms_ids uuid[] NOT NULL DEFAULT '{}',
    status_history_ids uuid[] NOT NULL DEFAULT '{}',
    employee_basis_seq BIGINT NOT NULL,
    approved_sheet_id uuid,
    decision TEXT NOT NULL CHECK (decision IN ('included', 'excluded', 'missing-timesheet', 'incomplete', 'conflict')),
    blockers JSONB NOT NULL DEFAULT '[]',
    inputs JSONB NOT NULL,
    row_hash TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, employee_id)
);

-- Required rule-pack pins: the exact version and status each pack
-- had at capture, with the evidence pin where one exists.
CREATE TABLE payroll_snapshot_pack (
    snapshot_id uuid NOT NULL REFERENCES payroll_snapshot (id),
    pack TEXT NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL,
    manifest_digest TEXT,
    effective_from DATE,
    effective_to DATE,
    review_ref TEXT,
    PRIMARY KEY (snapshot_id, pack)
);

-- The one legal snapshot mutation: valid to invalidated, with cause,
-- audited. Capture of a successor version is a separate INSERT the
-- partial unique admits once the tip is invalidated.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invalidate_payroll_snapshot(p_snapshot uuid, p_reason text, p_actor text)
RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_employer uuid;
    v_status text;
BEGIN
    IF p_actor IS NULL OR length(trim(p_actor)) = 0 THEN
        RAISE EXCEPTION 'invalidate_payroll_snapshot requires an actor';
    END IF;
    IF p_reason IS NULL OR length(trim(p_reason)) = 0 THEN
        RAISE EXCEPTION 'invalidation requires a reason';
    END IF;
    SELECT s.employer_id, s.status INTO v_employer, v_status
      FROM public.payroll_snapshot s
     WHERE s.id = p_snapshot
     FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'snapshot % not found', p_snapshot;
    END IF;
    IF v_status <> 'valid' THEN
        RAISE EXCEPTION 'snapshot % is already invalidated', p_snapshot;
    END IF;
    UPDATE public.payroll_snapshot
       SET status = 'invalidated', invalidated_at = now(), invalidated_reason = trim(p_reason)
     WHERE id = p_snapshot;
    INSERT INTO public.audit_event (employer_id, actor, service, action, entity, entity_id, before_state, after_state)
     VALUES (v_employer, p_actor, 'payroll', 'snapshot.invalidate', 'payroll_snapshot', p_snapshot,
             jsonb_build_object('status', 'valid'),
             jsonb_build_object('status', 'invalidated', 'reason', trim(p_reason)));
END
$$;
-- +goose StatementEnd

ALTER FUNCTION invalidate_payroll_snapshot(uuid, text, text) OWNER TO migration_admin;
REVOKE ALL ON FUNCTION invalidate_payroll_snapshot(uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION invalidate_payroll_snapshot(uuid, text, text) TO app;

-- Immutability under app: capture INSERTs once; no UPDATE, no
-- DELETE, so contents, membership, pins and hashes cannot change and
-- no child row can be re-parented.
REVOKE ALL ON TABLE payroll_snapshot FROM app;
GRANT SELECT, INSERT ON TABLE payroll_snapshot TO app;
REVOKE ALL ON TABLE payroll_snapshot_employee FROM app;
GRANT SELECT, INSERT ON TABLE payroll_snapshot_employee TO app;
REVOKE ALL ON TABLE payroll_snapshot_pack FROM app;
GRANT SELECT, INSERT ON TABLE payroll_snapshot_pack TO app;

-- Verify block: lock helpers, barrier triggers, re-locked functions,
-- snapshot tables with the one-valid unique, the invalidate path,
-- and the exact app privilege shape.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT has_function_privilege('app', 'public.acquire_payroll_population_lock(uuid)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE acquire_payroll_population_lock';
    END IF;
    IF NOT has_function_privilege('app', 'public.acquire_rule_pack_lock(uuid, text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE acquire_rule_pack_lock';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'employee_population_barrier') THEN
        RAISE EXCEPTION 'employee population trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'employment_terms_population_barrier') THEN
        RAISE EXCEPTION 'terms population trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'payroll_snapshot_one_valid') THEN
        RAISE EXCEPTION 'one-valid-snapshot unique missing';
    END IF;
    IF NOT has_function_privilege('app', 'public.invalidate_payroll_snapshot(uuid, text, text)', 'EXECUTE') THEN
        RAISE EXCEPTION 'app cannot EXECUTE invalidate_payroll_snapshot';
    END IF;
    IF has_table_privilege('app', 'public.payroll_snapshot', 'UPDATE')
       OR has_table_privilege('app', 'public.payroll_snapshot', 'DELETE')
       OR has_table_privilege('app', 'public.payroll_snapshot_employee', 'UPDATE')
       OR has_table_privilege('app', 'public.payroll_snapshot_employee', 'DELETE')
       OR has_table_privilege('app', 'public.payroll_snapshot_pack', 'UPDATE')
       OR has_table_privilege('app', 'public.payroll_snapshot_pack', 'DELETE') THEN
        RAISE EXCEPTION 'app keeps snapshot UPDATE or DELETE';
    END IF;
    IF NOT has_table_privilege('app', 'public.payroll_snapshot', 'INSERT') THEN
        RAISE EXCEPTION 'app cannot INSERT snapshots (capture path)';
    END IF;
END
$$;
-- +goose StatementEnd

-- Section RUNLIFE. Controlled payroll-run transitions (Prompt 15
-- section 9, P1; correction chain and validation hardening in P6).
--
-- Only draft -> validated is reachable in this milestone. Runs are
-- born draft; calculated, approved and finalized stay unreachable
-- until real calculation and approval artifacts exist — the trigger
-- below rejects those transitions for every role, service or
-- direct SQL alike.
--
-- Validation additionally requires exactly one valid snapshot tip
-- whose recorded employee decisions are all terminal
-- (included/excluded) and whose pinned packs are all
-- IMPLEMENTABLE, so direct SQL cannot bypass the blockers
-- assessment. Run identity (employer, period, kind, correction
-- link) freezes once any snapshot exists. Correction runs link
-- their original in the same employer and period; at most one
-- validated regular run stands per employer and period, and each
-- validated run takes at most one validated successor, so
-- corrections form one linear chain, never a second unrelated
-- run. The trigger gives the sequential refusal; the partial
-- unique indexes backstop races, which READ COMMITTED and
-- REPEATABLE READ snapshots cannot see.

ALTER TABLE payroll_run ADD COLUMN corrects_run_id uuid;
ALTER TABLE payroll_run ADD CONSTRAINT payroll_run_kind_link_ck
    CHECK ((kind = 'correction') = (corrects_run_id IS NOT NULL));
ALTER TABLE payroll_run ADD CONSTRAINT payroll_run_corrects_fk
    FOREIGN KEY (corrects_run_id) REFERENCES payroll_run (id);
CREATE UNIQUE INDEX payroll_run_one_validated_regular ON payroll_run (employer_id, pay_period_id)
    WHERE kind = 'regular' AND status = 'validated';
CREATE UNIQUE INDEX payroll_run_one_validated_successor ON payroll_run (corrects_run_id)
    WHERE status = 'validated';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_run_lifecycle() RETURNS trigger
    LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    v_tip uuid;
    v_tips int;
    v_other int;
    v_succ uuid;
    v_target record;
    v_snapped bool;
    v_blocked_emp int;
    v_blocked_pack int;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'draft' THEN
            RAISE EXCEPTION 'payroll runs are born draft, not %', NEW.status;
        END IF;
        IF NEW.corrects_run_id IS NOT NULL THEN
            SELECT employer_id, pay_period_id INTO v_target
                FROM public.payroll_run WHERE id = NEW.corrects_run_id;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'correction run target % does not exist', NEW.corrects_run_id;
            END IF;
            IF v_target.employer_id <> NEW.employer_id OR v_target.pay_period_id <> NEW.pay_period_id THEN
                RAISE EXCEPTION 'correction runs must target the same employer and pay period';
            END IF;
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.employer_id IS DISTINCT FROM OLD.employer_id
        OR NEW.pay_period_id IS DISTINCT FROM OLD.pay_period_id
        OR NEW.kind IS DISTINCT FROM OLD.kind
        OR NEW.corrects_run_id IS DISTINCT FROM OLD.corrects_run_id THEN
        SELECT EXISTS (SELECT 1 FROM public.payroll_snapshot WHERE payroll_run_id = OLD.id) INTO v_snapped;
        IF v_snapped THEN
            RAISE EXCEPTION 'run identity is frozen once a snapshot exists';
        END IF;
        IF NEW.corrects_run_id IS DISTINCT FROM OLD.corrects_run_id
            AND NEW.corrects_run_id IS NOT NULL THEN
            SELECT employer_id, pay_period_id INTO v_target
                FROM public.payroll_run WHERE id = NEW.corrects_run_id;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'correction run target % does not exist', NEW.corrects_run_id;
            END IF;
            IF v_target.employer_id <> NEW.employer_id OR v_target.pay_period_id <> NEW.pay_period_id THEN
                RAISE EXCEPTION 'correction runs must target the same employer and pay period';
            END IF;
        END IF;
    END IF;
    IF NEW.status IS NOT DISTINCT FROM OLD.status THEN
        RETURN NEW;
    END IF;
    IF OLD.status = 'draft' AND NEW.status = 'validated' THEN
        SELECT count(*) INTO v_tips
            FROM public.payroll_snapshot WHERE payroll_run_id = OLD.id AND status = 'valid';
        IF v_tips <> 1 THEN
            RAISE EXCEPTION 'run cannot validate without exactly one valid snapshot (found %)', v_tips;
        END IF;
        SELECT id INTO v_tip
            FROM public.payroll_snapshot WHERE payroll_run_id = OLD.id AND status = 'valid';
        SELECT count(*) INTO v_blocked_emp FROM public.payroll_snapshot_employee
            WHERE snapshot_id = v_tip AND decision NOT IN ('included', 'excluded');
        IF v_blocked_emp > 0 THEN
            RAISE EXCEPTION 'run cannot validate over % blocked employee decisions; only included/excluded snapshots validate', v_blocked_emp;
        END IF;
        SELECT count(*) INTO v_blocked_pack FROM public.payroll_snapshot_pack
            WHERE snapshot_id = v_tip AND status <> 'IMPLEMENTABLE';
        IF v_blocked_pack > 0 THEN
            RAISE EXCEPTION 'run cannot validate while % pinned packs are not IMPLEMENTABLE', v_blocked_pack;
        END IF;
        IF NEW.kind = 'correction' THEN
            SELECT status INTO v_target FROM public.payroll_run WHERE id = NEW.corrects_run_id;
            IF v_target.status IS DISTINCT FROM 'validated' THEN
                RAISE EXCEPTION 'correction run % cannot validate before its original % is validated', OLD.id, NEW.corrects_run_id;
            END IF;
            SELECT id INTO v_succ FROM public.payroll_run
                WHERE corrects_run_id = NEW.corrects_run_id AND id <> OLD.id AND status = 'validated'
                LIMIT 1;
            IF FOUND THEN
                RAISE EXCEPTION 'run % already has the validated successor %; link the correction to the latest validated run instead', NEW.corrects_run_id, v_succ;
            END IF;
        ELSE
            SELECT count(*) INTO v_other FROM public.payroll_run
                WHERE employer_id = NEW.employer_id AND pay_period_id = NEW.pay_period_id
                  AND id <> OLD.id AND status = 'validated';
            IF v_other > 0 THEN
                RAISE EXCEPTION 'a validated run already stands for this employer and pay period; open a correction run linked to it';
            END IF;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'run transition % -> % is not allowed in Milestone 2: only draft -> validated; calculated, approved and finalized need real calculation and approval artifacts',
        OLD.status, NEW.status;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER run_lifecycle
    BEFORE INSERT OR UPDATE ON payroll_run
    FOR EACH ROW EXECUTE FUNCTION check_run_lifecycle();

ALTER FUNCTION check_run_lifecycle() OWNER TO migration_admin;
REVOKE ALL ON FUNCTION check_run_lifecycle() FROM PUBLIC;

-- Verify block: the lifecycle trigger, the kind/link coherence
-- and the validated-run singletons stand.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'run_lifecycle') THEN
        RAISE EXCEPTION 'run lifecycle trigger missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payroll_run_kind_link_ck') THEN
        RAISE EXCEPTION 'run kind/link coherence check missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'payroll_run_one_validated_regular') THEN
        RAISE EXCEPTION 'validated-regular singleton index missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'payroll_run_one_validated_successor') THEN
        RAISE EXCEPTION 'validated-successor singleton index missing';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- Guarded forward-only rollback, mirroring 00011 Stage F: inspect
-- first, fail before anything destructive, and never silently drop
-- governed history. The reversal below runs only when every
-- dependent table is empty (freshly migrated development/test
-- databases). P1/P6 extend the guard and the reversal with their own
-- sections; each section below names its owner.
-- +goose StatementBegin
DO $$
DECLARE
    v_shg int;
    v_hist int;
    v_live int;
    v_corr int;
BEGIN
    -- SHG (P4): any instruction row blocks the unwind. Pending rows
    -- have no pre-00012 representation at all, and chained rows would
    -- lose their supersedes links, so the table must be empty.
    SELECT count(*) INTO v_shg FROM public.shg_instruction;
    IF v_shg > 0 THEN
        RAISE EXCEPTION '00012 Down refused: % SHG rows depend on this migration (forward-only; restore from backup)', v_shg;
    END IF;
    -- RULES (P5): any activation history or any non-default current
    -- status blocks the unwind. Plain BLOCKED rows predate the
    -- governance and survive it.
    SELECT count(*) INTO v_hist FROM public.rule_pack_activation_history;
    IF v_hist > 0 THEN
        RAISE EXCEPTION '00012 Down refused: % activation history rows depend on this migration (forward-only; restore from backup)', v_hist;
    END IF;
    SELECT count(*) INTO v_live FROM public.rule_pack_activation WHERE status <> 'BLOCKED';
    IF v_live > 0 THEN
        RAISE EXCEPTION '00012 Down refused: % non-default activation rows depend on this migration (forward-only; restore from backup)', v_live;
    END IF;
    -- SNAPSHOT (P2): any snapshot row blocks the unwind.
    SELECT count(*) INTO v_shg FROM public.payroll_snapshot;
    IF v_shg > 0 THEN
        RAISE EXCEPTION '00012 Down refused: % snapshot rows depend on this migration (forward-only; restore from backup)', v_shg;
    END IF;
    -- RUNLIFE (P6): any correction link blocks the unwind; the
    -- column has no pre-00012 representation.
    SELECT count(*) INTO v_corr FROM public.payroll_run WHERE corrects_run_id IS NOT NULL;
    IF v_corr > 0 THEN
        RAISE EXCEPTION '00012 Down refused: % correction runs depend on this migration (forward-only; restore from backup)', v_corr;
    END IF;
END
$$;
-- +goose StatementEnd

-- SHG reversal (P4): drop the lifecycle objects, restore the 00002
-- CHECKs and the 00004 per-fund exclusion verbatim, and hand direct
-- writes back to the app role.
DROP TRIGGER shg_instruction_created_at_normalize ON shg_instruction;
DROP FUNCTION normalize_shg_created_at();
DROP TRIGGER shg_instruction_lifecycle ON shg_instruction;
DROP FUNCTION check_shg_instruction();
DROP TRIGGER shg_instruction_basis_seq ON shg_instruction;
DROP FUNCTION add_shg_instruction(uuid, uuid, text, text, text, text, date, date, timestamptz, text, text, text);
DROP FUNCTION verify_shg_instruction(uuid, uuid, uuid, text, date, date, timestamptz, text, text, text);
DROP FUNCTION correct_shg_instruction(uuid, uuid, uuid, text, text, text, date, date, timestamptz, text, text, text);
ALTER TABLE shg_instruction DROP CONSTRAINT shg_status_values;
ALTER TABLE shg_instruction DROP CONSTRAINT shg_verified_consistency;
ALTER TABLE shg_instruction DROP CONSTRAINT shg_effective_order;
ALTER TABLE shg_instruction DROP CONSTRAINT shg_instruction_single_successor;
ALTER TABLE shg_instruction DROP CONSTRAINT shg_instruction_supersedes_fk;
ALTER TABLE shg_instruction DROP COLUMN supersedes_id;
ALTER TABLE shg_instruction DROP COLUMN basis_seq;
ALTER TABLE shg_instruction ADD CONSTRAINT shg_instruction_status_check
    CHECK (status IN ('active', 'opt_out', 'verified_none'));
ALTER TABLE shg_instruction ADD CHECK (
    status <> 'verified_none'
    OR (verification_basis IS NOT NULL AND verified_at IS NOT NULL)
);
ALTER TABLE shg_instruction ADD CONSTRAINT shg_instruction_no_overlap
    EXCLUDE USING gist (
        employer_id WITH =,
        employee_id WITH =,
        fund WITH =,
        daterange(effective_from, COALESCE(effective_to, 'infinity'::date), '[]') WITH &&
    );
GRANT SELECT, INSERT, UPDATE, DELETE ON shg_instruction TO app;

-- RULES reversal (P5): drop the activation path, the history and the
-- registry, and hand direct writes back to the app role. Plain
-- BLOCKED rows survive (the guard above proved nothing else does).
DROP FUNCTION activate_rule_pack(uuid, text, text, text, text, date, date, text, jsonb, text, text, text, text, text);
DROP FUNCTION ensure_pack_blocked(uuid, text);
DROP TABLE rule_pack_activation_history;
DROP TABLE rule_pack;
GRANT SELECT, INSERT, UPDATE, DELETE ON rule_pack_activation TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON mom_ot_params TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON mom_shift_params TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON cpf_rate_band TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON cpf_ceiling TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON sdl_params TO app;
GRANT SELECT, INSERT, UPDATE, DELETE ON shg_contribution TO app;

-- SNAPSHOT reversal (P2): drop the snapshot tables and the
-- invalidate path, remove the population barrier, and restore the
-- two re-locked pre-existing function bodies verbatim.
DROP FUNCTION invalidate_payroll_snapshot(uuid, text, text);
DROP TABLE payroll_snapshot_pack;
DROP TABLE payroll_snapshot_employee;
DROP TABLE payroll_snapshot;
DROP TRIGGER employee_population_barrier ON employee;
DROP TRIGGER employment_terms_population_barrier ON employment_terms;
DROP FUNCTION take_payroll_population_lock();
DROP FUNCTION acquire_payroll_population_lock(uuid);
DROP FUNCTION acquire_rule_pack_lock(uuid, text);
-- 00011 transition body verbatim (employee lock, no population lock).
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
-- 00011 E2 close body verbatim (no population lock).
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
-- activate_rule_pack stays dropped: it was born in the RULES block
-- above, so no pre-00012 body exists to restore. Only the two
-- re-locked pre-existing functions (transition, close) are restored.

-- RUNLIFE reversal (P1/P6): the lifecycle trigger, function,
-- singleton indexes and the correction link drop; runs return to
-- CHECK-only governance. The guard above proved no correction
-- link exists, so the column drop destroys no governed history.
DROP TRIGGER run_lifecycle ON payroll_run;
DROP FUNCTION check_run_lifecycle();
DROP INDEX payroll_run_one_validated_successor;
DROP INDEX payroll_run_one_validated_regular;
ALTER TABLE payroll_run DROP CONSTRAINT payroll_run_corrects_fk;
ALTER TABLE payroll_run DROP CONSTRAINT payroll_run_kind_link_ck;
ALTER TABLE payroll_run DROP COLUMN corrects_run_id;
