# Authorization Model (Amendment 3)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## Roles (platform-level; modules may add domain permissions)

platform-admin, org-admin, env-admin, operator, investigator, learner,
evaluator, auditor (read-only), service.

## Rules

- Authorization is data, not code paths: a permission table
  (`platform.grant`: role × scope × action) evaluated by a single
  `core/auth` checker; handlers declare required permissions.
- Workspace scope rides every check; cross-workspace access is denied by
  default and audited when granted.
- Modules contribute domain permissions (e.g. `payroll.run`) but the platform
  enforces them; no module implements its own auth system.
- Investigator/learner paths are read-only by role + grants; the only writes
  are investigation-ledger appends and portfolio exports.
- Sealed material (`evaluation_private`, oracle outputs, ground truth) is
  evaluator-only; leak scans run on API fixtures and rendered HTML.
- No enterprise authorization engine (ABAC, policy DSL) in the MVP — explicit
  roles + scopes, cleanly separated for later extension.
