# API Contracts — REST conventions and resource map

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08
**Machine contract:** root `openapi.yaml` (paths carry `x-maturity`).

## 1. Conventions (all hosts)

- Base path `/api/v1`; JSON only; UTF-8.
- Success envelope for collections: `{data[], page, page_size, total}`.
- Errors: `{error: "<message>"}` + `X-Request-Id` header echo; prod bodies
  stay generic, details stay server-side.
- Pagination: `?page=&page_size=` (max 200). Filtering: `?q=` + resource
  fields; sorting: `?sort=` allow-listed per resource.
- Mutating ops accept `Idempotency-Key`; replays return the original result.
- Correlation: clients may send `X-Correlation-Id`; the server mints one when
  absent and returns it on responses.
- Versioning: URL major versions; additive changes only within `v1`.
- Auth: none in skeleton; Bearer (local JWT) from Phase 4 — see
  `authentication-provider-boundary.md`.

## 2. Resource map by host and phase

ShiftHub (`:8081`): `/companies`, `/employees`, `/workforce/*`,
`/attendance`, `/timesheets`, `/rules` (read), console routes (Phase 6).
PayLoop (`:8082`): `/payroll/*`, `/payslips`, `/rules` (read).
Either (platform surfaces, served by the owning process): `/integrations/*`
(worker state, read), `/scenarios`, `/incidents`, `/investigations`,
`/evidence`, `/evaluations` (evaluator role only).

Full path list with maturity flags lives in `openapi.yaml`. No draft path may
be served before its phase lands; contract tests assert 404 for unshipped
drafts and schema-conformance for shipped ones.

## 3. Interface coverage matrix

| Operation | CLI | REST API | Web UI |
|---|---|---|---|
| Seed synthetic company | Yes | Optional | Admin |
| Inspect employee/timesheet/payroll | Yes | Yes | Yes |
| Run payroll | Yes | Yes | Authorized operator |
| Inspect integration job/attempts | Yes | Yes | Yes |
| Inject fault | Yes | Yes | Lab administrator |
| Create incident ticket | Yes | Yes | Yes |
| Run investigation | Yes | Yes | Yes |
| Inspect evidence/verify | Yes | Yes | Yes |
| Evaluate | Yes | Yes | Evaluator |
| View hidden ground truth | Restricted | Restricted | Restricted |
| Import CSV/JSON (8A) | Yes | Yes | Workspace owner |

## 4. Consistency rule (review correction 3)

Equivalent operations across CLI/API/UI must converge on identical persisted
state. Tests execute the same scenario against **separate identical fixtures**
(one per interface) and compare normalized business state, integration events
and evidence hashes. Mutating the same shared state three times is not a
consistency test.

## 5. OpenAPI governance

`openapi.yaml` is the contract source of truth: new resources are specified
there before handlers exist; `x-maturity` flips `draft → implemented` only in
the phase that ships them. CI checks that every `implemented` path has a
contract test and every served path appears in the spec.
