# Consolidated review: the net-valid conclusions

The three reviewers—DeepSeek, Claude and Z—now substantially agree on the next architectural direction:

Stop expanding the XID-ticket/session-lock design for now. Run a focused experiment of a `READ COMMITTED` transaction-scoped employer gate, enforced by PostgreSQL on every payroll-affecting write path.

I agree with that recommendation, with some important qualifications. The reviews contain a few overstatements about `DISCARD ALL`, SSI and how triggers establish the gate. Those should not be carried into the next implementation specification.

The right result is not simply “choose Option B.” It is to establish a simpler synchronization boundary without weakening the existing threat model or the snapshot-authority requirements.

## 1. What the reviews correctly establish

A. The XID-ticket solves a real problem, but its complexity is specific to the chosen protocol.

The ticket converts transaction ordering into a visibility test. That is a technically sound answer to the original PostgreSQL snapshot-timing defect. The existing 31 mechanism proofs are valuable engineering work and should be retained.

But the combination of session locks, physical connection pinning, ticket lifecycle, cleanup and raw advisory-function revocation is expensive. It should not become the default merely because its mechanism tests passed. The package explicitly leaves capture/validation unwired, so changing direction now does not require undoing an implemented production workflow.&#x20;

gate1_admission_package.md

writeup4_review2.md



B. A transaction-scoped gate is a credible simplification.

The fundamental idea is:

- Capture acquires an exclusive transaction-scoped gate.
- Every payroll-affecting writer must acquire that same gate before changing relevant inputs.
- The gate remains held until capture and its validation decision have completed.
- If a writer gets the gate first, capture waits for it to finish and then reads its committed changes.
- If capture gets the gate first, writers wait and are ordered after the capture.

PostgreSQL documents that `READ COMMITTED` gives each command a snapshot as of that command's start. Consequently, after waiting for the gate, capture's subsequent SQL commands can see the transactions that committed before it acquired the gate. Transaction-scoped advisory locks remain held until transaction end and have no manual unlock operation.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



This removes the special need for pre-BEGIN session locks and XID tickets. It also means `DISCARD ALL` cannot release the gate during the capture transaction, because PostgreSQL prohibits that command inside a transaction block.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



C. The gate must be enforced by PostgreSQL, not by Go conventions.

All three reviews correctly reject a design in which the API merely promises to acquire the gate. Direct SQL as `app` is part of the threat model.

Database-enforced triggers or controlled write functions must ensure that direct DML cannot modify payroll inputs without participating. The same writer matrix from Prompt17 remains necessary.&#x20;

writeup4_review2.md

writeup4_review2.md



D. Snapshot authority remains a separate P0.

ADR-1's Go-canonical resolver can remain. But the database verifier must prove that the submitted content is complete and correct against authoritative records. The application role must not be able to fabricate snapshot headers, child rows, hashes, validation state or audit history.

The gate does not solve snapshot fabrication. The authority boundary and its proof tests remain required under every synchronization option.

## 2. What I would correct in the reviews

There are four points where I would not carry their conclusions forward unchanged.

### A. `DISCARD ALL` does not, by itself, invalidate Option A

DeepSeek and Z treat `DISCARD ALL` as effectively fatal to the ticket design. That conclusion is too strong.

PostgreSQL confirms two facts:

- `DISCARD ALL` releases session-level advisory locks.
- `DISCARD ALL` cannot execute inside a transaction block.&#x20;

  [image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

  postgresql.org+1



That distinction matters.

Consider two possible sequences:

Release and reacquire before the RR snapshot:

1. Acquire barriers and issue the ticket.
2. Execute `DISCARD ALL`.
3. Reacquire the barriers.
4. Begin the `REPEATABLE READ` transaction and establish its snapshot.
5. Verify the ticket and current barriers.

This does not inherently violate freshness. Any writer that completed while the barriers were released must finish before the barriers can be reacquired. The subsequent snapshot can see those commits. Writers arriving after reacquisition are blocked.

Release and reacquire after the RR snapshot:

This is dangerous because a writer might commit after the snapshot was established but before the barriers were reacquired.

However, `DISCARD ALL` cannot perform that release inside the transaction. The real question is whether `app` has another permitted path to release a session lock after the snapshot is established—for example, a callable SECURITY DEFINER release helper.

Therefore, the right attack test is not simply `DISCARD ALL`. It is:

> Can `app`, after establishing an RR snapshot, release or replace the admission barriers through any permitted function or operation, then pass ticket verification?

Test every callable release path. Verify that raw advisory unlock functions are denied, and that controlled wrappers refuse unsafe release. Also assert that `DISCARD ALL` inside the transaction fails as PostgreSQL specifies.

This correction matters: the XID-ticket design has not been disproved by the `DISCARD ALL` observation. Its complexity still makes Option B worth evaluating, but A should not be declared impossible based on an incorrectly ordered attack.

Claude's refinement of the attack is the valid one.&#x20;

writeup4_review2.md



### B. A version-counter re-read is not a substitute for writer enforcement

DeepSeek suggests detecting non-participating writers through a re-read or version counter. This can be useful as a secondary consistency check, but it cannot establish the invariant by itself.

A re-read can detect a change that commits between two reads. It cannot guarantee that no change commits after the final read but before the validation transaction commits.

A version counter is stronger only if every relevant write increments it atomically, and capture coordinates with that increment. At that point, the counter is itself part of the synchronization mechanism.

So retain a fingerprint or version recheck as defense-in-depth, not as the mechanism that makes uncoordinated writers safe.&#x20;

writeup4_review2.md



### C. The trigger proposal needs a more complete write-path specification

The reviews describe triggers as though adding a few `BEFORE INSERT/UPDATE` triggers closes the writer boundary.

That is incomplete. The gate must cover every operation that changes relevant inputs:

- `INSERT`, `UPDATE` and `DELETE`.
- Multi-row operations, `INSERT ... ON CONFLICT`, `MERGE` and `COPY FROM`.
- Cascading changes and partitioned tables.
- Employment, compensation, attendance, timesheet, SHG and applicable rule-pack changes.

`TRUNCATE` needs separate treatment: revoke it from `app` unless a separately controlled operation is provided. PostgreSQL treats `TRUNCATE` as a distinct table privilege.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org



The application role must not own these tables or be able to disable their triggers. The acceptance suite should test the effective privileges, not merely the trigger definitions.

### D. The transaction gate must cover the actual validation decision

This is the most important design refinement I would add to all three reviews.

Suppose capture acquires the gate, builds the snapshot and commits. Then, in another transaction, the system changes the payroll run from `draft` to `validated`.

A writer could commit between those transactions. The validation operation could then approve a snapshot that was already stale at the time of validation.

The synchronization boundary must therefore cover snapshot derivation, verification and the validation decision.

There are two acceptable designs:

1. Capture, proof-check and transition to `validated` in one gated transaction.
2. Capture a draft snapshot, then reacquire the gate during validation and reverify that its inputs and provenance still match before permitting `draft → validated`.

There is another same-transaction edge case: a hostile `app` caller might capture a snapshot and then modify payroll inputs later in that same transaction. A transaction-scoped lock is reentrant, so the later write would not block itself.

The design must prevent post-capture input mutation within that transaction. A database-enforced transaction marker or a top-level procedure that owns the capture transaction are possible approaches. This needs an explicit attack test.

## 3. The design I would take forward



Preferred experiment

&#x20;Option B: READ COMMITTED + transaction-scoped employer gate



I would use one employer-wide gate for the current MVP, rather than a separate gate per pay period. Effective-dated compensation, employment changes and retroactive corrections can affect multiple periods. A period-specific lock creates an unnecessary burden to identify every affected period correctly.

With one employer in the MVP, the gate can be a single canonical advisory-lock key. Future cross-employer transactions would need a documented multi-key acquisition order or a broader gate.

The intended protocol is:

```

Capture transaction:
  BEGIN ISOLATION LEVEL READ COMMITTED

  acquire employer transaction-scoped gate

  read authoritative payroll inputs
  resolve canonical content in Go
  verify completeness and provenance in PostgreSQL
  create immutable snapshot content
  perform validation checks
  transition payroll run to validated
  append validation audit

  COMMIT
  -- Gate releases automatically.

Writer transaction:
  BEGIN

  before changing any payroll-affecting input:
    acquire the same employer transaction-scoped gate
    through a database-enforced trigger or controlled writer

  perform the write
  COMMIT
  -- Gate releases automatically.

```

The critical ordering is:

- A writer that acquires the gate first finishes before capture can acquire it.
- Capture then executes subsequent reads under fresh `READ COMMITTED` command snapshots.
- A writer that attempts to modify an input after capture acquires the gate waits until capture commits.
- Capture does not release the gate before its validation decision.

PostgreSQL's documented `READ COMMITTED` behavior supports this design: each command sees transactions committed before that command began. The gate prevents participating writers from committing during the capture critical section.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



One caveat: a `BEFORE` trigger acquires the gate when the DML reaches that trigger. That protects the capture ordering, but it does not refresh an earlier statement snapshot in a writer's own `READ COMMITTED` transaction. Complex read-modify-write operations may therefore need their own controlled transaction entry point that acquires the gate before doing their business reads. PostgreSQL explicitly warns that complex updating commands can observe inconsistent snapshots under `READ COMMITTED`.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org



I would use the advisory transaction lock as the initial implementation candidate. A parent-row lock is worth comparing only if it materially improves the data model or enforcement. There is no need to build both mechanisms into the product.

## 4. What about SERIALIZABLE?

DeepSeek is right to keep SSI in the discussion; Claude and Z are right that it should not automatically become the next implementation.

The deciding issue is the business meaning of a payroll snapshot.

| Required meaning                                                                                                  | Appropriate candidate                            |
| ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| Snapshot represents a valid serializable point; overlapping writers may be ordered before or after it             | `SERIALIZABLE` with complete transaction retries |
| Writers must be held out until capture and validation finish, so no payroll input can commit during that decision | `READ COMMITTED` with transaction-scoped gate    |

Under `SERIALIZABLE`, PostgreSQL guarantees a serial execution equivalent for successfully committed serializable transactions, but it can abort transactions with serialization failures. It does not promise that an overlapping writer will be ordered according to its wall-clock commit time.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



The hostile-direct-SQL concern is real, but it is not automatically fatal to SSI. PostgreSQL's own documentation recommends checking transaction isolation in triggers if other isolation levels could undermine integrity checks. The experiment would have to prove that every relevant writer and capture operation rejects non-serializable transactions.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org



I would only pursue SSI if the business explicitly accepts the serialization-point definition. Do not silently weaken Prompt17's stronger capture guarantee to make SSI look simpler.

## 5. What stays unchanged regardless of the synchronization choice

These are design-independent requirements and should not be reopened:

Snapshot authority: Keep ADR-1's Go-canonical resolver, provided the database verifier proves completeness against authoritative records. No caller-supplied JSON pass-through.

Snapshot immutability: The `app` role must not directly fabricate or alter snapshot headers, child rows, hashes, validated states or audit history.

Writer coverage: Keep the complete writer matrix from Prompt17. Changing locks does not make compensation, SHG, attendance, terms, timesheets or rule-pack changes disappear.

Historical evidence: Preserve supersession and correction history. Do not rewrite source history to make the snapshot easier to construct.

Statutory discipline: `SG-MOM-OT-001` remains blocked. No statutory calculations become authorized merely because the synchronization design is simplified.

The authority work can proceed in parallel with the Gate 2 experiment at the design and test level. Do not allow it to be lost when `00013` is respecified.&#x20;

gate1_admission_package.md

writeup4_review2.md



## 6. The consolidated next move: Gate 2

I would time-box the decision rather than begin another large implementation.

Step 1 — One targeted attack against Option A.

Test the dangerous post-snapshot release/reacquisition scenario through every function actually callable by `app`. Confirm that `DISCARD ALL` is rejected inside the transaction and that no permitted wrapper releases the required locks after snapshot establishment.

Do not spend days trying to defeat a scenario PostgreSQL already prohibits.

Step 2 — Build the Option B experiment on PostgreSQL 16.15.

Use a small fixture and representative writer paths: compensation change, timesheet approval, and SHG instruction. Add the population/status and attendance paths as needed to exercise the whole gate boundary.

Test:

- Writer-first and capture-first interleavings.
- Direct DML as `app`, including operations that attempt to bypass ordinary Go services.
- Every relevant DML operation and the `TRUNCATE` restriction.
- Bounded wait behavior, rollback, cancellation and connection loss.
- Capture/validation atomicity.
- An attempt to modify source inputs after snapshot creation in the same transaction.
- Persisted snapshot, source and audit state after both success and failure.

Step 3 — Compare designs by proof, not preference.

The decision criteria should be:

1. Can the ordinary `app` role bypass the gate?
2. Can any writer commit a relevant change during the capture/validation critical section?
3. Is the snapshot complete at the defined boundary?
4. Can the verifier reject omitted, fabricated or stale input?
5. Does failure leave no partially validated state?
6. Is the resulting implementation materially simpler to operate and maintain?

Step 4 — Only then authorize the replacement `00013` scope.

Keep migrations `00001–00012` unchanged. Retain the Gate 1 package and experimental code as historical evidence, but do not carry unnecessary ticket and session-lock machinery into the production path if Option B passes.

## Final consolidated decision

| Topic                   | Consolidated decision                                        |
| ----------------------- | ------------------------------------------------------------ |
| XID-ticket mechanism    | Technically valid; retain as documented fallback             |
| `DISCARD ALL` objection | Valid observation, but not proof that A is impossible        |
| Option B                | Preferred next experiment                                    |
| SSI                     | Conditional alternative based on explicit business semantics |
| Append-only inputs      | Long-term data-model direction, not the immediate D1 fix     |
| Third capture role      | Unnecessary unless the two-role designs fail                 |
| Go-canonical resolver   | Retain, with rigorous database verification                  |
| Snapshot authority      | Remains P0 under every option                                |
| Capture and validation  | Must be one protected decision boundary                      |
| OT-001 and calculations | Remain blocked                                               |

My final recommendation: choose Option B as the leading architecture to test, not yet as an approved implementation. Preserve Option A until the precise attack test is complete. Add the same-transaction post-capture mutation test and the capture-to-validation boundary requirement to the specification before writing `00013`.

The central lesson is that the solution should make the correct path structurally unavoidable, while removing machinery that exists only to defend a more complicated path.