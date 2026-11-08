# Database Design — v0.1 single database

**Status:** design gate candidate (rev. 2 — plan_prompt4 amendments) · **Date:** 2026-11-08

PostgreSQL 16, default `public` schema, one Goose migration set, two roles.
Multi-employer capable: `employer_id` on every employer-owned table with
composite unique keys and composite foreign keys. No RLS in v0.1 (single
employer operated; production isolation is a separate release gate).

## 1. Roles and grants

| Role | Purpose | Grants |
|---|---|---|
| `migration_admin` | Schema changes only | DDL; owns triggers and functions |
| `app` | The single application | SELECT/INSERT everywhere; UPDATE/DELETE revoked on insert-only tables (§2); parent-state immutability via triggers (§3) — draft records stay editable, published/approved records freeze |

Local Compose bootstraps both (dev passwords, localhost only); runtime uses
`app` via `DATABASE_URL`, migrations via `MIGRATION_DATABASE_URL`.

Privilege separation is enforced at startup, not just documented: CLI
commands, seed operations and `serve` refuse superuser and
`migration_admin` connections (`RefusePrivilegedRuntime`: ownership
overrides every REVOKE, so running as the owner would silently bypass
the C-stage protections); `migrate up` and `migrate status` preflight
for `current_user = 'migration_admin'` and fail loudly otherwise.
Neither runtime DSN may point at the container superuser.

## 2. Insert-only tables (privilege-enforced)

`REVOKE UPDATE, DELETE ON <table> FROM app` for: `clock_event`,
`audit_event`, `payslip`, `ket_record`, `roster_revision`,
`attendance_interval`, `attendance_correction`. Corrections and revisions
arrive as new rows (`supersedes_id`, new revisions, corrective runs) — never
edits. `roster_assignment`, `timesheet_line` and `timesheet_line_interval`
are deliberately NOT on this list: they must stay editable while their parent
is a draft, so their freeze is trigger-enforced (§3), never privilege-revoked.

## 3. Approval-gated immutability (trigger-enforced)

Status-dependent rules the privilege system cannot express use small triggers
(shipped in migrations, owned by `migration_admin`):

- `trg_timesheet_frozen` — on the timesheet, its lines and its interval
  allocations: after status becomes `approved`/`rejected`, no UPDATE/DELETE;
  in draft, both succeed.
- `trg_payroll_run_locked` — after status becomes `approved`, no
  UPDATE/DELETE on the run or its lines; corrections require kind=`correction`
  runs referencing the original.
- `trg_payroll_exclusion_locked` — exclusions may be inserted, updated or
  deleted only while the parent run is still `draft`; from `validated` on,
  any change fails.
- `trg_roster_published_frozen` — an assignment row may change only while it
  belongs to the roster's current revision AND the roster is `draft`; once
  published, the revision is frozen. Repair means an audited reopen to
  draft, a new revision row, and advancing the revision pointer — old rows
  stay untouched.
- `trg_leave_decided_frozen` — decided leave requests immutable (new request
  supersedes on re-application).

Each trigger raises a distinct SQLSTATE-marked exception the services map to a
machine-readable `422` code. Trigger bodies are short predicates over
OLD/NEW status — no business logic.

## 4. Keys, dedup and lineage

- `clock_event`: `UNIQUE(employer_id, source, source_event_id)` — each
  employer gets an independent event namespace; a vendor event ID is only
  unique within its source. The same per-employer scoping applies to any
  future integration identifiers.
- Business codes unique per employer: `UNIQUE(employer_id, employee_no)`,
  `(employer_id, code)` for clients, programmes, teams, templates, pay
  periods, rosters (per programme+week).
- Parents carry `UNIQUE(employer_id, id)`; children reference
  `(employer_id, parent_id)` — team memberships, roster assignments,
  timesheets, payroll lines and corrections can never cross employers.
- `timesheet(employee_id, pay_period_id)` unique within employer; one approved
  regular run per period (partial unique index on
  `(employer_id, pay_period_id) WHERE kind='regular' AND status='approved'`).
- Lineage: `timesheet_line_interval(line_id, interval_id)` join;
  `attendance_interval.supersedes_id` chains; `payroll_line.source_refs`
  points at timesheet lines, terms and rule versions.
- `leave_request` no-overlap per employee (exclusion constraint);
  `roster_assignment` exactly one of `template_id`/`rest_day` (CHECK) with
  per-revision employee+date uniqueness.

## 5. Employer-scoped table coverage

Every employer-owned table carries `employer_id` with the composite-key
pattern from §4. Coverage: `employee`, `employee_status_history`,
`employment_terms`, `compensation_line`, `ket_record`, `leave_request`,
`client`, `programme`, `programme_required_skill` (own `employer_id`),
`programme_staffing_requirement`, `team`, `team_membership`, `employee_skill`,
`shift_template`, `shift_segment` (own `employer_id`), `rotation_pattern`,
`rotation_assignment`, `roster`, `roster_revision`, `roster_assignment`,
`clock_event`, `attendance_interval`, `attendance_record`,
`attendance_correction`, `timesheet`, `timesheet_line`,
`timesheet_line_interval`, `shg_instruction`, `pay_period`,
`payroll_run`, `payroll_run_exclusion`, `payroll_line`, `payslip`,
`statutory_summary`, `payroll_correction`, `rule_pack_activation`,
`audit_event`, `idempotency_key`. Global (jurisdiction-level, no `employer_id`):
`skill`, `pay_code`, `leave_type`, `holiday_calendar`, all `mom_*`/`cpf_*`/
`sdl_*`/`shg_*`/`leave_*` rule tables, migration metadata.

## 6. Table conventions

- PK `id uuid PRIMARY KEY DEFAULT gen_random_uuid()` (extension in baseline).
- Time: `*_at TIMESTAMPTZ NOT NULL DEFAULT now()` recorded time;
  business events add `timezone` plus local context; business dates `DATE`.
- Money: `NUMERIC(12,2)`; exact intermediates in Go; rounding at verified
  statutory boundaries only.
- Effective dating: `effective_from DATE NOT NULL`, open `effective_to`;
  at most one covering row per key (exclusion or validated insert).
- `idempotency_key(employer_id, key, owner)` unique.

## 7. Retention

Employment and salary records are retained two years for current employees;
leavers' records are retained one year post-exit. v0.1 implements retention by
never hard-deleting business records and by acceptance tests that query aged
fixtures (seeded backdated rows still fully retrievable with lineage intact).
No purge job exists; adding deletion later requires its own design and tests.

## 8. Reproducibility contract (specified now, executed at the payroll slice)

`inputs_hash` plus `rule_versions` alone do not reproduce history. A payroll
result must preserve or immutably reference all five of: (1) the exact
approved timesheet and employment-term inputs, (2) the exact statutory
rule-pack versions, (3) the calculation implementation/version
(`calc_version`), (4) the rounding and calculation conventions applied,
(5) the resulting payroll lines and payslip. The payroll slice must test:
same immutable inputs + same rule versions + same calculation version =
same payroll output. This end-to-end test is deferred by design; what executes
now is its foundation (frozen runs, pinned versions, mandatory lineage).

## 9. Two-employer isolation tests (implemented with migrations)

Real PostgreSQL integration tests (never mocked repositories):

1. Create Meridian CX plus a second employer fixture — both coexist.
2. Same `EMP-001` and programme code under both — allowed.
3. Employer A employee into Employer B team/roster/timesheet/payroll — rejected
   by composite FKs.
4. Employer A timesheet referencing Employer B employee — rejected.
5. Employer A payroll run referencing Employer B timesheet — rejected.
6. Employer A API request reading Employer B employee — rejected (403
   `employer_context_rejected`).
7. Employer A payroll export containing Employer B records — impossible by
   construction (assert empty across all export shapes).
8. Deactivating an employer — business records retained, never orphaned or
   exposed to the other employer.

## 10. Migrations and seed

Prerequisite: the superuser bootstrap installs `pgcrypto` and `btree_gist`
and creates the `migration_admin` and `app` roles before any migration runs
(see `scripts/db/init-roles.sh`; the integrity suite repeats the bootstrap
on its scratch database).

Single embedded Goose set (baseline rewritten; pre-pivot 00001 discarded —
nothing released to migrate). `Down` for local dev only. `seed demo` loads
Meridian, BPO structure, templates, holidays, IMPLEMENTABLE-version rule data
only (unverified packs seed nothing activatable), and the deterministic
acceptance workforce with aged retention fixtures. Seed is versioned and
re-runnable: `seed demo --reset --admin-dsn <superuser> --target <db>`
recycles the named disposable database (drop, recreate, provision,
migrate) and reloads it; there is no row-level reset, because published
and decided rows cannot be deleted by any role. Explicit `sslmode=disable`
allowed for localhost only; any non-localhost DSN requires TLS. Pack
parameter tables are populated only from IMPLEMENTABLE packages; the
populating migration cites `rules/<RULE-ID>@<version>` in its header.

Pre-release migration policy (Milestone 1.1): fresh install only.
Upgrading a database that carries pre-pivot partial goose history fails
loudly at version 2 (`relation "employee" does not exist`) instead of
half-applying; no upgrade path is supported. No deployed user database
exists (compose volume unverified — see the M1.1 structural audit), and
no existing database is ever dropped by project tooling: recycle
operations refuse protected and non-disposable names, require an
explicit admin DSN on the same server, and require --target retyping
to confirm the database name.

Forward-only integrity migrations (Milestone 1.2+): M1.2 and later
integrity migrations are forward-only once dependent business data
exists. The application and Taskfile expose no migration Down
operation, and populated Down+Up safety is not claimed: original
events, disposition decisions, correction groups and audit history
must survive every supported workflow, so any manual rollback that
would destroy them is unsupported. Destructive recovery requires a
verified database backup plus a controlled restore/rebuild
procedure. Migration `00011` carries a guarded Down that inspects
dependent tables first and fails before any destructive change
whenever compensation, terms, correction-group, or basis-versioned
rows exist; it unwinds only freshly migrated empty databases
(proven by `TestDownRefusesPopulatedDatabase` and
`TestDownEmptyDatabaseRoundTrip`).

## 11. What this design refuses

No multi-schema service split, no outbox/worker tables, no event bus, no RLS,
no per-module roles, no vectors, no second database. Anything on that list
needs a new decision, not a quiet migration.
