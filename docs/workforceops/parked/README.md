# Parked design docs (pre-pivot)

These documents describe the pre-pivot platform direction (multi-process
services, module kernel, console UI, training/evaluation apparatus, enterprise
identity). They are preserved for reference but are **not authoritative** for
MVP v0.1. Do not implement from them without a new decision.

## Why parked, not deleted

The pivot narrowed v0.1 to a single integrated backend. Several parked ideas
(multi-tenancy, module system, investigation tooling, SSO roadmap) may return
in later phases. Parking keeps the reasoning without letting it contradict the
frozen MVP scope.

## Map

| Parked material | Status in v0.1 |
|---|---|
| `ui/` (7 docs) | No UI in v0.1; console deferred |
| `enterprise-*` (6 docs: roadmap, identity, auth providers, authorization model, environment isolation, identity lab) | Enterprise/SSO deferred |
| Module system (contract, manifest, lifecycle, versioning, SDK, tenant activation, private modules, adapters, extension points, contribution guide) | Registry retired; single app with domain packages |
| `commercial-services-model.md` | Post-MVP concern |
| `api-contracts.md`, `cli-contract.md` | Merged into live `api-cli-contract.md` |
| `architecture.md` (3-process) | Superseded by `mvp-scope.md` + `api-cli-contract.md` |
| `scenario-format.md`, `oracle-design.md` | Generic simulation deferred; independent-fixture verification retained in narrowed form (see dossier) |
| `refactor-plan.md` | Selective-port strategy completed its purpose at baseline |
| `decisions/ADR-002` (console hosting) | Dead (no UI) |
| `decisions/ADR-003` (goose+sqlc) | Goose kept; sqlc dropped for hand-written pgx (see new ADR in restructure) |
| `decisions/ADR-005` (three processes) | Dead (single binary) |
| `decisions/ADR-008` (static modules) | Dead (no registry) |

## Still live

`repository-audit.md` (historical record), `rule-source-register.md` (extended
by the rule pipeline), `licensing-and-ip-boundaries.md` (MIT stands),
`domain-model.md` + `database-design.md` (rewritten for v0.1),
`mvp-scope.md`, `singapore-rule-scope.md`, `api-cli-contract.md` (new),
`decisions/ADR-001/004/006/007`.
