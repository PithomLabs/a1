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
