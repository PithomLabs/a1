# ADR-004: Native pgxpool (not database/sql adapter)

**Status:** accepted · **Date:** 2026-11-08

## Context

bchat accesses Postgres through the pgx `database/sql` stdlib adapter; the
plan prescribes pgx + sqlc.

## Decision

Runtime access uses native `pgxpool` (typed errors, LISTEN/NOTIFY option,
first-class sqlc support). Pool sizing starts from bchat's proven values
(10/5/5m/1m, ping-with-timeout). The stdlib adapter is used only to hand a
`*sql.DB` to Goose, which requires it.

## Consequences

Two pgx APIs in the tree, each where its consumer demands it; `lib/pq` stays
banned everywhere (`task validate:nodeps` + arch test).
