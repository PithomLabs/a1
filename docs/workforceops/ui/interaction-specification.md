# UI Interaction Specification — HTMX patterns

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

- Full-page loads for navigation; HTMX partial swaps for filters, pagination,
  polling regions (job status, investigation progress) and form validation.
- Mutations use POST with explicit confirm steps for state-changing lab
  actions (inject, reset, import); every such action writes an audit record.
- Polling (not websockets) at ≥3 s intervals for run/job status; stops on
  terminal state. No auto-refresh of assessment content mid-session.
- Forms re-render with field errors; destructive buttons require typed or
  double confirmation.
- All timestamps render dual: absolute (`2026-10-31 14:02 SGT`) + relative.
- Copy-ID affordances on every entity (employee, batch, correlation, evidence).
- Keyboard: all actions reachable by Tab/Enter; skip links; focus-visible
  styles. Target WCAG 2.2 AA on core paths where practicable.
- Failure behaviour: request errors render inline retry cards with request ID;
  session/permission loss redirects to a dedicated page (never a silent empty).
- No data fetching from the browser except same-origin console endpoints;
  the browser never holds hidden-answer material (verified by leak scans).
