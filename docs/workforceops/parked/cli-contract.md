# CLI Contract — workforceops command surface

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

The CLI is the canonical automation and reproducibility interface. It calls the
same application services as the API and UI — never independent logic.

## 1. Global conventions

- `--dsn` > `MIGRATION_DATABASE_URL` (migrate only) > `DATABASE_URL`.
- `--format json|text` (default text for humans, json for scripts).
- `--workspace` selects the workspace (default: seeded guided-lab workspace).
- Exit codes: 0 success, 1 usage/runtime error, 2 unimplemented (until phased).
- All commands print the correlation ID of the operation they performed.

## 2. Command tree by phase

```text
workforceops
├── version                                  # Phase 1: implemented
├── migrate up|status                        # Phase 1: implemented
├── seed [--seed NAME] [--reset]             # Phase 2
├── oracle run --run-id ID                   # Phase 2
├── scenario list|run|reset ID               # Phase 3
├── investigate --ticket ID [--mode MODE]    # Phase 4
├── evaluate --run-id ID | --suite NAME      # Phase 5
├── import csv|json --file F --map M         # Phase 8A
├── workspace create|list|reset|manifest     # Phase 8A
└── demo [--scenario ID]                     # Phase 6 (full chain)
```

`task web:run` maps to the ShiftHub process (console co-hosted); there is no
`web` CLI command. Stubs exit 2 with `not implemented until Phase N` so scripts
can distinguish "missing" from "failed".

## 3. JSON output shapes (stable)

- `migrate up`: `{applied: [{version, path}]}`
- `scenario run`: `{ticket_id, scenario_run_id, seed, correlation_id}`
- `investigate`: `{run_id, verdict, report_path}`
- `evaluate`: `{run_id, scores: {dimension: value}, cost, duration_ms}`
- `demo`: `{fixture, ticket_id, run_id, report_md, evaluation}`

## 4. Non-goals

No interactive prompts (flags only — scriptable), no pager, no color coupling
(honor `NO_COLOR`), no business writes outside the service layer.
