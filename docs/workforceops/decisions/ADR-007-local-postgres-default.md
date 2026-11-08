# ADR-007: Local PostgreSQL default, cloud optional

**Status:** accepted · **Date:** 2026-11-08

## Context

Q1 deferred Fly.io; the product must clone-and-run without vendor accounts.

## Decision

Docker Compose PostgreSQL 16 is the mandatory dev/test data plane with
checked-in role bootstrap. Neon (isolated project/branch) and Fly.io (draft
config, dedicated non-prod app) are optional, documented, and never required
for `task demo`.

## Consequences

Zero-credential onboarding; cloud paths verified only when authorized. The
sandbox recipe (user-space PG on :5433) is a dev-environment detail, not a
product dependency.
