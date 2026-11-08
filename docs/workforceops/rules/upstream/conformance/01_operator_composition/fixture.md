# Fixture 01 — Operator composition (`(a·∂ₐ)²` vs `a²·∂ₐ²`)

- **Status:** GOLDEN (audit record included below)
- **Mode:** paper-extraction shape (imported operator rule + derived steps + asserted claim)
- **Constructs exercised:** `given`, `claim`, `derive`, `?`
- **Invariant tests:** I2 (both directions), I3 (negative case)
- **Document conformance (`PSEUDO.md`):** non-conforming, preserved verbatim
  — the `?` on `counter` states a settled verdict (§6: `?` is unresolved
  only), and `product rule` in `from [...]` is never declared (§2). Both are
  Stage 3 recorded findings (BLOCKING / MAJOR), not silent usage.

## Document

```text
# source: phys-trace DERIVATION_SPEC_GUIDELINES.md §6 — operator composition
given op: (a·∂ₐ)² f = a·∂ₐ(a·∂ₐ f)   from cited @project-guidelines
define f: f = f(a), differentiable on the working domain
claim W: (a·∂ₐ)² f equals a²·∂ₐ² f  for general f
derive expand(f) from [op, product rule] | (a∂ₐ)²f = a∂ₐ(a·f′) = a·(f′ + a·f″) = a·f′ + a²·f″
derive counter(f) from [expand(f)] | f(a) = a² ⇒ LHS = a·2a + a²·2 = 4a², RHS = a²·2 = 2a²
    ? [W is false: a·f′ + a²·f″ = a²·f″ requires a·f′ = 0, not true for general f]
```

## Audit record (independent hand-check — this derivation IS the audit)

Elementary-identity audit variant (operator definition + independent working;
no physics paper needed):

1. Operator definition: `(a∂ₐ)²f := a∂ₐ(a∂ₐ f)` — recorded in-document as
   `from cited @project-guidelines`, with the `# source:` comment pointing
   at phys-trace DERIVATION_SPEC_GUIDELINES.md §6. Stage 3 could not verify
   the handle; attribution wording amended, mathematics unaffected.
2. Product rule: `a∂ₐ(a·f′) = a·(f′ + a·f″) = a·f′ + a²·f″`. ∎
3. Test function f(a) = a²: f′ = 2a, f″ = 2.
   - LHS via step 2: `a·f′ + a²·f″ = a·2a + a²·2 = 2a² + 2a² = 4a²`.
   - RHS: `a²·f″ = a²·2 = 2a²`.
   - 4a² ≠ 2a². The equality fails.
4. Equality condition: `a·f′ + a²·f″ = a²·f″ ⇔ a·f′ = 0`.

Values derived here from the operator definition alone; they must never be
regenerated from any implementation or fixture under test. A previously
circulated scratch value (LHS 4a², **RHS 4a²**, i.e. the expressions equal)
was wrong and survives only as negative case N1.

## Expected MRC findings (recognition-level)

1. `W` is a **claim**, not a result: the document does not assert it true;
   no premise chain establishes it.
2. `expand` is a **derive** naming premises `op` and `product rule`; `op`
   resolves to the `given`, but **`product rule` is never declared** — the
   rule's text appears in the reasoning, its declaration does not exist
   (undeclared premise, `PSEUDO.md` §2; Stage 3 MAJOR — the earlier wording
   "premises … visible in-document" overstated this). Its inheritance from a
   `given ... from cited` rule is recognizable — the operator rule itself is
   *not* re-derived here.
3. `counter` is a **derive** from `expand` only; its expected values (4a²,
   2a²) are recognizable as the audit record's independently computed ones.
4. The `?` on `counter` states a **settled verdict** ("W is false: …")
   inside an unresolved marker. Under `PSEUDO.md` §6 (`?` = unresolved only)
   this is non-conforming: the refutation is delivered by the `derive
   counter(f)` result plus the audit working, not by the annotation. The
   document is preserved verbatim; recognizing the verdict-in-`?` misuse is
   itself the expected finding (Stage 3 BLOCKING — the earlier wording read
   this same `?` positively and has been corrected).
5. **I2 permitted:** a reviewer may recognize that `counter`'s ancestry
   consists of one cited rule, one definition, and one derivation — and that
   `W`'s ancestry is empty of support (it is a bare claim).
6. **I2 forbidden:** a reviewer must NOT conclude from finding 2–3 that
   "`expand`/`counter` are *mathematically justified* because their
   references resolve and no `?` sits in their premise chain." Their
   premises resolve; that is a fact about the document. Validity remains a
   review judgment — here confirmed only by the hand-check above, which is
   external evidence, not document structure.
7. **I3:** the document contains no author-typed status; nothing in the
   corpus may report `counter` as "proven" — only as derived-from-premises
   with the audit record as separate evidence.
8. **Refutation form:** `W` is refuted by `counter` (a result-bearing
   `derive` plus independent values); a reviewer must not treat the `?`
   annotation as the vehicle of refutation (Stage 3: add expected finding).

## Rationale

This is the corpus's anchor golden fixture: a real, historically audited
error class (`(a∂ₐ)²f = a²∂ₐ²f` was a documented wrong-form in the PhysTrace
guidelines). It protects the distinction between an imported rule, a
derived step, and a false claim — and it is the fixture where the corrected
expected values first enter the corpus.

## Negative cases

**N1 — the wrong expected result (ANTI-PATTERN):**

```text
derive counter_bad(f) from [expand(f)] | f(a) = a² ⇒ LHS = 4a², RHS = 4a²
    ? none   # W is true
```

Must be flagged: contradicts the audit record (RHS is 2a²). Its
`? none`-style ellipsis also violates annotation discipline (a false claim
with an empty gap marker). Tests that expected findings are checked against
the audit, not against plausible-looking text.

**N2 — status smuggling (ANTI-PATTERN, I3):**

```text
claim W_PROVEN: PROVEN that (a·∂ₐ)² f = a²·∂ₐ² f  for general f
```

Must be flagged: no construct licenses `PROVEN`; this is a spec violation
by text inspection alone, independent of the claim's falsity.

## Audit history — Stage 4 metadata corrections

Metadata only. The `Document` block was not intentionally edited in this
pass. Byte identity against Stage 2/3 was not verified by a repository-wide
diff; no VCS diff is available (spot checks of representative document lines
were performed).
Corrections per `plan/final_REPORT_LOCKED.md` §2 (Fixture 01) and
`PSEUDO.md`:

- Header: added the document-conformance line (verdict-in-`?`; undeclared
  `product rule`).
- Expected finding 2 — original: "`expand` is a **derive** whose premises
  (`op`, product rule) and reasoning are visible in-document …" Corrected
  because `product rule` is undeclared; "visible in-document" overstated
  (Stage 3 MAJOR).
- Expected finding 4 — original: "The `?` on `counter` explains *why* `W`
  fails (equality needs a·f′ = 0), rather than merely flagging the step."
  That wording positively endorsed a verdict-in-`?`; corrected to record
  the Stage 3 BLOCKING instead.
- Expected finding 8 — added (Stage 3 disposition: "add expected finding").
- Audit record step 1 — original: "… — from the cited guideline." The
  handle `@project-guidelines` is unverifiable in this repository (Stage 3
  attribution disagreement); wording amended, values unchanged (4a² / 2a²
  AGREE).
