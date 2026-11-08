# Domain Model — v0.1 bounded contexts

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Conventions: `id` is `uuid` PK unless noted; `created_at/updated_at` are
`TIMESTAMPTZ` (UTC instants); money is `NUMERIC(12,2)` SGD; all instants persist
in UTC with source offset retained; business dates derive in `Asia/Singapore`.

## 1. Tenant / workspace / environment (core)

- `tenant(id, slug, name)` — v0.1 ships one fictional tenant.
- `workspace(id, tenant_id, name, mode)` — `mode`: guided | sandbox | staging.
- `environment_manifest(id, workspace_id, version, manifest jsonb)` —
  append-only versions of the manifest in `architecture.md` §6.

## 2. ShiftHub — workforce source of truth (`shifthub` schema)

- `employee(id, employee_code, status, citizenship, date_of_birth, ...)`.
  `employee_code` like `EMP-SG-000142`; never real NRIC/FIN/bank data.
- `employment_terms(id, employee_id, effective_from, effective_until, class,
  monthly_basic, normal_hours_weekly, ...)` — `class`: workman | non_workman |
  manager_executive. Effective-dated; exactly one row covers any date.
- `department(id, code, name)`, `work_location(id, code, name)`.
- `shift_template(id, code, start_local, end_local, break_minutes)`.
- `roster_assignment(id, employee_id, template_id, business_date, location_id)`.
- `clock_event(id, employee_id, occurred_at, offset_minutes, kind, device_id,
  dedup_key)` — `kind`: in | out | break_start | break_end. Raw, immutable.
- `attendance_record(id, employee_id, business_date, paid_minutes,
  source_summary)` — interpreted from clock events; corrections are new rows
  superseding old ones, never updates.
- `timesheet(id, employee_id, pay_period_id, status, approved_at, approved_by)`.
- `timesheet_line(id, timesheet_id, pay_code, hours, source_ref)` — approved
  payable hours by source pay code (e.g. `EXTRA_HOURS` vs `OVERTIME`).

Invariants: clock events immutable; attendance derived, corrections supersede;
timesheets immutable once approved (a new revision supersedes).

## 3. PayLoop — downstream payroll (`payloop` schema)

- `pay_period(id, code, starts_on, ends_on, cut_off_at, status)`.
- `imported_employee(id, external_code, employee_ref, snapshot jsonb,
  imported_at, correlation_id)` — PayLoop's copy; never the master.
- `pay_mapping(id, source_pay_code, target_pay_code, effective_from,
  effective_until)` — versioned config, audited (review refinement D).
- `payroll_run(id, pay_period_id, rule_pack_version, calc_version,
  status, started_at, finished_at)`.
- `payroll_line(id, run_id, employee_ref, component, hours, amount,
  source_refs, correlation_id)` — every line traces to source refs.
- `payslip(id, run_id, employee_ref, payload jsonb, line_ids)` — itemised
  output + source line IDs.
- `export_batch(id, run_id, payload jsonb, status)` and
  `reconciliation(id, run_id, oracle_version, diff jsonb, status)`.

Invariants: every payroll line carries source refs + correlation ID; runs are
reproducible from (inputs, mappings, rule pack, calc version).

## 4. Platform — jobs and integration (`platform` schema)

- `outbox(id, aggregate, aggregate_id, event, payload, correlation_id,
  status, attempts, next_attempt_at)`.
- `delivery_attempt(id, outbox_id, target, request, response, status)`.
- `dead_letter(id, outbox_id, reason, payload, correlation_id)`.
- `idempotency_key(key, owner, first_seen, result_ref)` — unique per owner.
- `app_event(id, occurred_at, service, kind, correlation_id, payload)` —
  append-only operational history.

## 5. Investigation (`investigation` schema)

- `incident_ticket(id, scenario_id, title, body, severity, status)`.
- `agent_run(id, ticket_id, mode, model, status)` — `mode`: practice |
  assessment | interview | benchmark. Human runs record `actor = learner`.
- `hypothesis(id, run_id, statement, falsifier, status)`.
- `tool_execution(id, run_id, tool, params, row_count, result_hash)`.
- `evidence(id, run_id, tool_execution_id, source_type, ref, content_hash)`.
- `claim(id, run_id, hypothesis_id, statement)` + `claim_evidence(claim_id,
  evidence_id)` — every substantive claim cites ≥1 evidence.
- `verdict(id, run_id, root_cause, resolution, escalated, confidence)`.
- `portfolio_entry(id, learner_ref, ticket_id, run_id, report jsonb,
  exported_at)` — labelled simulated enterprise experience.

## 6. Evaluation, sealed (`evaluation_private` schema)

- `ground_truth(scenario_run_id, mechanism, params, causal_chain,
  required_evidence, red_herrings, expected_impact, resolution)`.
- `oracle_result(scenario_run_id, oracle_version, expected jsonb, diff jsonb)`.
- `rubric_score(run_id, dimension, score, notes)`.

## 7. Rules and versioned config

- `rule_pack(id, jurisdiction, version, status)` — `status`: unverified |
  reviewed | approved | superseded. Only overtime ships in v0.1.
- `rule(id, pack_id, rule_id, authority, source_url, section, retrieved_at,
  effective_from, effective_until, applicability jsonb, logic_ref, tests_ref)`.
- `config_record(id, kind, key, value jsonb, effective_from, effective_until,
  version, changed_by, change_reason)` — schedules, OT policies, mappings,
  thresholds, cut-offs, validation conditions. Never silently overrides
  statutory rules (enforced by a validation check in application services).

## 8. Import (8A, `platform` schema)

- `import_batch(id, workspace_id, source, mapping jsonb, status, stats jsonb)`.
- `rejected_record(batch_id, row_no, reason, raw jsonb)`.
