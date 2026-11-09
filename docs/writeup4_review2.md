## deepseek

# Recommendation and Next Move

## Short answer

**Recommend Option B (READ COMMITTED + transaction-scoped employer gate) as the design to evaluate and adopt.** Add **SSI (Option 3)** as the one parallel candidate worth comparing, but only after the business answers a single question — that answer determines whether SSI is even viable.

**Do not proceed with Option A.** The `DISCARD ALL` class is real, and A's complexity is choice-specific, not domain-inherent. Evaluating B (and possibly SSI) first is the cheaper path.

The next move is a **2–3 day experiment gate**, not another week of hardening A.

---

## 1. Why B is the right default

B is the only option on the table that closes the class A is trying to close **without** carrying A's machinery:

| Property | A (XID-ticket + session barriers) | B (RC + employer gate) |
|---|---|---|
| Snapshot ordering proof | XID visibility + ticket lifecycle | Transaction-scoped lock acquisition order |
| Survives `DISCARD ALL` | Unproven; the review correctly flags this | Yes — `DISCARD ALL` cannot run inside a transaction, and `pg_advisory_xact_lock` cannot be released mid-transaction |
| Survives `pg_advisory_unlock_all()` | No (session locks) | Yes (that function only releases session locks) |
| Dedicated connection pinning | Required | Not required |
| Cleanup on cancellation/deadline/panic | Must discard connection on uncertainty | Automatic (transaction rollback releases the lock) |
| 21-overload revoke | Required (to prevent session-lock subversion) | Nice-to-have hygiene, not load-bearing |
| SECURITY DEFINER helpers | Required for ordering | Not required for ordering |
| Writer participation contract | "Take session lock pre-BEGIN on the same connection" | "Take xact lock as first statement in your transaction" |
| Failure mode for non-participating writer | Silent stale snapshot (undetectable) | Mid-capture inconsistency (detectable via re-read) |

The critical rows are the `DISCARD ALL` / `unlock_all` ones. The review's Section 3 finding is correct: **the ticket proves ordering, not continuous hold.** A mechanism whose correctness depends on the caller not issuing a standard PostgreSQL utility command is a protocol, not a boundary. B is the first option in the list whose correctness does **not** depend on the caller refraining from anything — because transaction-scoped locks are released only by commit/rollback, and no utility command, function, or caller action can release them mid-transaction.

That is the decisive difference. B closes the class; A does not yet, and may not without a third role.

B also has a second, smaller advantage the review does not emphasize enough: **its failure mode is observable.** If a non-participating writer commits mid-capture under B, capture's later reads see a change that earlier reads did not. Capture can detect this cheaply (re-read a population count, `max(xmin_display)`, or a per-employer version counter) and either retry or fail. Under A, a non-participating writer's committed change is simply invisible — no read tells you it happened. Silent loss is strictly worse than detectable inconsistency.

---

## 2. Why not A

A's specific problem is not that it is wrong. It is that it is **doing real work with the wrong primitive.** Three things would need to hold for A to be a boundary:

1. No caller can release session locks between issue and use. (`DISCARD ALL` breaks this, as the review documents.)
2. No caller can reacquire locks after release in a way that passes verification. (The Gate 1 package does not show a hold-set fingerprint that would survive release-reacquire; the review correctly asks for this.)
3. Every writer participates in the session-lock contract. (P1a–g are still open.)

If (1) is broken, (2) and (3) are moot. Even if (1) turns out to be testable-and-closable, A still requires the dedicated-connection discipline, the XID-visibility logic, the ticket lifecycle, the discard-on-uncertainty protocol, and the 21-overload revoke. Those are all costs paid specifically because the design chose pre-BEGIN session locking. B does not pay them.

**There is no invariant A protects that B does not.** The review says this directly: "This avoids the need for pre-transaction session locks, ticket visibility, dedicated connection pinning and the associated cleanup protocol. It also avoids the `DISCARD ALL` window."

That is the whole argument. B is strictly cheaper for the same guarantee under the same threat model.

---

## 3. The one question that gates B vs SSI

SSI (Option 3) is the only candidate simpler than B. But it is only viable if the business accepts one specific semantic that is currently undefined.

The review states the issue correctly (Section 4, Option 3):

> Serializability is not the same as strict wall-clock freshness. An overlapping capture and writer may be assigned a valid serial order in which capture comes first, even though the writer commits before capture finishes.

**The question is: what is the snapshot's capture boundary?**

Two possible answers, and they lead to different mechanisms:

**Semantic 1 — "Serialization point".** The snapshot represents some point in a serial history. Any transaction that commits concurrently with capture is, by definition of the snapshot, either entirely inside or entirely outside — SSI decides which. If the business accepts that a writer that commits "during" capture may fall outside the snapshot (and will be picked up by the next snapshot, or by drift detection), **SSI alone is sufficient**. No gate, no advisory locks, no writer participation contract. Just `SERIALIZABLE` everywhere and a `40001` retry wrapper.

**Semantic 2 — "Wall-clock inclusion".** Every change that commits before the validation decision must be in the snapshot. Then SSI is not enough — a writer that commits at wall-clock T (during capture, before validation) may still be serialized outside the snapshot. **B is required**, because the gate serializes writers against capture by wall-clock ordering.

**Semantic 1 is almost certainly correct for payroll.** Payroll systems already define snapshots as of a period boundary or a validation time, not as of "everything committed before the validation click, no matter what was happening concurrently." Drift detection exists precisely because the boundary has to be explicit. If the business agrees that "snapshot = serialization point" is the semantic, **SSI wins on simplicity by a large margin** — the entire gate machinery disappears, and the only complexity left is a `40001` retry wrapper, which is a framework-provided pattern.

If the business insists on Semantic 2, **B is the answer.**

Either way, **A is not the answer.** The choice is between B and SSI, and it is made by one business decision, not by more mechanism proofs.

---

## 4. The recommended next move

Sequence it in four steps. Do not skip step 1.

### Step 1 — Pin the capture semantic (1 hour of business decision)

Document one sentence: **"A payroll snapshot represents the database state as of its serialization point; changes committed concurrently with capture are governed by drift detection, not by inclusion."**

If accepted → SSI is viable. If rejected → B is required. Either way, the answer is written down and gated.

This is the single most valuable hour in the entire milestone. Every mechanism decision downstream depends on it.

### Step 2 — Run a focused experiment (2–3 days)

Do **not** implement both A and B. Do **not** implement the full design. Run one narrow experiment against one writer class and one capture path.

**If Semantic 1 is accepted (SSI viable):**

Test SSI against three writer classes — timesheet approval, compensation insert, and SHG instruction — with:
- Writers at `SERIALIZABLE`.
- Capture at `SERIALIZABLE` with a `40001` retry wrapper.
- Both `writer-first` and `capture-first` interleavings.
- Direct SQL as `app` (writers that bypass the app layer still run at SERIALIZABLE if the framework sets it, but direct SQL at `READ COMMITTED` breaks the contract — record this as an explicit limitation and verify whether `app` at RC causes SSI to miss a conflict).
- Persisted-state inspection after success and after abort.
- A `pg_stat_database.xact_rollback` counter check to prove retries actually happened, not just "succeeded first try."

If that experiment shows SSI catches the writer set and capture completes with bounded retries, **SSI is the answer** and the entire D1 machinery is retired.

**If Semantic 2 is required (SSI insufficient):**

Test B against the same writer set:
- A single `pg_advisory_xact_lock(employer_gate_key($1))` as the first statement in every participating transaction (writers and capture alike).
- Writer participation enforced by a `BEFORE INSERT/UPDATE` trigger on the input tables, so direct SQL as `app` still takes the gate.
- Capture: `BEGIN; SELECT pg_advisory_xact_lock(key); -- now stable -- read inputs; -- write snapshot; COMMIT;`
- Test `writer-first`, `capture-first`, `capture-during-writer`, `writer-during-capture`, `DISCARD ALL` (should be a no-op for transaction-scoped locks — verify it empirically), cancellation, rollback, and connection loss (transaction-scoped locks auto-release; verify this empirically).
- Include the re-read verification (capture reads population count at start, reads again at end, aborts on mismatch) to catch non-participating writers. This is the cheap safety net B gains from its observable failure mode.

Either experiment is **2–3 days of focused work** because it eliminates the connection-pinning, ticket-lifecycle, session-lock cleanup, and 21-overload-revoke problems. It tests one mechanism against three writers, not a framework against everything.

### Step 3 — Gate the choice

The gate output is:
- One documented semantic (from Step 1).
- One chosen mechanism (SSI or B, from Step 2).
- One writer-participation contract (implicit in SSI, trigger-enforced in B).
- One set of adversarial tests that both the mechanism and the contract must pass.

Do not gate on "the tests are green." Gate on "the failure class is closed by construction, and the test proves it under the real `app` role."

### Step 4 — Then, and only then, write 00013

00013's scope becomes dramatically smaller under either SSI or B:
- SSI: a per-transaction isolation-level setting and a retry wrapper. No advisory revoke, no DEFINER helpers, no session barriers, no ticket tables.
- B: one `employer_gate_key()` function, one set of `BEFORE` triggers, capture as a single RC transaction. The 21-overload revoke becomes a hygiene nicety (still worth doing eventually) rather than a load-bearing boundary.

Either outcome is a 00013 that is **one-third the size** of the current plan's, with **one-third the on-call surface**, and it closes the `DISCARD ALL` class by construction rather than by further proof.

---

## 5. What I would not do

**Do not run the DISCARD ALL test against A first.** The review's Section 7 says "add the exact `DISCARD ALL` attack." I would add it only if Step 1 or Step 2 is inconclusive. If Step 1 pins the semantic and Step 2 shows SSI or B closes the class, then proving that A also closes the class (or that A doesn't) is wasted work.

**Do not go to Option 4 (third role).** The review treats it as an option. It breaks the frozen two-role model, adds operational credentials, and is unnecessary if either B or SSI works under two roles. Keep it in reserve only if Step 2 shows that neither B nor SSI works under the current threat model — which is unlikely.

**Do not use Option 2 (append-only) as the primary D1 fix.** The review's `MAX(event_id)` objection is correct: sequence values are non-transactional, so the watermark does not establish commit order. Append-only is still a good **data-model** improvement (it simplifies effective-date resolution, corrections, and audit), and it composes cleanly with either B or SSI. But it does not by itself solve the concurrency or authority problem, and it should be evaluated as an independent data-model upgrade, not as the D1 mechanism.

**Do not implement A and B in parallel.** The review is right: "Choose one synchronization authority." Step 2 picks one, Step 3 gates it.

**Do not accept "the tests are green" as gate evidence.** The Gate 1 package already has 31 green mechanism proofs and 12 red attack tests. Both the green and the red are consistent with the mechanism being correct **and** the boundary being incomplete. The gate criterion is not test color; it is **failure-class closure under the real `app` role**. That is what Step 2 must produce.

---

## 6. The bottom line

The review's Section 7 is close to correct. Its recommended next move — compare A and B — is the right shape but the wrong emphasis. **A should not be evaluated as a live candidate; it should be retired.** The `DISCARD ALL` finding did not merely uncover a gap in A; it clarified that A's boundary depends on a utility-command-free caller, which is a protocol, not a boundary.

**The recommendation is B, contingent on the business accepting Semantic 2. If Semantic 1 is acceptable, SSI is strictly simpler and should be preferred.** Either way, the next move is not "harden A." It is one hour of business decision, two to three days of focused experiment, one gate, and then a much smaller 00013.

The consolidated review's strongest sentence is the one it does not quite draw the conclusion from:

> The business domain deserves a simpler persistence boundary than the one currently emerging.

That is exactly right. The way to give it one is not to keep proving A — it is to recognize that A was always going to be expensive, and to choose the simpler boundary before paying for it.


## claude

I recommend Option 1, the READ COMMITTED employer gate, as the synchronization design. I'd treat Option 2 (append-only inputs) as the direction for the data model, not something to build in 2.1, and I wouldn't pursue Options 3 or 4 yet. The next move is a time-boxed spike of the gate, not more work on the XID-ticket design. I haven't seen the code, so this rests on the review and the Gate 1 package.

## Why Option 1

- **It removes the failure class rather than hardening it.** The `DISCARD ALL` finding is real: PostgreSQL documents that it releases session advisory locks. Every new attack on the session-lock design adds more machinery to close it. A transaction-scoped lock can't be released by `DISCARD ALL`, because that command can't run inside a transaction block.
- **It keeps your threat model.** My last answer framed the simplification as relaxing it. The review is right that it doesn't have to: if every writer reaches the input tables only through database-enforced gating (revoked DML plus triggers or DEFINER functions), a hostile `app` role can't bypass the gate. That writer matrix is needed under the ticket design too, so Option 1 only subtracts work.
- **What disappears:** session locks and connection pinning, the pre-BEGIN protocol, the ticket and hold tables and the XID logic. The 21-overload revoke and the helper redefinitions are no longer needed for integrity, only possibly for denial-of-service hardening.
- **What stays the same under any design:** the ADR-1 proof-check completeness. It is the real authority boundary, and the capture function must also reject any caller not running at READ COMMITTED.

## The other options

- **Option 2 (append-only):** I agree with the review, including that a `MAX(event_id)` watermark is wrong because sequences aren't commit-ordered. It's the right long-term shape, but a bigger change than 2.1 should carry.
- **Option 3 (SERIALIZABLE):** Beyond the freshness-semantics problem the review raises, the database can't force an isolation level on a hostile caller, who can simply set their own. I'd drop it.
- **Option 4 (dedicated capture credential):** It changes the frozen two-role model. A DEFINER capture function already acts as the capability boundary, so I'd keep this only as a fallback if the spike fails the hostile-caller tests.

## A refinement on the `DISCARD ALL` attack

The review's step order (release, reacquire, then BEGIN RR) probably doesn't yield a stale snapshot, because the snapshot would come after the writer's commit. The dangerous variant reacquires the barriers inside the RR transaction, after the snapshot is taken but before verification. The ticket would still be visible and the holds would be present. Whether it's closed depends on whether the wrapper refuses acquisition inside an open transaction or after ticket issuance, which I can't see from the package.

## Recommended next move

1. **Write one red test** for the reacquire-inside-RR variant against the current design. It's cheap and gives the decision record concrete evidence. Don't fix it yet.
2. **Run a small spike of the gate** on PG 16.15 with explicit pass/fail criteria:
   - writer-first and capture-first interleavings, with `pg_locks` showing real waits
   - direct SQL as `app` against every input table is blocked or gated
   - the capture function rejects a caller in REPEATABLE READ, and a caller with no gate held
   - the omitted-employee, omitted-segment and fabricated-value proof-check tests pass
   - rollback and cancel release the gate automatically
3. **Compare two gate forms** in the spike: a transaction-scoped advisory lock and a row lock on the employer row. As I understand PostgreSQL's FK locking, the row form gives a free barrier for inserts and keeps the privilege boundary to ordinary table grants, but verify that with a test.
4. **Write a short decision record** and only then approve the scope of 00013. Keep the compensation and rule-pack simplifications out of the spike and decide them as a separate step.

Do you want me to draft the spike's test matrix against your existing writer list?


## Z

Same question, same file — so I'll give you the complete answer, tightened. (If you saw my earlier reply, this is the same verdict; nothing in the attachment has changed.)

## Recommendation: **Option 1 — READ COMMITTED with a transaction-scoped employer gate, writer participation enforced by database triggers.** Run the `DISCARD ALL` attack against the current design first, then decide once.

### Why Option 1

**1. It closes the `DISCARD ALL` class structurally — it can't be patched.**
That finding is the pivot of the whole review. `DISCARD ALL` releases session advisory locks, cannot run inside a transaction block, and — crucially — **cannot be revoked**, because it's a utility command, not a function. So under Boundary A, "barriers remained continuously held between ticket and snapshot" is a property PostgreSQL can never prove from the inside; you'd be defending it forever with hold-lifecycle logic and hoping you've enumerated every session-state escape hatch. Under Option 1 there are **no session locks to leak**: the gate lives and dies with the transaction, and `DISCARD ALL` can't run mid-transaction. The attack window doesn't shrink — it stops existing.

**2. Attack surface scales with the number of coordinated locks. Option 1 has one.**
Boundary A's 21-overload revoke, tickets, and DEFINER wrappers exist to defend a *multi-lock protocol* against substitution. A single transaction-scoped gate has almost nothing to substitute: a hostile direct-SQL caller who takes the gate is simply *another participant* (mutual exclusion is symmetric). Worst case is a bounded delay — `lock_timeout` → typed error — not a correctness break. You trade a fortress around ten locks for one lock that needs no fortress.

**3. Trigger-enforced participation still satisfies "the database must enforce it under `app`."**
This is what makes Option 1 more than the "trusted Go helper" version Z proposed: `BEFORE INSERT/UPDATE/DELETE` triggers on the ~10 payroll-input tables that take the gate are **mechanically bypass-proof** — direct SQL as `app` fires them like everything else. One thing to verify: `DISABLE TRIGGER` requires table *ownership*, so confirm the input tables are owned by `migration_admin` and `app` holds only DML grants. Deadlock-freedom is trivial: one key, reentrant, acquired once per transaction — no ordering to violate.

**4. The proof burden inverts from "continuity over time" to "statement order."**
Boundary A's hardest open problem is proving a *temporal* property with a database that records no timestamps. Option 1's proof is two sentences: the gate is the first statement of the capture transaction in one shared function; under READ COMMITTED every subsequent read sees everything committed before it, and no writer can commit while the gate is held. Your invariant battery (writer-first × classes, capture-first, correction-during-capture, direct-SQL-as-app, rollback/cancellation, persisted-state checks) transfers almost unchanged — **the red tests encode the invariants, not the mechanism.**

**5. Your sunk cost is low *because of your own Gate-1 discipline*.**
The admission package was deliberately left unwired into capture pending approval. That decision makes switching cheap now: nothing in production depends on the tickets. The 31 proofs bought real understanding (the ticket's "turn ordering into visibility" insight is the same idea as the gate, at a higher threat-grade) and left a documented, tested fallback.

**6. It fits the workload.** Monthly capture, one employer, loopback API — a coarse per-(employer, period) gate costs nothing operationally, and RR's lost defense-in-depth is cheaply recovered with a fingerprint re-read under the gate before commit.

### The other options

| Option | Verdict |
|---|---|
| **2 — Append-only inputs** | Right long-term data model (your supersession/SHG design already leans there), but it's a v0.2 refactor touching every write path, doesn't solve authority by itself, and the sequence-watermark flaw shows it needs its own correctness work. Not an M2.1 move. |
| **3 — SERIALIZABLE** | Last. SSI guarantees *a* serial order, not wall-clock freshness — you'd first have to redefine the capture boundary as "the serialization point" (a business-semantics change), then absorb abort-storm retries. More subtlety, not less. |
| **4 — Dedicated capture role** | Cleanest *authority* end-state, composes with Option 1 later, but breaks the frozen two-role architecture. Defer; name it in the ADR as the escalation path. |

**Scheduling insight:** the locking decision and the authority work are **independent problems** (the review's own "freshness vs. authority" split). EF-E3/EF-F1 — revoke `app` writes on snapshot/audit tables, DEFINER capture that *derives* values, controlled validation — is P0 under *every* option, including the current one. Start it now in parallel; don't let it wait behind the synchronization debate.

## Recommended next move — Gate 2, ~3–4 days, then one decision

1. **Run the exact `DISCARD ALL` attack against the current mechanism (half a day).** Barriers → ticket → `DISCARD ALL` → reacquire → BEGIN RR → verify. If it fails *closed* (hold-lifecycle refuses reacquisition), pin it as a test — Boundary A becomes a proven alternative and the choice is real. If it fails open, Boundary A needs more machinery and the decision makes itself. Either way you've done due diligence on the sunk investment instead of abandoning it on an untested objection.
2. **Build the RC-gate experiment (1–2 days).** One gate key per (employer, period), ~10 one-line triggers, one shared capture function (gate-first → read → canonicalize → fingerprint recheck → persist → audit), `lock_timeout`-bounded waits. Run the transferred battery, including direct SQL as `app` (prove the trigger blocks it) and both orderings.
3. **Decide against criteria pinned *before* running:** enforcement points to maintain, new DB objects, attack-surface size, proof complexity, operational behavior. My prediction: the gate wins decisively — but if A survives `DISCARD ALL` cleanly, at least the choice is made on evidence rather than on objection.
4. **Write the ADR and amend the spec explicitly** (per your own §4/§17 rules): prompt17 §5.2's RR protocol superseded; §15 conditions 1–4 restated mechanism-neutrally ("capture is atomic with respect to concurrent writers: a writer committing before capture completes is always included, or capture fails closed"); admission package retained as documented, unwired fallback.
5. **Then implement `00013` once** — chosen synchronization design + the unchanged authority work in a single forward migration, `00001`–`00012` untouched — and proceed with plan steps 7–9 (comp, SHG, packs), which are design-independent.

One-line summary: **the current design is unproven at exactly the point where it is most complex; the simpler design is unproven at exactly the point where it is easiest to prove. Spend three days making the simple one proven, and take the fortress off the roadmap.**

Happy to draft the Gate-2 experiment plan (schema, trigger list, test matrix, pinned decision criteria) or the ADR/spec-amendment memo as ready-to-use documents — say which.