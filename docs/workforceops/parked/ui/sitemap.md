# UI Sitemap — console routes (served from :8081 in v0.1)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

```text
/                              overview (role-aware)
/learn                         workspace: path, progress, active ticket
/learn/portfolio               portfolio entries + export
/labs                          scenario catalogue
/labs/:id                      fixture prep, inject, ticket, reset
/company                       company, departments, locations
/employees  /employees/:id     roster, terms, history
/workforce                     shifts, clock events, attendance, timesheets
/payroll                       periods, runs, lines, payslips, reconciliation
/integrations                  jobs, attempts, dead-letter, mappings
/workbench                     SQL editor, schema browser, history
/logs                          correlation search, event stream
/investigations/:id            hypotheses, evidence, claims, verdict, report
/mentor                        practice hints (practice mode only)
/interview/:id                 mock interview session
/assessment                    scores, comparisons (role-gated)
/rulebook                      packs, sources, verification status
/environments                  workspaces, imports, manifests (8A)
/api/*                         REST (see openapi.yaml, same host for ShiftHub)
```

Every route declares required role + workspace scope; denials render a
dedicated 403 page (tested). Investigator routes never receive
`evaluation_private` data — enforced server-side, verified by leak scans of
rendered HTML.
