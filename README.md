# WorkforceOps Lab

An open-source Go-based enterprise HR, workforce-management and payroll application implementing realistic Singapore business rules and operational workflows, backed by PostgreSQL and populated with internally consistent synthetic business data.

v0.1 is one integrated backend for a fictional Singapore BPO employer: hire an employee, schedule them, record actual work, approve hours, and produce a traceable monthly payroll result — through a CLI and REST API sharing the same services. No UI, no microservices, no training platform yet.

> **Status: pivoted to the final MVP.** Design docs describe the v0.1 target;
> the scaffold is being restructured toward it. Nothing here is payroll,
> compliance or production software.

## Quickstart (local-first, no accounts needed)

```bash
cp .env.example .env            # then set POSTGRES_PASSWORD
docker compose up -d            # local PostgreSQL 16 (roles bootstrapped on first init)
task setup                      # go mod download
task db:migrate                 # apply migrations as migration_admin
task serve                      # API on 127.0.0.1:8081 (loopback only, no auth in v0.1)
```

Two database roles, two connections: runtime (`serve`, CLI, seed) connects as
`app` via `DATABASE_URL` and refuses superuser connections; migrations connect
as `migration_admin` via `MIGRATION_DATABASE_URL` and refuse any other role.
See `.env.example` and `docs/workforceops/database-design.md`.

Probes: `GET /healthz` (liveness), `GET /readyz` (readiness incl. database).

## Layout (target)

```text
cmd/workforceops        # single binary: serve + business commands
internal/               # platform, hr, wfm, attendance, payroll, audit services
rules/<RULE-ID>/        # research packages + pure calculation code (stdlib + math/big only)
migrations/             # single Goose set, public schema
fixtures/               # deterministic seeds and golden fixtures
tests/                  # contract, acceptance and architecture tests
docs/workforceops/      # live design docs (parked/ holds pre-pivot material)
```

## Commands

```bash
task build && task test && task lint   # before finishing any change
task db:migrate                        # apply migrations
task serve                             # run the API (after restructure)
task demo                              # scripted acceptance run (Phase: acceptance)
```

DB-gated tests run when `WORKFORCEOPS_TEST_DATABASE_URL` is set, otherwise skip.

## Documentation

- `docs/workforceops/mvp-scope.md` — frozen v0.1 scope and boundaries
- `docs/workforceops/domain-model.md` — entities and relationships
- `docs/workforceops/singapore-rule-scope.md` — rule packs and verification status
- `docs/workforceops/database-design.md` — tables, constraints, roles
- `docs/workforceops/api-cli-contract.md` + `openapi.yaml` — CLI/API contract
- `docs/workforceops/rule-source-register.md` — statutory provenance
- `docs/workforceops/parked/` — pre-pivot material (not authoritative)
- `AGENTS.md` — working agreements for coding agents

## License

MIT
