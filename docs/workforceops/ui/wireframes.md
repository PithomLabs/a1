# UI Wireframes — structural sketches (text)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Dense, restrained operations tooling. Three persistent regions: left nav
(module-aware), top bar (role, workspace, environment seed/version, search),
content. No marketing hero, no chatbot canvas as primary surface.

**Overview:** stat tiles (employees, periods, jobs, open incidents — all live
counts) + recent investigations + environment status card. Unseeded state:
setup checklist card.

**Business drill-down:** master table (paginated, filterable) → detail page
with tab strip (e.g. employee: terms · roster · attendance · timesheets ·
payroll) → each tab links evidence by ID, never by screenshot.

**SQL Workbench:** left schema browser (schemas → views → columns, synthetic
only), center editor + run (read-only note + timeout/limit badges), bottom
results grid + export + history rail. Errors show sanitized database messages.

**Investigation:** left ticket + hypotheses checklist (open/rejected/confirmed
with falsifiers), center evidence table (source, ref, hash) and claims with
evidence chips, right verdict panel + verification results + export buttons
(Markdown/JSON).

**Evaluation:** dimensional score bars, missed-evidence list, human/agent
comparison table (aggregate only for non-evaluators).

**Rulebook:** pack cards with status pills (verified/conditional/unverified/
deferred/N-A), source links, effective dates, golden-test counts.
