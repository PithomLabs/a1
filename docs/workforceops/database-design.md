# Database Design — schemas, roles, migrations

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## 1. Topology

One PostgreSQL 16 project (local Compose default; isolated Neon project or
branch when cloud is authorized). Five schemas, six least-privilege roles.
No row-level security in v0.1 — isolation comes from role grants plus
application-level tenant/workspace scoping (documented; RLS is Future C work).

## 2. Schemas

| Schema | Owner set | Purpose |
|---|---|---|
| `shifthub` | module migrations | Workforce source tables (§2 of domain model) |
| `payloop` | module migrations | Payroll tables (§3) |
| `platform` | core migrations | Outbox, delivery, idempotency, app events, import, config |
| `investigation` | core migrations | Tickets, runs, evidence ledger, portfolios |
| `evaluation_private` | core migrations | Ground truth, oracle results, rubric scores (sealed) |

## 3. Role matrix (grants)

| Role | Connect | Schemas (usage) | Tables |
|---|---|---|---|
| `migration_admin` | ✓ | all (all) | DDL; owns nothing at runtime |
| `shifthub_app` | ✓ | `shifthub` | CRUD on ShiftHub tables only |
| `payloop_app` | ✓ | `payloop` | CRUD on PayLoop tables only |
| `sync_worker` | ✓ | `shifthub`, `payloop`, `platform` | read sources, write imports/outbox/dead-letter |
| `investigator_readonly` | ✓ | `shifthub`, `payloop`, `platform`, `investigation` | SELECT (+INSERT on investigation ledger); SQL Workbench role |
| `evaluator` | ✓ | `investigation`, `evaluation_private` | SELECT investigation; full on sealed schema |

Hard denials (tested): `shifthub_app` ⇏ `payloop.*`; `payloop_app` ⇏
`shifthub.*`; `investigator_readonly` ⇏ `evaluation_private.*` and ⇏ any write
outside the investigation ledger; runtime roles ⇏ DDL.

## 4. Migration strategy

- Goose `*.sql` with `Up`/`Down`; `Down` supported for local dev only.
- Two embedded sets: `migrations/core` (runs first) and the module set under
  `modules/sg-wfm-payroll/migrations` (runs second, from Phase 2).
- `goose` version table lives in `platform` (`goose_db_version`).
- Consistency check (from Phase 2): `task validate:migrations` fails when a
  migration file changed after being recorded in a release tag (port of
  bchat's discipline).
- Role separation: `task db:migrate` uses `MIGRATION_DATABASE_URL`
  (`migration_admin`); runtime processes never hold it.

## 5. Key implementation patterns

- **Idempotency:** `platform.idempotency_key(key, owner)` unique; worker
  inserts before side effects, returns stored result on conflict.
- **Outbox:** business write + outbox insert in one transaction; worker claims
  with `FOR UPDATE SKIP LOCKED`, bounded exponential backoff, then dead-letter.
- **Correlation:** every cross-system record carries `correlation_id`
  (`uuid` generated at the boundary entry).
- **Audit:** `config_record` supersede-only; `app_event` append-only;
  attendance/timesheet corrections supersede, never mutate.
- **Timeouts:** statement timeout 5 s for investigator role
  (`ALTER ROLE ... SET statement_timeout`), 30 s default otherwise.

## 6. Bootstrap

- **Local:** `scripts/db/init-roles.sh` (compose init) creates roles + schemas +
  dev grants; `task db:migrate` converges objects. Passwords are dev-only and
  the script says so loudly.
- **Neon (when authorized):** same statements via a documented manual bootstrap
  run by a human with the project owner role; application roles receive
  generated secrets stored in Fly secrets, never in the repo.
- **Reset:** `task db:reset` (compose volume destroy) for local; scenario reset
  replays fixtures within a workspace without touching roles.

## 7. Connection security

Localhost may use `sslmode=disable` explicitly. Any non-localhost DSN must use
`sslmode=require` or stricter — enforced by a config validation check
(`core/config` rejects remote non-TLS URLs from Phase 2 on).
