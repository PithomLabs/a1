# Platform vs Module Boundary

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## Core owns (domain-free)

Module registry, tenancy/workspaces, authN/Z, database holder + migrator
runner, API/CLI/web shells, jobs/outbox, events/correlation, generic
connector framework, scenario runner + sealed ground-truth store, fault
registry, oracle execution contract, investigation lifecycle, evidence
ledger, evaluation harness, import pipeline, config-record store, audit,
version/compatibility checks.

Core must not name employees, payroll, CPF, overtime or any customer domain.
Enforced by the `tests/arch` dep-graph test; terminology grep is a heuristic
aid only (review correction 2).

## Modules own (domain-full)

Entities, workflows, rules, calculations, oracles, generators, adapters,
faults, scenarios, tools, rubrics, views, migrations, manifest. The reference
module additionally demonstrates every capability so others can copy it.

## Boundary tests (Phase 7 gate)

1. `go list -deps ./core/...` contains no `modules/` or `examples/` path.
2. Module registration works only through `module.Module` + `module.yaml`.
3. `order-fulfillment` registers and runs scenario→investigate→evaluate on an
   unmodified core.
4. Removing a module directory breaks only that module's tests.
