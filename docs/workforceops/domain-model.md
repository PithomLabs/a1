# Domain Model — v0.1 single-model HR/WFM/payroll

**Status:** design gate candidate (rev. 2 — plan_prompt4 amendments) · **Date:** 2026-11-08

One application-owned model in a single PostgreSQL database, multi-employer
capable from the start: every employer-owned record carries `employer_id`,
business codes are unique per employer, and relationships use composite keys
so cross-employer references are rejected by the database, not by convention.
v0.1 operates exactly one employer; `employer` is the legal employing entity.

Conventions: `uuid` PKs; UTC instants plus IANA zone plus original local
context; money `NUMERIC(12,2)` SGD with exact `math/big.Rat` intermediates;
business effective time separate from recorded time; corrections supersede,
never overwrite; no `hours_worked` single field — scheduled, recorded, actual
and paid time are distinct. No authentication in v0.1 (loopback-only local
tool); the actor is a declared operator string on every audited write — an
attribution label, not an identity proof.

Every application service operates within an explicit employer context passed
from the CLI/API boundary (conceptually `EmployeeService.Create(ctx,
employerID, input)`). A service must never treat an unqualified ID as globally
accessible.

## 1. Employer and HR

- `employer(id, code [MERIDIAN…, UNIQUE], name, uen, address,
  jurisdiction, UNIQUE(jurisdiction, uen))` — tenant root; one row seeded
  (Meridian CX Services Pte. Ltd., fictional). CLI and API accept either the
  code or the UUID wherever an employer is selected.
- `employee(employer_id, id, employee_no [EMP-001…, unique per employer],
  legal_name, address, gender, date_of_birth, citizenship, status,
  status_effective_date)` — SC reference population; other citizenships
  rejected in v0.1. Address and gender hold synthetic values. **No NRIC
  column**: real-PII identity handling is deliberately unsupported and KETs
  record that exclusion explicitly (see §5).
- `employee_status_history(employer_id, employee_id, status, effective_from,
  effective_to, reason)` — append-only.
- `employment_terms(employer_id, id, employee_id, effective_from,
  effective_to, job_title, job_duties, employment_category
  [workman|non_workman|manager_executive], part_iv_eligible,
  eligibility_basis, monthly_basic, working_arrangement, normal_hours_weekly,
  working_days, daily_hours, rest_day, start_date, end_date, contract_type,
  fixed_term_duration, place_of_work, salary_period, overtime_period,
  notice_period, probation, medical_benefits)` — exactly one row covers any
  date; classification recorded explicitly, never inferred from title.
- `compensation_line(employer_id, id, terms_id, kind [allowance|deduction],
  code, amount_monthly, effective_from, effective_to)`.
- `ket_record(employer_id, id, terms_id, schema_version [ket/v0.1],
  issued_at, payload)` — immutable snapshots generated on hire and terms
  changes (schema §5).
- `leave_type(code, name)` seeded: annual, sick (global reference).
- `leave_request(employer_id, id, employee_id, type, from_date, to_date,
  days, status, reason, approved_by, approved_at)` — balances computed.

## 2. BPO operations and WFM

- `client(employer_id, id, code, name)`;
  `programme(employer_id, id, client_id, code, name, service_window,
  timezone, status)`.
- `programme_required_skill(employer_id, programme_id, skill_id,
  minimum_level)` — roster validation compares these against effective-dated
  employee skills; composite FK to programme keeps requirements in-employer.
- `team(employer_id, id, programme_id, code, name)`;
  `team_membership(employer_id, team_id, employee_id, role
  [agent|lead|supervisor], effective_from, effective_to)` — composite FKs to
  both team and employee keep memberships within one employer.
- `skill(id, code, name, kind [language|product|process])` — global catalogue;
  `employee_skill(employer_id, employee_id, skill_id, level, valid_from,
  valid_to)`.
- `shift_template(employer_id, id, code, name, start_local, end_local,
  cross_midnight)`; `shift_segment(employer_id, id, template_id, kind
  [on_queue|admin|break|meal|training|coaching|meeting|other],
  start_offset_min, duration_min, paid)`.
- `rotation_pattern(employer_id, id, code, name, cycle_days, pattern)`;
  `rotation_assignment(employer_id, id, pattern_id, employee_id, team_id,
  effective_from, effective_to)` — exactly one of employee/team; deterministic
  generation of draft assignments.
- `roster(employer_id, id, programme_id, week_starting, status
  [draft|published], legal_validation_status [verified|unavailable],
  legal_validation_pack_version, legal_validation_checked_at,
  legal_validation_result, current_version)` — the label always carries which
  pack version was checked, when, and with what result;
  `roster_revision(employer_id, id, roster_id, version, published_at,
  published_by, note)` immutable;
  `roster_assignment(employer_id, id, revision_id, employee_id, business_date,
  template_id, rest_day, status)` — assignments belong to a revision;
  publishing a change creates a new revision. Rest days designated; rostering
  work on a rest day is rejected.
  `UNIQUE(employer_id, employee_id, id)` lets attendance intervals reference
  an assignment only together with its own employee (see below).
- `programme_staffing_requirement(employer_id, programme_id, shift_code,
  weekday, min_headcount)` — flags obvious gaps, nothing more.
- `clock_event(employer_id, id, employee_id, occurred_at, timezone, kind
  [in|out|break_start|break_end], source, source_event_id,
  UNIQUE(employer_id, source, source_event_id))` —
  raw, immutable, deduped per source.
- `attendance_interval(employer_id, id, employee_id, roster_assignment_id,
  interval_start, interval_end, timezone, local_date, kind
  [work|break_paid|break_unpaid], source [clock|correction|assumption],
  source_refs, shift_identity, supersedes_id)` — the base time layer.
  Composite FK `(employer_id, employee_id, roster_assignment_id)` to the
  assignment's `(employer_id, employee_id, id)` makes a cross-employee link
  impossible by construction, even within one employer. Cross-midnight
  intervals split at local midnight for calendar attribution while retaining
  shift identity. Break intervals are recorded facts; where actual breaks are
  absent the seed states its narrow demo assumption explicitly instead of
  treating planned breaks as taken. `source_refs` schema:
  `{clock_event_ids: [uuid…], correction_id: uuid|null, assumption:
  {id, statement}|null}` — exactly one source populated; clock IDs must
  reference real `clock_event` rows (checked by service + tests; relational
  enforcement where it matters, see `timesheet_line_interval` below).
- `attendance_record(employer_id, employee_id, business_date,
  roster_assignment_id, scheduled_min, recorded_min, actual_min, paid_min,
  status)` — reviewed aggregate over intervals;
  `attendance_correction(employer_id, id, attendance_id, field, old_value,
  new_value, reason, corrected_by, corrected_at)` append-only with composite
  FK to its attendance record, creating superseding interval rows where time
  changes.
- `timesheet(employer_id, id, employee_id, pay_period_id, status
  [draft|submitted|approved|rejected], submitted_at, approved_by,
  approved_at)`; `timesheet_line(employer_id, id, timesheet_id, pay_code,
  hours, pay_period_id)` plus `timesheet_line_interval(employer_id, line_id,
  interval_id)` with composite FKs to both line and interval — every line
  traces to its source intervals through relational links, not free text.
  Immutable once the timesheet is approved or rejected; freely editable in
  draft (enforced by trigger, not by revoked grants).
- `pay_code(code, name, kind [earning|leave|info], default_treatment
  [OW|AW|NON_CPF_WAGE])` seeded: REGULAR, OT_STANDARD, ANNUAL_LEAVE,
  SICK_LEAVE, UNPAID_LEAVE, PH_PAID (global reference).
- `holiday_calendar(date, name)` — SG public holidays, global reference.
- `shg_instruction(employer_id, employee_id, fund
  [CDAC|MBMF|SINDA|ECF], status [active|opt_out|verified_none],
  amount_or_basis, source_declaration, verified_at, verified_by,
  verification_basis, effective_from, effective_to)` — no race/religion
  stored; no status is ever inferred. `verified_none` is an explicit evidenced
  outcome (basis recorded, e.g. below-threshold), never an assumption from
  missing data. A missing or ambiguous instruction still blocks the
  employee's net-pay approval.

## 3. Payroll

- `pay_period(employer_id, id, code [2026-11], starts_on, ends_on, pay_date,
  salary_deadline, ot_deadline, status [open|processing|approved|locked])`.
- `payroll_run(employer_id, id, pay_period_id, kind [regular|correction],
  status [draft|validated|approved], inputs_hash, rule_versions,
  calc_version, rounding_conventions, started_at, finished_at,
  approved_by)` — preview computes without persisting; approval is
  all-or-nothing per run (any blocked included employee halts it);
  approval locks; post-approval changes require a corrective run.
- `payroll_run_exclusion(employer_id, id, payroll_run_id, employee_id,
  reason, actor, excluded_at)` — explicit pre-validation exclusions with
  composite FKs proving run and employee share the employer. An excluded
  employee is not paid by that regular run, stays visible in the register
  and audit history, and must be handled through a separately controlled
  process. Exclusions cannot be added, changed or removed once the run is
  validated (database-enforced). An approved run discloses its exclusions
  and is never described as complete for all employees without qualification.
- `payroll_line(employer_id, id, run_id, employee_id, component, pay_code,
  hours, amount, cpf_treatment [OW|AW|NON_CPF_WAGE], work_period,
  payable_date, contribution_period, source_refs)` — overtime apportioned by
  performance month. `source_refs` schema (mandatory, non-null):
  `{timesheet_line_ids: [uuid…], terms_id: uuid, rule_pack_versions:
  {pack: version…}, calculation: {version, rounding_applied}}` — every amount
  reconstructs to its inputs; relational links (`timesheet_line_interval`)
  carry the interval proof beneath.
- `payslip(employer_id, id, run_id, employee_id, schema_version
  [payslip/v0.1], payload, generated_at, issued_at)` — immutable (schema §5).
  SDL appears as employer cost information, never as an employee deduction.
- `statutory_summary(employer_id, id, run_id, kind [cpf|sdl|shg], payload)` —
  export-ready summaries, no submission automation.
- `payroll_correction(employer_id, id, run_id, employee_id, reason,
  adjustment, created_by, status)` — requests that become corrective runs.

## 4. Rules, audit and plumbing

- Jurisdiction-global effective-dated rule data (seeded from verified
  research, never hardcoded): `mom_ot_params`, `mom_shift_params`,
  `cpf_rate_band`, `cpf_ceiling`, `sdl_params`, `shg_contribution`,
  `leave_entitlement_rule` — each with `effective_from/effective_to`; runs
  record versions consumed.
- `rule_pack_activation(employer_id, pack, version, status)` — an employer
  operates only on IMPLEMENTABLE pack versions. `version` pins the rule
  package version, `status` mirrors the package manifest; activation of a
  non-IMPLEMENTABLE package is rejected by the approval predicate.
- `audit_event(employer_id, id, occurred_at, actor, service, action, entity,
  entity_id, before, after)` — append-only.
- `idempotency_key(employer_id, key, owner, first_seen, result_ref)`.

## 5. KET and payslip schemas

`ket/v0.1` (generated from effective-dated terms + compensation; immutable;
applicable fields only, each inapplicable field carrying an explicit reason):
employer, employee (name, address, gender, dob; NRIC explicitly excluded with
reason `real_pii_unsupported`), job title and duties, contract type and
fixed-term duration, start date, working days, daily hours, rest day, salary
(basic + allowances itemised), overtime period and rate basis, leave types,
medical benefits, probation, notice period, salary period, issued-at version.

`payslip/v0.1` (immutable): salary period, payment date, basic salary,
allowances itemised, additional payments, overtime hours and pay, gross,
deductions itemised (CPF employee, SHG), net salary, employer CPF and SDL
shown as employer-cost information, rule versions, generated/issued timestamps.

## 6. Retention

Employment and salary records satisfy the two-year retention requirement
(current employees; leavers' records retained one year post-exit). Nothing is
hard-deleted in v0.1; retention is demonstrated by acceptance tests querying
aged fixtures, not by a purge system we do not yet need.
