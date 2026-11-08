# API/CLI Contract — v0.1 single binary

**Status:** design gate candidate (rev. 3 — Prompt7 Milestone 1 served surface) · **Date:** 2026-11-08
**Machine contract:** root `openapi.yaml` (all business paths `draft` until
their slice lands). One binary, one API, one CLI, one service layer — the CLI
and API invoke the same application services and must never duplicate business
logic. Every operation resolves an employer context at the boundary and passes
it down; services never accept unqualified IDs.

## 1. Serving and safety

`workforceops serve` binds `127.0.0.1:8081` by default. `--bind` accepts
loopback addresses only in v0.1 and the process refuses anything else: there
is no authentication, so remote binding would expose a mutating API to the
network. The declared `--operator` string on audited writes is attribution,
not identity — anyone reaching the API can claim any operator name. That is
acceptable only for a loopback-only synthetic-data tool, and the contract
says so wherever the operator flag is documented.

## 2. CLI (`workforceops`)

```text
workforceops serve [--bind 127.0.0.1:8081] [--operator NAME]
workforceops migrate up|status
workforceops seed demo [--reset]
workforceops version
workforceops --employer MERIDIAN <command>     # code or UUID; defaults while exactly one employer configured

workforceops employer show
workforceops employee create|list --file F
workforceops employee status EMP-001 --to STATUS --reason R
workforceops employee terms set EMP-001 --file terms.json

workforceops client create|list --file F
workforceops programme create|list --file F
workforceops team create|list --file F
workforceops team add-member TEAM EMP
workforceops skill list|assign EMP-001 --file skills.json

workforceops shift-template create|list --file F
workforceops rotation pattern create|list --file F
workforceops rotation assign|list --file F
workforceops roster create|show|publish|list|assign --file F
workforceops attendance import|review|correct --file F
workforceops timesheet generate|show|submit|approve|reject
workforceops payroll readiness PERIOD

# Not in Milestone 1 (no such commands yet):
#   leave request|approve|balance; payroll preview|validate|run|approve;
#   payslip export; payroll export
```

File-based inputs (`--file` JSON, schema in §4) instead of dozens of flags.
Flags only, `--format json|text`, `--dsn` overrides env; exit 0 success,
1 usage/runtime error. There is no exit-2 unimplemented convention:
unimplemented commands are simply absent, and unsupported business cases fail
with the same machine-readable `422` codes as the API. Every mutating CLI
command accepts `--operator` (default: OS user, overridable) and refuses to
run when neither is set; the value is recorded verbatim as attribution,
never verified as identity.

## 3. REST API (`/api/v1`, JSON)

Employer-scoped paths from the beginning:

```text
/employers/{employer_id}
/employers/{employer_id}/employees[ /{id} | /{id}/terms | /{id}/terms/{terms_id}/compensation | /{id}/shg | /{id}/ket ]  # ket: draft, unserved
/employers/{employer_id}/clients|skills
/employers/{employer_id}/programmes[ /{id}/require-skills ]
/employers/{employer_id}/teams[ /{id}/members ]
/employers/{employer_id}/shift-templates|rotations|rosters[ /{id} | /{id}/assignments | /{id}/publish | /{id}/revise ]
/employers/{employer_id}/clock-events|attendance|attendance/intervals[ /{id}/corrections ]
/employers/{employer_id}/leave-requests[ /{id}/approve ]                      # draft, unserved
/employers/{employer_id}/timesheets[ /generate | /{id} | /{id}/submit|approve|reject|reissue ]
/employers/{employer_id}/pay-periods|payroll-readiness
/employers/{employer_id}/payroll-runs[ /{id}/exclusions ]                     # runs: exclusion containers only; no calculations
/employers/{employer_id}/payslips|payroll-register                            # draft, unserved
/rule-packs                                  # jurisdiction-global, read-only
/employers/{employer_id}/rule-activations   # read-only activation status
```

The `{employer_id}` segment accepts an employer code or UUID. API writes
require an explicit `X-Operator` header (no silent default), recorded like
the CLI flag: attribution, not authentication. Reads keep a harmless
default label.

Requests outside the configured employer context are rejected with `403`
`employer_context_rejected`. An employer ID in a URL is scoping, not
authentication — multi-employer deployment needs identity and authorization
before it can exist.

Conventions: collection envelope `{data[], page, page_size, total}`;
errors `{error, code}` plus `X-Request-Id`; pagination defaults 50/max 200;
workflow transitions only through their endpoints (roster draft→published,
timesheet draft→submitted→approved/rejected, run draft→validated→approved,
leave requested→approved/rejected). `Idempotency-Key` is required on roster
publish, timesheet approve, leave decisions, payroll run/approve and clock
import; replays return the original result.

Milestone 1 variance (rev. 3): list operations currently return bare JSON
arrays (no envelope, no pagination), and `Idempotency-Key` is not yet
enforced — clock-import idempotency rests on source event IDs. Both remain
contract target behavior for a later slice.

## 4. Worked contract example (pattern for all slices)

Roster publish response (note `legal_validation` and revision immutability):

```json
{
  "id": "9f2c…",
  "programme_id": "7a1e…",
  "week_starting": "2026-11-09",
  "status": "published",
  "legal_validation": "unavailable",
  "current_version": 3,
  "revision_id": "c41d…",
  "assignment_count": 42,
  "staffing_gaps": [{"shift_code": "NIGHT", "weekday": "2026-11-14", "short_by": 2}]
}
```

CLI file input (`roster create --file roster.json`) carries the same fields
the POST body takes (programme, week, assignments with template-or-rest-day,
operator note); the CLI posts them to the identical service call. Every slice
records its shapes in `openapi.yaml` before serving them — see §6 governance.

## 5. Shared-service rule

Handlers and CLI verbs are thin adapters over `internal/` services. A payroll
overtime figure computed by the CLI and the same figure fetched via the API
must come from the same function. Contract tests execute key workflows through
both interfaces against separate identical fixtures and compare normalized
results. Request/response JSON shapes per resource are defined in the
implementation slice that ships them and recorded in `openapi.yaml` — no shape
is served before it is specified.

## 6. OpenAPI governance

`openapi.yaml` is the contract source of truth: paths are specified before
handlers exist and flip `draft → implemented` only in the slice that ships
them. CI checks every `implemented` path has a contract test and every served
path appears in the spec.

## 7. Attendance import format

Clock-event import accepts CSV with a header row:

```csv
source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,evt-001,EMP-001,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,evt-002,EMP-001,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,evt-003,EMP-001,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,evt-004,EMP-001,2026-08-03T18:00:00+08:00,Asia/Singapore,out
```

- `kind` is one of `in`, `out`, `break_start`, `break_end`. Break events
  are the only evidence for actual breaks; a scheduled break segment never
  counts as an observed break.
- `occurred_at` is RFC 3339 with offset; `timezone` is vendor source
  provenance. Both are preserved on every event.
- Timezone authority: the employment timezone determines attendance
  business-date attribution; the vendor timezone never does. The
  client/programme timezone governs service operations. The host
  timezone never determines employment results.
- Dedup key is `(employer_id, source, source_event_id)`; re-imported rows
  return the original outcome without duplicating events.
- Unpaired `in`/`out`, dangling break events, out-of-order rows, and
  overlapping spans become explicit exceptions. The importer never invents
  events; corrections arrive as new auditable records.
