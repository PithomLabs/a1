# AGENTS.md — working agreements for coding agents

## What this is

WorkforceOps Lab (`github.com/PithomLabs/a1`): an open-source Go platform for
enterprise simulation, OJT and Tier 2 support-engineering assessment. Local
PostgreSQL is the default data plane; Neon/Fly.io are optional. `bchat/` is a
read-only reference — never modify it, never import from it.

## Non-negotiable boundaries

1. `core/` must never import `modules/` or `examples/` (enforced by
   `tests/arch/boundary_test.go` with the real dep graph, not greps).
2. The reference oracle must never import PayLoop calculation code (enforced by
   an import test from Phase 2 on, plus separate human review).
3. `lib/pq` is forbidden; `pgx` is the sole driver (`task validate:nodeps`).
4. No cross-schema business reads between ShiftHub and PayLoop; sync happens
   through the worker over APIs/payloads with correlation IDs.
5. `evaluation_private` is visible to the evaluator role only. No hidden answer
   may appear in API responses, templates, logs, HTML attributes or tool output
   reachable by investigators. Leak scans enforce this.
6. Read-only investigation: the agent path performs zero business writes.
   Permissions live in Go + Postgres grants, never in prompts.
7. Statutory rules need pinned official sources, effective dates, applicability
   and golden tests. Unverified cases return `indeterminate` — never guess.
8. No React/npm/CGO/`plugin`/Kafka/Redis. UI is Go templates + vendored HTMX.
9. Never claim anonymization from validation, compliance from simulation, or
   production readiness from synthetic pass rates.

## Commands

```bash
task setup && task db:migrate          # first run (after docker compose up -d)
task build && task test && task lint   # before finishing any change
go test ./core/database/ -run TestMigrate -v   # needs WORKFORCEOPS_TEST_DATABASE_URL
```

DB-gated tests skip without `WORKFORCEOPS_TEST_DATABASE_URL`. Set it to run them.

## Conventions

- Env-first config (`core/config`); secrets via env/Fly secrets, never committed.
- JSON error envelope `{ "error": "<message>" }`; request IDs on every request.
- Migrations are goose `*.sql` + `Down` for local dev; roles ship separately.
- sqlc (`sqlc.yaml`, pinned v1.30.0) generates query code into module packages;
  hand-written pgx is allowed where sqlc is awkward — never another ORM.
- Every phase ends at its gate: files changed, tests actually run + results,
  supported behavior, limitations, blockers, gate pass/fail. No gate is passed
  on unexecuted tests.
