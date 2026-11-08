# Scenario Format — reproducible incident definitions

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## 1. Principles

Mechanisms, not scripts: a scenario names a reusable, parameterized fault
mechanism; symptoms emerge from the faulty process. Same seed + versions +
scenario reproduces the same facts, fault and evidence.

## 2. Definition schema (YAML, module-owned under `scenarios/`)

```yaml
scenario_version: "0.1"
id: INC-001
title: Approved overtime missing from payroll
difficulty: intermediate
module: sg-wfm-payroll
seed: meridian-2026-v1
preconditions:
  employee_code: EMP-SG-000142
  pay_period: 2026-10
  timesheet_status: approved
mechanism:
  id: paycode-mapping-drop
  params: {source_pay_code: EXTRA_HOURS}
red_herrings:
  - resolved retry storm on export batch
ticket:
  severity: high
  body: "October overtime is missing from the November payslip..."
```

The definition contains **no diagnosis**. Expected root cause, causal chain,
required evidence, rejected hypotheses and resolution live in sealed
`evaluation_private.ground_truth`, written by the injector at scenario-run
time and never readable by investigator roles.

## 3. Lifecycle

`prepare` (clean fixture + seed) → `inject` (mechanism via real path) →
`ticket` (symptoms only) → `investigate` → `evaluate` → `reset` (back to
baseline). Every transition records a correlation ID and artifact hashes.

## 4. First three incidents

- **INC-001** — approved overtime dropped by pay-code mapping/filter.
  Trace: timesheet → export payload → mapping → import → payroll line → payslip.
- **INC-002** — hours truncated in sync (dedup collision / ordering /
  partial batch — one reusable mechanism, parameterized).
- **INC-003** — employee missing from payroll after HR update (external-ID
  mapping / cut-off ordering / dead-lettered sync).

Each ships one plausible-but-wrong alternative explanation and a scoring rubric.
A CPF rate/version scenario joins only after that rule pack passes its gate.
