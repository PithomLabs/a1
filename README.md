# WorkforceOps Lab

An open-source enterprise simulation, OJT and support-engineering assessment platform.

Individuals and organizations can create realistic business environments, import anonymized data, configure business workflows, execute operational transactions, simulate failures and investigate incidents.

Aspiring engineers use these environments to gain practical experience, prepare for interviews and demonstrate their troubleshooting capabilities. Organizations use the platform to reproduce business processes, test integrations, validate configurations and train their support teams.

The architecture accommodates additional business modules, private customer logic and future enterprise features such as SSO.

> **Status: Phase 1 skeleton.** The module builds, serves health probes, runs
> migrations and exposes module status routes. Business functionality arrives
> per the phased plan in `docs/workforceops/`. Nothing here is payroll,
> compliance or production software.

## Quickstart (local-first, no accounts needed)

```bash
cp .env.example .env            # then set POSTGRES_PASSWORD
docker compose up -d            # local PostgreSQL 16
task setup                      # go mod download
task db:migrate                 # apply core migrations
task run:shifthub               # API + console on :8081
task run:payloop                # API on :8082
task run:worker                 # sync worker
```

Probes: `GET /healthz` (liveness), `GET /readyz` (readiness incl. database).
Module status: `GET /api/v1/status` on either API.

## Layout

```text
cmd/                 # shifthub-api, payloop-api, sync-worker, workforceops CLI
core/                # platform kernel (must never import modules/ or examples/)
modules/sg-wfm-payroll/  # Singapore WFM/payroll reference module
sdk/module/          # module-authoring starter (Phase 7)
examples/            # tiny second-module proof (Phase 7)
migrations/core/     # kernel goose migrations
web/static/          # local CSS + vendored HTMX (no CDN, no npm)
scenarios/ fixtures/ # scenario definitions, fixed-seed fixtures
tests/arch/          # architectural boundary tests
docs/workforceops/   # audit, design artifacts, ADRs
```

## Commands

| Command | Purpose |
|---|---|
| `task build` / `task test` / `task lint` | Build, test, format+vet+dep rules |
| `task db:migrate` / `task db:status` | Core migrations |
| `task db:start` / `stop` / `logs` / `reset` | Local PostgreSQL lifecycle |
| `task run:shifthub` / `run:payloop` / `run:worker` / `web:run` | Processes |
| `task seed` / `oracle:run` / `scenario:*` / `investigate` / `evaluate` / `demo` | CLI surface (wired per phase; honest stubs until then) |

DB-gated tests run when `WORKFORCEOPS_TEST_DATABASE_URL` is set, otherwise skip.

## Documentation

- `docs/workforceops/repository-audit.md`, `refactor-plan.md` — Phase 0 audit
- `docs/workforceops/architecture.md` — system design and phase plan
- `docs/workforceops/decisions/` — architecture decision records
- `openapi.yaml` — REST API contract (implemented vs draft marked per path)
- `AGENTS.md` — working agreements for coding agents

## License

MIT, copyright Pithom Labs. See `LICENSE`.
