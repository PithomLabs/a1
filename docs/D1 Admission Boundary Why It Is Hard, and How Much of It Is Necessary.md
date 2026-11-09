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
