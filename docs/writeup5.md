This is a general database and software-architecture problem, not a payroll-specific problem. We encountered it through payroll because payroll combines several difficult requirements: many related records, concurrent changes, historical corrections, financial consequences and the need to prove exactly which information was used.

The same family of problems appears in banking, inventory, accounting, insurance, billing, healthcare, logistics, access control, financial reporting, data pipelines and many other systems.

But there is an important distinction:

- The underlying problem is general.
- The specific PostgreSQL failure we encountered is one particular manifestation.
- The XID-ticket or transaction-gate solution is a design choice, not a universal solution.

Let me build this from first principles.

# 1. The fundamental problem: the world keeps changing while you are trying to make a decision

Imagine a business that wants to calculate how much money it owes its employees at the end of a month.

To calculate payroll, it needs:

- Who was employed?
- What was each person's compensation?
- How much time did they actually work?
- Which timesheets were approved?
- Which deductions or instructions applied?
- Which rules were in force?

These facts live in different tables. Meanwhile, other users and processes are changing them.

One person corrects attendance. Another approves a timesheet. A manager changes compensation. An administrator updates an employee's status.

The payroll process is trying to answer:

> What was the complete, authoritative state of the business inputs at the point when I made this decision?

This is not merely a database query. It is a question about time, consistency, concurrency and authority.

The same question appears whenever a system must make a consequential decision from multiple changing facts.

## A simple illustration

Two simultaneous activities

Process A

Payroll capture

Reads employee records

Reads compensation

Reads attendance

Builds snapshot

Process B

Concurrent updates

Corrects attendance

Changes compensation

Approves timesheet

Commits changes

The question: Does Process A capture a complete state that actually existed, or a mixture of facts from different moments?

This is the central issue. A system can produce a perfectly valid-looking result that is nevertheless based on an incomplete or obsolete set of facts.

# 2. There are three different problems hiding inside this one

This is probably the most important thing to understand. People often use the words consistency, freshness and integrity interchangeably. They are not the same.

## A. Snapshot consistency: did I see one coherent version of the database?

Suppose an accounting system transfers $100 between two bank accounts.

Before the transfer:

- Account A: $500
- Account B: $200
- Total: $700

After the transfer:

- Account A: $400
- Account B: $300
- Total: $700

A report that reads Account A before the transfer and Account B after the transfer might see:

- Account A: $500
- Account B: $300
- Total: $800

That state never existed as a committed business state.

The database must give the reporting process a coherent view of the records. PostgreSQL's MVCC mechanism provides snapshots for this purpose. `REPEATABLE READ`, for example, gives successive queries in one transaction a stable view based on the transaction's initial snapshot.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



This problem is common in financial reporting, account balances, inventory valuation, consolidated statements and analytical exports.

## B. Freshness: did I see everything that should have been included?

Now imagine the accounting report starts, waits for another transaction to finish, and then reads its records.

The report's database snapshot might have been established before the wait. The other transaction commits, but the report continues seeing its older view.

The report can be internally consistent and still be stale.

This is the precise PostgreSQL difficulty we encountered. A `REPEATABLE READ` snapshot is not refreshed by waiting for a lock. PostgreSQL establishes it at the first relevant statement in the transaction.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org



The distinction is subtle:

- Consistent: everything I read belongs to one coherent database snapshot.
- Fresh: that snapshot includes the changes required by my business definition of the capture boundary.

A consistent snapshot is not necessarily the latest snapshot.

That distinction matters enormously in systems that close books, calculate obligations, reserve scarce resources or authorize irreversible actions.

## C. Authority: can I prove that the information was legitimately derived?

Imagine that the payroll application creates a snapshot containing:

- An employee who was never employed.
- A compensation amount that was never authorized.
- A timesheet that was never approved.

The application calculates a hash of the snapshot. The hash matches perfectly.

Does that prove the snapshot is correct?

No. A hash proves that the bytes match the hash. It does not prove that the data came from legitimate source records.

This is the authority problem.

It applies to virtually every serious system handling important records:

- Financial ledgers must establish the legitimacy of transactions.
- Insurance systems must establish the provenance of policy and claim information.
- Healthcare systems must establish the source and authorization of clinical records.
- Supply-chain systems must establish who issued and verified a certification.
- Access-control systems must establish which permissions were actually granted.

Our ADR-1 decision—Go performs canonical resolution while PostgreSQL verifies the submitted claims—is one particular way of dividing those responsibilities.

## The three guarantees compared

| Guarantee   | Question                                      | Typical failure                   |
| ----------- | --------------------------------------------- | --------------------------------- |
| Consistency | Did I see a coherent state?                   | Mixed versions of related records |
| Freshness   | Did I include the required committed changes? | Stale snapshot                    |
| Authority   | Were the inputs legitimate and complete?      | Fabricated or omitted records     |

These guarantees are related, but solving one does not automatically solve the others.

# 3. Where else does this problem occur?

Here are six examples. Notice that the business domains are very different, but the underlying questions are remarkably similar.

## 1. Banking and financial systems

[Financial statement review without the recalculation work | RobotX Studio](https://images.openai.com/static-rsc-4/420hTGQ09RUCVYR4xRPadcZtRNSLHotQFbcSHejGVwuq9XaAmzisiaTKfCEvyhlZIyKZul2QvIeiaPBVHKQil7ubYgpA0sXAKcBSsCh683S7vAa48eJUsOC3_S_rHwrXuH-AinEzzLngpWIXvYYwta0meGYezO54GQmakcjOD3g?purpose=inline)

Scenario: End-of-day financial close

A bank needs to calculate its end-of-day position. While it is doing so, transactions, reversals, fees and interest adjustments are still being recorded.

It needs a precisely defined closing boundary, a complete transaction population and a reproducible ledger balance.

Questions almost identical to ours:

- Did the closing report include all transactions inside its cutoff?
- Could a reversal commit while the report was being produced?
- Can a transaction be counted twice?
- Can an operator insert an unsupported adjustment into the closing report?

The relevant concepts include ledger immutability, transaction sequencing, reconciliation, cutoffs and authoritative snapshots.

## 2. E-commerce and inventory management

[Warehouse Errors: 10 Ways a WMS Cuts Mistakes (2026)](https://images.openai.com/static-rsc-4/yAHNUfUO47y8NDyU7Ly8YaQU9ffm7gSr7OZMTYSHKWzQMHknSP9MSLcmDdj3mZ-RavvRi2Ni0sVAIoO6GLoA9DpwRFjKTA8pZw0Nuh9kxAjz4qyIuIQys_wlc8h4uahwA7XLjp6MwZ7-uUdWMUM3_CKo0SQHrjBKDDOuERaOn4I?purpose=inline)

Scenario: Two customers buy the last unit

Two customers attempt to purchase the same remaining item at almost the same time.

The system must prevent both orders from successfully claiming stock that only exists once.

This is a related but slightly different problem.

The invariant is:

> Confirmed reservations must never exceed available inventory.

The system might use an atomic conditional update, row locks or a serializable transaction. It does not necessarily need to construct a large point-in-time snapshot.

The important lesson is that the right concurrency mechanism depends on the business invariant.

## 3. Insurance and claims processing

[The Importance of Recorded Statements in Insurance Claim Investigations](https://images.openai.com/static-rsc-4/79TbZdfpPw1PC5toXJJ8PGkoy1ChDgaqE-EgNw0mfoZ02tQSLOwqTyeoU-ReYN7fdQWGdrmOfKYbD1vJF1_0Fe-yyAruHgo-R8iU0Pa549rX7x6UmaLbWJ0Fkxis1iWpkuMb_UeD7Ot4JMmXoj0zZQJ_xb3JCtwfbzStQSOLqd0?purpose=inline)

Scenario: Approving an insurance claim

A claim is being assessed against policy terms, coverage, exclusions, premiums and previous claims. Meanwhile, the policy may be amended, a payment may be reversed, or another claim may be registered.

The system needs to establish:

- Which policy version governed the event?
- Which coverage and exclusions applied on the relevant date?
- Which claim records were available at the decision boundary?
- Which version of the evidence was actually assessed?

This resembles our effective-dated compensation and rule-pack problems. A later correction should not silently rewrite the evidence used in an earlier decision.

## 4. Healthcare and clinical systems

[ChatEHR: Innovative AI software developed by Stanford Medicine enables Clinicians to ask questions directly of Patient records](https://images.openai.com/static-rsc-4/7dY5x6p9DSiz_28XcpMx5c7Sg00SqZyFm83lZrjXStJI-9T6g22qRGYJ1YW_isZEfisWRmamA5t7kcRMgCECIkzCm58OI6-kZ3W7FUqlLEiFSN3T5sSKB8lYMxEsOrBCIY95q4alXv2RhMjGZicZNhHJXL4pdr9spw4RfKslLW0?purpose=inline)

Scenario: Medication decision

A clinician reviews medication, allergy and laboratory information while another process updates a patient's record.

The question becomes:

> Was the decision made using the appropriate and authoritative version of the patient's information?

This involves provenance, effective times, record corrections, access authority and consistency across related data.

Not every clinical system needs our exact locking protocol. But the underlying risk—making an important decision using incomplete or stale information—is the same.

## 5. Logistics and supply-chain management

[Transpo Logistics Co.,Ltd. | บริษัท ทรานสโป โลจิสติกส์ จำกัด](https://images.openai.com/static-rsc-4/BCS5ytoG5FPWVTB7hFkdO6e0Gt4Rjl9pY057cnAacUcCTICH78gGwtesfj200VMmJCec6h_vKl0CVsqlm6WK5LAhl7Gpdfs9lQoEgRTkcptT0Sgd_-WteeSzXHCmGIGgvKWalUGLq48uNnwWsEgGfRya7JkX0Oj6uyx4w1dA0L4?purpose=inline)

Scenario: Dispatching a shipment

Dispatch depends on inventory, warehouse allocation, order status, carrier availability, customs documents and delivery instructions.

These facts may change independently while the dispatch decision is being made.

The system needs to prevent dispatch based on a combination of records that never represented an eligible shipment.

It also needs to prove that the required approvals and documents were present when dispatch was authorized.

## 6. AI, data engineering and machine learning

[DualLife Quant | Intelligent Enterprise Solutions](https://images.openai.com/static-rsc-4/vw2QHU0k-h2WXsElXIiw-ZG-6IGHHNFXco_mlIumzvNvdzCr7jJNgSWLCH2D1ahq8LyctpuTm4GAxnz3iqzJaOmy2HgEY2TV2jQq35952RZZ-TjIR4MLZjabUTwkakrci0eoRhVCOxPwEqM8jqZn1xjs9sRZqV_0H4qdQnqkSfg?purpose=inline)

Scenario: Producing a training dataset

An organization builds a machine-learning dataset from customer transactions, account histories, labels and external data feeds while those sources are changing.

The questions:

- Which records were included?
- Which versions of the source data were used?
- Were labels added after the prediction period, causing data leakage?
- Can the same dataset be reconstructed later?
- Can the organization prove where each feature came from?

This is closely related to our snapshot authority and provenance problem.

The terminology changes—data lineage, reproducible pipelines, feature consistency and temporal leakage—but the underlying demand for trustworthy input state is familiar.

# 4. Does every application need this level of complexity?

Absolutely not. This is where architectural judgment becomes important.

Consider three different applications.

Application A: A simple contact directory

A user updates someone's phone number. Another user views the record.

The system usually needs ordinary transactional correctness and perhaps optimistic concurrency. It does not need a complete snapshot across twenty tables.

Application B: An analytics dashboard

A dashboard reads sales, customers and orders. Some data changes while the report is running.

The business may accept that the report represents the database at the time it started. It might be slightly behind real time, but that is acceptable. A stable read snapshot may be sufficient.

Application C: A payroll closing system

A payroll run is about to be validated. The organization needs an auditable, complete input set and must prevent relevant changes from slipping into the decision unnoticed.

Now the requirements are much stronger. We need to define an exact capture boundary, coordinate the writers, preserve provenance and protect the validation decision.

The same database features might be available to all three applications, but only Application C needs the full machinery.

## A useful way to classify applications

| Application requirement                            | Typical approach                                            |
| -------------------------------------------------- | ----------------------------------------------------------- |
| Read one record and update it                      | Ordinary transaction and constraints                        |
| Prevent two users overwriting each other's changes | Optimistic concurrency or row locking                       |
| Preserve a coherent report view                    | Consistent database snapshot                                |
| Prevent overselling or over-allocation             | Atomic updates, constraints or explicit locks               |
| Close a changing population of records             | Defined cutoff plus synchronization                         |
| Approve a consequential multi-record decision      | Snapshot, verification, provenance and controlled authority |
| Reconstruct exactly what happened historically     | Immutable versions, event history and temporal records      |

The important thing is to avoid using a more complicated mechanism than the invariant requires.

# 5. The deeper lesson: databases solve different kinds of time problems

Think about time in three different ways.

1\. What did the database look like?

A consistent snapshot at a defined observation point.

2\. Which changes must be included?

A defined cutoff or synchronization boundary.

3\. Which version of the business facts was authoritative?

Effective time, recorded time and correction history.

These are related, but independent questions.

For example, a compensation correction might be recorded today but be effective from the beginning of last month. The database must preserve both when the fact applies and when the system learned about it.

That is why effective-dated business data, immutable correction history and snapshot consistency are important in payroll, insurance and accounting.

# 6. Why our particular dilemma became so complicated

The difficult part is not that PostgreSQL cannot handle this problem. It can. The difficult part is that we combined several strong requirements with a particular way of accessing the data.

We chose:

1. Mutable operational records.
2. A `REPEATABLE READ` transaction.
3. A snapshot that must be sufficiently fresh for a consequential business decision.
4. An application role that cannot simply be trusted to follow Go-level conventions.
5. Multiple write paths, including direct SQL and database triggers.

That combination created a synchronization problem.

PostgreSQL advisory locks are particularly relevant here: they are application-defined coordination mechanisms. PostgreSQL does not automatically force every writer to acquire an advisory lock just because another transaction expects it. The application or database enforcement design must establish that participation.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



Our original solution therefore needed a complicated protocol to prove that the right locks had been acquired at the right time.

The proposed `READ COMMITTED` gate changes that design: subsequent statements can see commits made before they start, while participating writers are held out until the capture decision finishes. It is simpler for our workload, but it still requires comprehensive database enforcement and protection against same-transaction mutations.

This illustrates a general architectural principle:

Concurrency control and data visibility are two different problems. A lock controls who can proceed. A database snapshot controls what a transaction can see. A correct design must make those two mechanisms agree with the business rule.

# 7. The most important question to ask before designing any system

Whenever you build a system that aggregates information and makes a decision, ask:

> What exactly does this decision represent in time?

There are several legitimate answers:

A. The state at the beginning of the operation.

A report starts at 10:00 and represents the data visible at 10:00. Later changes appear in the next report.

B. The state after all previously active writers have finished.

The system waits for existing changes to complete, then establishes its observation boundary.

C. The state after every relevant writer has been excluded until the decision is complete.

The system prevents concurrent changes to the relevant data throughout the decision.

D. A logically serializable state.

The system guarantees that the successful concurrent operations could be understood as having occurred in some valid serial order. This does not necessarily mean the snapshot contains every change committed before the final wall-clock instant.

These definitions can produce different architectures, even when the underlying business operation looks identical.

PostgreSQL's `SERIALIZABLE` isolation provides the fourth kind of guarantee, subject to transaction retries; it does not automatically promise the third.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org+1



Getting this definition wrong at the beginning is one of the most expensive architectural mistakes a team can make.

# 8. What this means for how we should design future products

I would make this a standard part of our architecture process, not something we remember only when payroll breaks.

For every significant business operation, identify five things:

1. Business invariant: What must never become false?
2. Observation boundary: At what point in time is the decision supposed to be valid?
3. Concurrent writers: Which operations can change the information involved?
4. Authority: Who is allowed to create, approve or modify the resulting record?
5. Evidence: How can we reconstruct and prove what happened?

Then choose the simplest appropriate combination of database constraints, transactions, locking, versioning and immutable history.

This is relevant to our future Go training platform too. A realistic production-support exercise could involve an inventory oversell, a payroll snapshot that missed a correction, a billing run using inconsistent account states, or an authorization decision based on stale permissions. Those are all variations of concurrency and data-integrity failures that Tier 2 and Tier 3 engineers encounter.

One final distinction: although the concepts generalize across PostgreSQL, MySQL, SQL Server, Oracle and other database systems, the exact behavior of isolation levels, locks, triggers and transaction boundaries differs. Always reason from the database actually being used rather than transferring PostgreSQL assumptions blindly.

## My biggest takeaway

# The business operation determines the invariant. The invariant determines the consistency requirement. The consistency requirement determines the architecture.

Do not start by asking: Which ORM, framework or locking mechanism should we use?

Start by asking: What must be true about the business state when this operation succeeds, even when everything else is changing concurrently?

That is the transferable lesson from our payroll dilemma. We did not discover an obscure payroll problem. We discovered a fundamental challenge in building trustworthy systems that make decisions from changing information.



## locked in

Locked as a standing architectural principle for WorkforceOps Lab and our future system-design work.

We will use the following as our governing design philosophy:

# Invariant-First, Time-Aware Architecture

> The business operation determines the invariant. The invariant determines the consistency requirement. The consistency requirement determines the architecture.

Before choosing a framework, ORM, database feature, locking mechanism or implementation pattern, establish:

1. Business invariant: What must always remain true?
2. Observation boundary: At what point in time must the decision be valid?
3. Concurrent writers: Which operations can change the information involved?
4. Authority: Who is permitted to create, approve or modify the resulting record?
5. Evidence: How can we reconstruct and prove what happened?

We will explicitly distinguish three independent guarantees:

- Consistency: Did the operation observe a coherent state?
- Freshness: Did it include the changes required by the defined observation boundary?
- Authority: Were the inputs legitimately derived, complete and authorized?

We will also follow these principles:

- Choose the simplest mechanism that demonstrably satisfies the business invariant.
- Do not introduce sophisticated concurrency machinery without establishing that the business semantics require it.
- Do not confuse locking with visibility or hashes with provenance.
- Treat database-specific behavior as database-specific; never blindly transfer assumptions between PostgreSQL, MySQL, SQL Server or other systems.
- Enforce critical invariants at the authoritative boundary, including against direct SQL.
- Prove both concurrent execution orderings and inspect persisted state.
- Preserve historical evidence and correction lineage.
- Never weaken a business guarantee merely to simplify implementation without an explicit decision.

The governing question for every significant design decision:

> What must be true about the business state when this operation succeeds, even when everything else is changing concurrently?

This becomes our standard architecture-review lens—not just for payroll, but for HR, WFM, accounting, banking, inventory, billing, healthcare, logistics, data pipelines and other systems we design.