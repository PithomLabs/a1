# ADR-003: Goose migrations + sqlc codegen (pinned)

**Status:** accepted · **Date:** 2026-11-08

## Context

The MVP plan prescribes Goose + sqlc; bchat ships a custom Memos-coupled
migrator with parity scripts.

## Decision

Use Goose v3.27.3 as an embedded library (Provider API, separate core/module
sets) and sqlc v1.30.0 for query codegen with pgx/v5 output. bchat's migrator
is not ported — only its discipline (versioned files, consistency checks,
role-separated execution). Hand-written pgx stays allowed where sqlc is awkward.

## Consequences

Standard tooling contributors already know; pins recorded here and in
`licensing-and-ip-boundaries.md`. sqlc config + first queries land in Phase 2.
