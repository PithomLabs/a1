# Design-Gate Checklist — Trust-Critical "Freeze-and-Compute" Systems

*A cross-domain checklist of questions that must be answered at design stage — before implementation — for any system that computes something important over mutable, concurrently-changing facts: payroll, billing, banking close-of-business, claims, regulatory reporting, ERP/MRP, order processing, data pipelines.*

**Core premise:** every question below is a *business decision disguised as a technical detail*. The single most expensive pattern in software is leaving these to be "figured out during implementation" — because each one is inherited by every feature that follows, and retrofitting them after data exists means re-auditing everything ever written. (Evidence: an entire remediation milestone spent converting ten such deferred decisions into invariants.)

---

## How to use this

Run it as a workshop with the **domain owner in the room** (finance, compliance, actuarial, clinical — not just engineering). Every answer must produce one of:

- a **written decision** with a named owner and a test,
- a **documented artifact** (inventory, matrix, diagram),
- or an **explicit deferral** with an owner and a trigger date.

An unanswered question here is not neutral — it is a decision made by accident, in production, at 10x cost.

---

## The Ten Killer Questions

The distillation. Each maps to a real defect class that cost a full milestone to close.

| # | Question | Late discovery cost |
|---|---|---|
| 1 | **As of *when* does this result exist — and can any input change while it is being computed?** | The freeze decision is inherited by every writer and feature forever; retrofit = a synchronization protocol across ~a dozen write paths |
| 2 | **Who may *create* each critical record — not edit, create?** | Immutability enforced as "no UPDATE" falls through the INSERT hole; fabricated records under a valid-looking hash |
| 3 | **Can any rule pass vacuously — when a list is empty or a query returns nothing?** | Zero rows must mean *fail*, not pass; vacuous passes are found by auditors, not tests |
| 4 | **Does each status word mean a verified fact or a hope?** ("active", "verified", "complete", "approved") | Ambiguous status semantics silently produce wrong money; redefining them later invalidates historical data |
| 5 | **Does each fact cover the whole period it claims to cover?** | "One matching row exists" ≠ "coverage is complete"; coverage is a union computation with gap detection |
| 6 | **Do the writer and the reader share one definition of "effective"?** | Divergent interpretations store data successfully that is *permanently unusable* — the most expensive bug class there is |
| 7 | **Is verification derived from actual execution, or asserted by a label?** | Hardcoded "VERIFIED" labels rot the whole trust chain; evidence must be tied to outcomes |
| 8 | **List every path that can write every input — including the ones added next quarter.** | The invariant is only as strong as the least-cooperative writer; one forgotten path reopens the entire class |
| 9 | **When the system lacks evidence, does it block loudly or silently proceed?** | Missing evidence must never be treated as verified non-applicability; silent exclusion surfaces in reconciliation, months later |
| 10 | **Must the result be reproducible years later — from what inputs, verifiable by whom?** | Provenance is a feature, not metadata; you cannot reconstruct evidence you never captured |

---

## A. Computation boundary & freeze semantics

*The "as of when" decision. Everything else inherits it.*

1. Enumerate the **complete input inventory**: every fact the computation reads — including hidden inputs (configuration, rule packs, tax tables, calendars, exchange rates, feature flags).
2. For each input: **can it change while the computation runs?** By whom, how often, at what speed?
3. Define the **temporal meaning of the result**: a wall-clock cut ("everything committed before point T is included") or a merely consistent cut ("a valid serialization — some concurrent changes may fall to the next run")? *These differ; pick deliberately per business need.*
4. Choose the **freeze mechanism** — one of five, knowingly:
   - **freeze by lock** (block writers during computation),
   - **freeze by append** (inputs are immutable events; read a cut),
   - **freeze by version** (optimistic: verify nothing changed, retry if it did),
   - **freeze by boundary** (dedicated executor / batch window),
   - **freeze by time** (bitemporal effective-dated records).
5. Define **re-run semantics**: does recalculation *replace* or *supersede*? Is the old result still binding for already-reported outputs?
6. Define what happens to **events that arrive after the cut**: rejected, deferred to the next cut, or handled by a formal retroactive-amendment workflow?
7. How long may the world stay frozen (SLA), and what does the business experience while frozen?

*Bites late because:* every feature added after this decision either inherits it cleanly or reopens it expensively.

## B. Time, effective dating & coverage

1. Does every fact carry **business-effective dates** distinct from **record/processing time** (bitemporality — two clocks, two meanings)?
2. For every period-scoped rule: is **complete-period coverage** required, or is partial coverage prorated? Is proration *supported* or *explicitly blocked with a typed reason* until researched?
3. For mid-period starts/ends, adjacent successors, gaps, and overlapping conflicts: **allowed, rejected, or resolved by priority** — per entity, written down.
4. What defines "the period" for each consumer (pay period, reporting period, policy period) — one authority or several?
5. Calendar semantics: business dates vs system dates, time zones, DST, retroactive date corrections.

*Bites late because:* mid-period bugs are discovered by finance in production, not by design review; proration is a research project, not a flag.

## C. Corrections, supersession & history

1. Is correction **append-only** (successor records) or **in-place**? Never both for the same entity.
2. Supersession rules: may a successor **widen, narrow, or overlap** the predecessor's effective span? Who computes the surviving spans — the *writer* (at validation time) or the *reader* (at resolution time)? **One definition, shared.**
3. May a correction arrive for a period whose output was already computed or reported? What re-computation and approvals does that trigger?
4. Are historical facts *ever* mutated? If never — what enforces it across every path?
5. How are dangling, circular, or ambiguous supersession chains **detected and blocked**?

*Bites late because:* writer/resolver divergence produces records that store successfully and remain permanently unresolvable — data that is a liability the moment it is written.

## D. Authority, roles & integrity

1. For each critical artifact: **creator, editor, reader, forbidden** roles. Creation authority is the hole every immutability design falls through.
2. "Immutable" decomposed: how are **append, re-parenting, pointer/current-version changes, and hash alteration** each separately blocked?
3. Full **role inventory + privilege model**: what does each role legitimately need; what does it never need?
4. **Separation of duties** for approval chains: may the preparer approve? the approver release?
5. What can **administrators and migrations bypass**, and what must they still not? Are privileged actions audited? (Honest limit: you cannot protect against the schema owner — say so, and audit everything else.)

*Bites late because:* privilege re-architecture after data exists means re-validating every historical record's integrity.

## E. Write-path inventory & participation

1. Enumerate **every mutation path** per input: application services, database functions, triggers, batch imports, admin consoles, CLI, APIs, future integrations, direct SQL.
2. Decide the **enforcement boundary per invariant**: database (triggers, privileges, controlled functions) vs application — and state explicitly what a bypasser achieves.
3. If synchronization is needed: **one protocol, one key derivation, one acquisition order** — shared, never reimplemented per feature.
4. Define the **new-writer onboarding rule**: what must any future feature do to comply, and which test catches forgetting it?
5. Which invariants must hold **even under hostile direct SQL**? (This one answer sets the cost tier of the entire enforcement architecture.)

*Bites late because:* every unenumerated path is a future synchronization defect; "who else writes this?" costs an hour at design and a milestone in remediation.

## F. Lifecycle, state machines & vacuous truth

1. Every lifecycle entity: **states, legal transitions, who may trigger, evidence required per transition** — an explicit machine, not conventions.
2. **Vacuous-truth audit**: for every rule over a collection, what happens when the collection is empty? Zero rows must fail, not pass.
3. **Freshness preconditions** (offers, tokens, quotes): how is freshness defined, verified, and made single-use?
4. **Concurrent transitions**: two simultaneous captures/approvals — exactly one winner, others typed-rejected?
5. **Idempotency policy** per operation: same request twice; concurrently; after partial failure.

*Bites late because:* vacuous passes and duplicate transitions are found by auditors and reconciliation; retrofitting state machines touches every screen, report, and integration.

## G. Failure semantics, blockers & atomicity

1. **Blocker taxonomy**: typed, business-readable, actionable — and produced *identically* by every surface that can detect the condition (readiness, capture, validation), never special-cased per endpoint.
2. Confirm per rule: **missing evidence ≠ verified non-applicability**.
3. **Atomicity boundary** per operation: fully applied, or zero residue — including *no audit residue on rejection*.
4. **Retry posture**: bounded, fresh context, no unlimited retries hiding inconsistency; timeouts are typed outcomes, not hangs.
5. What does the user see when blocked: error envelope, request ID, next action?

*Bites late because:* silent exclusion is invisible until totals are reconciled; inconsistent blockers across surfaces multiply integration and support cost.

## H. Evidence, provenance & reproducibility

1. **Reproducibility requirement**: from what inputs, to what precision, verifiable by whom, retained how long?
2. **Evidence chain**: source bytes → digests → reviewed artifacts → computed output. Are source bytes actually re-hashed, or only declared? (A valid-looking digest string proves nothing.)
3. Verification is **derived from execution or digest-pinned independent artifacts** — never a hardcoded status label.
4. **External dependencies** (rule packs, tax tables, models, rates): version pinning, bundle-digest coverage, effective-period coverage, a review ceremony for changes, auditable history of every re-pin.
5. **Canonical form**: who owns the byte-canonical encoding and hashing — one brain or two — and can a third party independently re-verify?
6. **Provenance completeness**: does every output figure carry its parent/source record identifiers?

*Bites late because:* retrofitting provenance onto a live system means reconstructing evidence you never captured — often impossible.

## I. Audit, compliance & retention

1. Which events are **audit-worthy**, with what fields (actor, reason, before/after, references), with what **tamper-evidence**, enforced where?
2. **Retention vs privacy**: legal erasure rights vs mandatory financial retention — decide the exception mechanism early (pseudonymized evidence, crypto-shredding, or documented exemption).
3. **Jurisdiction inventory**: record-keeping and e-signature rules per regime; who signs what, with what weight?
4. Can privileged users **forge audit rows**? What is the containment story?
5. Whose **clock** is audit evidence?

*Bites late because:* retention-vs-erasure conflicts are legal problems discovered after the schema can support neither.

## J. Threat model & privilege architecture

1. Enumerate **actors and capabilities**: users, services, admins, migrations, integrations — and an attacker holding each credential.
2. Set the **trust dial consciously**: which invariants must survive a hostile database credential? (Trusted-application boundary ≈ baseline cost; hostile-role-proof ≈ multiples of that. The dial is nearly impossible to turn *up* later.)
3. **Containment** per compromise scenario; blast radius per role.
4. **Supply chain**: what enters the trust root (packs, models, imports, third-party data) and what ceremony gates it?
5. **Environment trust**: disposable test databases only; automated fresh-environment acceptance (stale-schema reuse silently invalidates test evidence); user data never in test.

*Bites late because:* the threat model silently prices the whole architecture; discovering it late converts design decisions into remediation projects.

## K. Change, migration & decision governance

1. Migration policy: forward-only? guarded downs? **historical-file integrity checks** (checksummed baselines)? role-separated execution?
2. **Decision register**: every semantic decision ("what does X mean") recorded with rationale, tests, and a **named owner**; amendments only via explicit approval.
3. Domain specifications as living artifacts: where do they live, who updates them when behavior changes?
4. Can a migration or refactor **invalidate historical evidence**? Version every consumer-facing contract.
5. Deprecation and sunsetting policy for old versions and old computed results.

*Bites late because:* unpinned semantics rot silently — the next team re-litigates "what does active mean" with production data on the line.

## L. Operations: clocks, environments & observability

1. **Time-zone & clock strategy**: correct under multiple time zones; business date vs wall-clock; **never infer ordering from timestamps**.
2. **Performance of the freeze**: how long are writers blocked, what queues under load, what degrades first, what is the retry-storm posture?
3. **Observability**: operators must distinguish *blocked-by-design* from *hung*; blocker rates alertable; audit trail queryable.
4. **Backup/restore**: does a restore preserve evidence integrity, pin history, and hash verifiability?
5. **Acceptance hygiene**: clean environments with fresh identities per run; skipped tests never count as passes; recorded exact commands and outcomes.

*Bites late because:* time-zone drift and stale-schema drift are found in production reconciliation; observability gaps turn incidents into mysteries.

---

## Artifacts the checklist must produce

1. **Input inventory** — facts × mutability × writers × speed of change.
2. **Writer matrix** — every mutation path × every invariant × enforcement boundary.
3. **Decision register** — semantic decisions, owners, rationale, amendment procedure.
4. **Threat-model one-pager** — actors, credentials, the consciously-set trust dial.
5. **Evidence-chain diagram** — source → pin → compute → report, with verification points.
6. **Blocker taxonomy** — every blocking condition, its typed code, its business text.
7. **State machines** — per lifecycle entity, with evidence requirements per transition.

## Definition of done for the design gate

- Every question above has a written, owned answer — or an explicit deferral with an owner and a trigger date.
- Every semantic decision has an owner **and a test**.
- Every write path in the inventory has an enforcement note.
- The freeze pattern, the trust dial, and the coverage rules are signed off by the **domain owner**, not only by engineering.
- Re-run the checklist whenever a **new input type, a new writer, a new jurisdiction, or a new output consumer** appears.

---

*The ten killer questions and the five freeze patterns generalize across payroll, banking, billing, insurance, healthcare, ERP, and data-platform domains. The domain supplies the vocabulary; the checklist supplies the invariants.*
