## prompt

evaluate plan_prompt17.md against prompt17.md

## answer

I've read both files in full (`prompt17.md` — 1474 lines, `plan_prompt17.md` — 52 lines). Here is the evaluation.

# Evaluation: `plan_prompt17.md` vs `prompt17.md`

## Verdict

**Strong alignment — approve with minor revisions.** The plan traces every finding ID from §1, follows the binding §16 order exactly, makes technically sound decisions on the points prompt17 leaves open, and its success criteria map to the 20 acceptance conditions. The gaps below are mostly *explicitness* gaps (details delegated to prompt17's own text) plus one substantive technical watch-point that should be surfaced before execution.

## Coverage matrix

| prompt17 requirement | Plan coverage | Status |
|---|---|---|
| §2 ten-point invariant-first analysis | Approach ¶1, red-first per phase | ✅ |
| §2.1 permanent requirements (both orderings, persisted state, atomicity, positive controls) | Validation Plan + §12 batteries (step 10) | ✅ |
| §3 baseline/preservation | Step 1 (adds 00012 hashing — sound) | ✅ |
| §4 frozen architecture | Only implicit via Goal | ⚠️ not restated (low risk) |
| §5.1 red reproduction (4 classes + T2) | Step 2, plus 4 extra classes | ✅ |
| §5.2 protocol + 9 PostgreSQL details | D1 protocol decision | ✅ |
| §5.3 stable enumeration, §5.4 both ops, §5.5 40001 | Step 3 | ✅ |
| §6 P1a–g + writer matrix + canonical lock hierarchy | Step 4 + D1 decision | ✅ (P1d/P1g preferences compressed — see below) |
| §7.1 capture governance + SD hygiene | Step 5 | ⚠️ SD hardening checklist not restated |
| §7.1 pre-BEGIN proof | Pre-BEGIN decision | ⚠️ residual attack must be explicit — see finding 1 |
| §7.2 child immutability, §7.4 direct-SQL tests | Steps 5–6 + §12B | ✅ |
| §7.3/§11 validation governance | Step 6 | ✅ (state-machine restriction implicit) |
| §8.1 WIDEN / §8.2 FREETAIL / §8.3 MIDPERIOD | Step 7 | ✅ |
| §8.4 F-A2 provenance | Decided *now*, not deferred | ✅ safe-side deviation |
| §9.1 B1 / §9.2 B2 / §9.3 X-FUND / §9.4 negatives | Step 8 (9 coverage scenarios, all 9 negative classes) | ✅ |
| §10.1–10.6 packs + OT-001 | Step 9 (3-level OT-001 blocking) | ✅ |
| §12 A–G batteries | Steps 2, 10, Validation Plan | ✅ |
| §13 deferred, §14 report, §15 acceptance, §16 order, §17 stop condition | Approach / Steps 10–11 / Success criteria | ✅ |

## What the plan gets right

- **D1 design is technically correct**: session-level advisory locks acquired pre-BEGIN on one dedicated `pgxpool.Acquire` connection; writers using transaction-scoped locks on identical keys genuinely conflict with session locks in the same advisory namespace, so cross-boundary mutual exclusion holds. Same-connection discipline, reverse-order release, discard-on-unlock-failure, and bounded retry with pre/post-BEGIN population match all map to §5.2's nine details.
- **The §16 order is followed literally** (sync → capture/validate → compensation → SHG → packs → verification), and each phase follows the reproduce-red → fix-at-boundary → prove-both-orderings pattern.
- **Honest limitation handling**: "any residual unenforceable under the two-role model gets an explicit documented limitation, never a silent claim" matches §7.1's stop-condition.
- **C1-GOLDENS decision keeps the lifecycle honest** (real packs without executable gates cannot reach verified status) — exactly §10.2's intent.
- Skipped-DB-tests policy, `pg_locks` proofs, no-partial-state checks, and `git diff --check` are all carried through.

## Findings to fix before execution (priority order)

1. **Pre-BEGIN residual attack must be explicit (the one substantive gap).** The `pg_locks` assertion inside the DEFINER function proves the locks are *held by the same backend*, but cannot distinguish locks acquired *before* BEGIN from locks acquired *after* BEGIN in that session — `app` can acquire session-level advisory locks itself, so a caller can begin a stale RR transaction, take the locks, then invoke the capture function and pass the assertion. This is precisely the attack §7.1 says must be prevented *or* documented with the exact limitation. The plan should (a) add this exact attack to the fabrication battery, and (b) pre-draft the documented-limitation / smallest-privilege-change text rather than discovering it mid-Phase-5. The current wording ("ordering itself is guaranteed by the single Go protocol") leans on application-side enforcement — acceptable since §5.2 itself mandates a Go protocol, but the residual must be named.

2. **SECURITY DEFINER hardening checklist (§7.1) is not restated**: restricted `search_path`, schema-qualified references, correct ownership, narrow EXECUTE, no mutable authorization setting, atomic audit. Add to steps 5–6 so the implementer doesn't rely on re-reading prompt17.

3. **P1d preference inverted-by-omission.** prompt17 states the *preferred* design is routing through the existing controlled status-transition function (trigger as alternative); the plan says only "revoke-or-trigger". Name the controlled-function route as primary.

4. **P1g versioning preference unaddressed.** prompt17 prefers immutable versioning / controlled parent succession "where history matters"; the plan commits to revoke-or-route + drift header compare without the evaluate-and-justify step. Add one line.

5. **Fidelity nits to reconcile:**
   - "**13 value classes**" vs §7.1's list of 12 — align the count or enumerate the 13th.
   - "**× 3 TZs**" vs §12G's two named zones (UTC, Asia/Singapore) — name the third (presumably workspace precedent).
   - "**the four report-claim corrections**" — prompt17 gives no count; verify against the review in Phase 0.
   - §8.3's "preserve both historical compensation records" and §2.1's "historical evidence remains unchanged" deserve explicit mention in steps 7 and the battery.

6. **"Open questions: None" is slightly overconfident.** Three inspect-then-decide points exist (P1a population semantics, verified-active status shape, executor-vs-gate-artifact fallback) plus the Phase-5 residual from finding 1. All have decision rules pre-authorized by prompt17, so "None" is defensible — but phrase it as "none *unpinned*; three deferred decisions with pre-authorized conservative defaults."

## Scope additions (all defensible, flagged for awareness)

- 4 extra reproduction classes beyond §5.1's four (term change, termination, attendance correction, SHG correction) — consistent with §12A's nine-race list.
- Verifying `00012` Down validity + `00013` guarded-Down round trip — not required by prompt17, sensible.
- Building the fixture-gate executor — permitted via §10.2's preferred path; keep it "minimal deterministic" to honor §17's no-generic-frameworks rule.
- D5-P2a/P2b folding — 40001 mapping is mandated by §5.5 anyway; P2b is capture-integrity-adjacent, so it doesn't violate §16's "no P2/P3 detours" rule.

**Recommendation:** apply findings 1–4 as small edits to the plan (they're one-liners each), then execute. If you want, I can produce the revised `plan_prompt17.md` with these edits incorporated.



## prompt

write a technical writeup, what exactly are the challenges here, why is it so complex, is it about database technicality or is the architecture wrong, is this inherently complex or we just stumbled with accidental complexity


## answer

# Why M2.1 Is This Hard — A Complexity Autopsy

## 0. Straight answers first

**What exactly are the challenges?** Three interacting problems, not one:

1. **The isolation trap (D1)** — PostgreSQL establishes a REPEATABLE READ snapshot at the *first statement*, not at `BEGIN`, and advisory locks never refresh snapshots. "Lock, then read" silently reads stale data.
2. **The authority problem (EF-E3/EF-F1)** — a content hash is an integrity claim, and an integrity claim is only as strong as the exclusivity of the role that writes the hashed rows.
3. **The contract-breadth problem (P1a–g)** — a snapshot invariant is only as strong as the *least-cooperative writer*. Every path that mutates snapshotted inputs must join one protocol.

On top of all three sits the acceptance standard: none of this may be *asserted* — it must be *proven* against live PostgreSQL while authenticated as the untrusted role.

**Why is it so complex?** Because the three problems interact. The isolation fix (session barriers acquired pre-BEGIN) creates a lock-substitution surface (`app` can call `pg_advisory_*` directly), which forces the revoke/wrapper privilege boundary, which breaks frozen SECURITY INVOKER helpers and trigger-transitive paths, which must then be re-proven. Each fix widens the surface the other fixes must cover. **Trust work added late to a working system is quadratic in surface area** — that, more than any single hard problem, is why M2.1 feels heavy.

**Database technicality, or wrong architecture?** The hard core is genuine, unforgiving database technicality — subtle, documented PostgreSQL semantics that no flag removes. But the *severity* is architectural: the system was built feature-first on mutable, app-writable inputs and acquired its trust requirements late. The architecture is not *wrong*; it is *under-instrumented for trust*, and M2.1 is the retrofit invoice.

**Inherent or accidental?** A mix, and it can be priced: roughly **half inherent**, **a quarter chosen-constraint**, **a quarter accidental retrofit debt**. The accidental part is one-time and is being converted into permanent mechanism — that conversion is precisely what Gate 1 is.

---

## 1. What the system is actually being asked to guarantee

Strip away the finding IDs and M2.1 reduces to a four-clause trust specification over one artifact — the payroll snapshot:

1. **Complete** — every input committed before capture synchronized is in the snapshot.
2. **Authentic** — no supported role can fabricate, append, or alter captured content.
3. **Reproducible** — the canonical hash identifies exactly those bytes, derivable from source records.
4. **Governed** — every mutation path participates in one protocol, one key derivation, one acquisition order.

This is a ledger-grade specification. Ordinary CRUD asks *"does it work?"* A payroll trust boundary asks *"can you prove nobody can make it silently lie?"* A stale snapshot in a dashboard is a UI annoyance; a stale snapshot in payroll is paying the wrong person, with an audit trail that says you didn't.

---

## 2. The four real challenges

### 2.1 The MVCC trap (D1-P0)

The defect needs no "bug" in the everyday sense — every line of the failing code looks correct:

```
Writer A:  BEGIN; acquire barrier; insert compensation; ... (uncommitted)
Capture B: BEGIN REPEATABLE READ        ← snapshot NOT yet taken
Capture B: try same barrier             → blocks on A
Writer A:  COMMIT
Capture B: acquires barrier
Capture B: first SELECT                 ← snapshot established NOW
           → A's committed write is invisible → stale, self-consistent snapshot
```

The trap: *"we hold the lock, therefore we are serialized"* is true for **execution** and false for **visibility**. A lock wait never updates your snapshot. This is documented PostgreSQL behavior, but obscure enough to survive code review, type systems, and conventional tests — nothing in a normal suite interleaves two sessions adversarially at statement granularity.

The fix is a *protocol*, not a patch: session-level barriers, same physical connection, canonical order (deadlock-freedom by construction), every writer holding conflicting locks on identical keys, one shared implementation, centralized key derivation — retro-applied across the whole writer matrix.

### 2.2 The observability wall — and the XID ticket

prompt17's first-cut proof mechanism — *"assert via `pg_locks` that the barriers are granted"* — turned out to be **unprovable**. Gate 1 demonstrated this live on PG 16.15: session-level and transaction-level advisory locks are **byte-identical in `pg_locks`**, and `pg_locks` carries **no acquisition timestamp**. Presence is observable; **order is not**. A capture function that checks `pg_locks` passes identically whether the caller locked before `BEGIN` or after — exactly the attack §7.1 forbids closing silently.

The Gate-1 answer is the most elegant engineering in the package: **convert ordering into visibility**. You cannot ask PostgreSQL *when* a lock was taken — but you can ask it, exactly, whether a row was committed before your snapshot. So put the proof in a row:

```
acquire session barriers
  → insert ticket row, committed in a READ COMMITTED step   ← XID committed
  → BEGIN REPEATABLE READ
  → ticket visible in this snapshot  iff  its XID precedes the snapshot
  → invisible ⇒ snapshot predates the barriers ⇒ reject
```

Visibility is checked per-XID (`pg_visible_in_snapshot`), not by comparing global xmin — so an unrelated long transaction pinning xmin cannot cause false rejections (`TestTicketOldXminAccepted` proves both the pressure and the immunity). The ticket is bound to pid, employer, expiry, single-use; aborted and RR-issued tickets are rejected.

Two things worth naming: **the mechanism is close to minimal** (one table, one visibility predicate — the complexity lives in the proof, not the mechanism), and **the episode validates your remediation philosophy**: the *invariant* was right, the spec's first-cut *mechanism* was unprovable, and invariant-first thinking allowed swapping the mechanism without re-opening the goal.

### 2.3 Authority and the privilege boundary (EF-E3-P0, EF-F1A/B)

Immutability is commonly misunderstood as "no UPDATE/DELETE." If `app` can **INSERT**, it can fabricate. The root invariant: **the component that creates the snapshot must be the exclusive authority over its contents.** The correction — REVOKE direct writes + SECURITY DEFINER capture that *derives* every value rather than accepting caller JSON — then generates second-order work, and the interaction spiral becomes visible:

- `app` holds EXECUTE on `pg_advisory_*` by default, so the **locks themselves become a bypass surface** — the substitution attack proven red (`TestLockSubstitutionUndetectable`), closed by revoking **all 21 advisory overloads** from PUBLIC with DEFINER wrappers as the sole path.
- Even the *revoke* needed guarding: the pattern `pg\_advisory\_%` silently missed all 8 `pg_try_*` overloads — a **fail-open security inventory**, caught by the inventory test, now pinned at 21 so a future PG release breaks loudly.
- Three frozen INVOKER helpers broke under the revoke → 00013 redefines them as DEFINER, byte-identical bodies, proven.
- The bootstrap path still used a direct advisory lock → narrow wrapper, gap pinned by test.

Each item is small; together they illustrate the thesis — **each trust boundary added late multiplies the work of every other**.

### 2.4 Breadth and the proof burden

The contract must hold across roughly a dozen writer classes (services, triggers, functions, direct SQL) with identity derived from canonical records, one key derivation, one acquisition order — and acceptance is adversarial: red-first attacks, both orderings, persisted-state inspection, pin tests that *stay red on purpose* until the mechanism lands. The 12 red tests are not embarrassments; they are calibrated instruments. That is why a gate produces "31 green + 12 pinned red" instead of a merged feature.

---

## 3. Database technicality, or wrong architecture?

Both, in honest proportion — and they should not be conflated. The technicality is real; but technicality alone yields *hard*, not *sprawling*. The sprawl comes from three architectural choices — each defensible, all frozen:

**Choice 1 — Mutable current-state inputs, frozen by locking.** Payroll computes over a cut of data that operations keeps mutating. Two fundamental designs exist: (a) freeze the live tables with locks (chosen), or (b) make inputs append-only and read a commit-boundary cut (event-sourced/bitemporal). Design (b) **dissolves D1 almost entirely** — no barriers, writers never block, population derived from the cut. It was reasonably rejected for v0.1 (replay/versioning machinery, bigger departure than the milestone could carry) — but see the trade clearly: **the entire barrier apparatus is the tax on choice (a)**. D1 is not "how databases are"; it is "how *this data model* meets MVCC." (A third option — SERIALIZABLE with abort-and-retry instead of explicit barriers — was available; the spec chose deterministic barriers over SSI's abort storms. A judgment call, not a mistake.)

**Choice 2 — The two-role privilege model.** With `app` EXECUTE-by-default on everything, the DB's own coordination primitives become an attack surface, and capability must be re-created via revokes and DEFINER wrappers. A dedicated snapshot-writer role would shorten this; the frozen model forbids it — and Gate 1 shows the model can still be made **self-enforcing** (boundary A: direct use fails loudly) rather than merely disciplined (boundary B: works but nothing stops future bypass). One-time cost, small permanent guard.

**Choice 3 — Go-canonical computation, DB-side enforcement (ADR-1).** Logic lives in Go; the trust boundary must live in the DB. That split demands an explicit verification protocol between the two (DB proof-checks Go's claims; SQL never generates canonical bytes). Sound — and it is why "derive, don't accept" needs machinery rather than convention.

**Verdict: not a wrong architecture — a conventional one meeting ledger-grade requirements it wasn't originally shaped for.** Correct diagnosis: "under-instrumented for trust." Correct treatment: what's underway — add the trust spine once, at the authoritative boundary, with proof.

---

## 4. Inherent vs accidental — the ledger

| Complexity source | Class | Permanence |
|---|---|---|
| RR snapshot at first statement; locks never refresh views | **Inherent** (PG semantics) | Permanent, hidden behind one shared admission impl |
| Freezing mutable inputs against concurrent writers | **Inherent** (payroll domain) | Permanent per writer; mechanically enforced after 00013 |
| Exclusive creation authority for money-adjacent records | **Inherent** (trust/compliance) | Permanent |
| Supersession chains, span carving, coverage unions (comp/SHG/packs) | **Inherent** (domain-hard, ordinary) | Permanent — "normal hard," not exotic |
| Two-role model → 21-overload revoke + DEFINER boundary | **Chosen constraint** | One-time (00013) + small permanent guard |
| Spec-pinned barrier protocol over SSI abort/retry | **Chosen constraint** | Permanent, deliberate tradeoff |
| Proof-grade acceptance (red-first, both orderings, adversarial batteries) | **Chosen standard** | Permanent by design — already caught the fail-open revoke |
| Duplicated advisory key derivation across services | **Accidental** | Removed by 00013 canonical key function |
| Frozen helpers as SECURITY INVOKER | **Accidental** | Fixed by 00013 redefinition |
| Fail-open revoke inventory (missing 8 `pg_try_*`) | **Accidental** | Fixed; count pinned, fails loudly |
| Pool-slot leak; TEMP-revocation test surprise | **Accidental** | Fixed in-experiment |
| Writers grown milestone-by-milestone without a barrier contract | **Accidental — the single biggest factor** | Closed by the writer matrix; new writers inherit wrappers |

Two readings matter: the **inherent rows are permanent but invisible once wrapped** (after 00013, a new writer path inherits the protocol by construction — it cannot get it wrong, loudly or quietly), and the **accidental rows are one-time** — retrofit debt paid *once, into mechanism*, rather than forever, into vigilance. That conversion is the whole point of a gated milestone.

---

## 5. Why this is convergence, not runaway complexity

1. **The hard, novel part is done and self-contained** — a proven admission mechanism (31 green, 12 pinned red) existing *before* any capture code changed, deliberately unwired pending approval. When the riskiest engineering is complete and inert, the milestone is past its complexity peak.
2. **Steps 7–9 apply a proven mechanism** — barrier matrix, wiring, comp/SHG/packs, battery, report. Breadth with known shapes, not new inventions.
3. **Every accidental find was fixed and pinned in the same experiment.** The process is consuming its own complexity — which is what "healthy" looks like.
4. **The end state has the best property a trust design can have: the safe path is the only path.** After 00013, `app` cannot touch advisory primitives, cannot write snapshots, cannot validate directly — the invariant class is closed *by construction*, not by developer memory. Most systems never get there; they run on convention and hope.

---

## 6. What can still bite

- **Barrier waits must stay timeout-bounded** (already on the post-Gate-1 list) — otherwise the lock protocol itself becomes a DoS surface against capture.
- **Retry posture must be uniform at every caller** (bounded, fresh connection, fresh barrier set) or mismatch-retry becomes a flake generator under load.
- **The pinned 21-overload inventory is deliberately fail-loud** — a future PostgreSQL adding overloads breaks the guard by design. Budget a small maintenance cost per upgrade; that's the price of fail-closed.
- **The riskiest window is right now**: the mechanism exists but is unwired. Two disciplines coexist until capture switches over and the 12 reds flip green. Land it in one migration and one cutover, not gradually.
- **Keep comp/SHG/pack work from re-litigating the barrier pattern** — those are domain-hard but mechanically routine once the admission boundary holds.

---

## 7. Bottom line

- **The challenges** are the MVCC ordering trap, exclusive write authority, and universal writer participation — three problems whose solutions interact and amplify each other.
- **The complexity** comes less from any single hard problem than from adding trust boundaries late to a feature-first system; *interaction, not difficulty, is the multiplier*.
- **It is genuine database technicality at the core** — but the architecture isn't wrong; it's a conventional mutable-state, two-role design meeting ledger-grade trust requirements, and M2.1 retrofits the missing trust spine at the authoritative boundary.
- **It is roughly half inherent, a quarter chosen, a quarter accidental** — and the accidental quarter is being converted into permanent, self-enforcing mechanism, which is the best possible outcome. The XID ticket at the center of Gate 1 is evidence the team is not drowning in this complexity but *mastering* it: they found the spec's proposed proof unprovable and replaced it with a construction that turns an unobservable ordering into an exactly observable visibility fact — using nothing but the database's own commit-order semantics.

---

The full writeup is saved at `download/writeup_m21_complexity_autopsy.md` (markdown, matching your repo's `docs/writeup*.md` convention — say the word if you'd like it as docx/pdf, or renamed/retuned for `docs/`). A natural follow-up: I can draft the Gate-1 approval decision memo, or a `docs/writeup4.md` version framed for your team's engineering culture (the "five lessons" style of prompt17).


## prompt

as a developer who wants to simply code business rules and concentrate on the business domain only, this low-level database complexity is unacceptable, if I use Ruby on Rails framework or any comparable framework in Go, it is a non-issue, is that fair to say? do those frameworks the guardrails, integrity, provenance and the like aspects of what we are doing here? how would you vastly simplify this using a known framework in Go without the low-level complexity?


## answer

Fair question — and it deserves a precise answer, because it's *half* right. The short version: **Rails (or any mature framework) would genuinely make the *mechanics* a non-issue. It would not make your *guarantees* a non-issue. The complexity in this repo is generated less by Go or PostgreSQL than by one sentence in your own spec — and you have three honest ways out, which I'll give you concretely.**

## 1. What frameworks actually give you

| Requirement | Rails / comparable Go stacks (ent, sqlc+pgx, Django) | What WorkforceOps demands |
|---|---|---|
| Transactions, rollback | `ActiveRecord.transaction`, `BeginFunc` — solved | Same |
| Row locking | `with_lock` = `SELECT FOR UPDATE` — solved | Same |
| Optimistic concurrency | Built-in (`lock_version` + `StaleObjectError`) | Same, unused |
| Isolation semantics | Defaults to **READ COMMITTED**; you never touch RR | Spec **pins REPEATABLE READ** + pre-BEGIN protocol |
| Audit trail | paper_trail / audited gems — who changed what, when | Same-ish, but forgeable by direct SQL |
| Invariants under **hostile direct SQL** | **Nothing. Zero.** All framework guardrails live in the application layer and evaporate the moment someone opens `psql` as the app role | This is the core of prompt17: *"The database must enforce the invariants under the ordinary application role"* |
| Cryptographic provenance (canonical bytes, digests, gate artifacts, pin ceremonies) | **Nothing**, in any framework, ever | Core of Milestone 2.1 |

That last two rows are the whole answer. EF-E3 exists *because* application-layer authority proved insufficient — the app role could fabricate snapshots by bypassing the app entirely. Rails guardrails are application-layer. **Port this exact spec to Rails and you would fail the same adversarial audit** — you'd end up writing the same REVOKEs, the same SECURITY DEFINER functions, the same admission proof, just in Ruby migrations instead of goose. The framework changes where the conveniences live, not what the spec demands.

## 2. Why it *feels* like a non-issue in Rails — the legitimate kernel

There is a real sense in which you're right, and it's worth naming precisely: **the framework's default path avoids the swamp by convention.**

The idiomatic Rails payroll capture is: one service object → one transaction (READ COMMITTED) → `with_lock` on the inputs (or one coarse lock) → **copy the inputs into snapshot rows** → hash → commit. Under READ COMMITTED, "wait for lock, then read" is *correct* — each statement gets a fresh snapshot, so the writer who committed while you waited is visible. The D1 trap only exists because WorkforceOps pinned REPEATABLE READ, where the snapshot predates the wait.

So: Rails doesn't solve MVCC. It **shapes your design so the MVCC trap never opens** — and thousands of Rails payroll-ish apps run exactly that pattern and are fine. What they don't have is your acceptance bar: writer-first *proofs*, hostile-role fabrication batteries, `pg_locks` evidence. "Non-issue" in Rails-land usually means *unproven but probably fine*, because nobody runs an adversarial review against a CRUD app.

## 3. How you'd vastly simplify this in Go — the three levers

The entire admission machinery (XID tickets, session barriers, dedicated-connection lifecycle, 21-overload revoke, DEFINER wrappers, 7-class barrier matrix) exists because of **three choices, each reversible**:

1. **REPEATABLE READ** → causes the snapshot-before-lock trap → forces the pre-BEGIN protocol and the ticket proof.
2. **Fine-grained barriers** (population → employees → packs) → forces the writer matrix, canonical ordering, key derivation.
3. **Hostile `app` role** (direct SQL is in the threat model) → forces revokes, DEFINER wrappers, self-enforcing boundaries.

Drop those three choices and the synchronization contract collapses to **one concept: a single lock per (employer, pay period), taken by every writer and by capture, under READ COMMITTED**:

```go
// The entire synchronization contract, replacing the barrier matrix
func lockPayrollPeriod(ctx context.Context, tx pgx.Tx, employerID, periodID uuid.UUID) error {
    _, err := tx.Exec(ctx,
        `SELECT pg_advisory_xact_lock(hashtextextended($1 || ':' || $2, 0))`,
        employerID, periodID)
    return err
}

func (s *Capture) CaptureSnapshot(ctx context.Context, emp, period uuid.UUID) error {
    return s.pool.BeginFunc(ctx, func(tx pgx.Tx) error {      // READ COMMITTED (default)
        if err := lockPayrollPeriod(ctx, tx, emp, period); err != nil { return err }
        inputs := s.readInputs(ctx, tx, emp, period)           // sees every commit before the lock
        snap  := s.canonicalize(inputs)                        // copy-on-capture, hash in Go
        if err := s.insertSnapshot(ctx, tx, snap); err != nil { return err }
        return s.writeAudit(ctx, tx, snap)
    })
}

// Every writer, all ~a dozen of them:
func (w *Writer) Anything(ctx context.Context, ...) error {
    return w.pool.BeginFunc(ctx, func(tx pgx.Tx) error {
        if err := lockPayrollPeriod(ctx, tx, emp, period); err != nil { return err }
        // ... business mutation ...
    })
}
```

**Why this is correct with zero ceremony:** total mutual exclusion (every writer and capture takes the same lock) + per-statement fresh snapshots (RC) = capture always reads the latest committed state, and nothing can change it mid-capture. Writer-first: capture waits on the lock, then *sees* the commit. Capture-first: writer waits, no mid-capture mutation. Both acceptance orderings hold — and notice this is *the same idea as the XID ticket* (prove "no writer interleaved"), just at a threat-model grade that trusts participants.

Guardrails you'd **keep** at the DB level — four statements, not twenty-one overloads:

```sql
REVOKE UPDATE, DELETE ON payroll_snapshot, payroll_snapshot_employee, payroll_snapshot_pack FROM app;
REVOKE UPDATE ON payroll_audit_log FROM app;   -- append-only stays enforced
```

Stack for the simple design, Go-native: **ent** (schema-as-code, hooks, validations) or **sqlc + pgx** (explicit SQL, matches your frozen arch), **goose/atlas** migrations, **echo/chi + oapi-codegen** for REST. Scaffolding productivity,Rails-grade-ish, Go-idiomatic.

**What you'd drop vs. keep:**

| | Current M2.1 | Simple design |
|---|---|---|
| Sync contract | Session barriers + tickets + admission protocol + 12 red attack tests | One lock helper, ~5 tests (both orderings, fabrication-denied, empty-population, hash-recompute) |
| Privilege work | 21-overload revoke, 3 DEFINER redefinitions, bootstrap wrapper, inventory guard | 4 REVOKE statements |
| Writer matrix | 7 classes × both orderings × direct-SQL variants | "take the lock" in a shared `BeginTx` helper — one place |
| **Domain work (resolver, SHG coverage, pack digests, golden gates)** | unchanged | **unchanged — no framework on earth gives you this** |

Realistically that's **~60–70% of the milestone's machinery gone**, in any stack.

## 4. What you honestly give up

1. **Protection against a hostile credential-holder.** Someone with `app` credentials can still INSERT a forged snapshot (the REVOKEs cover mutation, not insertion-by-app — that's what the DEFINER capture was for). If an auditor ever asks "prove nobody with DB access could fabricate payroll inputs," the simple design answers "we trust the application boundary" — which is a *documented downgrade*, not a solution.
2. **Defense-in-depth against a non-participating writer.** RR made capture internally consistent even if a buggy writer skipped its lock. The simple design's mitigation is cheap: re-read a fingerprint of the inputs at the end, under the lock, before commit.
3. **Fine-grained concurrency.** Capture blocks all writes for that employer-period for its duration. At monthly payroll cadence, irrelevant.

## 5. The recommendation

You have three coherent positions, and the honest framing is that **this is a spec decision, not a stack decision**:

- **Tier 1 — Framework-simple** (above): threat model = "the application is the boundary." ~1–2 weeks of work. Right if this system's trust story is "tests are green and the app layer is the only writer."
- **Tier 2 — Middle** (what I'd pick for a compliance-adjacent payroll): simple lock + copy-on-capture + the 4 REVOKEs + DEFINER capture function *only for the snapshot tables*. DB-enforced where money lives, app-enforced everywhere else. Maybe 20% of the current machinery.
- **Tier 3 — Current path**: full adversarial proof, Gate 1 as built. Right only if the acceptance bar genuinely requires proving correctness against a hostile role — and note your Gate-1 work is already *done and proven*, which is not nothing.

Two cautions. First: your frozen architecture and the adversarial acceptance bar are *written commitments* (prompt17 §4: reopening requires explicit approval; §15: the DB must enforce under `app`). If you now judge that bar too expensive, the correct move is to **amend the spec explicitly** — "we downgrade the threat model from hostile-app-role to trusted-app, we accept these documented losses" — not to quietly drift. Second: don't switch stacks for this. Rails would re-buy you conveniences you already have (transactions, migrations, validation) while abandoning a proven, unusual asset — a *machine-checked* trust boundary — and it wouldn't remove one line of the resolver, the SHG coverage algorithm, or the pack digest ceremony. The desire to "just code business rules" is legitimate, and after 00013 the design actually grants it: business developers inherit the wrappers and *cannot* touch the protocol. You paid for that property already; the framework alternative means paying a smaller amount for a weaker version of the same property.

If you want, I can draft the spec-amendment memo (Tier 1 or Tier 2, with the exact prompt17 clauses affected, what's kept, and the migration path from the Gate-1 work), or append this analysis as a second section to the complexity writeup.