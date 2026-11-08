# Phys Pseudo — Conformance Corpus (Stage 2, read under Stage 4 conventions)

**Status:** Stage 2 corpus — `Document` blocks preserved, fixture metadata
revised (see below). Since Stage 4 the canonical
rules are [`../PSEUDO.md`](../PSEUDO.md) (language) and
[`../MRC_REVIEW_PROTOCOL.md`](../MRC_REVIEW_PROTOCOL.md) (review);
[`../CONVENTIONS.md`](../CONVENTIONS.md) is a pointer note. Still no grammar,
parser, status engine, checker, or registry — explicitly out of bounds.

**Governing baseline:** [`../plan/plan2.md`](../plan/plan2.md); Stage 3 audit
record: [`../plan/final_REPORT_LOCKED.md`](../plan/final_REPORT_LOCKED.md).

**Reading this corpus.** Fixture `Document` blocks were not intentionally
edited during Stage 4 or the final consistency amendment; they retain
deliberate counterexamples and forms the canonical spec marks non-conforming
(bare `[assume]` premises, result-less `derive`s, verdict-form `?`,
undeclared premises). Fixture *metadata* — headers, status lines, expected
findings, and audit-history sections — **was** edited; each fixture records
its own edits in an `Audit history` section. A fixture whose document
deviates is not an example to copy: the status table and each fixture's own
header say which forms are canonical.

## What a fixture is

Each directory contains one `fixture.md` with:

1. **Document** — a pseudocode reasoning example using the seven constructs.
2. **Expected MRC findings** — *recognition-level* obligations ("the reader/
   reviewer must recognize that …"), never prescribed annotation wording.
3. **Rationale** — what distinction the fixture protects.
4. **Audit provenance** — how the fixture's mathematical content was checked
   (per the plan2 D4 audit rule: paper equations → source + independent
   derivation; elementary identities → operator definition + independent
   working). Golden fixtures require a completed audit; non-golden ones say
   what is missing.
5. **Invariant test** — at least one deliberate exercise of binding
   invariants I1/I2/I3 (now `PSEUDO.md` §7 and protocol ground rules),
   including the *forbidden* direction where applicable.
6. **Negative cases** — deliberately wrong documents or readings the corpus
   must reject or flag.

## Status vocabulary

| Marker | Meaning |
|---|---|
| **GOLDEN** | Mathematical content independently audited; expected values derive from the audit record, never from any implementation. |
| **TOY (golden by construction)** | Symbolic content with no external mathematical claim; correctness is checkable by inspection of the document itself. |
| **ILLUSTRATIVE (non-golden)** | Shape demonstration only. Physics or source content unaudited. Cannot be promoted without a completed D4 audit. |
| **ANTI-PATTERN (negative case)** | Present only to be flagged; never an example to copy. |

These labels describe **audit provenance or test purpose only**. None of
them asserts scientific truth, validity, or correctness: `GOLDEN` means the
encoded mathematical steps were independently checked against a recorded
audit — not that any physics is true; `TOY` means checkable by inspection of
the document itself; `ILLUSTRATIVE` means unaudited shape; `ANTI-PATTERN`
means present-to-be-flagged.

## Binding invariants under test (`PSEUDO.md` §7)

- **I1 — advisory guide is advisory:** may motivate an annotation; may never
  reject, score, or gate. (Strongest test: a fixture whose mathematics the
  guide does not cover must still be fully expressible.)
- **I2 — facts, not verdicts:** dependencies are reportable; justification,
  applicability, and support are never inferable from structure. Clean
  ancestry ≠ correct inference; guide match ≠ applicable; ancestry datum ≠
  support.
- **I3 — no author-typed status:** no construct licenses `PROVEN`-style
  labels; such text is a spec violation wherever it appears.

## Coverage matrix (structural, verified by grep at Stage 2 audit)

The `Document` block was not intentionally edited in this pass. Byte identity
against Stage 2/3 was not verified by a repository-wide diff; no VCS diff is
available. (Spot checks of representative `Document` lines were performed
during the Stage 4 metadata edits.) The last column records known
non-conformance of the *document* under `PSEUDO.md` (each fixture's header
marks it; documents untouched, defects preserved as adversarial inputs).

| Fixture | Mode | Status | given | define | assume | claim | derive | if/then/else | ? | Invariant(s) | Document vs `PSEUDO.md` |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 01 operator composition | paper | GOLDEN | ✓ | ✓ | | ✓ | ✓ | | ✓ | I2, I3 | verdict-form `?`; undeclared `product rule` |
| 02 ladder operators | constructed derivation / paper-extraction shape | GOLDEN (audited in-fixture) | ✓ | ✓ | ✓ | ✓ | ✓ | | ✓ | I2, I3 | unnamed `assume`; `as lowest_state` naming; multi-name `define`; result-less `derive` labels |
| 03 missing prerequisite | exploration | TOY | ✓ | ✓ | | | ✓ | | ✓ | I2 | undeclared `K`, `c` (subject defect); duplicate name `m`; result-less `derive m` |
| 04 conditional | both | TOY | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | I2 | unnamed/bare `[assume]`; result-less `derive step`; `?` at `else` indent |
| 05 dimensional mismatch | exploration | TOY | ✓ | ✓ | | ✓ | ✓ | | ✓ | I2 | result-less `derive step`; `E`, `p` not in premises |
| 06 novel structure | exploration | TOY | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | I1 (+ I2 via EF4) | laws inside `define`; result-less `derive`; bare premise tokens; σ use-before-declaration; anaphoric `if` |
| 07 cited result | paper | TOY + ANTI-PATTERN | ✓ | ✓ | ✓ | ✓ | ✓ | | ✓ | I2, I3 | unnamed/bare `[assume]`; result-less `derive step` |
| 08 competing hypotheses | exploration | TOY | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | I2 | unnamed `assume`; result-less `derive`; `s` undeclared; `?` at `else` indent |
| S cosmology sketch | exploration | ILLUSTRATIVE | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | I2 | unnamed `assume`; unresolvable premises; `else:` at column 0 |

## Stage 2 acceptance gate (applied at corpus audit)

1. Every GOLDEN fixture has an auditable derivation and an independent check
   recorded inside the fixture.
2. Every non-golden fixture is visibly marked, in the fixture and in this
   matrix.
3. Source claims, assumptions, and inferred material remain distinguishable
   (fixtures 01, 02, 07, S).
4. No implementation capability is implied merely because a Markdown fixture
   "passes": nothing here is executable, and expected findings are review
   obligations, not machine results.

## What this corpus deliberately does not do

It does not validate mathematics, prove entailments, or certify any claim.
A fixture "passing" means only: a reviewer found the document well-formed
against the conventions and the expected findings recognizable. Grammar and
checker remain out of scope; the language itself is now specified in
[`../PSEUDO.md`](../PSEUDO.md) (Stage 4).
