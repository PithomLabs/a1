# UI Information Architecture — Support Engineering Lab

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Stack: Go `html/template` + vendored HTMX + local CSS. No CDN, no npm, no
client-side business logic. Templates render server-side data only; every
action posts to the same application services as CLI/API.

## Areas

1. **My Learning Workspace** — enrolled path, progress, active ticket, portfolio.
2. **Incident Labs** — scenario catalogue, fixture prep, inject, ticket, reset
   (operator controls separated from investigator views).
3. **Business browser** — company → employees → workforce → payroll →
   integrations drill-down on real data.
4. **SQL Workbench** — editor, schema browser, history, export (read-only role).
5. **Logs & APIs** — correlation-ID search across app events, delivery attempts.
6. **Investigation workspace** — hypotheses, tool runs, evidence, claims,
   verification, verdict, report (record-first, never transcript-first).
7. **AI Mentor & Mock Interviews** — hints (practice), follow-ups (interview).
8. **Skill Assessment & Evaluation** — scores, missed evidence, comparisons
   (evaluator-gated for hidden material).
9. **Rulebook & Sources** — implemented packs, verification status, golden tests.
10. **Environments (8A)** — workspaces, imports, manifests, config versions.

Global shell: module-aware nav (platform owns shell; modules contribute
entries), role badge, environment badge (seed/versions), empty states with
setup guidance everywhere data may be absent.
