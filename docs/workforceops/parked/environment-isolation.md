# Environment Isolation (Amendment 3)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

An environment (workspace instantiation) eventually owns: module versions,
business rules, users/groups, integration credentials, identity config,
database state, scenarios, audit history. Isolation ladder:

1. **v0.1 (this plan):** one reproducible local environment; workspace scoping
   columns + authorization checks; sealed-schema grants; manifest-pinned replay.
2. **8A:** multiple workspaces in one database with strict scoping tests and
   per-workspace service checks; imports fenced to the owning workspace.
3. **Future A:** separate databases/containers per environment for customer
   staging; promotion/snapshot/clone operations.

A learner must never observe another workspace's data; tests assert this with
cross-workspace fixtures. Raw imported data never leaves the owning workspace
and is never submitted to external LLM providers automatically.
