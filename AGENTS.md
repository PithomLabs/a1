# AGENTS.md — working agreements for coding agents

## What this is

WorkforceOps Lab (`github.com/PithomLabs/a1`): an open-source Go HR/WFM/payroll
backend for a fictional Singapore BPO. One binary, one API, one CLI, one
PostgreSQL database. Local-first; cloud optional. `bchat/` is a read-only
reference — never modify it, never import from it.

## Non-negotiable boundaries

1. CLI and API invoke the same `internal/` services. No duplicated business
   logic, no handler-side calculations.
2. `rules/` packages are pure: stdlib plus `math/big` only (arch test
   enforced). No DB, HTTP, or time-of-day calls inside calculations.
3. No binary floating point for money. `NUMERIC(12,2)` storage, exact
   intermediates, rounding only at verified statutory boundaries.
4. Rates and thresholds are effective-dated rule data, never hardcoded
   constants. Runs record the rule versions consumed.
5. Statutory rules need pinned official sources, effective dates,
   applicability, and golden tests. Unverified cases return `indeterminate`
   or are rejected with a machine-readable code — never guessed.
6. Quarantined fixtures can never load in statutory runners (test enforced).
7. Corrections supersede via new auditable records; approved artifacts
   (timesheets, runs, payslips, KETs, clock events) are never edited in place.
8. No UI, no second service, no event bus, no RLS in v0.1. Runtime
   multi-employer operation is deferred, but employer-scoped schema
   readiness (employer_id boundary + composite keys) is mandatory.
   Anything on that list needs a new decision, not a quiet addition.
9. Never claim anonymization from validation, compliance from simulation, or
   production readiness from synthetic pass rates.

## Statutory rule workflow

All future L1 statutory/financial rules use the `rules/` Phys-Pseudo
package standard (`docs/workforceops/rules/adaptation-spec.md`); L2
selectively, L3 not required. Each payroll slice starts by advancing its
package through the lifecycle: Go work begins at `GOLDENS_VERIFIED` with
fixtures in hand, activation at `IMPLEMENTABLE` after independent review.
Every `indeterminate`/`422` path maps to an open spec `?`, listed in the
slice plan. Review checklist: every `derive` implemented, every `?` closed
with pinned evidence or mapped to a blocked path, no fixture generated
from the implementation.

## Commands

```bash
task setup && task db:migrate          # first run (after docker compose up -d)
task build && task test && task lint   # before finishing any change
```

DB-gated tests skip without `WORKFORCEOPS_TEST_DATABASE_URL`. Set it to run them.

## Conventions

- Env-first config; secrets via env only, never committed.
- JSON error envelope `{ "error": "<message>", "code": "<machine_code>" }`;
  request IDs on every request.
- Migrations are Goose `*.sql` with `Down` for local dev; roles ship separately.
- Hand-written pgx with explicit SQL; `lib/pq` forbidden.
- Every phase ends at its gate: files changed, tests actually run plus results,
  supported behavior, limitations, blockers, gate pass/fail. No gate is passed
  on unexecuted tests.


### The mandatory principle

Every remediation must correct the underlying invariant, not merely suppress the observed failure.

Before implementation, the agent must establish:

1. Business invariant: What truth must always hold, independently of implementation?
2. Root cause: What database, transaction, concurrency, relationship, privilege or business-logic mechanism allowed it to fail?
3. Complete write paths: Which Go services, CLI commands, REST handlers, SQL operations, triggers, database functions and administrative paths can affect it?
4. Authoritative enforcement: Where must the guarantee be enforced so that supported operations cannot bypass it?
5. Independent proof: How will the original failure be reproduced, the correction verified and persisted state inspected?
6. Regression analysis: What new failure modes could the correction introduce?
7. Acceptance: What evidence proves the entire class of failure has been addressed?

### Non-negotiable rules

- No service-only fix for an invariant that direct SQL can violate.
- No additional lock without a documented purpose, identity, acquisition order and transaction boundary.
- No timestamp substitution for transaction-ordering guarantees.
- No exception suppression or arbitrary retry loops to hide consistency failures.
- No weakening constraints to make tests pass.
- No treating missing evidence as verified information.
- No accepting a successful write that produces permanently unresolvable business data.
- No declaring a defect fixed merely because a regression test exists.
- No modifying historical records when controlled succession is required.
- No claiming a guarantee stronger than the actual evidence demonstrates.

The governing test is:

> What invariant was previously unenforced, which operations could violate it, and what now makes every such violation impossible or explicitly blocked within the documented system threat model?

I will include this principle in future WorkforceOps remediation prompts as a mandatory section, alongside root-cause analysis, enforcement boundaries, adversarial regression tests and proof of persisted-state correctness.

The objective is not to make the reported failure disappear. It is to make the underlying failure mechanism incapable of silently violating the business invariant.