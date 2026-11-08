# Integration recommendations — rules layer into WorkforceOps docs

Targeted amendments only. No frozen decisions reopened (single binary,
single schema, two roles, approval-gated immutability, pivot scope).

## 1. `singapore-rule-scope.md`

- §1 (MVP packs): add a `spec` column pointing each pack at its rule
  package (`rules/<RULE-ID>/`); SG-MOM-OT-001 starts `BLOCKED` with the
  manifest's `blocked_reason` summarized in one line.
- §2 (blocking matrix): append one paragraph — a pack reaches
  `IMPLEMENTABLE` only through the §5 lifecycle in
  `rules/adaptation-spec.md`; the matrix's `IMPLEMENTABLE` gate and the
  manifest's `status` field are the same gate, and
  `rule_pack_activation.status` mirrors the manifest (never leads it).
- §5 (jurisdiction scoping): reference the package `applicability` block
  as the machine-readable form of the scope prose.

## 2. `domain-model.md`

- §3 (Payroll): annotate `rule_pack_activation` — `version` pins the
  package version, `status` mirrors the manifest; activation of a
  non-`IMPLEMENTABLE` package is rejected by the approval predicate
  (already specified; this only names the manifest as the source).
- No entity changes. The six L1/L2/L3 levels need no schema: rigor is a
  package attribute, not a runtime one.

## 3. `database-design.md`

- §6 (rule packs, if present; else the pack paragraph): one line — pack
  parameter tables are populated only from `IMPLEMENTABLE` packages, and
  the populating migration cites the package version.
- No migration from this task. Future pack-parameter seeding migrations
  reference `rules/<RULE-ID>@<version>` in their headers.

## 4. Implementation workflow (AGENTS.md / slice planning)

- Each payroll slice starts by advancing its rule package through the
  lifecycle; Go work begins at `GOLDENS_VERIFIED` with fixtures in hand,
  and activation at `IMPLEMENTABLE` after independent review.
- The `indeterminate`/`422` paths specified in plan4.3 map to open `?`s:
  slice plans list which `?` each error path covers.
- Review checklist addition: spec/implementation/fixture consistency
  (every `derive` implemented, every `?` either closed with MOM evidence
  or mapped to a blocked path, no fixture generated from the
  implementation).

## 5. Explicitly not recommended

- No spec parser/checker (upstream specifies none; a mechanical `?`-sweep
  is the ceiling without a reviewed tooling milestone).
- No rules engine, no code generation from specs, no grammar changes.
- No new top-level docs beyond the `rules/` tree created here; the
  amendments above are paragraphs, not rewrites.
