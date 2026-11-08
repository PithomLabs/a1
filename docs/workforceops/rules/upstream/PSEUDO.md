# PSEUDO.md — Phys Pseudo Canonical Language Specification

**Status:** the sole authoritative specification of the minimal MRC pseudocode
(Stage 4). This document defines the language. It is **not** a grammar, not a
parser contract, not a checker specification, and it computes no statuses.

**Supersedes:** [`CONVENTIONS.md`](CONVENTIONS.md), now a pointer. Usage
history and the Stage 3 audit record live under [`plan/`](plan/).

**Module:** `github.com/PithomLabs/phys-pseudo` (a module file is orthogonal
to this specification).

**Purpose.** One shared language for three workflows: (1) AI agents express
physics hypotheses, explore mathematical consequences, and run adversarial
reviews without presenting speculation as established fact; (2) humans inspect
the essential reasoning behind Phys Go derivations before their
implementations; (3) humans read a paper's assumptions, central reasoning,
results, source claims, gaps, and open questions in compact form.

---

## 1. The seven constructs

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

This is the complete vocabulary. Nothing else is a construct. Comments and
header lines (`# ...`) are plain document prose. There is no eighth construct,
no status construct, and no provenance construct.

| Construct | Meaning |
|---|---|
| `given NAME: <content>` | Declares an input the reasoning starts from: measured data, the problem statement, an established mathematical rule, or a cited result. Attribution to sources is ordinary prose (§8), never syntax. |
| `define NAME := <meaning>` | Establishes the mathematical meaning of a symbol, object, or operation. Asserts no proposition (§3). |
| `assume NAME: <premise>` | States a premise the reasoning adopts, without asserting that the premise is true in Nature. |
| `claim NAME: <proposition>` | Records a proposition being asserted or examined. Not automatically derived; carries no premises. |
| `derive NAME: <explicit result> from [NAMES] \| <reasoning>` | Asserts that `<explicit result>` follows from the listed premises along the sketched reasoning. A derivation written here is **not thereby validated** (§4). |
| `if <condition>: then/else` | Conditional reasoning. Branch statements hold only under the condition (§5). |
| `? <...>` | Marks an unresolved or underspecified point (§6). Never "known false". |

---

## 2. Names and references

**A `NAME`** is the identifier introduced by a construct — the word after
`given`, `define`, `assume`, `claim`, or `derive`. A name is a plain
identifier (letters, digits, underscores) with **no argument list**: write
`expand`, not `expand(f)`; say "for every f" in the statement itself. Names
are ordinary reference identifiers: no ontology, no registry, no
scientific-status meaning.

Rules:

1. **Declaration before use.** Every name appearing in a premise list must be
   introduced by an *earlier* statement in the document. No forward
   references. A premise list contains only names.
2. **References are exact name matches.** A bare construct keyword (`assume`),
   a topic phrase (`product rule`), or a source handle (`@Buchert1997`) is
   **not** a reference. To reason from background mathematics or an imported
   result, declare it first:
   `given product_rule: (uv)' = u'v + uv' # calculus; standard result`.
3. **No unnamed premises; no empty lists.** Where the syntax takes `[NAMES]`,
   at least one name is required, and only names may appear. `from []` is not
   part of the language. An `assume` that later steps will cite must be named
   at its declaration.
4. **One document, one name per construct introduction.** Reusing a name for a
   second declaration makes both references ambiguous; non-conforming.
5. **Scope.** A name introduced inside a branch exists only inside that branch
   and cannot be referenced from outside it. Names introduced at top level are
   visible document-wide.
6. **Mathematical symbols in reasoning.** Right-hand sides are free-form
   mathematics and/or prose; ordinary background notation (constants,
   standard operators) needs no declaration. But free form is not a license to
   smuggle quantities: an **essential** quantity — one a step would fail
   without — must be introduced by an earlier `given`/`define`/`assume` and
   must appear among the step's premises when it carries the step. Authors
   introduce them; reviewers hunt for the missing ones (§11, protocol step 4).

---

## 3. The roles: given, define, assume, claim

- **`define` introduces meaning or convention.** It does not establish an
  empirical fact, prove a theorem, or license a physical assumption. A law,
  theorem, or independent physical premise written inside a `define` and later
  used as if it were one is a defect: state it as `given ...` (with its
  source in prose) or `assume ...`. This is the ban on smuggling assumptions
  through definitions. **Operational test:** a `define` may be replaced by its
  right-hand side everywhere without changing any proposition in the
  document. If removing or altering it would change which physical situations
  the reasoning describes, it is not a definition — write it as `assume` or
  `given`. Definitions that presuppose existence or uniqueness ("the lowest
  state", "the solution of …") smuggle that presupposition in: state it as an
  `assume` explicitly.
- **`assume` makes a premise explicit** without asserting it holds in Nature.
  An assumption is adopted, not endorsed.
- **`claim` records a proposition under examination.** Being a `claim` says
  nothing about whether it is true; a claim is not promoted by proximity to
  anything else in the document. A `claim` may be listed as a premise of a
  later `derive` (§4); the result then rests on a proposition the document
  does not establish.
- **`given` records what the reasoning starts from.** Declaring an input is
  not proving it; provenance and trust in the input are prose matters (§8).

The four roles are not interchangeable. Confusing them — a definition used as
a premise, an assumption presented as data, a claim presented as a result — is
a reportable defect, not a style choice.

---

## 4. `derive` — explicit results, proposed premises

```text
derive NAME: <explicit result> from [NAMES] | <reasoning>
```

Every element is required:

- **`NAME`** — so later steps can reference this derivation.
- **`<explicit result>`** — the proposition the step asserts (an equation,
  inequality, identity, or statement). A derivation whose result lives only in
  the reasoning text asserts nothing checkable and is non-conforming. This
  includes writing no result at all.
- **`from [NAMES]`** — the *proposed* premises: what the author offers as the
  inputs this result follows from. This list is a declaration of intent, **not
  source attribution** (§8) and **not proof that every relevant premise has
  been listed**. A missing premise is invisible by construction — which is
  exactly what review must look for. The list is never empty: `from []` is
  not part of the language (§2.3).
- **A `claim` may be listed as a premise.** The result then rests on a
  proposition the document does not establish; a reviewer must say so.
- **`| <reasoning>`** — a concise sketch of the inference. Free-form. It may
  compress routine algebra; it may never be the only place the result appears.

Template deviations (missing name, missing result, empty premise list) are
non-conforming and are findings for a reviewer to report — not silently
tolerated usage. The language defines no violation engine; recognition is the
reviewer's job (§11).

**What `derive` does not mean.** Writing a step in this language does not
validate it. A derivation in `phys-pseudo` asserts; a review examines; only
independent checking (human, CAS, or later implementation per
[`conformance/DERIVATION_SPEC_GUIDELINES.md`](conformance/DERIVATION_SPEC_GUIDELINES.md))
can establish correctness — and even that establishes mathematics, not
physical truth.

---

## 5. Conditionals: scope, structure, references

Rules:

1. **Layout.** `if <condition>:` opens an indented block. `then` and `else`
   each begin an indented line inside the block; a consequence is written
   inline after the keyword, or as statements on the following lines indented
   one level deeper. The block ends when the indentation returns to the `if`'s
   level; statements after it are unconditional.
2. **Which condition governs what.** The condition governs every statement in
   both branches. Statements inside a branch hold **only** under that
   condition; nothing outside the block is governed by it.
3. **Conditions are identifiable propositions.** An `if` condition must be an
   explicit mathematical/logical expression using declared names — not a vague
   reference to surrounding prose ("if the natural way holds").
4. **A branch is not proof that its condition holds.** Consequences inside a
   branch are conditional statements, never evidence of antecedent truth.
5. **Continuing under a condition** means placing further statements (more
   `derive`s, `claim`s) *inside* the branch. Branch conclusions and conditions
   are **not referenceable from outside** — the `if` carries no name slot.
   This is a deliberate expressiveness boundary of the minimal vocabulary, not
   an oversight: a later step that depends on a branch must live inside that
   branch, or restate the needed condition as its own `assume NAME:`.
6. **Scope of names.** Names declared inside a branch die with the branch
   (§2.5).

A worked example demonstrating scope, references, and `?` placement is in
§9.

---

## 6. `?` — unresolved, and only unresolved

`?` means **unresolved or underspecified**. It never means "known false".

Two forms, with mechanical attachment:

- **Statement-level `?`** — a `?` line that *immediately follows* a statement
  and is indented **exactly one level deeper** than that statement questions
  that statement. Inside a branch, a `?` one level under a branch statement
  (and within that branch's block) questions that branch statement. There is
  no other reading: attachment = the statement directly above the `?`, at
  exactly one indent level less. An intervening line, or a different
  indentation, breaks the attachment.
- **Document-level `?`** — a `?` at column 0 (not nested under any
  statement) questions the document as a whole.

A `?` whose position does not satisfy these forms — at the same indentation
as a statement rather than under it, or separated from its target — is
non-conforming. Questions about a conditional *as a whole* (the case split,
the exhaustiveness, the condition) belong at document level, since no branch
statement owns them.

Content obligations: say **what is missing** and, where possible, **what
would resolve it** — which evidence, premise, calculation, derivation, or
experiment would close the question. "unresolved" alone is incomplete.

**Known-false propositions are refuted, not questioned.** Express a refutation
with a counterexample, a contradiction, or conflicting reasoning — typically a
`derive` whose result is the refuted proposition's failure, or a `claim` plus
the refuting step. `? [W is false because ...]` is non-conforming: the content
is a settled verdict wearing an open marker.

**Resolution.** When a `?` is closed, delete the `?` line and record the
resolution in ordinary prose (what settled it). There is no closure construct.

---

## 7. The central invariant: representation is not endorsement

**Representing a proposition or reasoning step does not endorse its truth,
validity, physical applicability, or empirical correctness.**

Consequences:

- The language supports speculative exploration and adversarial scrutiny.
  Nothing in a document is weakened merely for being unestablished; nothing is
  strengthened by being written down.
- Structure carries no verdicts. Premise lists, ancestry, and clean formatting
  never imply that a conclusion is justified, applicable, supported, or true —
  in any direction.
- **No author-typed status.** Words like `PROVEN`, `VERIFIED`, `SUPPORTED`,
  `CONFIRMED` as labels or claims of status are non-conforming wherever they
  appear; the vocabulary licenses no such marker. (Denying a status in prose —
  "not independently verified" — is a statement about review history and is
  ordinary prose, not a status label.)
- The advisory guide (and all review knowledge) may *motivate* an annotation;
  it may never reject, score, or gate a document, and guide coverage never
  bounds what is expressible.

---

## 8. Source fidelity without provenance syntax

Paper-overview documents must keep these apart, in prose:

1. what the paper explicitly states;
2. what the paper derives or sketches;
3. results the paper cites from external sources;
4. what the pseudocode author or AI reconstructs or infers;
5. what remains unresolved.

Convey attribution with ordinary document prose: a source note, a comment
(`#`), a header line, or a sentence at the declaration point. Rules:

- `from` is syntax only in `derive`, where `from [NAMES]` lists premises. On
  any other line the word is ordinary prose: `given op: … from cited
  @guidelines` or `claim R: … from paper, conclusion` is read as a source
  note inside the content, with no clause force. A bracketed `from [` never
  appears except in a `derive`.
- Do **not** add provenance operators, evidence fields, citation registries, or
  an evidence-status taxonomy to the language.
- Citation handles inside prose (`@Buchert1997`) are prose, not references;
  they never appear in premise lists (§2.2).
- **No automatic `?`.** A paper assertion without a displayed derivation is a
  `claim` with honest prose attribution — that alone is *not* an unresolved
  status. Add a `?` only when a consequential inference, justification, or
  supporting result is genuinely missing or underspecified (§9's paper
  overview shows both: attribution carried by notes with no `?`, and a `?`
  for the inference that is actually missing).

The distinction between "the paper says X" and "X is established" is carried
by the prose and by the review, never by syntax.

---

## 9. Examples

Illustrative only: they show form, not established physics.

**Scope, references, and `?` placement** (toy):

```text
given ab: a and b are real numbers
define s := a + b
assume a_pos: a > 0
if b > 0:
    then
        derive s_pos: s > 0 from [s, a_pos] | positive plus positive is positive
    else
        derive s_below: s ≤ a from [s] | b ≤ 0 gives s = a + b ≤ a
? for b ≤ 0 the sign of s is undetermined; it needs a relation between |b| and a
derive s_real: s is a real number from [ab, s] | the reals are closed under addition
```

`s_pos` and `s_below` hold only under their branch condition and are visible
only inside it; a top-level `derive … from [s_pos]` would be invalid (§2.5,
§5.5). The `?` is at column 0: a document-level agenda item recording what
the `else` case leaves open — the *sign* of `s`, not the inequality `s ≤ a`
that `s_below` establishes. A `?` one level under `s_below` questioning the
sign would misattach: it would appear to question an established inequality.
`s_real` is unconditional.

**Refutation instead of `?`** (the operator-composition case; values checked
by hand, matching fixture 01's audit record):

```text
given fn: f is any twice-differentiable function of a
define sq := (a·∂ₐ)² f ≡ a·∂ₐ(a·∂ₐ f)
claim W: (a·∂ₐ)² f = a²·∂ₐ² f for every such f
derive expand: (a·∂ₐ)² f = a·f′ + a²·f″ from [sq, fn] | product rule: a·∂ₐ(a·f′) = a·(f′ + a·f″)
derive counter: for f(a) = a², (a·∂ₐ)² f = 4a² while a²·∂ₐ² f = 2a² from [expand] | f′ = 2a, f″ = 2: a·2a + a²·2 = 4a²; a²·2 = 2a²
derive not_W: W is false from [counter] | one f with unequal sides refutes the universal claim
```

`W` is refuted by a result-bearing `derive`, never by a `?` (§6).

**Paper overview shape** (schematic):

```text
# source: <paper>, §N — schematic, structure only
given law: [the paper's field equation]            # stated in the paper, eq. (N); cited there from elsewhere
define q := [the paper's defining relation]
derive step_1: [intermediate result] from [law, q] | [the paper's displayed reasoning]    # the paper's own derivation
derive step_2: [next result] from [step_1] | [reasoning]    # reconstruction by the pseudocode author; not in the paper
claim result: [the paper's conclusion]             # asserted by the paper; its displayed derivation stops at step_1
    ? no derivation from step_2 to the conclusion is given; a derivation or a cited result would resolve this
```

Attribution lives in the notes, not in `from` (§8). The `claim` carries no
`?` merely because the paper asserts without display; the attached `?` marks
the inference that is genuinely missing — from `step_2` to the conclusion.

---

## 10. Relationship to the other layers

Three roles, three documents, deliberately not merged:

| Layer | Document | Governs |
|---|---|---|
| **Language** | this file, `PSEUDO.md` | how reasoning is written and read |
| **Review** | [`MRC_REVIEW_PROTOCOL.md`](MRC_REVIEW_PROTOCOL.md) | how that reasoning is adversarially examined (advisory knowledge: [`plan/reality_check.md`](plan/reality_check.md)) |
| **Implementation-ready** | [`conformance/DERIVATION_SPEC_GUIDELINES.md`](conformance/DERIVATION_SPEC_GUIDELINES.md) | the additional normative requirements when a derivation is specified for implementation and verification |

An exploratory hypothesis or paper overview needs only this specification. A
handoff to implementation must additionally satisfy the guidelines. Syntactic
clarity in `phys-pseudo` never implies implementation readiness.

**Tooling.** This specification makes no claims about tooling. No checker,
linter, or parser exists or is implied; a future one requires its own
milestone and its own review.

---

## 11. Stage 3 findings → resolutions

The adversarial corpus review (`plan/final_REPORT_LOCKED.md`, verdict
PASS-WITH-AMENDMENTS) recommended clarificatory amendments, not new
constructs. Map:

| Stage 3 finding | Resolution here |
|---|---|
| "label" never defined; premises citing non-labels (`product rule`, `Buchert identity`) | §2 — name defined; references are exact earlier names only; background results declared as `given` |
| `given NAME =` vs `given NAME:` divergence | §1 — one spelling: `given NAME:` (and `:=` only for `define`) |
| `from` provenance defined on `given`, used on `assume`/`claim` in examples | §8 — `from` exists only in `derive`, for premises; attribution is prose |
| Invariant scope conflict (§2 preamble vs document text vs tooling ceiling) | §7 — the invariant binds every document and every future tool; no tooling ceiling needed because no tooling is specified |
| Unlabeled `[assume]` used as a premise reference | §2.3 — `assume` must be named if referenced; bare keywords are not references |
| `?` attachment ambiguous in multi-line `if` | §6 — mechanical forms: the statement directly above at exactly one level deeper (statement), column 0 (document); other placements non-conforming |
| `if` conditions/consequences not citable | §5.5 — recorded as a deliberate boundary with the in-branch continuation rule |
| `derive` template deviations unclassified | §4 — all slots required; deviations are non-conforming findings |
| Result-less `derive` (fixtures 05/06/08) | §4 — explicit result mandatory |
| Declaration-before-use failures (σ, s, m₀, Var, f′) | §2.1, §2.6 |
| Laws smuggled into `define` (fixture 06) | §3 — ban stated |
| Verdict-in-`?` (fixture 01) | §6 — known-false is refuted, not questioned |
| No `?` lifecycle / comment lines outside vocabulary / citation keys as references | §6 resolution-in-prose; §1 comment note; §8.3 |

**What remains open by design** (recorded, not defects of this spec): fixture
02's domain conditions stay `?` by design; no automation is specified; the
conformance corpus's document blocks are preserved verbatim as adversarial
inputs, including forms this spec now marks non-conforming (see
[`conformance/README.md`](conformance/README.md)).

**Final consistency amendment** (`plan/PSEUDO_STAGE4_AMENDMENT.md`): selected
improvements from the independent review under `plan/claude/` were adopted —
the define-substitution test (§3), counterexample tactics in the review
protocol, the corrected §9 examples, and the tightened `?` rule (§6). The
seven constructs, the ≥1-name premise lists, and the no-tooling stance are
unchanged; `from []`, a universal tooling ceiling, and automatic `?` for
paper claims were explicitly rejected.
