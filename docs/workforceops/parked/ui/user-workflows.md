# UI User Workflows — journeys A–F + applicant loop

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Each journey lists entry → steps → success + failure/empty states. UI tests in
Phase 6 execute every journey including permission denials and empty states.

**Journey A — Developer onboarding.** Clone → `docker compose up -d` →
`task setup` → `task db:migrate` → `task test` → `task demo` → open console.
Success: seeded company visible. Empty: guided setup banner, never dead ends.

**Journey B — Payroll discrepancy.** Company → employee → timesheet → payroll
line → payslip → supporting evidence. Success: discrepancy traced to source
refs. Failure: clear "no data for period" states.

**Journey C — Integration failure.** Integrations → failed job → attempts →
correlation trail → destination state. Success: root hop identified with IDs.

**Journey D — Incident reproduction.** Scenario → fixture → inject → ticket →
symptoms. Success: ticket matches seeded symptoms; reset restores baseline.

**Journey E — AI investigation.** Ticket → start → hypotheses → evidence →
verification → report. Success: every claim expands to its source evidence.

**Journey F — Evaluation.** Suite → run → results → human/agent comparison.
Success: dimensional scores with evidence recall/precision; hidden material
visible to evaluators only.

**Journey G — Applicant loop (plan3.3 overarching test).** Provision/select
environment → import own data (8A) → operate → encounter incident →
investigate (SQL/API/logs) → resolve in sandbox → export portfolio report
labelled simulated enterprise experience. This is the MVP acceptance journey.
