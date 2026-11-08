# WorkforceOps Phys-Pseudo adaptation specification

Phys-Pseudo is our business-rule specification language: the formal layer in
which a rule's inputs, assumptions, applicability, claims, derivations,
branches and open questions are written before any Go exists. This document
adapts its use to Singapore HR/WFM/payroll rules **without changing its
grammar or semantics** — `PSEUDO.md` (vendored under `upstream/`) remains
the sole authority for what the constructs mean. WorkforceOps adds only:
what each construct *carries* in a rule package, the research lifecycle
around it, and the rigor levels that decide how much of it a rule needs.

## 1. Role in the project

```text
MOM/CPF/IRAS source → pinned evidence → extracted claims → Phys-Pseudo spec
→ independent derivation → golden/anti-golden/boundary fixtures
→ independent review → Go implementation → fixture verification
```

- Phys-Pseudo defines how we express specifications.
- This document + the package template define how we research, verify and
  approve them.
- Go implements approved specifications; fixtures verify the implementation.
- Phys-Pseudo is never the source of legal authority. The authority is the
  pinned statute/guidance. A spec never elevates an assumption,
  interpretation or generated conclusion into an established legal fact.

## 2. Per-construct usage for business rules

**`given`** — facts and inputs from the pinned source or the scenario:
statutory thresholds, rates, formulas stated by MOM, employee/employment
inputs supplied to the calculation. Every `given` carries a prose source
note (`# MOM <page>, <section>; retrieved <date>; sha256:<…>` for source
facts, `# scenario input` for scenario data). A `given` without a source
note is a review finding, not a default accept.

**`define`** — terminology, quantities, classifications, derived
expressions: `hourly_basic`, `statutory_cap`, `covered_employee`.
Substitution test applies (§3): a definition that changes which employees
or situations the rule covers is a smuggled premise — rewrite as `assume`
or `given`. Never infer legal classifications (workman, Part IV coverage)
inside a `define`.

**`assume`** — conditions adopted for the derivation's scope: effective
dates, "retrieval pass confirms X", scenario stipulations. Every
load-bearing assumption states what depends on it. Assumptions about what
the law *says* are forbidden as silent premises — an uncertain reading is
a `claim` plus a `?`, never an `assume` that lets the derivation proceed.

**`claim`** — the business-rule result under examination: eligibility
outcomes, computed amounts, classification results. A claim in a
non-`IMPLEMENTABLE` package stays exactly that.

**`derive`** — the reasoning from givens/definitions/assumptions to the
claim, with the explicit result and proposed premises. Arithmetic must be
checkable: intermediate values shown, rounding stated as a premise (a
rounding rule from the source is `given`; an adopted convention is
`assume` with rationale). Two derivations reaching the same amount by
different routes stay separate steps.

**`if/then/else`** — applicability and alternative outcomes, both sides
explicit: covered/exempt, capped/uncapped, eligible/ineligible. The `else`
is never empty prose ("n/a") — it states the outcome (no statutory
obligation, `indeterminate`, falls to another rule). Branch names die with
the branch; a later step needing a branch result restates the condition as
its own `assume`.

**`?`** — the load-bearing mechanism. Statement-level `?` (one indent
deeper, immediately following) questions that statement; document-level
`?` (column 0) questions the spec as a whole. Every `?` says what is
missing and what would resolve it. An unresolved load-bearing question
blocks `IMPLEMENTABLE` — it is never converted into a default, a zero, or
an optimistic reading. Discrepancies between sources are `?` content, with
both values quoted; known-false readings are refuted with a `derive`, not
questioned.

## 3. Applicability and scope block

Every L1 spec opens with givens/assumptions covering: jurisdiction,
employer/employee categories, employment arrangement, wage basis and salary
period, effective date, work arrangement, eligibility conditions,
exemptions/exclusions, calendar period, required inputs, and the explicit
unsupported/indeterminate set. A category not named is not covered.

## 4. Edge cases

Every L1 rule gets a deliberate edge-case review, selecting relevant
categories (threshold ±ε, exact thresholds, caps, eligibility boundaries,
missing/contradictory inputs, zero/negative values, effective-date
transitions, retroactive term changes, month/pay-period boundaries,
cross-midnight shifts, duplicate source events, overlapping records,
rounding boundaries, partial periods, concurrent conditions, conflicting
provisions, unsupported categories). Each selected case records: trigger,
expected outcome if established, supporting source/derivation, spec
coverage, fixture need, and resolution state. Six outcomes stay distinct:
valid zero, valid absence of obligation, inapplicable rule, missing
information, unresolved interpretation, unsupported scenario.

## 5. Research and verification lifecycle

`DISCOVERED → SOURCES_PINNED → CLAIMS_EXTRACTED → FORMALIZED →
INDEPENDENTLY_CHECKED → GOLDENS_VERIFIED → IMPLEMENTABLE`

- Statuses live in the package manifest (prose), never in the spec: the
  language has no status construct and `IMPLEMENTABLE` in a spec file
  would be non-conforming.
- Only `IMPLEMENTABLE` may be activated for a production calculation
  (wired to `rule_pack_activation.status`).
- Independent derivation means a derivation produced without copying the
  spec's steps; independent review means a reviewer other than the
  formalizer, following the MRC protocol adapted in §7. An AI-generated
  spec is not verified because the AI says so.

## 6. Rigor levels

- **L1** (statutory, financial, high-consequence): full package — source
  evidence, claims, spec, assumptions, `?`s, independent derivation,
  golden/anti-golden/boundary fixtures, independent review, implementation
  contract. Includes: overtime eligibility/calculation, CPF OW/AW
  classification and contributions, SDL, SHG, leave, public-holiday pay,
  statutory records.
- **L2** (complex workflows/integrations): explicit states, invariants,
  exception paths; Phys-Pseudo selectively for calculations, eligibility
  decisions, critical invariants (roster publication, corrections,
  approvals, attribution).
- **L3** (ordinary CRUD/infrastructure): conventional Go validation and
  tests; no Phys-Pseudo without a genuine business-rule reason.

## 7. MRC protocol, adapted

Reviews follow the vendored `MRC_REVIEW_PROTOCOL.md` with domain
substitutions: "mathematical validity" → derivation follows from premises;
"well-formedness" → quantities typed, domains stated, caps/boundaries
handled; "physical applicability" → the rule applied only inside its
stated applicability; "empirical support" → source evidence actually
inspected (rendered pages for formulas, not snippets). Counterexample-first
(realistic employee scenarios), evidential-boundary paragraph mandatory,
no global verdicts beyond checks performed.

## 8. Spec ↔ Go ↔ tests

The spec is the contract the Go implements: every `derive` maps to
implementation code, every `?` maps to `indeterminate`/blocked paths or
open work — never to a guessed value. Golden fixtures written from the
spec's derivations (not from the implementation) verify the Go;
anti-goldens prove plausible wrong readings fail. The implementation
contract section of the package names the owning Go package, version, and
the fixture set that gates it.
