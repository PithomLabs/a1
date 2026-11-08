# UI Permission Matrix — route × role

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Roles: platform admin, org admin, env admin, operator, investigator, learner,
evaluator, auditor (read-only), service. Matrix shows the console-relevant
subset; full model in `authorization-model.md`.

| Area | Learner | Investigator | Operator/Lab-admin | Evaluator | Auditor |
|---|---|---|---|---|---|
| Overview, business browser | ✓ | ✓ | ✓ | ✓ | ✓ |
| SQL Workbench (read-only) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Logs & APIs (own env) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Incident Labs: view ticket/symptoms | ✓ | ✓ | ✓ | ✓ | ✓ |
| Labs: inject / reset / re-seed | — | — | ✓ | — | — |
| Investigation workspace | own runs | ✓ | ✓ | ✓ | ✓ |
| Mentor hints | practice only | — | — | — | — |
| Mock interviews | own sessions | — | — | — | — |
| Assessment scores (own) | ✓ aggregate | ✓ aggregate | ✓ aggregate | ✓ full | ✓ aggregate |
| Hidden ground truth / oracle | — | — | — | ✓ | — |
| Rulebook | ✓ | ✓ | ✓ | ✓ | ✓ |
| Environments: import/configure | own ws | — | ✓ | — | — |
| Portfolio export | own | own | ✓ | ✓ | — |

Enforcement: route middleware (role) + service checks (workspace scope) + DB
grants (defense in depth). The LLM never enforces permissions. Every denial
path has a UI test; rendered HTML is leak-scanned for ground-truth markers.
