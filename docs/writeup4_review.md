The three reviews converge on one important diagnosis: the complexity is not inherent to writing payroll business rules. It is largely the cost of the particular combination chosen for snapshot capture: mutable inputs, `REPEATABLE READ`, pre-transaction advisory barriers, an application-owned capture session, and a threat model that does not trust direct SQL as `app`.

There is also one finding I would elevate above the others. DeepSeek's `DISCARD ALL` objection is valid and was not addressed in the Gate 1 package. PostgreSQL 16 documents that `DISCARD ALL` releases session advisory locks, and it cannot be run inside a transaction block. That means the ticket's proof that it predates the repeatable-read snapshot does not, by itself, prove that the same barriers remained held between ticket issuance and capture. Revoking the 21 advisory-function overloads should not be treated as proof that this utility-command path is closed.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+2



I would therefore revise my earlier conditional approval: accept the XID-ticket as a valid ordering mechanism, but do not yet approve Boundary A as a proven, self-enforcing privilege boundary. The consolidated assessment follows.

## 1. The consolidated verdict

The reviews are mostly aligned, but they need three corrections before becoming one coherent assessment.

First, the XID-ticket design solves the original ordering-proof problem. It replaces the invalid inference from `pg_locks` with a direct test of whether a committed ticket is visible in the caller's `REPEATABLE READ` snapshot. It also avoids the false-rejection problem caused by comparing global `xmin`. The Gate 1 package reports 31 successful mechanism proofs, including the old-`xmin` test and concurrency/cleanup tests.&#x20;

gate1_admission_package.md



Second, the ticket does not automatically prove that the barriers remained continuously held after ticket issuance. That is the important additional concern from DeepSeek. The package must close this before claiming that Boundary A is self-enforcing.

Third, the architecture can be simplified substantially, but only by changing a design constraint or the threat model. Switching from Go to Rails, Ent or GORM does not remove the need to define snapshot semantics, authoritative write paths and provenance.

## 2. What the three reviews correctly identified

### A. This is really two problems, not one

The invariant has two independent dimensions:

- Freshness: the snapshot includes every relevant input within the defined capture boundary.
- Authority: the snapshot was derived from authoritative records, rather than fabricated or selectively supplied by its caller.

The XID-ticket addresses the first dimension. The controlled snapshot writer and its verification logic address the second. A correct hash does not establish that the underlying data was authentic or complete.

Claude's distinction between freshness and authority is useful, as is DeepSeek's emphasis that immutability must prevent unauthorized insertion, not merely modification or deletion.&#x20;

writeup4_claude.md

writeup4_deepseek.md



### B. PostgreSQL's MVCC behavior is the underlying technical difficulty

The original failure is subtle:

A transaction begins at `REPEATABLE READ`. It waits for a writer holding a lock. The writer commits. The waiting transaction acquires its lock but still sees its earlier snapshot.

PostgreSQL explicitly documents that `REPEATABLE READ` uses the snapshot from the first non-transaction-control statement, and subsequent queries do not see changes committed after that snapshot. A lock wait does not refresh it.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



The resulting engineering work is understandable:

- Synchronization must happen before the relevant snapshot is established.
- Session-level locks are needed for the pre-transaction protocol.
- One physical connection must retain those locks through the transaction.
- The population and lock set must be stable.
- Writers must participate in the same synchronization contract.
- Lock cleanup must work under cancellation, failure and connection uncertainty.

This complexity is real, but it is specific to the chosen protocol—not an unavoidable requirement of payroll software.

### C. The snapshot authority problem is separate from the locking problem

ADR-1's Go-canonical resolver is a defensible choice because it avoids implementing the 366-line compensation resolver and its canonical encoding twice.

But there is a cost: the database verifier becomes the security boundary.

It must prove that the Go-supplied claim is complete and correct relative to the authoritative records. It must not merely check that the submitted JSON is well-formed or that its hash matches.

The acceptance tests need to cover omitted employees, omitted compensation segments, fabricated values, incorrect source identities, stale versions, wrong employer or period, supersession errors, and forged rule-pack references.

Claude identified this trade-off correctly.&#x20;

writeup4_claude.md



## 3. The important unresolved finding: `DISCARD ALL`

DeepSeek's concern deserves to become an explicit Gate 1 attack test.

The PostgreSQL 16 documentation says `DISCARD ALL` releases session resources and includes the equivalent of `SELECT pg_advisory_unlock_all()`. It also explicitly says that `DISCARD ALL` cannot run inside a transaction block.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



This creates a specific attack window:

1. Acquire all session barriers.
2. Issue and commit the XID ticket.
3. Execute `DISCARD ALL` before beginning the `REPEATABLE READ` transaction.
4. Attempt to reacquire the same barriers through the permitted wrappers.
5. Begin `REPEATABLE READ`.
6. Attempt ticket verification and snapshot capture.

The key issue is that the ticket can still predate the snapshot even though the original locks have been released. The session PID and employer may also remain the same.

Revoking the 21 raw advisory-function overloads is not, by itself, proof that this route is closed. PostgreSQL's implementation handles `DISCARD ALL` through its own session-cleanup path; it releases advisory locks internally.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



There is one important qualification: the package may already have a ticket/hold lifecycle that prevents a second acquisition after ticket issuance. I have not seen the actual function bodies, so I will not claim the attack necessarily succeeds. But the package does not report the exact `DISCARD ALL`-between-ticket-and-capture test.

That test must prove one of two things:

- The caller cannot reacquire the barriers after ticket issuance, and verification consequently fails closed; or
- The ticket is explicitly invalidated or otherwise rejected when lock continuity is lost.

If neither is established, Boundary A has not been proven.

This changes my earlier assessment: D1 ordering is accepted as demonstrated; the complete privilege boundary is not yet approved.

## 4. Which proposed architectural alternatives are valid?

The reviewers offered several alternatives. They are not equally strong, and some need qualification.

### Option 1 — READ COMMITTED with transaction-scoped coordination

This is the most practical simplification to investigate first.

Under PostgreSQL `READ COMMITTED`, each SQL command starts with a fresh snapshot. A transaction can acquire a transaction-scoped lock, wait for a competing writer, and then issue subsequent reads that see commits completed before those reads begin.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



For the existing single-employer MVP, I would investigate a single employer-level capture gate:

- Every payroll-affecting write acquires the same transaction-scoped gate.
- Capture acquires the gate as its first separate statement in a `READ COMMITTED` transaction.
- Capture reads the authoritative inputs in subsequent statements.
- Snapshot verification and persistence occur while the gate remains held.
- Commit releases the gate.

The strongest version would make writer participation database-enforced through controlled write functions or triggers, not merely through a Go helper. The application role must not be able to bypass the gate by writing directly to an input table.

This avoids the need for pre-transaction session locks, ticket visibility, dedicated connection pinning and the associated cleanup protocol. It also avoids the `DISCARD ALL` window because the coordination lock is transaction-scoped and `DISCARD ALL` cannot run inside the transaction.

This is a candidate design, not a proven replacement. It needs the same writer-first/capture-first tests and persisted-state checks as the current design.

Z's proposal to use one shared transaction-scoped advisory lock is valid when the application itself is trusted and every writer uses the shared helper. It is not sufficient under the existing hostile-direct-SQL threat model, because direct SQL can bypass a Go helper.&#x20;

writeup4_z.md



### Option 2 — Append-only, versioned inputs

This is the best long-term data-model simplification.

Instead of editing payroll inputs in place, record new facts and corrections as immutable successor records. Build snapshots from a consistent MVCC view of those records.

This reduces the number of mutable inputs and makes historical reconstruction much easier. It also aligns naturally with compensation corrections, timesheet revisions, SHG instructions and audit requirements.

However, the reviewers' proposed `MAX(event_id)` watermark has a serious flaw.

PostgreSQL sequences are not transactional: sequence values are allocated immediately and are not rolled back with the transaction. Therefore, increasing IDs do not establish commit order.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org



For example, transaction A can allocate event ID 100 and remain uncommitted. Transaction B allocates 101 and commits. A snapshot sees 101 but not 100. If A commits later, replaying all events with IDs up to 101 would include an event that was not in the original snapshot.

The solution is not to abandon append-only inputs. It is to avoid treating a sequence number as a commit-order watermark. Use a consistent database snapshot and preserve the exact included event set or another demonstrably commit-ordered boundary.

Append-only storage also does not eliminate the need for source validation, effective-date resolution or exclusive snapshot-writing authority.

### Option 3 — SERIALIZABLE with transaction retries

This is a legitimate candidate, but DeepSeek overstates what it guarantees.

PostgreSQL SSI guarantees that successfully committed serializable transactions are equivalent to some serial execution. It requires all relevant read and write transactions to use the isolation level, and the application must retry complete transactions that fail with SQLSTATE `40001`. PostgreSQL explicitly recommends a framework that can perform those retries.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+2



But serializability is not the same as strict wall-clock freshness. An overlapping capture and writer may be assigned a valid serial order in which capture comes first, even though the writer commits before capture finishes.

That may be acceptable if the business defines the snapshot's cut as its serialization/snapshot point. It is not automatically acceptable if the requirement is literally that every change committed before the final validation decision must be included.

So I would test SSI as an alternative, but I would not approve a simple isolation-level change without first pinning down the exact meaning of the capture boundary.

### Option 4 — A dedicated capture capability

DeepSeek is also correct that a separate capture process or credential can make the trust boundary clearer. The ordinary `app` role requests a capture; a separate executor controls the database connection and snapshot operation.

That is the cleanest way to prevent an ordinary caller from controlling the capture session itself. But it changes the frozen two-role architecture, introduces another credential boundary and requires explicit approval.

It should remain an option, not an unapproved change.

## 5. Is the architecture wrong, or is the problem inherently complex?

My consolidated classification is:

Inherent complexity

- Correct effective-date and historical compensation resolution.
- Attendance, timesheet and payroll provenance.
- SHG coverage and verification semantics.
- Rule-pack evidence and source integrity.
- The requirement for an accurately defined, reproducible payroll snapshot.

These are business-domain and trust requirements. Rails, Go, Ent or GORM will not remove them.

Choice-specific complexity

- `REPEATABLE READ` combined with pre-BEGIN advisory barriers.
- Session-lock connection pinning.
- XID tickets and ticket lifecycle.
- Revoking all 21 advisory-function overloads.
- Redefining existing helpers as `SECURITY DEFINER`.
- The large fine-grained lock hierarchy.

These are consequences of the current execution and synchronization design. They are not inherent to the payroll domain.

Accidental retrofit complexity

- Writer paths developed before a shared synchronization contract existed.
- Duplicated lock-key derivation.
- Legacy privilege assumptions.
- Discovering the trust model after the main workflows were already built.
- Combining snapshot concurrency, compensation, SHG and rule-pack supply-chain work into one milestone.

The reviews' percentages—40/60 or 50/25/25—are subjective, so I would not preserve them. The more important distinction is that some complexity is permanent domain work, some is the price of a chosen architecture, and some is one-time retrofit debt.

The architecture is not fundamentally incoherent. But the combination of mutable inputs, an app-owned `REPEATABLE READ` transaction, advisory barriers and hostile direct SQL is considerably more demanding than an ordinary business application needs unless that threat model is genuinely required.

## 6. Would Rails or a comparable Go framework make this a non-issue?

No—not under the current security requirements.

Frameworks provide transactions, migrations, row-locking helpers, validation, associations, optimistic concurrency and audit extensions. They do not guarantee that a hostile database caller cannot fabricate a payroll snapshot or bypass application validations.

The reason Rails applications often appear simpler is that their ordinary design usually relies on PostgreSQL's default `READ COMMITTED` behavior and treats the application as the trusted writer. The framework makes that workflow comfortable; it does not provide the underlying integrity guarantee.

For Go, I would not switch frameworks just to escape this problem.

- Ent + Atlas would provide schema-as-code, generated persistence APIs and a more framework-like developer experience.
- sqlc + pgx + goose would retain explicit SQL while reducing repetitive database code.
- In either case, critical database constraints, triggers and privileges still belong in PostgreSQL. Application callbacks and hooks are not a substitute for database enforcement.

The best outcome is not that business developers learn less about SQL while the database silently accepts invalid state. It is that the database guarantees are implemented once behind a small, stable application interface, so business developers do not have to think about lock lifecycles and privilege mechanics every day.

## 7. What I recommend doing next

I would not proceed directly from this package into the full `00013` implementation.

I would make the next gate a focused comparison of just two designs:

A. Current XID-ticket design

Add the exact `DISCARD ALL` attack described above. Verify that the ticket/hold lifecycle prevents lock release and reacquisition from being mistaken for uninterrupted admission. Also complete the privilege installation, `SECURITY DEFINER` hardening and exact Down-ACL checks identified in the earlier review.

B. READ COMMITTED transaction-scoped employer gate

Build a small live PostgreSQL 16.15 experiment using one shared gate row and database-enforced writer participation. Test both writer-first and capture-first interleavings, direct SQL as `app`, cancellation, rollback, and snapshot persistence. Compare its correctness and operational complexity against A.

Do not implement both designs in parallel or maintain two resolvers. Choose one synchronization authority.

The invariant-first review still applies to either choice:

1. What business invariant must always hold?
2. What actual mechanism violated it?
3. What are all possible write paths?
4. At what authoritative boundary is the invariant enforced?
5. How is the original failure reproduced and persisted state inspected?
6. What new failure modes does the correction introduce?
7. What evidence proves the complete failure class is closed?

My recommendation: preserve the current work, but make the READ COMMITTED gate experiment the next architectural decision before paying the remaining cost of the session-lock design. The business domain deserves a simpler persistence boundary than the one currently emerging.

The consolidated reviews support that direction: Claude's distinction between necessary and placement-specific complexity, DeepSeek's capability-boundary criticism, and Z's simpler transaction-scoped coordination model are all useful. The `DISCARD ALL` finding is the correction that prevents us from prematurely declaring the current approach complete.&#x20;

writeup4_claude.md

writeup4_deepseek.md

writeup4_z.md



Bottom line: the XID-ticket is a good solution to the snapshot ordering proof. It is not yet sufficient evidence for the complete admission boundary. And no framework makes that distinction disappear.