# Refactor Plan — selective port from bchat to a1

**Date:** 2026-11-08 · **Phase:** 0 · **Status:** complete
**Strategy:** selective port into a new root module. `bchat/` stays read-only;
no in-place refactor, no deletions from bchat, no shared database objects.

## 1. Target module

- Module: `github.com/PithomLabs/a1`, directive `go 1.25` (deliberate; matches
  installed toolchain — review correction 5).
- License: MIT, copyright Pithom Labs (Q3 approved). Applied at scaffold time.
- Processes: `shifthub-api`, `payloop-api`, `sync-worker` (thin hosts loading
  `core/` + registering module capabilities), plus `workforceops` CLI.
- Default data plane: local PostgreSQL 16 via Docker Compose. Neon/Fly optional.

## 2. Target layout

```text
a1/
├── cmd/
│   ├── shifthub-api/        # ShiftHub host (Echo, module-registered routes)
│   ├── payloop-api/         # PayLoop host
│   ├── sync-worker/         # outbox/delivery/reconciliation worker
│   └── workforceops/        # CLI: migrate/seed/oracle/scenario/investigate/evaluate/demo
├── core/                    # platform kernel — MUST NOT import modules/
│   ├── module/              # module contract, manifest, registry
│   ├── tenant/              # tenant + workspace + environment activation
│   ├── auth/                # Principal, local auth provider, authorization
│   ├── database/            # pgxpool holder, migrator runner, roles
│   ├── api/                 # Echo bootstrap, JSON conventions, errors, pagination
│   ├── cli/                 # Cobra/Viper bootstrap helpers
│   ├── web/                 # template shell, HTMX/static, navigation registry
│   ├── workflow/            # job/outbox primitives
│   ├── events/              # append-only app events, correlation IDs
│   ├── integration/         # generic connector framework (no domain assumptions)
│   ├── simulation/          # scenario runner, ground-truth store (sealed)
│   ├── fault/               # reusable fault-mechanism registry
│   ├── oracle/              # oracle execution contract + reproducibility types
│   ├── investigation/       # generic case/hypothesis/evidence/claim/verdict lifecycle
│   ├── evidence/            # evidence ledger store + hashing
│   ├── evaluation/          # generic scoring harness + rubrics contract
│   ├── import/              # CSV/JSON import, mapping, validation (8A)
│   ├── rulesconfig/         # versioned/effective-dated/audited config records (D)
│   └── observability/       # slog setup, request IDs, health
├── modules/
│   └── sg-wfm-payroll/      # reference module: domain, rules, oracle impl,
│       │                   # generator, scenarios, tools, migrations, web views
│       └── module.yaml
├── sdk/module/              # minimal module-authoring Go starter + docs
├── examples/modules/order-fulfillment/   # tiny Phase 7 second module
├── migrations/core/         # platform/investigation/evaluation_private
├── web/static/              # local CSS + vendored HTMX (no CDN, no npm)
├── scenarios/               # scenario definitions (module-owned content)
├── fixtures/                # fixed-seed fixtures
├── docs/workforceops/       # Phase 0/1 design artifacts (this directory)
├── tests/                   # cross-interface + architecture-boundary tests
├── docker-compose.yml .env.example Taskfile.yml Dockerfile fly.toml
├── openapi.yaml go.mod go.sum README.md AGENTS.md LICENSE
└── CONTRIBUTING.md SECURITY.md (completed in Phase 6)
```

## 3. Port list (bchat source → a1 destination)

| # | bchat source | a1 destination | Treatment |
|---|---|---|---|
| P1 | `server/server.go` Echo setup, `/healthz`, prod error handler, pprof gate | `core/api`, `core/observability` | Port pattern, drop gRPC/cmux/frontend |
| P2 | `bin/memos/main.go` Cobra/Viper bootstrap | `core/cli`, `cmd/workforceops` | Port shape, new command tree |
| P3 | `internal/profile` config + validation | `core/platform`→`core/database`+`core/auth` config | Adapt to env-first config |
| P4 | `store/db/postgres` pool tuning, DSN handling, pgx-only rule | `core/database` | Port values; native `pgxpool` |
| P5 | Migration discipline (versioned files, consistency check) | `migrations/`, `task db:*` | Discipline only; Goose runner |
| P6 | `fly_pg.toml`, compose PG service, `.env.example` conventions | `fly.toml`, `docker-compose.yml`, `.env.example` | Port shape, new names/ports |
| P7 | OpenRouter call shape (`agent/service.go`) | `core/llm` provider interface + `openrouter` impl + `mock` | Seam only, behind interface |
| P8 | `slog` usage, request-ID logging | `core/observability` | Port convention |
| P9 | JWT non-empty-secret enforcement | `core/auth` local provider | Hygiene only, new model |

Nothing else is ported. In particular: no RAG, chat, widget, React, Memos
tables, gRPC, multi-driver store, or custom migrator code.

## 4. Database plan (detail: `database-design.md`)

- Core migrations own `platform`, `investigation`, `evaluation_private`.
- Module migrations own `shifthub`, `payloop`.
- Roles: `migration_admin`, `shifthub_app`, `payloop_app`, `sync_worker`,
  `investigator_readonly` (SQL Workbench role — read-only enforced by grants),
  `evaluator`, `a1_app` (local-dev convenience, localhost only).
- `evaluation_private` is granted to `evaluator` only; sealed from all
  investigator/API/UI paths and from ground-truth leak scans.

## 5. Tooling decisions (ratified in ADRs)

- Go 1.25 directive; Echo v4; pgx/v5 native (`pgxpool`); Cobra + Viper;
  Goose (embedded, `goose` lib) for migrations; sqlc for query codegen with
  pgx/v5 emit (pinned version recorded in `docs`; hand-written pgx stays
  allowed where sqlc is awkward); `html/template` + vendored HTMX + local CSS;
  OpenAPI maintained by hand alongside handlers (spec-first for new resources).
- `lib/pq` forbidden (adopt bchat's `validate:no-libpq` as `task validate:nodeps`).
- No CGO, no node, no `plugin` package, no Kafka/Redis/K8s.

## 6. What is deliberately NOT built (MVP fence)

React/Next/Vue, RAG/embeddings/vector DB, native PG export/import, automated
anonymization, advanced rules engine, custom-Go-module runtime loading,
snapshot/clone infra, multi-environment hosting, SSO/OIDC/SAML/SCIM
implementations (or stubs), billing, SIEM, multi-app Fly topology.

## 7. Verification spine (detail per phase in design docs)

- `task build/test/lint`, `go vet ./...`, dep-graph + oracle-import boundary
  tests, role-denial tests, answer-key isolation + leak scans, normalized
  cross-interface consistency, manifest-replay tests, fresh-clone
  `docker compose up -d && task setup && task demo`.
- DB-gated tests run here against scratch PG `127.0.0.1:5433`
  (`WORKFORCEOPS_TEST_DATABASE_URL`); contributors use compose (`:5432`).
