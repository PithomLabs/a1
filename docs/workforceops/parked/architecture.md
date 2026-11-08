# Architecture — WorkforceOps Lab v0.1

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## 1. Product definition (frozen)

WorkforceOps Lab is an open-source enterprise simulation, OJT and
support-engineering assessment platform. Individuals and organizations can
create realistic business environments, import anonymized data, configure
business workflows, execute operational transactions, simulate failures and
investigate incidents. Aspiring engineers gain practical experience, prepare
for interviews and demonstrate troubleshooting capabilities. The architecture
accommodates additional business modules, private customer logic and future
enterprise features such as SSO.

## 2. System context

```text
            Applicants / Engineers / Evaluators
                          |
            +-------------+-------------+
            |             |             |
      Web console    REST API         CLI
      (:8081,        (:8081/:8082)    (workforceops)
      co-hosted)
            |             |             |
            +------+------+------+------+
                   |             |
        Go application services (one codebase, three processes)
                   |
     +-------------+-------------+--------------+
     |             |             |              |
 ShiftHub API  PayLoop API  sync-worker   module-registered
  (:8081)       (:8082)     (outbox/       services
                 |          delivery)      (sg-wfm-payroll)
                 |             |
                 +-----+-------+
                       |
              PostgreSQL 16 (local default; Neon optional)
     shifthub | payloop | platform | investigation | evaluation_private
```

One application, three interfaces, three processes, one database project with
hard schema/role separation. All business logic lives in Go application
services; handlers, templates and CLI verbs are thin adapters.

## 3. Processes and ports

| Process | Binary | Default port | Serves |
|---|---|---|---|
| ShiftHub API | `shifthub-api` | 8081 | ShiftHub routes, module status, console UI (v0.1, ADR-002) |
| PayLoop API | `payloop-api` | 8082 | PayLoop routes, module status |
| Sync worker | `sync-worker` | — | Outbox delivery, retries, reconciliation, scheduled jobs |
| CLI | `workforceops` | — | migrate/seed/oracle/scenario/investigate/evaluate/demo |

Processes share `core/api` (Echo host, probes, error envelope) and register the
same module set, but mount only their own route surfaces. Cross-system sync
flows through versioned payloads with correlation IDs — never direct table
access across the `shifthub`/`payloop` boundary.

## 4. Layering

```text
Presentation   CLI verbs · REST handlers · HTML templates (+HTMX)
Application    use cases: workforce, payroll, sync, scenario, investigation, evaluation
Domain         shifthub · payloop · rules · oracle · faults · evidence models
Infrastructure pgx pools · goose migrations · LLM adapters · workers · logging
```

Dependency rules (tested, not conventional): `core/` ↛ `modules/`,
`examples/`; module `oracle/` ↛ module `payloop/` calc packages; presentation
↛ infrastructure except through application services.

## 5. Data flows

**Clean slice (Phase 2):** employee/terms → roster → clock events → attendance
→ approved timesheet → export payload → import + pay-code mapping → payroll
line → payslip. Oracle recomputes expected results from canonical source facts
+ rule pack + terms + policy; PayLoop reconciles within rounding rules.

**Incident (Phase 3):** fault mechanism mutates processing/config through the
real path → observable symptom → ticket (symptoms only) → ground truth sealed
in `evaluation_private`.

**Investigation (Phase 4/5):** applicant or CaseTrace queries read-only tools
→ evidence ledger (hash-chained) → claims cite evidence → verifier replays →
verdict + report + portfolio; evaluator scores against hidden ground truth.

**Environment (8A):** workspace create → CSV/JSON import (map/preview/validate)
→ versioned config → reset/replay via manifest.

## 6. Environment manifest (v0)

Every workspace carries a manifest pinning module versions, migration
versions, rule-pack versions, dataset version + seed, config versions and
scenario state, so any environment replays bit-identically:

```yaml
manifest_version: "0.1"
modules: [{id: sg-wfm-payroll, version: 0.1.0}]
migrations: {core: 1, sg-wfm-payroll: 0}
rule_packs: [{id: SG-MOM-OT, version: 2026.1, status: unverified}]
dataset: {generator: sg-wfm-payroll, version: 0.1.0, seed: meridian-2026-v1}
config_version: 7
scenario_state: {active: null}
```

## 7. Final phase sequence and gates

| Phase | Deliverable | Gate |
|---|---|---|
| 0 | Audit + refactor plan | Docs complete; no bchat writes |
| 1 | This design package + working skeleton | Design review passes (this gate) |
| 2 | Reference slice + oracle + generator | Fixed-seed replay identical; oracle reconciles; unverified → indeterminate |
| 3 | Sync + INC-001–003 | All three reproduce from clean fixtures with stable evidence |
| 4 | CaseTrace + applicant agents | Read-only holds; sealed-evaluator tests green |
| 5 | Training/assessment/portfolio | Leak tests green; export reproducible without LLM |
| 6 | Lab console + OSS readiness | Credential-free demo; journeys green |
| 7 | Second module proof | Unmodified core runs both modules |
| 8A | Basic environments (early track) | Manifest replay; imported data drives full loop |
| 8B/9 | Advanced authoring, private staging | Documented only until a driver exists |

8A starts once the Phase 2 data model freezes and must land before Phase 6
UI sign-off (review reconciliation D1). No fault injection before baseline
reconciliation; no agent evaluation before the investigation path works.

## 8. Non-functional stance

Reproducibility outranks performance. Modest targets: list views p95 < 300 ms
on local hardware; `task demo` < 10 min; generator 100 staff × 6 months < 5 min.
Observability: structured JSON logs with request/correlation IDs; health +
readiness probes per process; append-only app events for every state-changing
job. Security posture: least-privilege roles, sealed answer key, read-only
investigation, honest capability labels (see `authorization-model.md`).
