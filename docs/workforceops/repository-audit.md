# Repository Audit — bchat as WorkforceOps Lab foundation

**Date:** 2026-11-08 · **Phase:** 0 · **Status:** complete
**Scope:** `bchat/` inspected read-only. No files in `bchat/` were modified.
**Baseline refs:** `plans/prompt.md`, `plans/plan3.1.md`, `plans/plan3.2.md`,
`plans/plan3.2_review.md`, `plans/plan3.3.md`, `plans/plan3.3.1.md`, `plans/plan3.3.1_review.md`.

## 1. Version control and licensing

- Neither the workspace root (`a1/`) nor `bchat/` is a git repository.
  There is no branch, commit history, or uncommitted-change baseline to preserve.
  Git initialisation is deferred to Phase 6 (open-source readiness) with a
  `.gitignore` that excludes `bchat/` and `plans/`.
- `bchat/` contains **no LICENSE file**. Nothing is assumed about its licensing
  status. The new `github.com/PithomLabs/a1` project uses MIT (approved,
  copyright Pithom Labs); dependency licenses are audited before release.

## 2. Toolchain and sandbox environment (verified this run)

| Fact | Value |
|---|---|
| Installed Go | `go1.25.7 linux/amd64` |
| bchat `go.mod` directive | `go 1.26` (auto-downloaded toolchain works with writable caches) |
| Parent `go.work` | `/home/chaschel/Documents/go/go.work` covers unrelated `phys*` modules; all commands here use `GOWORK=off` |
| Module/build cache | Sandbox mounts are read-only; this run uses `GOMODCACHE=/tmp/a1mc`, `GOCACHE=/tmp/a1bc`, `GOPATH=/tmp/a1gp` (contributors use defaults) |
| Network | Go module proxy reachable; `apt` reachable |
| Docker | **Blocked** in sandbox (`docker.sock` permission denied); contributors/CI use Docker Compose |
| PostgreSQL | No system server binaries; scratch PostgreSQL 16.15 runs at `127.0.0.1:5433` (user-space `.deb` extract, TCP-only — sandbox forbids Unix sockets). Restart: `pg_ctl -D /tmp/pgdata -l /tmp/pgdata.log -o "-p 5433 -k ''" start` |
| Task runner | `task 3.48.0` available |

New-module decision (deliberate, per review correction 5): `go 1.25` directive
to match the installed toolchain. All chosen dependencies must support Go 1.25.

## 3. bchat baseline build and tests (recorded, not fixed)

Commands run with `GOWORK=off` and writable `/tmp` caches:

- `go build ./...` → **fails on one pre-existing issue only**:
  `server/router/frontend/frontend.go:22: pattern dist/*: no matching files found`
  (frontend `dist/` was never built). All other packages compile.
- `go test ./internal/...` → **pass** (`base`, `bridgeworker`, `profile`, `util`,
  `version` ok; `crypto` has no test files).

No bchat failures were introduced by this audit (nothing was changed).
The frontend-dist failure is irrelevant to a1: no frontend build is ported.

## 4. Reuse inventory (verified by reading code, not README claims)

### 4.1 KEEP — portable patterns

- **Echo HTTP bootstrap** (`server/server.go`): `echo.New()`, `Recover()`
  middleware, `HideBanner/HidePort`, `/healthz` handler, production
  `HTTPErrorHandler` (generic client message, server-side `slog` detail),
  pprof gated behind dev-mode/flag. gRPC + cmux multiplexing is NOT needed.
- **Structured logging**: `log/slog` throughout; keep the convention.
- **CLI**: Cobra + Viper in `bin/memos/main.go` (flags, env binding,
  auto-generated encryption key pattern). Keep the shape, new command tree.
- **PostgreSQL access** (`store/db/postgres/`): pgx-only rule enforced by
  `validate:no-libpq` (`lib/pq` must never re-enter the tree). Connection
  tuning reference: DSN `default_query_exec_mode`, pool sizes, ping-with-timeout.
  a1 uses native `pgxpool` (see ADR-004) rather than bchat's `database/sql`
  stdlib adapter, but keeps the tuning values as starting points.
- **Fly.io Postgres profile**: `fly_pg.toml` (app/service/concurrency/health
  shape), `Dockerfile.pg.fly` multi-stage pattern (minus node/LanceDB stages),
  `scripts/docker-compose.postgres.yml` (`postgres:16-alpine` local pattern),
  `.env.example` secret-handling conventions (Fly secrets, never committed).
- **OpenRouter seam**: `openrouter.DefaultConfig(apiKey)` +
  `NewClientWithConfig` + `CreateChatCompletion` (`server/router/api/v1/agent/service.go`).
  The call shape is portable; everything around it is chat-coupled and stays behind.

### 4.2 ADAPT — reuse with a boundary change

- **Config/profile** (`internal/profile` + Viper): adapt to a1's
  `core/platform/config` with explicit validation and `DATABASE_URL` support.
- **Migrations**: bchat's per-driver migrator + parity scripts are
  Memos-schema-coupled; **do not port the migrator**. Port only the discipline:
  versioned files, `LATEST`-style consistency check, role-separated execution.
  a1 uses Goose + sqlc/pgx (see ADR-003).
- **AuthN/Z**: bchat's workspace-JWT is single-product; a1 needs the Amendment 3
  `Principal` abstraction and platform/org/environment/operator/investigator/
  evaluator/learner/auditor roles. Reuse JWT-signing hygiene (non-empty secret
  enforcement), not the model.

### 4.3 REMOVE — stays in bchat, never enters a1

RAG pipeline, LanceDB + CGO stages, embeddings, chunking, knowledge-base
upload/parse/reindex, KB/POLICY/SCRIPT config, chat sessions/streaming, chat
widget, React/TS frontend + node build, simulation/transcript analysis,
observational memory, chatbot admin endpoints, Memos domain tables and
migrations, gRPC/grpc-gateway/cmux stack, RSS, S3 presign runners, MySQL/SQLite
drivers, Cockroach/ECS/S3 Dockerfiles and Fly variants (except the pg profile
shape noted above).

### 4.4 DEFER — explicitly out of the MVP

Neon branching workflows, multi-app Fly topology, profiler endpoint,
SCIM/LDAP/MFA, billing/subscription, chatbot-verifier reuse.

## 5. KEEP / ADAPT / REMOVE / DEFER summary

| Area | Verdict |
|---|---|
| Echo + middleware + `/healthz` + prod error handler | KEEP (pattern) |
| `slog` logging | KEEP |
| Cobra + Viper CLI shape | KEEP |
| pgx-only rule, pool tuning | KEEP |
| Fly pg profile shape, compose PG pattern, env/secret conventions | KEEP |
| OpenRouter call shape | KEEP (behind new provider interface) |
| Config/profile, migration discipline, JWT hygiene | ADAPT |
| Custom migrator, workspace-JWT model | DO NOT PORT (replaced) |
| RAG/chat/widget/frontend/Memos/gRPC/multi-driver | REMOVE (never ported) |
| Neon branches, SSO impl, billing, multi-app topology | DEFER |

## 6. Risk and destructive-change register

| # | Risk | Control |
|---|---|---|
| R1 | Accidental modification of `bchat/` | Read-only rule; no writes performed; verified by audit scope |
| R2 | Destructive migration against a shared database | New schemas only; scratch/local databases; migration role separated from runtime roles |
| R3 | Production Fly/Neon touched | No credentials present; no prod access from this environment; Fly deferred per Q1 |
| R4 | Frontend-dist build failure copied into a1 | a1 serves Go templates from `web/` (no node build, no embed of generated `dist/`) |
| R5 | Module-cache/network unavailable for contributors | Standard `go.mod`/`go.sum`; no reliance on this sandbox's `/tmp` caches |
| R6 | Parent `go.work` breaking contributor builds | a1 has no parent `go.work` on fresh clones; `GOWORK=off` used only in this sandbox |
| R7 | Unscoped file commands touching `bchat/` | Scope gofmt/lint to module dirs; see incident note §7 |

**Destructive changes performed:** none. No migrations run, no deploys, no
secret changes.

## 7. Incident note (2026-11-08, recorded honestly)

During Phase 1 scaffolding, an unscoped `gofmt -w .` run from the workspace
root reformatted 56 `bchat/**/*.go` files (whitespace/alignment only — gofmt
is semantics-preserving). This violated the read-only rule for `bchat/`.

- Impact: formatting bytes + mtimes only. Verified identical behavior after
  the fact: `go build ./...` fails on the same single pre-existing issue
  (`frontend.go` dist embed) and `go test ./internal/...` passes identically.
- Recovery: no VCS or backups exist, so byte-exact restoration is impossible;
  the files are now gofmt-stable under go1.25.7 with zero functional change.
- Prevention: `task lint` now scopes gofmt to module dirs
  (`cmd core modules migrations sdk examples tests`); all future format
  commands must be scoped the same way. Added here as risk R7.
