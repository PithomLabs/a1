# Tenant / Module Activation

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

- v0.1 ships one fictional tenant; the schema models many
  (`tenant`, `workspace`, `environment_manifest` in `domain-model.md`).
- A workspace activates module versions explicitly; activation records
  (`platform.module_activation`: workspace, module, version, status) gate
  which services, routes, tools and scenarios are visible there.
- Default guided-lab workspaces activate pinned reference versions so every
  learner starts from an identical, replayable state.
- Private customer modules activate only in customer-owned workspaces or
  builds (see `private-module-isolation.md`); activation metadata never leaks
  across workspaces.
- No elaborate multi-tenancy service in the MVP: one local environment, clear
  scoping columns, authorization checks on every service call, and per-module
  schema separation. True multi-tenant hosting is Future C/D work.
