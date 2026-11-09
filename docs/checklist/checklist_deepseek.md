# The Cross-Domain Early-Design Checklist

## A business analyst's checklist for systems that must maintain consistent, authoritative, auditable state under concurrency

Use this before any code, before schema, before story points. Each question, if left unasked, becomes an expensive retrofit later. The cost of a one-hour workshop question is the cost of a three-week rearchitecture. The checklist is grouped by concern; each section names the question, what it forces you to pin down, and what it costs when left to "we'll figure it out".

---

## A. Invariants and the meaning of "correct"

The most expensive rework comes from discovering, mid-implementation, that nobody agreed what the system guarantees.

**A1. What business invariants must always hold, independent of how they are computed?**  
Name them in domain language ("a payroll snapshot includes every input committed before validation", "no instruction covers less than its full required period", "no fabricated record can be distinguished from a genuine one by its content alone"). Force each invariant into one sentence with a subject, a predicate, and a scope. Vague invariants become unmaintable code.

**A2. What is the *semantic* of "point in time"?**  
- Serialization point (some consistent serial history exists)?  
- Wall-clock (everything committed before time T, no matter what)?  
- Validation boundary (everything committed before the validation decision, no exceptions)?  

These are three different requirements with three different mechanisms. A two-hour conversation here saves months. Payroll, ledger, inventory, and audit all have this choice, and picking the wrong one produces "technically consistent but business-wrong" results.

**A3. What counts as "included" versus "excluded" at the boundary?**  
Define inclusion and exclusion rules explicitly: concurrent commits, uncommitted-at-validation writes, writes that were visible during capture but rolled back, writes from a different trust zone. If you cannot write the sentence "a change is in the snapshot if and only if...", you do not yet have a snapshot.

**A4. What does "complete" mean for the domain?**  
Coverage is not existence. A period-covered instruction is not the same as an existence of a qualifying row; a full-month salary is not the same as one effective segment. Pin the exact definition of "this input set covers the required scope". This is where SHG partial coverage, effective-date gaps, and rule-pack period coverage all originate.

**A5. What is authoritative? Where does the "truth" live?**  
Does truth live in a row, in an event log, in an external system, in a signed artifact, or in a derived projection? Name the single source for each input class. If there is more than one, the system must reconcile them explicitly, not silently.

**A6. What fails loudly, and what fails silently?**  
For each invariant, decide the failure mode: abort, block, mark stale, escalate. Silent failures are the ones that cause payroll incidents. Write the matrix.

---

## B. Data model and mutability

This is the single largest lever for reducing concurrency complexity.

**B1. Are the inputs mutable in place, or append-only?**  
If mutable, you inherit every snapshot-isolation and coordination problem discussed in this milestone. If append-only, reads and writes do not conflict, and the class of anomalies collapses. This is the first question to ask, and it is almost never asked early.

**B2. If append-only: how do you establish a *commit-ordered* watermark?**  
A `MAX(id)` over a sequence does **not** establish commit order; sequence values allocate at write time and roll back with the transaction, so a lower ID may commit after a higher one. You need a stable snapshot (MVCC) or a commit-ordered column set that is verified, not assumed. Decide the watermark mechanism before the schema exists.

**B3. If mutable: who owns the row?**  
One writer, many writers, or "the app"? Shared mutable rows without an ownership contract are the root of most consistency bugs. Name the owner.

**B4. What is the identity model?**  
Immutable primary keys, UUIDs vs. natural keys, employer scoping, multi-employer readiness. Identity choices calcify fast. Payroll's "employer-scoped, multi-employer-ready" schema is an example of a decision that is cheap early and expensive later.

**B5. What is the correction model?**  
In-place UPDATE, successor row, compensating event, or supersession chain? Corrections are the hardest test of a data model. Pin the model and its resolution rules **before** the first correction is implemented. F-A1-WIDEN and F-A1-FREETAIL in this milestone are direct consequences of not pinning this early.

**B6. What is the temporal model?**  
Effective dates, transaction dates, recorded dates, half-open vs. closed intervals, time zone semantics, inclusive vs. exclusive boundaries. Payroll, insurance, and scheduling all get this wrong on the first pass. Pick one and document it once.

**B7. What is the "lineage" model?**  
For any derived record, what are its ancestors, and how is that lineage stored? Compensation segments, effective employment terms, and rule-pack pins all need lineage. Decide whether lineage is stored explicitly or derivable, and whether the resolver loads full ancestry or only overlapping spans. F-A1-WIDEN is a query-design bug born from not distinguishing these.

**B8. What is the "evidence" model?**  
Every claim in the system that can be audited must have evidence. Where does it live, who can alter it, and how is it bound to the record? Source bytes, goldens, review refs, and gate artifacts are all evidence. Decide the evidence schema before you need it for audit.

---

## C. Trust, authority, and capability boundaries

The second largest source of retrofit cost is the trust model.

**C1. Who is the caller, and what is the caller authorized to do?**  
Name every principal: the user, the application service, the database role, the migration runner, the admin, the external system. For each, name the *capability*, not just the *permission*. A capability is what the caller may cause the system to do; a permission is a list of tables and functions. Capabilities age far better.

**C2. Where is the trust boundary?**  
Is the app trusted to be correct? Is direct SQL a threat? Is a compromised app a threat? Is a compromised admin a threat? Each answer changes the design fundamentally. Payroll under "app is untrusted" requires database-level enforcement; the same domain under "app is trusted" can be vastly simpler. This is one of the most expensive decisions to change late, because it changes every enforcement mechanism simultaneously.

**C3. Can a caller fabricate a record that will later be treated as authoritative?**  
For each authority-bearing table (snapshots, validation records, pay-run status, audit, pins, evidence), can the ordinary caller `INSERT`? Can it `UPDATE`? Can it change a pointer? Can it append a child row? This is the "authority" half of the payroll dilemma, and it generalizes to any "evidence" table anywhere. Decide which tables are *derivation-only* and enforce it at the boundary.

**C4. Which capabilities require a non-app connection?**  
Some capabilities (snapshot capture, evidence generation, pin ceremony, signing) are best performed by a role that the ordinary app cannot impersonate or whose connection the app cannot control. Identify them early. A third role, a separate process, a short-lived credential, or a signed artifact are all viable; the decision has to be made before the caller is already in production.

**C5. Which utilities can break your protocol?**  
`DISCARD ALL`, `pg_advisory_unlock_all()`, `SET session_replication_role`, `SET CONSTRAINTS`, `SET ROLE`, `RESET ALL`, cancellation, prepared statements, and connection reuse all have subtle effects. For every protocol, ask: *can the caller issue a standard command that invalidates my assumption?* If yes, the protocol is not a boundary. This is the `DISCARD ALL` lesson, and it appears in every "session-scoped" design: connection pooling, transaction-scoped locks, prepared statements, temporary tables, session variables.

**C6. Where is the single source of key derivation?**  
If every service, trigger, and function derives a lock key, sorting order, or scope independently, you have latent divergence. One function, one canonical derivation, tested behaviorally. This is cheap early; it is constant-divergence-detection late.

**C7. Where is the single source of canonical bytes and hash?**  
If multiple components can compute a canonical hash of the same logical record, you have a subtle class of "correct hash, wrong bytes" bugs. Pin the canonical form and hash computation to one authority (Go or SQL, not both), and ensure the other side verifies against it rather than reimplementing.

---

## D. Concurrency and ordering

**D1. Which operations must be mutually exclusive, and against which operations?**  
Build the matrix *before* implementation: writer × writer, writer × reader, writer × validator, reader × reader, capture × capture, capture × validation. For each cell, name the invariant it protects and the mechanism that protects it. If you cannot fill in a cell, you have a gap.

**D2. What is the lock scope (transaction vs. session) and ordering?**  
Transaction-scoped is simpler and safer; session-scoped is necessary only if coordination must span BEGIN. Pin the scope, name the acquisition order, and document the reverse release order. If more than one scope is used, justify each, and test the interaction (the same key in two scopes conflicts only because PostgreSQL shares the namespace).

**D3. What is the lock key derivation, and is it unique per invariant?**  
Each lock must protect a named invariant. An overly broad lock (one global lock) blocks unnecessarily; an overly narrow lock (per-row) fails to serialize multi-row invariants. Sort order and key uniqueness are not optional.

**D4. What are the deadlock risks?**  
Any two writers that acquire more than one lock can deadlock if they acquire in different orders. The canonical order is not a "nice to have"; it is a correctness property. Test that all paths follow it.

**D5. What are the cancellation, timeout, and failure paths?**  
If a writer is cancelled mid-transaction, does the lock release cleanly? If capture is cancelled mid-session, does the dedicated connection get discarded or returned safely? Each failure path is a potential source of stuck locks, connection leaks, or stale state.

**D6. What is the "writer participation" contract?**  
Can a new writer be added to the system without knowing about the contract? If yes, the contract is unenforced. Either the database enforces it (trigger, DEFINER function, REVOKE), or every writer must be audited whenever added. This is the P1a–g lesson, and it recurs in every "coordination" architecture.

**D7. What happens when the invariant is violated?**  
Abort with a specific error, retry with bounded backoff, or degrade. Pin the failure envelope (error class, retriability, observability) before implementation. `40001` mapping is a symptom of not pinning this early.

---

## E. Temporal semantics

**E1. What is the temporal grain?**  
Month, day, hour, instant? Payroll is monthly, but overtime is hourly, SHG is daily. Grain mismatches produce mid-period ambiguity. Pick one and document cross-grain rules.

**E2. What are the interval rules?**  
Half-open vs. closed, inclusive vs. exclusive, time zone, DST. These are the silent sources of "off by one day" that produce regulatory findings.

**E3. What is the effective-date model?**  
Effective from, effective to, transaction date, recorded date, backdating rules, future-dating rules. Every domain has all five; most systems conflate two or three. Name them all.

**E4. What happens to historical records when a new fact arrives?**  
Does the past get rewritten, or does a successor record exist? Does the resolver honor the historical context, or the current context? This is the "effective vs. recorded" distinction and it recurs across ledger, insurance, HR, and scheduling.

**E5. What is "now" in each context?**  
Server time, database time, transaction start, statement time, user-provided time, business calendar time. Mixing them produces non-reproducible results, which is fatal for audit and reconciliation.

**E6. What is the mid-period rule?**  
If an input changes mid-period, does the system prorate, block, treat as full-period, or require explicit correction? Payroll's `COMP_MID_PERIOD_CHANGE` blocker is a domain-specific answer to a general question. Every domain has an analogue.

---

## F. Coverage and completeness

**F1. What must be covered, for what scope, at what time?**  
Coverage is not the same as existence, and "the required scope" is domain-specific (pay period, claim period, contract term, reporting period). Name the scope before implementing "presence".

**F2. How is coverage measured?**  
Union of intervals, intersection, superset, exact match, with gaps, with overlaps. The SHG nine coverage scenarios (mid-period start/end, gap, adjacent, conflicting overlap, verified-none successor, historical outside period) are a template. Enumerate the analogous scenarios for every "coverage" requirement.

**F3. What happens when coverage is incomplete?**  
Block with a precise gap, warn and proceed, or auto-fill? Blocking with a precise message is the safe default; the message must name the gap, not just "incomplete".

**F4. What is the aggregation model across multiple qualifying records?**  
One record must span the whole period, union of records must cover, or first-match wins? Payroll's cross-fund blocker is an instance of "the domain has not authorized aggregation". Name the rule and the authority behind it. If the domain has not authorized a rule, block, do not infer.

**F5. What is the "verified none" model?**  
If a required input is genuinely absent (no SHG instruction, no claim, no medical certificate), how is "absent" distinguished from "not yet recorded"? This distinction is essential for completeness, and it is almost always missed.

---

## G. Evidence and provenance

**G1. Which artifacts are evidence, and who verifies them?**  
Source bytes, golden fixtures, review records, gate artifacts, signed receipts, third-party confirmation. For each, name the verifier and the verification rule. "Verified" is a claim; evidence is the thing that supports it.

**G2. Where is evidence stored, and is it immutable?**  
Append-only evidence tables, content-addressed storage, digests in a separate trust root. If evidence is mutable, it is not evidence.

**G3. What is the "trust root" and who can change it?**  
The reviewed version, the pinned digest, the signed artifact, the approved config. Changing the trust root must be an audited operation, not a `UPDATE`. The C2-REPIN finding is the payroll instance of a general problem.

**G4. What is the hash chain, and what does it cover?**  
Hashes should cover the substantive evidence, not just the manifest. If the hash is computed over a summary that can be tampered with independently of the underlying bytes, the hash proves nothing. Every content-addressed system must answer: what exactly is being hashed, and why can't the bytes drift?

**G5. What is "verified", and what evidence supports it?**  
"GOLDENS_VERIFIED" is a label, not a proof. What was executed, when, by whom, and where is the execution record? A label that can be set without evidence is not a verification; it is a self-attestation. The C1-GOLDENS finding is the payroll instance.

**G6. Where does the "verified" state live?**  
In a record, in an external system, in a signed artifact? If it lives only in the app's memory or a log line, the verification is not reproducible.

**G7. What is the tamper-evidence model?**  
Signatures, hash chains, append-only tables, external attestation? Pick one, and name the threat model it defends against.

---

## H. Change and correction

**H1. What is the correction model?**  
In-place update, successor row, compensating event, supersession chain, or explicit revocation? Each has different consequences for audit, for resolution, and for downstream systems. F-A1-FREETAIL is a direct consequence of not pinning this.

**H2. Who can correct what, and what is the approval model?**  
Corrections to payroll, ledger, or audit have different approval requirements than ordinary writes. Name the approval matrix and its enforcement point.

**H3. What does a correction do to already-validated records?**  
Invalidate, supersede, annotate, or leave untouched? Each choice is a business decision, and each has a record-lifecycle consequence.

**H4. What does a correction do to downstream consumers?**  
Snapshots, reports, filings, integrations. If corrections can be picked up silently, downstream systems may diverge. If not, they may go stale. Decide the propagation model early.

**H5. What does "supersede" mean, and where is it enforced?**  
Successor chains are the domain's hardest resolver problem. The resolver must agree with the writer on effective spans, surviving tails, and supersession semantics. The F-A1-WIDEN bug is a resolver/writer disagreement, and it recurs every time a supersession chain meets a date range.

**H6. What is the correction audit trail?**  
Who, when, why, prior state, new state, evidence, and reviewer. If corrections are invisible in the audit, the audit is not trustworthy.

---

## I. Failure modes and observability

**I1. What fails loudly, and what fails silently?**  
For each invariant, name the observable signal of violation. Silent violations are the ones that produce incidents. Every "cannot happen" must be accompanied by a monitoring check that fires if it does.

**I2. What is the error taxonomy?**  
Transient, retriable, permanent, policy-blocked, and "unknown". Each maps to a different caller action. Mixing them is a common source of unbounded retries, stuck workflows, and opaque support tickets.

**I3. What is the observability story?**  
Metrics, logs, traces, and — critically — domain-level audit records that the business can read. "The database did something" is not observability. "A snapshot was captured with hash H, covering period P, for employer E, with N employees" is.

**I4. What is the replay/rerun model?**  
If a step fails, can it be re-run? Is it idempotent? What state must be reset? Payroll, ledger, and booking all need this; specifying it early prevents a class of "fix forward by hand" operational incidents.

**I5. What is the "known-blocked" list?**  
Some capabilities are explicitly deferred or blocked (OT-001 is the payroll example). This must be enforced at the boundary (manifest, registry, activation), not remembered by humans.

---

## J. Testing and acceptance

**J1. What is the adversarial test set?**  
For each invariant, one test that reproduces the original failure and one positive control that proves the legitimate operation still works. "Happy path green" is not acceptance.

**J2. What is the direct-SQL threat model in testing?**  
Does the test suite operate as the ordinary role, not a superuser? A test that runs as superuser does not prove the app role is safe. Direct SQL as `app` must be a first-class test category.

**J3. What is the concurrency test strategy?**  
Separate physical connections, both orderings (writer-first, reader-first), `pg_locks` or equivalent observation, persisted-state inspection after success and after failure. "It worked in CI" is not a concurrency proof.

**J4. What is the schema-drift strategy?**  
Fresh database names, checksummed migrations, no silent reuse of stale schemas. Goose does not prove that an already-applied migration file is unchanged. The H-STALE-SILENT finding is the payroll instance of a general problem.

**J5. What is the acceptance evidence format?**  
Exact commands, database identities, TZ, tool versions, and outcomes. Reproducible evidence is the only evidence that survives review.

**J6. What does "green" mean?**  
A unit test passing is not "the invariant holds"; it is "the code path exists". Acceptance is defined by invariant closure under the real role, not by test count.

---

## K. Operational and organizational

**K1. Who owns each invariant?**  
A named person, not "the team". Unowned invariants decay first.

**K2. What is the review process for new writers?**  
Any new input path (feature, import, integration, admin tool, direct SQL) must be reviewed against the concurrency and authority contracts. If the process does not exist, P1a–g happens by default.

**K3. What is the "trust boundary" review cadence?**  
Roles, credentials, evidence roots, and admin paths should be re-reviewed periodically, not only at design. Trust drift is a real phenomenon.

**K4. What is the "deferred" register?**  
Explicitly deferred capabilities (OT-001, third role, payroll-line governance) live in a register, enforced at the boundary, and reviewed per milestone. Deferrals do not silently reappear.

**K5. What is the environment contract?**  
Disposable DBs, loopback-only API, no production deploys during remediation, no modification of upstream modules. The baseline preservation discipline (00001–00012 untouched, work uncommitted) is a general practice, not a payroll quirk.

---

## The top ten non-negotiables

If you read nothing else, these ten questions determine whether you will have an expensive retrofit:

1. **What is the semantic of "point in time"?** Serialization, wall-clock, or validation boundary?
2. **Are inputs mutable or append-only?** This is the single biggest complexity lever.
3. **Who is the trust boundary, and what can the caller fabricate?** Ordinary app or adversarial direct SQL?
4. **What is the correction model, and does it agree with the resolver?** The F-A1-WIDEN/FREETAIL class.
5. **What does "coverage" mean, and how is it measured?** Existence is not coverage.
6. **What is evidence, who verifies it, and where does verification live?** Labels are not proofs.
7. **What is the trust root, and who can change it?** Audited pin ceremony, not `UPDATE`.
8. **Where does every lock, key, and hash get derived?** One authority, tested behaviorally.
9. **What utility commands can break the protocol?** `DISCARD ALL`, session vs. transaction scope, `SET ROLE`, cancellation.
10. **What is the "verified" evidence format, and what test proves it under the real role?** Direct SQL as `app`, both orderings, persisted state, no partial writes.

---

## How to use this checklist

Run it as a **two-hour structured workshop** at the start of any milestone that touches concurrency, authority, temporal semantics, evidence, or coverage. For each item, record:

- The question.
- The answer (pinned).
- The enforcement point (DB boundary, app, process, or accepted risk).
- The test that proves the enforcement.
- The owner.
- The deferral register entry, if any.

The output is not "we discussed it". The output is a **decision log** that future milestones can reference, that reviewers can gate on, and that implementers can treat as a contract. The reason this milestone is expensive is not that the questions are unanswerable; it is that they were answered late, in code, by trial and error, and each late answer constrained the next.

The checklist is reusable. Payroll is one instance. The next milestone (first Singapore statutory calculation) will hit items A2, A5, C2, C3, D6, E4, E6, F1, G3, G5, and J2 almost immediately. If those answers are already pinned, that milestone is significantly cheaper. If not, it inherits this milestone's retrofit cost.

The general principle is the one this milestone demonstrated:

> **Pin the invariant, the semantic, and the enforcement boundary before you pick the mechanism. Mechanisms are cheap to change when the invariant is pinned, and impossible to change when the invariant is discovered late.**