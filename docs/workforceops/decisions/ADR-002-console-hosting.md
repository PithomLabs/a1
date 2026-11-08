# ADR-002: Console co-hosted on the ShiftHub API in v0.1

**Status:** accepted · **Date:** 2026-11-08

## Context

The plan fixes three processes, but the console UI needs a host. Options were
a fourth process or co-hosting on one API.

## Decision

Co-host console routes on `shifthub-api` (:8081) in v0.1, on a separate
connection pool using `investigator_readonly`, with module-contributed views.
The console is not ShiftHub — route groups and pools keep the boundary.

## Consequences

One fewer deployment unit; slight conceptual crowding on the ShiftHub host.
Revisit a dedicated console host only if auth, scaling or team ownership
demands it.
