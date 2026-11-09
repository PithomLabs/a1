## prompt

evaluate plan_prompt17.md against prompt17.md

## answer

Still reading prompt17.md (I'm about three-quarters through) so I can compare it against the plan. The last section is next.

The plan tracks prompt17 closely and follows the §16 order, but it has one substantive gap on the D1/EF-E3 core and several smaller mismatches. I'd fix the gaps before approving `go`.

## What the plan gets right

- It covers every finding ID, the lock hierarchy (population → employee → pack → operation), and the shared single-connection session-lock protocol.
- It covers the 40001 mapping, the drop-first and fresh-name acceptance approach, and OT-001 protection.
- It plans the 20-section report and the three live service-as-`app` probes.
- Its claim that session-level and transaction-level advisory locks conflict in one namespace is correct. That makes the mixed session-lock/writer-lock design sound.

## Gaps that matter

**1. The §7.1 pre-BEGIN proof isn't actually solved.**
- Prompt17 names the exact attack: begin RR, take the lock afterwards, call the privileged function.
- The plan's `pg_locks` assertion shows only that a lock is held, not that it was taken before the snapshot.
- As far as I know, `pg_locks` also can't tell a session-level advisory lock from a transaction-level one, so a caller could pass the check with locks acquired mid-transaction. Please verify that.
- The plan then says "ordering is guaranteed by the Go protocol". That is the "silently claim fixed" outcome §7.1 forbids.
- Prompt17 says that if this can't be enforced under the two-role model, the plan must explain the PostgreSQL limitation and propose the smallest privilege change for approval.
- "Open questions: None" is therefore wrong. A possible direction is a DEFINER "acquire barrier" function called outside a transaction that records a ticket. The capture function would then require the ticket to predate `transaction_timestamp()`. I haven't validated that.

**2. Capture logic may end up duplicated.**
- The compensation resolver lives in Go (`internal/compbasis/resolve.go`). A DEFINER capture that "derives every value" must either reimplement it in SQL or "verify" Go-supplied content, which is the JSON pass-through the plan says it avoids.
- The canonical hash has the same problem: PL/pgSQL has to match Go's canonicalization byte for byte.
- Prompt17's third lesson is that writer and resolver must share one definition of effective data. Two resolvers would recreate that failure.
- The plan needs an explicit decision here, and it belongs in Risks. If the resolver moves to SQL, the compensation phase (step 7) changes and probably precedes step 5.

**3. Sequencing causes rework.**
- Step 3 builds population enumeration before step 4 decides the P1a population semantics, which the plan itself says to "inspect first".
- Step 3 also rewires the Go `CaptureSnapshot`/`ValidateRun`, which steps 5–6 then replace with DEFINER functions.

## Smaller mismatches

- **SECURITY DEFINER hardening** is missing. Prompt17 §7.1 requires restricted `search_path`, schema-qualified references, correct ownership, narrow EXECUTE grants and atomic audit, and the plan mentions none of these. Capture audit isn't mentioned either.
- **Writer-first test classes** don't match §12A. The plan lists "4 missing classes" (term change, termination, attendance correction, SHG correction). Prompt17 lists term closure, clock-event disposition, status-history, draft-line and header mutation. Several of these appear only implicitly under "both orderings per writer".
- **"13 value classes"** doesn't match prompt17's 12. The plan's own parenthetical lists only 10 and omits run and employer identity.
- **F-A2** is decided up front ("added, not deferred"). Prompt17 §8.4 says to evaluate whether existing lineage suffices first. Changing segment content also affects the canonical hash, and the plan doesn't mention a snapshot schema-version bump.
- **Acceptance isolation:** "extend the drop+recreate precedent" needs a guard restricting drops to explicitly disposable names. Prompt17 forbids dropping any user database.
- **Repin trail:** the plan relies on `migration_admin` as the documented trust boundary. Prompt17 says direct registry changes must not silently bypass history. An AFTER UPDATE trigger on the registry that writes pin history regardless of caller would close most of that.
- **Pin ceremony fields** omit jurisdiction/scope, effective period and timestamp.
- **Not mentioned:** pinning the gate-artifact digest, verifying reviewer and evidence references, the concurrent pin/supersession single-winner test, and the SHG verifier/date/basis fields.
- **Smaller omissions:** the panic path in lock release, and "only `draft → validated` reachable" with later states structurally unavailable.
- **Baseline sums:** the recorded sums cover only `00001`–`00011`, while prompt17 says to verify `00001`–`00012` against the recorded baseline. The plan handles this sensibly by hashing `00012` fresh. I'd flag it as a discrepancy in the report.
- **New documentation:** the golden schema doc and any other new docs should stay within prompt17's allowed new files (migration, implementation, tests, report).

I'd revise the plan to resolve items 1 and 2 as explicit decisions or approval requests, reorder steps 3–5, and add the missing tests. Do you want me to draft the revised plan?


## prompt

write a technical writeup, what exactly are the challenges here, why is it so complex, is it about database technicality or is the architecture wrong, is this inherently complex or we just stumbled with accidental complexity

## answer

# D1 Admission Boundary: Why It Is Hard, and How Much of It Is Necessary

Technical write-up on the M2.1 Gate 1 package (prompt17 as amended by plan\_prompt17.1). Nov 9, 2026. Based on `gate1_admission_package.md`, `prompt17.md` and `plan_prompt17.md`; I have not seen the code itself.

## 1. Short answer

**Is it database technicality or is the architecture wrong?** Neither alone. The root is a PostgreSQL semantics fact meeting a requirement that MVCC does not give you for free. The size of the solution comes from one architectural placement decision combined with a strong threat model. The architecture is not wrong, but one decision multiplies the work.

**Inherent or accidental?** Both, and they separate cleanly:

- **Inherent:** the business invariant (a validated snapshot must include everything committed before the decision), the need for every writer to participate in one synchronization contract, and the need for a fixed lock order. Any design has to pay for these.
- **Consequence of the threat model:** treating direct SQL as the `app` role as hostile. That forces enforcement into the database and removes the usual shortcut of trusting the Go caller.
- **Accidental (relative to the chosen design):** the ticket machinery, the 21-overload advisory revoke, the helper redefinitions and the session-lock connection lifecycle. They exist because Go opens the REPEATABLE READ transaction and the database must then retroactively prove when that transaction's snapshot was taken. That is a consequence of where authority was placed, not of the problem itself.

Section 6 sets out an alternative placement that may remove much of the accidental part. I can't tell from the package whether it was evaluated.

## 2. The invariant, stated plainly

> A payroll snapshot, once validated, reflects every input change that committed before the validation decision, and no snapshot can be created or altered except by a process that derived it from authoritative records.

There are two halves:

1. **Freshness (D1).** Nothing committed earlier may be missing from the snapshot.
2. **Authority (EF-E3, EF-F1).** Nothing may be present that the controlled process did not derive.

The Gate 1 package is almost entirely about the first half, plus the privilege boundary needed to make it unforgeable.

## 3. Why freshness is hard: a chain of seven problems

Each problem is the consequence of solving the one before it.

### 3.1 A repeatable-read view is fixed at the first statement

In REPEATABLE READ, PostgreSQL establishes the snapshot when the first statement of the transaction starts. If that first statement is an advisory-lock call that blocks behind a writer, the snapshot was already taken before the wait. The writer then commits, the lock is granted, and the transaction proceeds on a view that predates the writer. It is internally consistent and wrong. This is the original D1 defect, and the red test `TestStaleSnapshotAttack` still reproduces it.

**Inherent.** The isolation level cannot be raised after a query has run, so a lock taken inside the transaction cannot repair the view.

### 3.2 Therefore locks must be taken before BEGIN, so they must be session-level

Transaction-scoped locks vanish when the transaction ends, and they cannot exist before it begins. Barriers acquired before `BEGIN` must be session-level advisory locks. Session-level locks live on a physical connection, which brings in all the connection-lifecycle problems:

- one dedicated connection for acquire, transaction and release;
- release in reverse order;
- never return a connection to the pool while it still holds locks;
- discard the connection on any uncertainty (unlock failure, cancellation, deadline);
- handle panic, error and rollback paths.

The package proves eight cleanup cases. That is real engineering effort, but it is a direct consequence of 3.1 given the pinned protocol.

**Inherent given the protocol; the protocol itself is a choice (section 6).**

### 3.3 The population is unknown until the lock is held

You need employee locks, but which employees are in scope depends on data a writer can change. The package's answer is enumerate-after-barrier, then lock per employee, then re-enumerate inside the snapshot, with a bounded retry on mismatch. It is a standard lock-then-verify loop, and it is correct, but it adds a retry state machine and a `MismatchError` path.

**Inherent** to locking a set whose membership is data-dependent.

### 3.4 Deadlock freedom needs a global lock order

Once readers hold many locks, every writer must acquire its subset in the same order (population, then employees by sorted UUID, then packs, then operation-specific). This is why the P1a–g matrix exists: seven writers were outside the contract, so some paths could change captured inputs without touching any barrier.

**Inherent** whenever a pessimistic multi-lock design is chosen. It is also the largest source of review surface, because every present and future writer must follow the order.

### 3.5 A database function cannot see when the caller's snapshot began

This is the turning point. The capture function runs in the caller's transaction. A hostile caller can `BEGIN` an RR transaction, take the session lock afterwards, then call the privileged function. The function sees the lock held and cannot tell the order.

The package shows, on PG 16.15, that granted session and transaction locks look identical in `pg_locks`. So presence is observable, but scope and order are not. Its answer is the XID ticket:

- a ticket row is committed in a read-committed operation before `BEGIN`;
- inside the RR transaction, the row is visible only if its XID predates the snapshot;
- binding to backend pid and employer, 5-minute expiry, single use;
- visibility decided per XID with `pg_visible_in_snapshot`, never by comparing xmin, because an unrelated open transaction can pin xmin and false-reject valid tickets.

This is clever and carefully tested. It is also a large mechanism whose sole purpose is to answer: when did the caller's snapshot begin?

**Accidental relative to the chosen placement of authority.** The question only exists because the caller owns the transaction.

### 3.6 Raw advisory functions let the app role substitute or release locks

Session and transaction locks on the same key are interchangeable to an observer, so a caller can swap one for the other. `TestLockSubstitutionUndetectable` demonstrates it. The package closes this by revoking all 21 advisory overloads from `PUBLIC` and routing everything through DEFINER wrappers.

Three consequences follow:

- The revoke inventory initially matched `pg\_advisory\_%` and missed all eight `pg_try_advisory_*` overloads. It failed open. The inventory test caught it and the pattern was widened to `%advisory%` with the count pinned at 21.
- The three frozen lock helpers were `SECURITY INVOKER`, so the revoke broke every writer path and trigger that used them. They have to be redefined as `SECURITY DEFINER`.
- A direct advisory call in employer bootstrap needs its own wrapper.

**Mostly accidental.** The revoke is a hardening of the ticket design. The package itself says the session-only alternative is integrity-sound and leaves only a denial-of-service exposure.

### 3.7 Authority over the snapshot content (ADR-1)

Even with perfect freshness, the app role must not fabricate or alter snapshot rows. ADR-1 chose Option 2: the Go resolver stays canonical (366 lines, with carve logic not ported), the database proof-checks claims and derives decisions, and Go keeps the single canonical byte form and the SHA-256. This avoids two resolvers and avoids reimplementing canonical hashing in PL/pgSQL, which matches the lesson that writer and resolver must share one definition of effective data.

The cost is that the database verifies claims rather than regenerating content, so how complete the proof-check is becomes the real security boundary.

**Inherent trade-off.** It was decided explicitly, with eight criteria.

## 4. Classification

| Challenge | Cause | Class |
| --- | --- | --- |
| RR snapshot fixed at first statement | PostgreSQL semantics | Inherent |
| Recency is not what MVCC or SERIALIZABLE gives | Requirement vs semantics | Inherent |
| Session locks before BEGIN, connection lifecycle | Pinned protocol (RR plus freshness) | Inherent given the protocol |
| Data-dependent population, retry loop | Locking a mutable set | Inherent |
| Canonical lock order, seven writer gaps | Pessimistic multi-lock design | Inherent given pessimistic locking |
| Proving snapshot-after-lock to the DB | Caller owns the transaction | Accidental to the placement |
| XID tickets, per-XID visibility, expiry | Consequence of the line above | Accidental |
| 21-overload revoke, helper redefinition, bootstrap wrapper | Closing lock substitution under that design | Accidental, and optional for integrity per the package |
| Fabrication, validation authority, ADR-1 | Hostile direct SQL as `app` | Threat-model driven, inherent to that model |
| Stale schemas, pinned gap tests, legacy INVOKER helpers | History | Accidental |

A note on SERIALIZABLE, which is the natural question. It does not help here. A snapshot that was taken before a writer committed can still be a valid serial order (snapshot before writer). The invariant is a real-time recency requirement, which PostgreSQL SERIALIZABLE does not provide. That is why this is not solved by raising the isolation level.

## 5. Is the architecture wrong?

Three choices interact:

1. **The snapshot is derived at capture time from many mutable tables.** Every writer to any of them becomes a potential staleness source. This is what generates the writer matrix.
2. **Pessimistic advisory barriers are the synchronization primitive.** They are keyed by convention rather than attached to the data, so participation is a discipline that has to be enforced path by path.
3. **The Go caller owns the transaction and the isolation level, and the database must trust it or prove it.** This generates the ticket mechanism and the revoke.

None of these is unreasonable for a one-process, one-database system. Choice 3 is the multiplier. If the database controls the transaction boundary and the isolation level, the proof problem largely disappears.

The package's evidence also suggests the process is working: it found real PostgreSQL facts (identical lock rows, xmin false-rejection, the fail-open revoke pattern, a pool-slot leak) that a purely paper design would have missed.

## 6. The design question to ask before approving

This is a question, not a verdict. The package does not show whether it was considered.

**Alternative: optimistic Go read, pessimistic database verification in read committed.**

1. Go reads inputs and builds the canonical content without holding barriers. This is only a claim.
2. Go calls the DEFINER verifying writer in a read-committed transaction.
3. The function checks `transaction_isolation = 'read committed'` and rejects anything else.
4. It acquires transaction-level barriers itself, in canonical order, using the same helpers writers use, including the enumerate-then-lock-then-re-enumerate step.
5. Under the locks, every later statement in a volatile function takes a fresh snapshot, so it sees everything committed before the lock was granted.
6. It proof-checks the claim against current data, rejects stale or torn claims, and persists on success.

What this would remove: session locks and connection pinning, the pre-BEGIN protocol, the ticket table and XID logic, and the integrity requirement for the 21-overload revoke (it stays available as denial-of-service hardening). The ordering proof reduces to checking the isolation level, which the function can read reliably.

What it would cost or risk:

- Reads done in step 1 can be torn across tables, so some attempts will be rejected and retried. For a low-contention payroll workload that is probably acceptable, but it must be measured.
- The proof-check has to cover staleness explicitly (for example, claimed records are the current tips and no other live rows overlap), because ADR-1 keeps carve logic out of SQL. This is where the design succeeds or fails.
- It departs from the pre-BEGIN protocol prompt17 pinned in section 5.2, so it needs explicit approval rather than a silent swap.
- The writer matrix is still required, since a non-participating writer could commit mid-verification. That work is unavoidable in either design.

I cannot judge from the package whether ADR-1's proof-check is strong enough to carry this. If it is, the alternative removes most of the accidental complexity. If it is not, the Gate 1 mechanism is the safer path and the cost is worth paying.

## 7. What Gate 1 gets right

- It states plainly that `pg_locks` cannot be the proof and replaces it with a mechanism that has a sound basis (per-XID visibility).
- It tests the failure mechanism, not just the happy path: 12 red attack tests still fail as required, and 31 mechanism proofs pass.
- It finds and fixes its own fail-open (the revoke pattern), and records the empirical corrections.
- It does not claim the problem is solved before capture is wired in.
- It keeps the two-role model and asks for approval for the privilege change, as prompt17 required.

Worth noting as a smell rather than a flaw: roughly 43 tests and a 00013 scope with ten object types are in place before any capture code is connected. That volume is a signal that the mechanism is carrying more weight than the problem might require.

## 8. Recommendation

1. Before approving the privilege request, ask whether the read-committed verifying-writer design in section 6 was evaluated, and if so why it was rejected. The answer determines whether the ticket and revoke work is necessary or only defensible.
2. If the Gate 1 mechanism stays, treat the 21-overload revoke as separately approvable hardening, since the package says boundary B is integrity-sound. Splitting it reduces the blast radius of 00013.
3. In either design, keep the writer-to-barrier matrix and canonical lock order as the main review focus, because every future writer must conform.
4. Make the ADR-1 proof-check completeness criteria explicit and testable. In both designs it is the real authority boundary.



## prompt

what would you do differently from architecture and design perspective if you have the foresight to do it right at the outset

## answer

I'd make six changes, and most of them would remove whole categories of the problem rather than make each one easier to solve. This is judged from the two packages and the prompts, not from your code.

**1. Make inputs append-only and versioned from day one.** The core difficulty is deriving a snapshot at capture time from many tables that can be edited in place. Every mutable table is a potential staleness source, and that is where the seven P1 gaps and the writer matrix come from. You already use supersession for compensation and SHG. I'd apply that same discipline to terms, status, timesheet lines, employer and period headers, and pack pins. A snapshot then becomes a manifest of immutable record IDs plus an as-of date, and "was anything missed?" becomes "were there newer versions?"

**2. Give the app role no DML on input tables at all.** It would have SELECT plus EXECUTE on a small set of gateway functions. The P1d, P1f and P1g findings are all direct-write paths that bypassed the barrier. If every payroll-affecting write goes through one function, barrier participation lives in one place instead of being a discipline across triggers, services and direct SQL. The gateway also writes the audit row.

**3. Attach the barrier to data, not to advisory locks.** Advisory locks have no relationship to MVCC snapshots, which is why you needed session locks, tickets and the revoke. I'd use a per-employee and per-employer epoch row that every input write bumps in the same transaction. As I understand PostgreSQL's documented behavior, a REPEATABLE READ transaction that tries to lock a row updated after its snapshot fails with SQLSTATE 40001 rather than proceeding on the stale view. The original D1 interleaving would then fail safe by construction. It needs the retry path you already have, and I'd verify the behavior with a test first. The one rule is that every writer must bump the epoch, which the gateway in item 2 enforces.

**4. Store the resolved timeline, not only the corrections.** F-A1-WIDEN and F-A1-FREETAIL happened because the writer and the resolver each had their own definition of effective compensation. I'd keep the effective segments as rows with an exclusion constraint on the date ranges, maintained atomically by the write function. Ambiguity then cannot be stored. The resolver becomes a trivial query, and the supersession ledger remains as history. The cost is that derived state must be kept consistent, but one function doing it atomically is easier to defend than two implementations agreeing.

**5. Decide the hash and resolver authority before writing any code.** ADR-1 decided this late, after the resolver already existed in Go. If resolution lives in SQL, the database can derive everything and "verifying writer" becomes "deriving writer". That removes the proof-check completeness question from section 8 of the write-up. If it must stay in Go, I'd at least define the canonical encoding with shared test vectors up front.

**6. Make rule-pack trust derived, not self-attested.** Pack bytes would go into content-addressed storage and gate runs would be immutable records. The pin registry would be an append-only ledger, with the current pin as a view over it. `GOLDENS_VERIFIED` and the C2 labels then cannot exist as free-text fields that can disagree with the evidence.

Two process changes would also have saved time:

- **Write the threat model first.** "Is the `app` role trusted code or an adversary?" was effectively answered late, and the strongest answer drove most of the machinery. I'd pick it explicitly, then derive the privilege model from it.
- **Build the adversarial harness before the features.** That means two-connection interleaving, a direct-SQL-as-`app` runner, and a privilege-inventory golden test of the kind your 21-overload count already is.

Not everything was knowable early. The `pg_locks` ambiguity and the xmin false-rejection are empirical findings you could only get by testing. The structural decisions in items 1 to 4 followed from your invariant-first principle, though.

A caution: item 3 is also close to what I suggested as an alternative in section 6 of the write-up, so the same open question applies. Changing course now would mean explicitly revisiting parts of prompt17's pinned protocol. Do you want me to sketch what a mid-course version would look like against the current Gate 1 package?


## prompt

as a developer who wants to simply code business rules and concentrate on the business domain only, this low-level database complexity is unacceptable, if I use Ruby on Rails framework or any comparable framework in Go, it is a non-issue, is that fair to say? do those frameworks the guardrails, integrity, provenance and the like aspects of what we are doing here? how would you vastly simplify this using a known framework in Go without the low-level complexity?

## answer

Half fair. It is fair that this much machinery is optional and that a typical Rails or Go app would have far less of it. It is not fair that the framework is what removes it. As far as I know, none of the mainstream ones (Rails, Django, Spring, or Go's Ent, GORM and sqlc) gives you snapshot freshness, provenance or tamper-resistance. What they give you is persistence convenience and application-level validation.

**What frameworks do and don't provide**

- **Do:** migrations, ORM or query layers, transactions, optimistic locking (`lock_version`), `with_lock` row locks, validations and callbacks. Gems like `paper_trail` or `audited` add change history.
- **Don't:** any guarantee that a validated snapshot includes everything committed before it. Nothing protects against direct SQL, because validations and callbacks live in the app. Nothing verifies provenance or that the pinned rules are the reviewed ones.

So prompt17's "database enforces it under the ordinary `app` role" requirement is not something a framework satisfies.

**Why it feels like a non-issue in Rails**

- Rails on PostgreSQL uses READ COMMITTED by default, where every statement gets a fresh snapshot. "Take a lock, then read" simply works. The D1 defect came from choosing REPEATABLE READ for capture.
- Most Rails apps treat the app as the only writer and the DB user as trusted. They never take on the hostile-`app`-role threat model that generated the ticket, revoke and DEFINER machinery.

So the simplifier is two decisions, not the framework: use read committed with a row lock, and decide how much to distrust your own app role.

**How I'd simplify it in Go**

Keep pgx, goose and sqlc (or Ent if you want schema-as-code), and move the invariants into plain declarative database features:

1. **One lock, taken as a row lock.** Capture does `SELECT ... FROM employer WHERE id = $1 FOR UPDATE` in a normal read committed transaction. If inputs are insert-only and reference the employer by foreign key, then as I understand PostgreSQL's FK locking, each insert takes a `FOR KEY SHARE` on the employer row. That blocks behind capture automatically, with no advisory locks, sessions or tickets. Verify this with a two-connection test.
2. **Exclusion constraints for compensation and SHG.**
   ```sql
   EXCLUDE USING gist (employee_id WITH =, effective WITH &&) WHERE (superseded_by IS NULL)
   ```
   Overlapping or ambiguous effective rows then cannot be stored. That removes the WIDEN and FREETAIL class instead of reconciling writer and resolver.
3. **Snapshot as one immutable row.** Store the canonical bytes (`bytea`) plus the hash, with `CHECK (inputs_hash = encode(sha256(content), 'hex'))`, and `REVOKE UPDATE, DELETE`. Go still builds the canonical form, and the hash can't disagree with the content.
4. **One generic audit trigger** on the input tables, written once.
5. **Rule packs as embedded files** (`go:embed`) with digests checked in CI and at startup. Git history is the pin history and `go test` runs the goldens. The snapshot only records which digest was used. This replaces most of the registry and pin-ceremony work.
6. **A `WithTx` unit-of-work helper** so domain code never sees locks or isolation.

**What you give up**

- A compromised or buggy app role could write bad inputs. Items 2 and 3 and the revokes keep the cheap protections, but you no longer defend against a hostile `app` role.
- It is a threat-model change from prompt17's frozen decisions (two-role distrust, pinned pre-BEGIN protocol), so it needs your explicit approval.
- Row-lock contention means writers wait during capture. For one employer in v0.1 that is likely fine.

If payroll data must withstand a tampering operator with SQL access, some of the heavier machinery is legitimately needed. Is that a real requirement for v0.1, or was the strong threat model a conservative default?
