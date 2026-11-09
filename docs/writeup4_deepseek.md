## prompt

write a technical writeup, what exactly are the challenges here, why is it so complex, is it about database technicality or is the architecture wrong, is this inherently complex or we just stumbled with accidental complexity

## answer

# WorkforceOps M2.1: Why This Is Hard

## A technical writeup on inherent vs. accidental complexity in the D1 admission remediation

---

## 1. The invariant, stated plainly

The system must guarantee that **a validated payroll snapshot is a complete, authenticated, point-in-time-consistent view of every input that a payroll calculation would consume**, and that this guarantee survives:

- every supported write path (service, trigger, DEFINER function, direct `app` SQL);
- every interleaving of concurrent writers and readers;
- the ordinary `app` role, not a privileged one;
- adversarial callers who are authorized but not trusted to follow the protocol.

That is the invariant. Everything else — XID tickets, session locks, DEFINER wrappers, 21-overload revokes — is machinery in service of it.

The Gate 1 dispute is not about whether the machinery works. The tests prove it does. The dispute is whether the machinery *enforces* the invariant under the ordinary `app` role, or merely *implements a protocol that `app` is expected to follow*. Prompt 17 §2.1 explicitly forbids the latter: "Do not use application-only checks to protect database invariants."

This distinction is the source of nearly all the difficulty.

---

## 2. The inherent PostgreSQL complexity

Three facts of PostgreSQL create an irreducible floor of difficulty.

**2.1 MVCC snapshot timing is not when you think it is.** A `REPEATABLE READ` transaction's snapshot is established at the first statement after `BEGIN` (or at `BEGIN`, depending on the isolation-level implementation detail), not at the moment the caller *decides* to capture. Any wait that occurs inside the transaction — including waiting on an advisory lock — happens after the snapshot is already fixed. The writer can commit during the wait, and the reader will never see it.

This is not a bug. It is correct MVCC behavior. It is also why "add a lock at the start of `CaptureSnapshot`" is the wrong remediation and why Prompt 17 §5.2 insists on `Synchronization first → MVCC snapshot second`.

**2.2 Session and transaction advisory locks share one namespace but differ in lifetime.** Transaction-scoped locks release on commit/rollback. Session-scoped locks release on explicit release or connection close. Writers naturally use xact locks (short-lived, automatic cleanup). Readers *must* use session locks to hold a barrier across `BEGIN RR`, because there is no "pre-BEGIN transaction-scoped lock." The two mechanisms interoperate correctly only because they share a namespace, which is a subtle and load-bearing fact.

**2.3 Session-scoped locks require a dedicated physical connection.** `pgxpool` gives you a connection, not a connection identity. Acquiring a session lock through the pool and beginning the transaction on a different pooled connection silently breaks the protocol. The single-connection discipline in the plan (acquire from pool, hold, use for lock + transaction + release, discard on unlock failure) is the smallest correct implementation. It is also non-obvious and easy to get subtly wrong in cancellation, deadline, and panic paths.

These three facts are **inherent**. Any system attempting "read a consistent point-in-time view after waiting for writers" on PostgreSQL faces them. The correct answer — pre-BEGIN session barriers on a dedicated connection, writer-side xact locks on the same keys, discard-on-uncertainty — is well defined but delicate.

---

## 3. The accidental complexity

Most of the remaining difficulty is *not* inherent to the problem. It is produced by decisions the system has already made.

**3.1 The two-role freeze is the dominant source of friction.** The system has `app` and `migration_admin` and nothing in between. Every capability you grant to `app` — advisory lock functions, table INSERT/UPDATE, function EXECUTE — `app` can use arbitrarily, including in ways the protocol designer did not anticipate. The Gate 1 package's own finding (`DISCARD ALL` releasing session advisory locks outside a transaction) is exactly this: a protocol that depends on `app` *choosing not to release* a session lock is not a boundary. It is a convention.

The remediation strategy — revoke the raw capability from `app` and route through `SECURITY DEFINER` functions — is legitimate. But it does not change the fact that `app` is the caller. If `app` can invoke the DEFINER function inside a stale RR transaction, the function's `pg_locks` assertion only proves *presence*, not *ordering*. The package acknowledges this ("the mechanism uses `pg_locks` for nothing") and then reintroduces `pg_locks` in the same document for hold verification — which is honest but reveals that the DEFINER wrapper is carrying more weight than it can bear alone.

The XID-ticket mechanism is the right instinct: it proves ordering rather than presence. But it only proves ordering of *ticket issue*, and only if the barriers held at ticket issue are the same barriers held at ticket use. That is exactly what `DISCARD ALL` can break. A protocol whose correctness depends on "the caller did not release the session lock between step 4 and step 7" is again a convention, not a boundary.

**3.2 Writer participation is incomplete, and that is a historical problem.** Prompt 17 §6 lists seven writer classes (P1a–g) that do not currently participate in the barrier contract. Each is a separate gap created at a different time by a different feature. The snapshot protocol is only as strong as the weakest writer. This is not inherent; it is the natural residue of a system that grew writers incrementally without a unified barrier discipline. The plan correctly defers full writer closure to post-Gate-1, but this deferral means the Gate 1 "proven" mechanism is only proven *for the paths that currently take barriers*. The claim of closure is narrower than it sounds.

**3.3 DEFINER as a security mechanism without a hardening discipline.** The privilege request redefines three helpers as `SECURITY DEFINER` "byte-identical bodies." That is necessary but not sufficient. A DEFINER function without restricted `search_path`, schema-qualified references, narrow `EXECUTE` grants, and audited mutation is a privilege-escalation surface. Prompt 17 §7.1 spells this out; the Gate 1 package does not yet commit to it. This is accidental complexity: the protocol does not need DEFINER functions to be correct *in principle*; it needs them because of the two-role freeze, and the cost of using them correctly is the hardening checklist.

**3.4 The canonical key derivation is duplicated today.** The plan notes "00013 introduces one canonical key function to end the duplication." That duplication is itself a symptom: multiple services independently derive the same lock key, and only behavioral tests prove they agree. The correct fix (one SQL function, called from Go and from triggers) is cheap, but its absence is a source of latent divergence and exactly the kind of "reimplemented per service" pattern Prompt 17 warns against.

**3.5 Domain complexity masquerading as D1 complexity.** The compensation resolver (WIDEN, FREETAIL, MIDPERIOD), SHG semantics (verification standard, coverage, cross-fund), and rule-pack trust (source bytes, goldens, repinning) are genuinely hard. But they are hard for **domain reasons**, not for D1 lock reasons. They are bundled into M2.1 because they are all P0/P1 in the same review, not because they share the D1 mechanism. Conflating them under "admission" obscures that compensation lineage and SHG coverage have nothing to do with advisory locks, and that rule-pack verification is a supply-chain problem.

This bundling is itself accidental complexity: it makes the remediation look like one monolithic problem when it is actually four independent problems (concurrency, compensation domain, SHG domain, supply-chain trust) that happen to be scheduled together.

---

## 4. The DISCARD ALL finding, in context

The most useful thing about the `DISCARD ALL` finding is not that it is a bug. It is that it demonstrates, empirically, the difference between *implemented protocol* and *enforced boundary*.

The protocol says: "acquire barriers, issue ticket, begin RR, verify, work, release." The mechanism enforces ticket ordering and hold presence. It does **not** enforce that the barriers held at ticket issue are the same barriers held at ticket use, because `app` can release and reacquire them between those two points.

There are only three ways to close this:

1. **Prevent `app` from releasing.** Possible if the release path is only reachable through revoke-controlled wrappers and `DISCARD ALL` does not release advisory locks. The package has not tested this on PG16.15, and I expect it to fail.
2. **Bind the ticket to a connection identity that cannot survive release.** E.g., ticket records the backend PID and a lock-set fingerprint; verification recomputes the fingerprint and requires exact match. This is stronger but still relies on `app` not having a way to make the recomputation match a stale set.
3. **Introduce a third role or a non-`app` capture connection** that `app` cannot invoke `DISCARD ALL` on, and whose locks therefore cannot be released by the caller.

Options 1 and 2 are attempts to stay within the two-role freeze. Option 3 is the honest answer: **a protocol whose correctness depends on the caller voluntarily refraining from a standard PostgreSQL command is not a boundary.** If Prompt 17's acceptance conditions are to be met ("database must enforce the invariants under the ordinary application role"), the smallest privilege-boundary change is likely a dedicated capture capability, not another DEFINER wrapper.

This is not a database technicality. It is an architectural question about where trust lives.

---

## 5. Is the architecture wrong?

Not fundamentally, but it is **under-articulated at the trust boundary**. The frozen architecture says "one database, two roles, `migration_admin` for controlled operations." That is fine as a deployment posture. What it does not say is *where snapshot creation lives as a capability*. Today the answer is "in `app`, using a protocol that `app` is expected to follow." That is the actual architectural defect.

The remediation is trying to move the capability into DEFINER functions, which is the right direction. But moving a capability behind a function does not move the *caller* — it moves the *implementation*. If the caller can invoke the function in a state the function cannot detect and cannot refuse (stale RR), the capability has moved but the trust has not.

The XID-ticket mechanism is a real improvement because it makes ordering *observable* rather than *asserted*. But observability is not the same as enforcement. The `DISCARD ALL` gap is the evidence.

So: the architecture is not "wrong" in the sense that the frozen decisions are incoherent. It is **insufficient** for the invariant, and the insufficiency is concentrated at one point: the trust boundary between "the caller is authorized to capture" and "the caller is trusted to have followed the protocol."

---

## 6. Inherent vs. accidental: a taxonomy

| Layer | Inherent? | Notes |
|---|---|---|
| MVCC snapshot timing | Inherent | Correct PostgreSQL semantics; the remediation must respect it. |
| Session vs. xact lock lifetimes | Inherent | The mechanism must use session locks pre-BEGIN; there is no alternative. |
| Dedicated-connection discipline | Inherent | Pool management is a real cost; discard-on-uncertainty is the right discipline. |
| Writers must take the same barriers | Inherent | No reader-side protocol survives a non-participating writer. |
| Two-role freeze | **Accidental / architectural** | Chosen; creates the `app`-can-subvert-protocol problem. |
| Caller-controlled release (`DISCARD ALL`) | **Accidental / architectural** | Consequence of (5); not inherent to PostgreSQL. |
| DEFINER without hardening discipline | **Accidental / process** | Chosen cost of (5); Prompt 17 §7.1 already requires the fix. |
| Duplicated key derivation | **Accidental / historical** | Cheap to fix; already scheduled. |
| Incomplete writer participation (P1a–g) | **Accidental / historical accretion** | Not inherent; each gap is a legacy of uncoordinated feature growth. |
| Compensation lineage, SHG semantics, rule-pack trust | **Domain, not concurrency** | Genuinely hard, but orthogonal to D1; bundling is accidental. |

The pattern is clear. The **hard irreducible core** is small: MVCC timing, session locks, dedicated connections, writer participation. The **accidental mass** is larger: two-role-induced caller-control, DEFINER hardening, key duplication, incomplete writer coverage, and the bundling of four unrelated problem domains into one milestone.

---

## 7. What this means for Gate 1

The Gate 1 package is honest about the mechanism and honest about the residual gaps in most places. Its weakness is that it draws the boundary of "proven" at *implemented protocol* rather than *enforced invariant*, and that is exactly the line Prompt 17 §2.1 forbids.

The amendments I recommended are therefore not additional rigor for its own sake. They are the minimum needed to convert the mechanism from "a protocol `app` follows" into "a boundary `app` cannot subvert":

- the `DISCARD ALL` / release-reacquire attack, because it is the test that distinguishes the two;
- ticket/hold table privileges, because otherwise the ticket is a suggestion;
- DEFINER hardening, because the privilege request depends on it;
- claim corrections, because "the mechanism uses `pg_locks` for nothing" and "deadlock-free by construction" are not true as written and would be cited later as established facts;
- an explicit decision on the third role, because the honest answer to "can two roles enforce this" is probably no.

The Gate 1 package is a serious piece of work. It is not yet a gate.

---

## 8. Closing observation

The complexity in M2.1 is real, but it is not mostly database technicality. The genuinely hard parts of the milestone — compensation lineage, SHG coverage, rule-pack trust — have almost nothing to do with advisory locks. The lock machinery is small and well understood. The difficulty is that the milestone is trying to enforce a **security boundary** using **protocol discipline** inside a frozen two-role architecture, and protocol discipline is not a boundary.

That is the actual lesson of the Gate 1 review, and it is the same lesson as Prompt 17's opening:

> **Correctness is about ordering, not just locking.**

To which Gate 1 adds a corollary:

> **Enforcement is about capability, not just ordering.**

The XID-ticket is a good mechanism. It proves ordering. What it does not yet prove is that the caller cannot substitute a fresh lock set for a stale one between the two. That gap is small in code and large in meaning, and it is the difference between "we implemented the protocol correctly" and "the database enforces the invariant."


## prompt

what would you do differently from architecture and design perspective if you have the foresight to do it right at the outset


## answer

# Designing Payroll Snapshot Integrity From First Principles

## A retrospective on architecture, if we were starting over

---

## 1. What the problem actually is, stripped of the current design

A payroll snapshot must answer one question, provably:

> "What was the complete, authenticated set of inputs for employer E, period P, as of a consistent point in time T, that payroll calculation would have consumed?"

Every word in that sentence is load-bearing. *Complete* means no writer's committed change is omitted. *Authenticated* means only authorized code produced it. *Consistent point in time* means the set corresponds to a single observable state of the database. *Would have consumed* means the set is exactly what the calculator would read.

Three degrees of freedom in how you achieve it:

- **Data-model level**: shape the storage so a consistent read is trivially obtainable.
- **Isolation level**: shape the transaction semantics so the DB does the coordination.
- **Execution-model level**: shape who executes capture and under what authority.

The current design fixes (2) as `REPEATABLE READ` and (3) as "`app` executes, following a protocol". Almost all the difficulty lives in those two choices, not in the problem itself.

---

## 2. The two architectural decisions that cost the most

### 2.1 `REPEATABLE READ` + advisory locks

`REPEATABLE READ` gives a consistent snapshot — but the snapshot is established *before* any wait, so locks acquired inside the transaction cannot make the snapshot fresh. The only correct pattern is pre-BEGIN session locks on a dedicated connection. That works, but it has three structural consequences:

- **Every writer must participate** in the same barrier contract on the same keys. Writers that don't take the barrier are invisible to the protocol. That's why P1a–g exist as a list of seven gaps: each is a writer that historically didn't know it was required to participate. Snapshot correctness is therefore *as strong as the weakest writer*, and every new writer must remember.

- **The trusted role can subvert the protocol**. `app` is authorized to call advisory functions, run `DISCARD ALL`, and invoke DEFINER functions inside a stale RR. A protocol that depends on the caller not doing these things is not a boundary. Gate 1 discovered this the hard way.

- **Ordering must be proven, not asserted**. Because `pg_locks` shows granted state but not acquisition order or lock scope (session vs transaction rows are byte-identical on PG 16.15), the mechanism must use XID visibility — a clever but genuinely intricate construction.

### 2.2 `app` as the capture executor

If `app` runs capture, then `app` must be trusted with the *capability* to capture. That forces every remediation into one of three shapes:

- **Revoke and re-grant through a DEFINER wrapper** (current plan). Solves the direct-call case, but the wrapper is invoked by `app`, so `app` still chooses *when* and *from which transaction state*. That's the `DISCARD ALL` / stale-RR class.

- **Revoke everything and use a third role.** Cleanest, but breaks the frozen two-role model.

- **Trust the protocol.** Not a boundary at all.

None of these is satisfying because the design keeps trying to express a *capability* (`capture a payroll snapshot`) using *permissions* (`INSERT`, `EXECUTE`, advisory lock functions`) that are much broader than the capability. The finer-grained capability the design needs — "run this specific procedure against this database in a pre-barriered state" — has no direct expression in PostgreSQL's GRANT model.

That mismatch is the architectural defect.

---

## 3. Three alternative designs that avoid the class of problem entirely

### 3.1 Append-only inputs + watermark capture (event sourcing, lite)

If inputs are append-only and versioned, capture needs no coordination with writers. It needs only a consistent read.

```sql
-- Every mutation is an append.
CREATE TABLE payroll_input_event (
  event_id     BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  employer_id  UUID   NOT NULL,
  employee_id  UUID   NOT NULL,
  event_type   TEXT   NOT NULL,     -- compensation_set, terms_start, timesheet_approved, ...
  payload      JSONB  NOT NULL,
  effective_from DATE,
  effective_to   DATE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  created_by   UUID   NOT NULL
);

REVOKE UPDATE, DELETE ON payroll_input_event FROM app;

CREATE TABLE payroll_snapshot (
  snapshot_id     BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  employer_id     UUID  NOT NULL,
  period_start    DATE  NOT NULL,
  period_end      DATE  NOT NULL,
  event_watermark BIGINT NOT NULL,   -- max(event_id) included
  projection      JSONB  NOT NULL,   -- the resolved view at the watermark
  content_hash    TEXT   NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
```

Capture:

```sql
BEGIN ISOLATION LEVEL REPEATABLE READ;
  SELECT max(event_id) FROM payroll_input_event
   WHERE employer_id = $1 AND created_at <= clock_timestamp()
   INTO $watermark;

  -- Project the event log at the watermark.
  -- Because events are immutable, any consistent read gives the same result.
  -- RR guarantees the watermark and the projection see the same state.

  -- Compute and insert snapshot with content_hash derived from the projection.
COMMIT;
```

**Why this works:** writers append; readers read. There is no shared resource to coordinate because reading never conflicts with writing. The only synchronization needed is a single consistent read, which `REPEATABLE READ` provides for free.

**Why it fits payroll specifically:** payroll inputs are naturally event-like (hire, compensation change, timesheet approval, SHG instruction) and payroll *already requires* audit trails and immutability by law. The event log is not extra — it is the audit system the domain already demands.

**What it costs:** a redesigned schema. Mutable rows become projections over the event log. All writers change. That's a large migration — but the current design is also paying a large cost, just spread across advisory locks, writer participation in P1a–g, DEFINER wrappers, and the `DISCARD ALL` class.

### 3.2 `SERIALIZABLE` isolation alone

PostgreSQL's SSI (Serializable Snapshot Isolation) is designed exactly for the invariant "the read I made reflects a consistent whole." If *both* writers and capture run at `SERIALIZABLE`:

- Capture reads inputs. If any writer commits during capture that would make its read inconsistent, SSI aborts capture with `40001`.
- Writers that conflict with capture are similarly aborted.
- Both retry.

No advisory locks. No XID tickets. No DEFINER wrappers for ordering. No `DISCARD ALL` issue. No writer participation contract beyond "run your write at SERIALIZABLE." SSI tracks read/write dependencies at the transaction level, automatically, across triggers and functions.

For a payroll system with one employer and low write throughput, SSI conflicts are rare. The cost of retries is negligible. The benefit is *the invariant moves into the isolation level*, where PostgreSQL enforces it correctly and cheaply.

Why wasn't this chosen? The current design likely chose RR to avoid SSI false positives under load. That's a legitimate engineering tradeoff — but for payroll (low throughput, high correctness), SSI is exactly the right choice, and it eliminates the entire class of D1 defects.

### 3.3 Dedicated capture role

If capture must be executed by a specific DB user (`payroll_capturer`) whose credentials the `app` process does not hold, and whose connection `app` cannot control, then:

- `app` cannot invoke capture inside its own transaction.
- `app` cannot `DISCARD ALL` on the capture connection.
- `app` cannot release capture's session locks.

This is what Gate 1 is effectively rediscovering as "maybe we need a third role." It is not a new idea — it's the standard expression of "this capability is separate from the caller's authority." It costs one more role, one more credential, and a small process or connection boundary. It buys an honest boundary.

---

## 4. What the right design looks like

Combining the above, a coherent design from scratch:

**Data model.**
- All payroll inputs are append-only events with monotonic IDs. No mutable input rows.
- Snapshots are immutable rows referencing an event watermark and carrying a hash of the resolved projection.
- Corrections append new events that supersede prior ones; history is preserved by construction.

**Isolation.**
- Writers append. No cross-writer coordination needed.
- Capture reads at `REPEATABLE READ` (or even `READ COMMITTED` with a single-statement read of the projection). The append-only model makes the RR view *sufficient* — there is nothing to coordinate with.

**Authority.**
- `app` appends events and can *request* a snapshot.
- A dedicated capture role (or a verifying DEFINER function invoked by a dedicated connection) materializes the snapshot. It verifies the watermark and hash against the event log, which is immutable, so verification is trivial and cannot be subverted.
- `app` has no `INSERT` on `payroll_snapshot`. It has `INSERT` only on `payroll_input_event`, where it can only append.

**Trust boundary.**
- The boundary is not "did the caller follow the protocol." It is "can a snapshot exist only if it is derived from immutable data." That's a property of the data model, not of execution discipline.
- No `DISCARD ALL` class. No stale RR. No XID tickets. No `pg_locks` assertions.

**What remains genuinely hard** in this design:
- Compensation lineage resolution (WIDEN, FREETAIL) — domain complexity, unrelated to concurrency.
- SHG verification and coverage — domain semantics.
- Rule-pack source/golden trust — supply-chain, not concurrency.
- Drift detection — comparing current projection to snapshot projection; easy under append-only.

That's roughly the same hard domain problems as today, minus the entire D1 class.

---

## 5. The design principles, extracted

Three rules that would have prevented the M2.1 situation:

**5.1 Choose a data model where the invariant is structural, not procedural.**
If correctness requires every writer to do X, you have a protocol. If correctness follows from the schema, you have a boundary. The append-only + watermark design makes "the snapshot is consistent" a consequence of "rows are immutable," which is enforced by `REVOKE UPDATE, DELETE`. That is a boundary.

**5.2 Never express a narrow capability with broad permissions.**
"Run this specific procedure against this database in a pre-barriered state" is a narrow capability. `INSERT`, `EXECUTE`, and advisory lock functions are broad permissions. No combination of broad permissions expresses a narrow capability correctly when the trusted role is authorized. Either use a dedicated role, or shape the data so the capability is trivially verifiable (see 5.1).

**5.3 When the DB has an isolation level designed for your invariant, use it.**
`SERIALIZABLE` exists for "my read must be globally consistent." Payroll's throughput does not justify avoiding SSI. Choosing `REPEATABLE READ` plus advisory locks is choosing to reimplement SSI manually in application code, with worse guarantees and more surface area.

---

## 6. Is this fair to the current design?

Yes and no.

**Fair**: the current design is coherent, disciplined, and its authors clearly understood MVCC, advisory locks, and the session vs transaction distinction. The XID-ticket is a genuinely elegant construction. Nothing here suggests the original designers did not understand the problem.

**Also fair**: the choice of RR + advisory locks is defensible in a system that already uses advisory locks heavily and prefers explicit control. The choice of two roles is a deployment simplification that is fine until it isn't. The choice of `app`-executes-capture is the natural first attempt.

**What was not anticipated**: that a working capture mechanism would still fail the *boundary* test, because the mechanism implements ordering but not capability isolation. That is exactly the kind of gap that only surfaces when you write adversarial tests.

**What a careful upfront design would have done differently**: spend the first day asking not "how does capture work" but "what is the invariant, and what structure makes it true by construction." That question leads to append-only inputs (or SERIALIZABLE) long before it leads to advisory locks.

---

## 7. What to do now, given the current state

Not recommend a rewrite. The current system is closer to correct than the difficulty suggests. But there are two decisions worth making explicitly:

**7.1 For D1, either use SSI or use a dedicated capture role.** Both are smaller changes than continuing to iterate on the advisory protocol, and both close the class rather than the instance. SSI is the smallest change (change one isolation level, add retry-on-40001). A dedicated role is the cleanest expression of the boundary. Continuing with DEFINER wrappers plus `pg_locks` plus XID tickets is workable but will keep producing findings like `DISCARD ALL` — because the underlying design keeps trying to enforce a boundary through a protocol.

**7.2 For future critical invariants, add an architectural checklist.** Before choosing a mechanism:

- Is the invariant structural or procedural?
- If procedural, is the protocol enforced by capability or by discipline?
- If by discipline, does the trusted role have the means to subvert it?
- Is there a DB primitive (isolation level, constraint, trigger, append-only shape) that makes it structural?
- What would the adversarial test look like, and can the design pass it?

The adversarial test for D1 — "as `app`, begin a stale RR, invoke the privileged function, prove it fails" — would have rejected RR + advisory locks in favor of SSI or append-only in the first design conversation.

---

## 8. The short answer

**The complexity here is roughly 40% inherent and 60% accidental.**

The inherent 40%: MVCC timing, session-vs-xact lock lifetimes, dedicated-connection discipline, and the fact that every writer must participate if you chose a protocol-based design.

The accidental 60%: the choice of RR + advisory locks (instead of SSI), the choice of two roles (instead of a dedicated capture role), the choice of `app`-executes-capture (instead of app-requests-and-someone-else-executes), the choice to make input rows mutable (instead of append-only), and the choice to bundle four unrelated problem domains into one milestone.

If you started over with the invariant stated first and the DB's own primitives chosen second, you would land on one of: append-only + watermark, SERIALIZABLE, or a dedicated capture role. Each closes D1 as a *class*, not as a set of findings. The current design closes D1 as a set of findings, and Gate 1 is the moment that difference becomes visible.

The lesson worth carrying forward is not "use SSI" or "use a third role." It is:

> **Design invariants so the database enforces them structurally. If you cannot, make the enforcement a capability, not a protocol.**

That is the architectural foresight the M2.1 review is, in retrospect, rediscovering.


## prompt

as a developer who wants to simply code business rules and concentrate on the business domain only, this low-level database complexity is unacceptable, if I use Ruby on Rails framework or any comparable framework in Go, it is a non-issue, is that fair to say? do those frameworks the guardrails, integrity, provenance and the like aspects of what we are doing here? how would you vastly simplify this using a known framework in Go without the low-level complexity?


## answer

Your frustration is entirely fair, and your instinct is correct: a business developer should not have to hand-roll advisory-lock protocols and XID-visibility proofs just to record a payroll input. But the claim that a framework like Rails or a Go equivalent would make this a **non-issue** is only partially true. Frameworks give you substantial guardrails, but they do not eliminate the class of problem we have been fighting. They move it, and in some cases they hide it.

Here is the honest breakdown.

---

## 🛤️ What frameworks actually give you (and what they don't)

A framework like Rails, GORM, Ent, or Bun is excellent at **CRUD, associations, migrations, validation, and basic audit trails**. It will save you from writing boilerplate SQL and from many ordinary consistency bugs.

But the M2.1 problem is not an ordinary CRUD bug. It is a **concurrency + authority + provenance** problem. Frameworks typically do **not** solve:

| Concern | Framework support | Reality |
|---|---|---|
| **Snapshot isolation under concurrent writers** | Minimal. Most default to `READ COMMITTED` and do not coordinate multi-table reads. | You still need `REPEATABLE READ` + retry, or `SERIALIZABLE`, or a data model where reads do not conflict with writes. |
| **Enforcing immutability at the DB level** | Audit gems/plugins track changes, but rows are often still mutable. | You must `REVOKE UPDATE, DELETE` and use triggers or a dedicated append-only table. |
| **Provenance / tamper evidence** | Some gems emit events, but the default is “log changes to a table.” | True provenance requires append-only design and hash chaining; this is architectural, not a plugin. |
| **Cross-writer coordination for capture** | None. | If capture reads mutable rows, every writer must participate in a barrier contract, regardless of ORM. |
| **Preventing a trusted role from subverting the protocol** | None. | This is a privilege-boundary problem; ORMs do not model capabilities. |

So the framework removes the *boilerplate* of persistence, but it does not remove the *semantic* problem of “how do I guarantee a consistent, immutable, authenticated point-in-time view.”

---

## 🧠 Why the “framework makes it a non-issue” belief is a trap

The trap is thinking the complexity is in the **SQL syntax**. It is not. The complexity is in the **invariant**. PostgreSQL's documentation is explicit about this: even with `REPEATABLE READ`, MVCC snapshots have “read/write conflicts” that can make integrity checks incorrect without additional help. The docs say the clean answer is to use `SERIALIZABLE` for all writes and all reads that need a consistent view, and to let the framework retry `40001` automatically. That is the real simplification—**choosing the right isolation level**—not the ORM.

Frameworks do not automatically choose `SERIALIZABLE` for you. Rails, GORM, and Ent all default to `READ COMMITTED`. If you use them as-is, you will still have the stale-read problem. The framework will just make it easier to write the buggy code.

---

## 🧱 How to vastly simplify this with a Go framework (the right way)

The simplification comes from **three architectural choices**, not from the framework alone. Use the framework to implement them, but the decisions are yours.

### 🗃️ Use an append-only data model for payroll inputs

This is the single biggest simplification. If every payroll input is an **immutable event** (compensation changed, timesheet approved, SHG instruction added), then capture does not need to coordinate with writers at all. Writers append; readers read a stable set of rows.

Frameworks help here: Ent supports immutable fields (`field.Int64("id").Unique().Immutable()`) and database triggers managed as part of the schema. GORM has hooks, but triggers are more reliable because they fire even for direct SQL. You can define an audit-trigger in the same migration as the schema, so the immutability and audit trail are enforced at the database boundary, not in application code.

With append-only inputs, capture becomes:

```sql
-- inside a REPEATABLE READ transaction
SELECT max(event_id) FROM payroll_input_event WHERE employer_id = $1;
-- project the event log at that watermark
-- insert immutable snapshot
```

No advisory locks. No XID tickets. No writer participation matrix. The invariant is structural: rows are immutable, so any consistent read is correct.

### 🛡️ Let `SERIALIZABLE` do the coordination for you

If you keep mutable rows, the next biggest simplification is to set the default isolation to `SERIALIZABLE` and let the framework retry `40001`. The PostgreSQL docs say explicitly: “If the Serializable transaction isolation level is used for all writes and for all reads which need a consistent view of the data, no other effort is required to ensure consistency”. They also recommend that the application framework automatically retry serialization failures.

In Go, this is a middleware or a wrapper around your DB transaction: catch `SQLSTATE 40001`, retry with bounded backoff. GORM and Ent both allow you to control the isolation level per transaction. You do not need to write a single advisory lock. You do not need to prove ordering with XID visibility. SSI tracks read/write dependencies for you.

The tradeoff is potential false-positive aborts under high write contention. For payroll (one employer, low write throughput), this is a non-issue. For a high-throughput system, SSI may be expensive—but payroll is not that.

### ⚖️ Make the framework responsible for auditing, not for authority

Use the framework's audit capabilities for **history** (who changed what, when), but do not confuse that with **authority** (only a controlled operation may create a snapshot). Frameworks like Rails' PaperTrail or Go's Ent triggers are excellent at recording changes. But they do not prevent a direct `INSERT` into the snapshot table. You still need `REVOKE INSERT ON payroll_snapshot FROM app` and a controlled capture function or role. The framework can help you *write* that function, but it cannot *enforce* the privilege boundary for you.

---

## 🧭 A concrete Go stack that simplifies this

If I were building this from scratch in Go with minimal low-level complexity:

1. **Ent** for schema-as-code, type-safe queries, and migration management. Use `Immutable()` on event fields and define audit tables as first-class schemas.
2. **Atlas + Ent** to manage database triggers in the same migration unit. Attach an `audit_trigger` to every input table so that any mutation (including direct SQL) is recorded immutably at the database layer.
3. **`SERIALIZABLE` isolation by default**, with a retry wrapper around every transaction. Catch `40001` and retry up to N times. This replaces the entire advisory-lock protocol for ordinary capture.
4. **A dedicated `payroll_capture` role** (or a DEFINER function owned by a role `app` cannot impersonate) that materializes snapshots. `app` can request a snapshot; it cannot write one. This is the smallest privilege-boundary change that closes the `DISCARD ALL` / stale-RR class.
5. **Append-only event log for the critical inputs** (compensation, terms, SHG). This makes even `REPEATABLE READ` sufficient, because reads and writes no longer conflict.

With this stack, the business developer writes:

```go
err := db.Tx(func(tx *ent.Tx) error {
    // read inputs — SSI guarantees consistency
    // build snapshot in Go
    // insert snapshot via the controlled function
})
```

No advisory locks. No XID tickets. No `pg_locks`. No writer-to-barrier matrix. The framework handles the transaction, the retry, and the audit trigger. You handle the business rules.

---

## 💎 The bottom line

Your instinct is right: the low-level database complexity in M2.1 is excessive for a business application. But the fix is not “use Rails and it goes away.” The fix is:

1. **Choose a data model where the invariant is structural** (append-only inputs), **or**
2. **Choose an isolation level that enforces the invariant for you** (`SERIALIZABLE` + retry), **and**
3. **Use the framework to implement that choice cleanly**, not to substitute for the choice.

Frameworks give you the plumbing; they do not make the architectural decision. If you use Rails or GORM with default `READ COMMITTED` and mutable rows, you will rebuild the same problem, just in a more comfortable language. If you use the framework with `SERIALIZABLE` or an append-only schema, the complexity collapses to almost nothing—and that is the simplification you are looking for.

So: **fair to say frameworks help a lot? Yes. Fair to say they make it a non-issue? No.** The non-issue comes from the data model and isolation choice, and the framework is how you express that choice without fighting the database.
