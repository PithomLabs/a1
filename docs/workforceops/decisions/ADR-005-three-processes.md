# ADR-005: Three processes + one CLI (no single-API collapse)

**Status:** accepted · **Date:** 2026-11-08

## Context

Amendment 2 sketched a single `cmd/api`; prompt.md requires ShiftHub, PayLoop
and sync-worker boundaries.

## Decision

Keep `shifthub-api`, `payloop-api`, `sync-worker` plus the `workforceops` CLI,
implemented as thin hosts over `core/` + statically registered modules.

## Consequences

Incident realism (INC-001–003) rests on genuine process + role boundaries.
Cost: three binaries to build/deploy — handled by one Dockerfile with Fly
process overrides (draft, undeployed per Q1).
