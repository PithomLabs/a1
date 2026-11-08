# UI Design System — tokens and components (v0)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Single local stylesheet (`web/static/lab.css`), no framework, no webfonts
(system stack). CSS variables for palette, spacing scale (4px base), radius,
and status colors with contrast-checked pairs:

- Statuses: ok / warn / error / info / muted — always paired with text labels,
  never color-only.
- Components: shell, nav, tiles, tables (sticky header, numeric right-align),
  tabs, badges/pills, code blocks (events, payloads, SQL), diff view (expected
  vs actual for reconciliation, evaluator-gated), forms, confirm dialogs,
  empty/loading/error states, provenance footers (seed, versions, hashes).
- Data honesty rules: simulated entities carry a `SYNTHETIC` marker style;
  agent conclusions are visually distinct from observed data; unverified rules
  render with warning treatment; portfolio exports stamp simulated enterprise
  experience.
- Density: comfortable for desktop/tablet; tables virtualize past 200 rows via
  pagination (no infinite scroll).
- Naming: BEM-ish classes prefixed `lab-`; modules reuse classes, never ship
  competing stylesheets.
