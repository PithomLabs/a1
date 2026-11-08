# Phys-Pseudo local codebase assessment

Inspection of `/home/chaschel/Documents/go/phys-pseudo`, read-only. The
upstream repository was not modified.

## 1. Repository examined

- Path: `/home/chaschel/Documents/go/phys-pseudo`
- Branch: `main`, tree clean, commit `b9bb9a4` ("Add Phys Pseudo Stage 1-4
  corpus: canonical spec, MRC review protocol, conformance fixtures, plan
  history, agent/human entry docs")
- Remote: `https://github.com/PithomLabs/phys-pseudo.git`
- Second commit only: `9e088e6 Initial commit`
- 52 files, all Markdown except `LICENSE` and `plan/files.zip` (a snapshot
  of three Stage 1–3 docs, no code)

Read in full: `PSEUDO.md` (397 lines), `README.md`, `LICENSE`, `AGENTS.md`,
`CONVENTIONS.md`, `MRC_REVIEW_PROTOCOL.md`, `conformance/README.md`,
`conformance/DERIVATION_SPEC_GUIDELINES.md` (676 lines), fixture
`01_operator_composition` (full) and `04_conditional` (document + findings
head), `plan/PSEUDO_STAGE4_AMENDMENT.md` (full). Diffed
`plan/claude/PSEUDO.md` (225 lines) against root `PSEUDO.md` (530 diff
lines — a different document, see §7).

## 2. Authoritative files

| File | Role |
|---|---|
| `PSEUDO.md` | Sole authority for syntax and meaning (self-declared §1, confirmed by README, AGENTS.md, protocol) |
| `MRC_REVIEW_PROTOCOL.md` | Sole authority for the adversarial review method; adds no syntax |
| `conformance/README.md` | Fixture status vocabulary + reading rules for the corpus |
| `conformance/DERIVATION_SPEC_GUIDELINES.md` | Normative only for implementation-ready physics derivations (PhysTrace context); not language |

Non-authoritative: `plan/` (design history and review record, including the
`plan/claude/` independent-review inputs), `CONVENTIONS.md` (explicit
pointer), fixture `Document` blocks (adversarial inputs, several deliberately
non-conforming — never examples to copy blindly).

## 3. Actual constructs and semantics

The seven-construct vocabulary is confirmed exactly as previously described.
Exact spellings from `PSEUDO.md` §1:

```text
given NAME: <content>
define NAME := <meaning>
assume NAME: <premise>
claim NAME: <proposition>
derive NAME: <explicit result> from [NAMES] | <reasoning>
if <condition>:
    then <consequence>
    else <alternative>
? <what is unresolved and what would resolve it>
```

Semantics that matter for adoption:

- Names are plain identifiers, no argument lists, declared before use,
  unique per document, branch-scoped inside `if` (§2).
- `from [NAMES]` needs ≥1 name; `from []` is not in the language; the list
  is *proposed* premises, never a completeness proof (§4). Only `derive`
  has `from`; elsewhere the word is prose (§8).
- Every `derive` states its result explicitly; result-in-reasoning-only is
  non-conforming (§4).
- `define` carries meaning only; the substitution test decides
  (§3). Existence/uniqueness presuppositions belong in `assume`.
- `?` has mechanical attachment: statement-level = immediately follows its
  target, exactly one indent deeper; document-level = column 0. Anything
  else is non-conforming. `?` never means known-false; refutations are
  `derive`s (§6).
- Central invariant: representation is not endorsement; no author-typed
  status (`PROVEN`, `VERIFIED`, …) anywhere (§7).
- Attribution is prose/`#` comments, never syntax; citation handles never
  enter premise lists. No automatic `?` for unattributed paper claims (§8).

## 4. Grammar and formatting conventions

There is no grammar file and no formatter. Conventions from the spec:
4-space indentation (load-bearing for `if` blocks and `?` attachment),
`#` notes to end of line, `NAME:` with single colon (`:=` only for
`define`), free-form mathematics/prose after `:`, `:=`, `|`. Fixture
documents use ```text fenced blocks; the paper-overview shape (§9) shows
the `# source:` header convention.

## 5. Examples and validation facilities

- 10 fixtures (`01`–`08` + `S_cosmology_sketch`): each has a `Document`
  block, recognition-level expected MRC findings, rationale, audit
  provenance, invariant test, negative cases. Statuses (GOLDEN / TOY /
  ILLUSTRATIVE / ANTI-PATTERN) describe audit provenance or test purpose
  only — never truth.
- **No parser, checker, linter, test suite, or executable exists or is
  implied** (`PSEUDO.md` §10; confirmed by file listing: no code in the
  repository). Recognition of non-conformance is the reviewer's job. Any
  future tooling needs its own milestone and review.

## 6. License and attribution

`LICENSE` is the MIT License, copyright 2026 Pithom Labs — the same
copyright holder as this project. Redistribution is permitted provided the
copyright and permission notices travel with copies. No contributor
agreements, no patent grants beyond MIT's text, no redistribution blockers.
Vendoring requires including the license text (§10.D5 does so).

## 7. Discrepancies with previous project assumptions

1. **`plan/claude/` is review input, not an alternate spec.** Its `PSEUDO.md`
   is a 225-line independent draft, rejected wholesale; only selected
   improvements were adopted into the canonical spec (amendment §2–§3).
   One material semantic difference: the draft lets standard mathematics be
   invoked by name in reasoning, while canonical §2.2 requires declaring
   background results as `given`. The canonical rule governs.
2. **`DERIVATION_SPEC_GUIDELINES.md` is PhysTrace-specific**, not generic
   methodology: expression trees, tensor ranks, `manifest.json` +
   `manifest_test.go` + `protocol/derivation_test.go`, `Verify()` coverage.
   Its *shape* (source chain, quantity identity, independent goldens,
   anti-goldens) transfers; its physics content does not. It is excluded
   from vendoring; the adaptation spec re-expresses the transferable shape
   for payroll rules.
3. **Fixture documents are not clean examples.** Every fixture `Document`
   block is preserved verbatim including Stage 3-recorded defects. Only
   `PSEUDO.md` §9 shows conforming usage. Vendored fixtures must carry
   their headers and expected findings, never the document alone.
4. **No tooling to reuse.** Earlier discussion sometimes implied a checker
   might exist. It does not. Conformance stays a human/AI review
   obligation; a mechanical `?`-sweep is the most we should automate
   without a reviewed tooling milestone.
