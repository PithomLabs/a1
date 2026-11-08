# Meridian demo dataset

`workforceops seed demo` loads a deterministic synthetic dataset through
the same services the CLI and API use. Re-running refuses unless
`--reset` deletes and reloads the MERIDIAN subtree first.

## Contents

- Employer MERIDIAN (synthetic UEN `SYNTH-DEMO-001`), one client
  (ACME), one programme (ACME-CARE, Asia/Singapore), one team (CARE-A).
- Three employees: DEMO-001 (clean week), DEMO-002 (Friday punch-out
  missing, no break evidence), DEMO-003 (overnight NIGHT shifts).
- DAY (09:00–18:00, unpaid meal 12:00–13:00) and NIGHT (22:00–06:00,
  unpaid break 02:00–03:00) templates; one published roster week
  2026-08-03 with Sunday rest.
- Clock import incl. an idempotent replay proving duplicate delivery.
- Pay period PP-2026-08; every rule pack activated BLOCKED at v0 so
  readiness reports the block explicitly.

## Contractual assumptions (demo only)

- Meridian's contractual overtime policy for supported profiles is
  exactly the statutory minimum 1.5×. Any arrangement paying above the
  minimum is out of the demo's scope and would surface as
  unsupported/`indeterminate`, per OT-001 `assume mvp_rate`.
- All names, UEN, addresses and declarations are synthetic and marked as
  such. No real personal data.
