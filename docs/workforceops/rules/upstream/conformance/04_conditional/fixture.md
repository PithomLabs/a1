# Fixture 04 — Conditional reasoning (symbolic toy)

- **Status:** TOY (golden by construction)
- **Mode:** both (the same shape serves exploration and paper extraction;
  shown here in exploration form with a paper-mode note)
- **Constructs exercised:** all seven (`?` via the standing note)
- **Invariant test:** I2 (conditional ≠ verdict)
- **Document conformance (`PSEUDO.md`):** non-conforming, preserved verbatim
  — the premise token `[assume]` is a bare construct keyword and the
  `assume` itself is unnamed (§2); `derive step` states no explicit result
  (§4); the `?` sits at the same indentation as the `else:` line, so it
  attaches to nothing (§6) and cannot reach the `then` step it was written
  to question.

## Document

```text
given data: a sequence (zₙ) with declared bound |zₙ₊₁ − zₙ| ≤ q·|zₙ − zₙ₋₁|
define contractive: the sequence is called contractive when q < 1 holds
assume the bound holds for all n ≥ 1 with a fixed q
claim H: (zₙ) converges
derive step from [assume, contractive] | successive differences form a decreasing geometric majorant, so (zₙ) is Cauchy
if contractive(q < 1):
    then derive convergence from [step] | Cauchy completion ⇒ (zₙ) converges; H follows within the declared space
    else: H does not follow by this argument
    ? [completeness of the underlying space is not declared here]
```

(Paper-mode note: identical structure; the `given` and `assume` lines would
carry prose source notes (e.g. `# from paper §N`) — source fidelity is now
governed by `PSEUDO.md` §8: attribution is prose, never syntax; the old
CONVENTIONS §1 fidelity rules are superseded.)

## Expected MRC findings (recognition-level)

1. `convergence` sits **inside a `then` branch**: its justification is
   structurally conditional on `contractive(q < 1)`. Reporting it without
   the condition is a finding-level failure.
2. The `else` branch is an explicit alternative outcome, not a failure
   state: `H` simply does not follow *by this argument*. Competing outcome
   remains open — no branch is "wrong."
3. The `?` in the branch was written to question the `then` step, but its
   layout does not deliver that localization: at the same indentation as
   the `else:` line it attaches to nothing (`PSEUDO.md` §6; Stage 3
   recorded the straddle). What survives is a branch-level flag that a
   completeness prerequisite is unaddressed — a conditional *and*
   unresolved reading only at that coarser level, with the misattachment
   itself a finding.
4. **I2 permitted:** report "`convergence` is conditional on q < 1, and a
   completeness gap is flagged in its branch — noting that completeness is
   **not** among `[step]`'s declared premises, so the gap is a nearby
   annotation, not an ancestry fact. Structure cannot deliver the ancestry
   claim (Stage 3: the earlier wording asserted it as settled); review
   adds it.
5. **I2 forbidden:** infer that H is *true* when the then-branch is taken —
   the branch states what follows *if* the condition holds; and infer that H
   is *false* when the else-branch is taken — the else states argument
   insufficiency, not refutation.
6. `assume` premises make all downstream conclusions structurally
   conditional regardless of branch; the report distinguishes
   assumption-conditional from branch-conditional.
7. **(Stage 3 BLOCKING — added)** line 5's `derive step` is not entailed by
   its premises: neither the unnamed `assume` (a bound with a fixed q) nor
   the *definition* `contractive` asserts q < 1 — q = 1 and q = 2 satisfy
   both while contradicting the geometric-majorant reasoning. Recognizing
   "a condition used in the reasoning that no premise asserts" is required;
   it also composes with N1's condition-drop.

## Rationale

Tests branch binding, the two-sided reading of `else`, the coexistence of
conditional + unresolved on one conclusion, and the I2 prohibition against
converting either branch into a verdict.

## Negative cases

**N1 — condition drop (ANTI-PATTERN):** reporting "`derive convergence from
[step]`" as a top-level step outside the `if`. Must be flagged: the
condition that licenses the step is no longer attached to it.

## Audit history — Stage 4 metadata corrections

Metadata only. The `Document` block was not intentionally edited in this
pass. Byte identity against Stage 2/3 was not verified by a repository-wide
diff; no VCS diff is available (spot checks of representative document lines
were performed).
Corrections per `plan/final_REPORT_LOCKED.md` §2 (Fixture 04) and
`PSEUDO.md`:

- Header: added the document-conformance line (bare `[assume]` token and
  unnamed `assume`; result-less `derive step`; `?` straddling the `else:`
  line).
- Paper-mode note: repointed from CONVENTIONS §1 (superseded) to
  `PSEUDO.md` §8.
- Expected finding 3 — original: "The `?` inside the branch localizes a
  second prerequisite (completeness) that the branch step depends on …"
  Corrected: under the canonical attachment rule the `?` localizes nothing
  (Stage 3 straddle finding).
- Expected finding 4 — original: report "`convergence` is conditional on
  q < 1 and its ancestry contains an unresolved prerequisite
  (completeness)." Corrected: completeness is not a declared premise of
  `[step]`; the ancestry claim was asserted as settled and is not.
- Expected finding 7 — added (Stage 3 BLOCKING: line 5 not entailed; was
  absent from the expected findings entirely).
