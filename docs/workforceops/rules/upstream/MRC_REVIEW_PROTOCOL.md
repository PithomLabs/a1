# MRC_REVIEW_PROTOCOL.md — Math Reality Check Review Protocol

**Status:** the operational review method for `phys-pseudo` (Stage 4).
It says *how* to examine reasoning written in the language. It does not
change the language: [`PSEUDO.md`](PSEUDO.md) is the sole authority for
syntax and meaning.

**Advisory knowledge.** [`plan/reality_check.md`](plan/reality_check.md) is
consulted, not embedded: domain-specific failure experience (what tends to
break dimensional analysis, boundary conditions, limits, symmetries…) that
*motivates* checks and examples. It never gates a document, never bounds what
may be expressed, and is not reproduced here.

**Boundary.** This protocol governs review of exploratory and paper-overview
documents. When a derivation is being specified for implementation, the
additional normative requirements of
[`conformance/DERIVATION_SPEC_GUIDELINES.md`](conformance/DERIVATION_SPEC_GUIDELINES.md)
apply on top — review here never substitutes for it, and it never applies to
documents that are not implementation candidates.

---

## Ground rules

- **Representation is not endorsement.** Reviewing a step does not promote
  it; failing to refute one does not establish it. The reviewer's product is
  findings, not verdicts baked into the document.
- **The premise list is proposed, not complete.** `from [NAMES]` records what
  the author offered. Omitted premises are invisible by construction; finding
  them is the central job, not a gotcha.
- **Useful under uncertainty.** When a hypothesis is speculative, a paper is
  incomplete, or a step cannot be settled, the reviewer states the current
  evidential boundary and the unresolved obligations — never manufactures
  certainty, and never converts a settled failure into a `?`.

---

## The eight responsibilities

### 1. Reconstruct the target

Before evaluating anything, write down: the question being asked, the
stated result (if any), the regime (scales, limits, approximations), and the
intended scope of the conclusion. If these cannot be reconstructed from the
document, that is itself the first finding — an argument with no clear target
cannot be assessed as correct or incorrect.

### 2. Separate epistemic roles

Walk the document and classify each statement by its construct: definition,
given, assumption, claim, derivation, conditional, unresolved question — and,
in paper overviews, by the prose attribution: what the paper states, what it
derives, what it cites, what the author reconstructed, what is open.

Check specifically:

- No `define` used as a premise for an independent physical law or unproved
  theorem (smuggling through definition; test: substitution — replace the
  definition by its right-hand side everywhere and see whether any
  proposition changes).
- No `claim` silently treated as derived; no source assertion or AI
  reconstruction silently promoted to proved result. If a `given` quotes or
  cites a source, check that the source says that; if you cannot check it,
  say so — an unverifiable citation is not established.
- Attribution conveyed in prose, not smuggled into `from`.

### 3. Check mathematical validity

Independently examine each consequential `derive`. For each:

- Do the listed premises actually suffice to yield the explicit result?
- Is any dependency omitted (a premise used but not listed)?
- Is any step circular — does a conclusion depend on itself or on a later
  step, directly or through a name chain?
- Does the result actually follow, or is it stronger than the premises
  support? Typical overreach patterns: bound → equality, recurrence →
  complete spectrum, "for some" → "for all", existence → uniqueness,
  eigenvalue relation → nonzero eigenvector.
- **Counterexample-first.** Try the simplest case in which the premises hold
  and test the result there; a concrete counterexample beats a general
  objection. State whether each check was done by hand or with a computation
  tool.

Routine algebra may be spot-checked; every step on which the central claim
rests is checked. Findings cite the specific `derive NAME:`.

### 4. Check mathematical well-formedness — as relevant

Examine the quantities, types, dimensions, domains, definitions, operator
conditions, normalization, boundary conditions, limits, degenerate cases
(zero, sign changes), and applicable mathematical regimes **that this
particular argument relies on**. Relevance is judged per argument: a step
where dimensions balance carries no dimension finding, and the reviewer does
not mechanically assert every checklist item applies.
When a required condition is absent (an undefined essential quantity, an
unstated domain restriction), name it; when it is present, say so.

*Illustration — attributed, not corpus-audited.* The independent review
under `plan/claude/` reported (as symbolically checked by its author) that
the formal ladder-operator algebra yielding ground energy ħω/2 for the
full-line harmonic oscillator "works" unchanged for the half-line oscillator
with a Dirichlet boundary condition at 0, whose reported ground energy is
3ħω/2: the lowered state is nonzero at the boundary and therefore outside
the Hamiltonian's domain. The algebra silently relied on a domain /
self-adjointness premise the argument never stated. This is the *class* of
omission step 4 exists to catch — formal operator algebra not establishing a
spectral conclusion because operator-domain or boundary conditions are
unmet. The example is an illustration from the review record; it is not
verified by the Phys corpus, not proven here, and not experimentally
established. It does not change the rule: such checks are demanded only
where these conditions actually bear on the argument.

### 5. Check physical applicability

Separately from validity: does the mathematical result apply under the
stated physical assumptions and regime? A mathematically valid derivation
from true premises still says nothing about Nature unless the model's
assumptions hold in the regime of interest. Check that regime conditions are
stated and that conclusions are not drawn outside them.

### 6. Check empirical support separately

Identify what empirical evidence is actually available for the claims at
issue — citations, measurements, none. Do not invent experimental
confirmation, and do not count a theoretical derivation as empirical support.
In particular, flag any statement that treats theoretical validity as
empirical truth, or vice versa.

### 7. Locate the decisive gaps

- Every unresolved point appears as a `?` with what is missing and what would
  resolve it; missing `?`s for genuinely open points are findings.
- A proposition the reviewer knows to be false is **refuted** — counterexample,
  contradiction, or conflicting reasoning — not relabeled `?`.
- Rank gaps: which unresolved points, if closed either way, would actually
  change the conclusion? Those are the decisive ones; the rest are noted.

### 8. Report actionable findings

Each material finding states:

1. **Where** — the affected statement or step (name it: `derive NAME:`).
2. **What** — the defect, classified: invalid inference, omitted premise,
   circularity, ill-formed quantity, regime mismatch, role confusion,
   unsupported empirical claim, template deviation…
3. **Why** — the supporting reasoning or counterexample.
4. **Minimum fix** — the smallest correction or additional evidence that would
   resolve it.

Distinguish consequential scientific defects from cosmetic preferences
explicitly; cosmetic items are labeled as such and never padded into the
material list.

Close every review with an **evidential-boundary paragraph**: what was
actually checked and how (by hand, with a tool, against a source), what could
not be checked, and what remains unresolved. Do not issue a global
"correct/incorrect" verdict, and never write that anything is "verified"
beyond the checks actually performed.

---

## Output shape

A review produced by this protocol is itself written in the language where
it expresses reasoning: findings as `claim`s, refutations as `derive`s,
open points as `?`s, with prose attributing what came from the document under
review versus the reviewer's own reconstruction. The review does not edit
the reviewed document's conclusions; it reports.
