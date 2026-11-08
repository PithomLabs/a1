# ADR-008: Static module registration (no plugin system)

**Status:** accepted · **Date:** 2026-11-08

## Context

Extensibility must support public, community and private customer modules.

## Decision

Modules register through compiled Go code (`module.Module` + `module.yaml`
metadata) wired explicitly in host mains. No `plugin` package, no dynamic
loading, no marketplace, no billing in the MVP. Private modules compile into
customer-specific builds from separate repositories.

## Consequences

Reproducible builds and simple reasoning; adding a module means rebuilding —
acceptable and explicitly chosen. The contract freezes at Phase 7.
