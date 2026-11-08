# Module Lifecycle

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

```text
author → validate (manifest + contract tests) → register (host boot)
  → migrate (module set after core) → activate (per workspace, Phase 8A+)
  → operate → upgrade (versioned, additive) → deactivate → remove
```

- **Validate:** `task module:validate PATH=` (Phase 7) runs manifest checks,
  contract tests and the boundary suite against a scratch database.
- **Register:** compiled in; hosts fail fast on duplicates or incompatibility.
- **Migrate:** module sets version independently; core never edits module
  tables and vice versa.
- **Activate:** workspaces pin module versions in the environment manifest;
  activation records live in `platform` (see `tenant-module-activation.md`).
- **Upgrade:** additive migrations + data backfills owned by the module;
  major versions may ship parallel schemas during transition (documented per
  module, not frameworked in v0.1).
- **Remove:** module `Down` chain is dev-only; production removal is a
  designed data-retention operation, never a cascade drop.
