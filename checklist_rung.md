Great question to drill into — this ladder is the single most consequential *early* decision in the whole checklist, because (as your milestone proved) every rung has architectural prerequisites that cannot be retrofitted. Let me build it out properly.

---

## 1. The ladder, restated: it's a *claim-evidence* ladder

Each rung is not "more testing effort." Each rung answers a **different question**, produces a **different kind of evidence**, and — the part people miss — requires a **different kind of architecture** to even be possible.

| Rung | The question it answers | Evidence artifact | What makes it *red* |
|---|---|---|---|
| 1 — Demonstration | "Does it look right?" | Demo, manual run | Someone sees it fail |
| 2 — Example-tested | "Does it do the right thing on the cases **I thought of**?" | Green test suite | A chosen example fails |
| 3 — Property-tested | "Does it do the right thing on cases **nobody thought of**?" | Invariants hold over generated inputs | A shrunk counterexample |
| 4 — Adversarially verified | "Can a **competent adversary** make it do the wrong thing?" | Attack battery + mechanism proofs (Jepsen-style) | A successful attack |
| 5 — Formally verified | "Can **anything** make it do the wrong thing?" (within a stated model) | Machine-checked proof | An error in the proof/model |

Read the right column carefully — that's the deepest part. **Every rung's green looks identical** ("all checks passed ✅") while meaning completely different things. A rung-2 green and a rung-4 green both print `OK`. The difference is *what would turn them red*. A rung-2 test is falsified by a failing example; a rung-4 test is falsified by a *successful attack*; a rung-5 proof is falsified by a hole in the model.

That is exactly what prompt17's sentence — *"a green test is evidence only when it exercises the failure mechanism"* — is doing. It redefines what a green test *means*: not a demonstration that the happy path works, but a failed attempt to cause a specific, named failure. It inverts the polarity: at rung 4, the interesting artifacts are the **red tests** — the ones that try to break the mechanism and are expected to lose.

---

## 2. The rungs, one at a time

### Rung 1 — "It works" (demonstration)
- **Claim:** the feature did the right thing in the scenarios the developer happened to try.
- **Evidence:** a demo, a screenshot, "I tested it by hand."
- **Catches:** nothing systematically. **Misses:** everything untried, plus all regressions.
- **Cost:** ~zero.
- **Appropriate when:** the artifact is throwaway, or correction is free and failure is loud (internal scripts, prototypes).

### Rung 2 — Example-based testing
- **Claim:** the system does the right thing on a finite, developer-enumerated set of examples, and keeps doing so (regression).
- **Evidence:** green CI suite, coverage numbers.
- **Catches:** bugs in paths you enumerated; reintroductions of bugs you already fixed.
- **Cannot catch:** everything you failed to enumerate — untried data shapes, concurrency interleavings, adversarial misuse, environment drift. The fundamental blindness: **you test your mental model, including its errors.** D1 would never appear in a rung-2 suite, because no one writes a test for "snapshot taken at first statement, not at BEGIN, after a lock wait" unless they already understand the anomaly — and if they understand it, they've already paid the education cost somewhere expensive.
- **Cost:** baseline (~1×). Linear in the number of examples; drags over time as the suite grows.
- **Prerequisite you must build early:** testable seams (dependency injection, replaceable clocks, mockable boundaries). Retrofitting seams into entangled code is painful but *possible* — which is why rung 2 is the default resting place of every codebase that never decided otherwise.

### Rung 3 — Property-based / model-based testing
- **Claim:** stated invariants hold across a *generated, sampled* input space; the implementation matches a reference model over random operation sequences.
- **Evidence:** "invariant held over 100k generated cases, minimal counterexample absent" (QuickCheck, Hypothesis, proptest); metamorphic relations.
- **Catches:** boundary and combination bugs nobody enumerated — proration edge cases, rounding drift, sum-conservation violations, parser corners.
- **Cannot catch:** rare interleavings (property tests are usually single-threaded or loosely concurrent), genuine adversaries, probability tails. Sampling is not exhaustion.
- **Cost:** ~2–3× for the tested core — but the expensive part isn't the tooling, it's that **you must state the invariant out loud**. This is a hidden gift: rung 3 forces spec clarity as a side effect.
- **Prerequisite you must build early:** invariants written down *outside* implementation code, and pure functions to test them against. You cannot property-test an invariant you never articulated — and you cannot extract one cleanly from code where the rule is smeared across six services.
- **Canonical users:** codecs and parsers, financial calculation libraries, the Rust/Haskell ecosystems (where this is culture, not specialty).

### Rung 4 — Adversarial / mechanism verification
- **Claim:** the *mechanism* withstands an adversary who knows the internals and controls timing, inputs, scheduling, and (some subset of) privileges. Named failure classes are either impossible or detected.
- **Evidence:** a battery of attacks, each targeting a **specific failure mechanism**, each demonstrably blocked — plus fault injection (kill -9, disk full, clock skew), Jepsen-style history checking (linearizability/serializability verification over concurrent histories), race detectors, deterministic simulation (FoundationDB, TigerBeetle's VOPR), and privilege-escalation probes.
- **Catches:** TOCTOU, snapshot staleness, lock-order flaws, fail-open configuration, bypass paths, replica anomalies — the entire class that rungs 1–3 are structurally blind to, because that class lives in *interleavings and intent*, not in inputs.
- **Cannot catch:** flaws outside the modeled adversary; wrong specifications; anything you didn't think to attack. Also note what rung 4 is *not*: passing Jepsen is not certification — it's "the adversary you modeled lost."
- **Cost:** ~5–10× for the mechanism, and most of that cost is *infrastructure and architecture*, not test-writing.
- **Prerequisite you must build early — this is the big one:** the mechanism must be **isolable**. You cannot adversarially verify a correctness rule smeared across application code, because the adversary attacks the *edges*, and smeared rules have infinite edges. Concretely: fault-injection points, schedulable/deterministic concurrency, observable histories, and a small kernel whose internals the adversary can't route around. **Your milestone lived this:** part of why Gate-1 was needed at all is that the original mechanism wasn't isolated (duplicated key derivation, INVOKER helpers, writers not yet wired) — the mechanism and the app were entangled, so "prove no one can bypass it" had no answer.
- **Canonical users:** databases (Jepsen found real consistency violations in systems everyone trusted — MongoDB losing writes, async-replication data loss in popular stores), consensus protocols (AWS adopted TLA+ after subtle replication bugs — Newcombe's CACM 2015 paper documents ten systems where model checking found bugs that had survived design review, testing, and in some cases years in production), FoundationDB ("we don't do integration testing in production" — bugs aren't allowed to escape the simulator), TigerBeetle.

### Rung 5 — Formal verification
- **Claim:** a machine-checked mathematical proof that the artifact satisfies its specification — *within a stated model* (hardware, compiler, and spec assumptions are trusted).
- **Evidence:** a proof artifact checked by a small trusted kernel: Coq/Lean/Isabelle/Dafny/F*, SPARK contracts; the seL4 microkernel (~9k lines of C, roughly 20 person-years of proof effort); CompCert (the verified C compiler); verified crypto (HACL*, Fiat-Crypto) now shipping in mainstream browsers; Signal's protocol verified in Tamarin.
- **Catches:** *everything inside the model* — including attacks no adversary ever thought of. That's the qualitative jump: rung 4 is bounded by the adversary's imagination; rung 5 is bounded only by the model.
- **Cannot catch:** the model–reality gap. A proof about the algorithm says nothing about a compiler bug, a side channel, or — most commonly — a *wrong specification*. "Proved garbage" is still garbage. Even crypto pairs its rung-5 proofs about the *math* with rung-4 tooling (constant-time verification) about the *implementation*.
- **Cost:** ~50–100× per line historically. seL4 is the honest data point: two decades of specialist effort for a kernel smaller than one of your migration files.
- **Prerequisite you must build early:** a formal spec, a tiny trusted base, and a design friendly to the logic (avoid unbounded structures, keep the kernel small and side-effect-free). You cannot retrofittably prove a system that wasn't shaped for proving.

One note on your ladder's famous names: **DO-178C DAL A** is really "rung 4.5 with heavy paperwork" — its power is requirements traceability and MC/DC coverage mandates, with formal methods (DO-333) as a supplement. **Jepsen** is rung 4 *as a service* — it doesn't certify, it attacks. **Cryptography** is rung 5 for the mathematics and rung 4 for the implementations. Real fields blend rungs; nobody lives at a single altitude.

---

## 3. How a domain picks a rung: the cost-of-being-wrong calculus

The selection isn't about budget or machismo. It's four multipliers on the expected cost of an undetected defect:

1. **Blast radius** — what's the worst credible consequence? A web-app bug costs a ticket and a hotfix. A payroll defect costs statutory penalties, audit findings, and re-issued statements. An avionics defect costs lives.
2. **Detectability** — is failure *loud* (crash, error, complaint) or *silent* (a wrong number that looks perfectly plausible)? Loud failures can live at low rungs because the world catches them for you. Silent failures demand high rungs, because nothing else will.
3. **Reversibility** — can you compensate, claw back, re-run? Or does the wrongness propagate forward into derived artifacts (every future report reads the poisoned snapshot; every downstream statement inherits the error)? Irreversible propagation pushes you up.
4. **Adversary presence** — is failure only accidental (entropy) or is there an actor — external attacker, compromised role, or merely a *privileged-but-buggy component* — actively routing around your controls? An adversary buys you at least one rung, because rungs 1–3 only test what *you* thought of, and the adversary's entire value proposition is thinking of what you didn't.

And a fifth, often decisive factor: **who must be convinced.** Rung-2 evidence convinces the team. Rung-3 convinces a careful reviewer. Rung-4 convinces a skeptical auditor. Rung-5 convinces a regulator or a mathematician. The required rung is set by the *most demanding consumer your evidence will ever face* — which is why "evidence grade: user vs auditor vs adversary" was one of the three trigger questions earlier. It's the same question from the audience side.

Map your milestone onto this and the discovery explains itself: payroll snapshots scored **high on all four multipliers** — large blast radius (money), silent failure (the D1 snapshot anomaly produces plausible numbers), irreversible propagation (every downstream artifact inherits it), and an active adversary by *construction* (the `app` role itself was a potential fabricator — a privileged-but-buggy actor is an adversary for evidence purposes). Rails is rung 2 as a service not because Rails is weak but because a typical Rails workload scores low on all four: loud failures, compensable, entropy-only. **Same framework quality, different domain constant.**

---

## 4. The fact nobody tells you: rungs attach to *claims*, not systems

This is the design insight that makes the ladder usable. You never put "the project" on a rung. You put each **correctness claim** on one:

- "No writer can mutate a committed snapshot" → rung 4 (attack battery: direct SQL as `app`, DISCARD ALL probes, trigger-bypass attempts)
- "Capture reflects all pre-gate committed writes" → rung 4 (the D1 class; optionally TLA+ model-check the protocol — that's the affordable 4.5 for a Go+PG stack, and exactly how AWS de-risked this problem class)
- "Proration and rounding conserve totals" → rung 3 (property tests: conservation, monotonicity, idempotence)
- "The CRUD screens render employee data" → rung 2
- Authority invariants (EF-E3: creation exclusivity) → rung 4 (the only defense against a fabricating writer is an attack battery that *tries* to fabricate)

The same web application can be rung 2 for features and rung 4 for its payment path. Security is its own claim family with its own ladder (pentests are rung 4 for security claims). **The design-stage artifact is therefore a claim inventory: list the top 5–10 correctness claims as falsifiable sentences, assign each an evidence grade, and only then design mechanisms.** Which claims you're willing to defend, and at what altitude, *is* the architecture.

---

## 5. The checklist — what to pin before implementation begins

This is the part you carry into every project. Sixteen questions, five groups, each with the cost of asking it late.

### A. Consequence — what's actually at stake
1. **What is the worst credible *silent* defect, quantified?** (money, legal exposure, safety, trust) — Late: you size the evidence effort after the architecture is fixed, and it never fits.
2. **Is failure loud or silent? Detectable after the fact — by whom, within what window?** — Silent defects are invisible to every rung below 4; discovering this post-launch means discovering it from an auditor.
3. **Is wrongness reversible (compensation, clawback, re-run) or does it propagate into derived artifacts?** — Propagation converts one bug into thousands of contaminated records; that multiplier changes the rung, and it's a property of the *data model*, which is the hardest thing to change later.
4. **Is anyone routing around the controls — external attacker, compromised role, or merely a buggy-but-privileged component?** — "Privileged but buggy" is the trap; it's how `app` with INSERT became a fabricator.

### B. Audience — what evidence will be accepted
5. **Who must ultimately be convinced — user, reviewer, auditor, regulator, court — and what artifact satisfies each?** — The most demanding consumer sets the required rung. Finding out who that is *after* building means rebuilding the evidence, not just the tests.
6. **Does a mandated regime apply (SOX, PCI, DO-178C, IEC 62304), and does it demand process evidence or outcome evidence?** — Regimes are effectively pre-pinned rungs; discovering one mid-build is a schedule catastrophe.

### C. Claim inventory — the actual design act
7. **What are the top correctness claims, written as falsifiable sentences?** ("No writer can mutate a committed snapshot.") — If they only exist in prose and code, no rung above 2 is available to you at any price.
8. **Which rung does each claim's evidence grade require?** — Write it *next to the claim*, before the mechanism exists.
9. **Which claims can degrade gracefully — make failure *detectable* (checksums, re-derivation, audit) instead of *impossible*?** — Detection is often an order of magnitude cheaper than prevention, but it's a different architecture (append-only logs vs. locks), so it must be chosen at design time.

### D. Architectural prerequisites — what the chosen rung demands
10. **Can each high-rung claim be concentrated into a small, isolable mechanism — or is correctness smeared across application code?** — *The* question of your milestone. Smearing is unretrofittable; it is why Gate-1 had to exist.
11. **Does the design expose fault-injection and scheduling control — deterministic tests, controllable time, crash injection, history replay?** — You cannot attack interleavings you cannot reproduce. Retrofitting determinism into entangled code is close to a rewrite.
12. **Are the invariants written down outside implementation code — in a form that is executable (property tests), model-checkable (TLA+), or provable (Dafny/F*)?** — The spec *format* is chosen by the target rung; a user-story backlog cannot be model-checked.
13. **What is the trusted base — DB engine, framework, compiler, ops procedures — and is that trust explicit and documented?** — Your DISCARD ALL finding is precisely this question being asked late: session-lock continuity silently depended on an operator action that couldn't be revoked, inside the trusted base nobody had enumerated.

### E. Economics & governance
14. **What does the chosen rung cost *in this stack*, and what would it cost to move up after implementation?** — Tooling is wildly uneven: property and simulation cultures are rich in Rust/Haskell; Go's testing culture is example-heavy, so rungs 3–4 carry extra infrastructure cost there. And the retrofit multiplier is the lesson of milestone 2.1: the rung you skip early, you pay for later at a multiple — because rung-4 evidence requires isolation, and isolation is architectural.
15. **Where is the rung decision recorded, with acceptance criteria pinned *before* experiments run?** — Your Gate-2 discipline (criteria pinned in advance) is this question institutionalized; without it, evidence gets re-litigated after the fact and the ladder silently slides back down.
16. **Who re-verifies under change, and who verifies the verifiers?** — A rung is a **subscription, not a badge**: the red battery must rerun on every migration, proofs must recheck on toolchain updates, and the checkers themselves deserve at least rung 3 (checkers have bugs too).

---

## 6. The two failure directions — and the tripwires

The checklist exists to prevent failure in *both* directions:

- **Under-rung** (your milestone): implicitly rung 2 in a domain that required rung 4. The cost wasn't the bugs — it was that the *architecture* had to be partially rebuilt to make rung-4 evidence possible at all.
- **Over-rung** (the opposite trap): demanding proofs for a CRUD screen and attack batteries for a marketing page. Equally expensive, more common in teams freshly burned by an under-rung incident. The checklist forces a *deliberate per-claim* choice instead of an emotional global one.

How to recognize, early, that you're implicitly below your required rung — the tripwires your project hit:

1. The suite is green, yet a serious defect was found by **reading**, not testing.
2. Someone asks "prove nobody can bypass this" and there is **no artifact to point at** — the debate happens in prose.
3. The spec starts prescribing **evidence** ("mechanism proofs," "attack tests") while the codebase still produces only demonstration evidence.
4. Correctness arguments keep containing the word "**just**" — "just don't do that," "just always use the helper" — which is rung-1 evidence wearing a rung-4 costume.

Any one of those firing means: stop, inventory the claims, assign rungs, and check whether the architecture can even host the evidence the domain demands — *before* more implementation lands on top.

---

**The one-sentence version:** pick the rung per claim from the cost of being silently wrong and the audience your evidence must convince; then build the architecture the rung demands — because every rung's prerequisite is a design property, and design properties are the one thing that gets exponentially more expensive to change after the first line of implementation.