## prompt

in light of above, educate me per below where we are now at major decision points that must be addressed now in order to minimize accidental complexity, raise the issues that I need to decide on.

How the industry usually handles it

The common approach is business-level, not technical. Accounting systems close a period: once a period is closed, postings into it are rejected, and corrections are made as new adjustment entries in a later period. Payroll systems typically use a cut-off date, and late changes flow into the next run as retro adjustments. I’m describing the general practice, not any specific product.

That is the process-level version of your gate and your append-only option. It works because the business accepts “late changes go in the next run” instead of requiring that no late change can ever be missed.

**##** **A** **ladder** **of** **rigor**
1\. Ordinary CRUD. Default isolation and nothing special.
2\. Consistent report. A repeatable-read snapshot where “as of when I started” is acceptable.
3\. Cut-off completeness. A close-the-window state, or a gate lock plus read-committed reads.
4\. Evidence grade. Add append-only storage, hashes and restricted writers.
5\. Adversarial writers. The database enforces who can write and what, even against direct SQL.

Most products stop at rung 2 or 3. Your prompt17 declares rung 5 for payroll. The real design question is which rung your first release needs, which is the same question I raised at the end of the last two answers.

When you meet a “snapshot” feature in any project, ask three things:&#x20;

1\. what is the cut-off
2\. who can write
3\. who relies on the output


## answer

# Where we are now: the business decisions are more important than the remaining locking decisions

We have reached an important point in WorkforceOps architecture.

Gate 2 has given us a preferred technical synchronization design: `READ COMMITTED` with a transaction-scoped employer gate, enforced by PostgreSQL. Option A, the XID-ticket/session-lock design, has been retired as the production candidate. However, production implementation remains blocked by snapshot-authority, validation-lifecycle and full writer-coverage findings.&#x20;

gate2_synchronization_experiment.md

gate2_synchronization_adr.md



But there is a more fundamental question we have not fully settled:

Are we trying to make payroll include every change committed before the instant of validation, or are we trying to enforce a business-defined payroll cutoff after which late changes follow a controlled correction process?

Those are different business requirements. They lead to different architectures and different costs.

The industry pattern you described—period closing, rejecting ordinary postings into closed periods, and using subsequent adjustments—is important because it can transform the problem.

Instead of requiring the system to continuously capture a moving target, the business establishes a boundary after which the relevant population of records is no longer ordinarily mutable.

That does not eliminate database concurrency requirements. The transition into the closed state must still be race-safe. But it can make the invariant much simpler.

I would now put the following decisions in front of the business owner before authorizing the next production implementation stage.

# 1. Understand the rigor ladder before choosing a rung

First, I would make one correction to the ladder: these are not strictly cumulative levels. Evidence and adversarial security are partly independent dimensions. A system can have immutable records without a strict freshness guarantee, or strict synchronization without a sophisticated historical evidence model.

Nevertheless, the ladder is an excellent way to classify the requirements of each business operation.

## The five levels of rigor

Rung 1 — Ordinary CRUD

Basic

Records are created, read and updated using ordinary transactions and constraints.

Example: updating an employee's contact information.

Rung 2 — Consistent reporting

Moderate

A report sees a stable database view. It is acceptable for the report to represent the data visible when its observation began.

Example: generating a sales report as of 10:00 AM.

Rung 3 — Business cutoff completeness

Controlled

The business defines a cutoff or closing boundary. Relevant records cannot be ordinarily posted into the closed period; late changes follow an explicit correction process.

Example: closing a payroll period before calculating final pay.

Rung 4 — Evidence-grade records

Auditable

The system preserves immutable inputs, corrections, provenance, rule versions, snapshots and reproducible calculations.

Example: reconstructing exactly which timesheet, compensation and rule versions produced a payroll result.

Rung 5 — Adversarial writer protection

Strongest

Critical invariants are enforced at the database capability boundary, including against direct SQL from the ordinary application role.

Example: even a compromised application component cannot directly fabricate a validated payroll snapshot.

Notice something important: Rung 3 and Rung 4 answer different questions.

- Rung 3 establishes which business inputs are eligible for a particular decision.
- Rung 4 makes it possible to prove what happened and reconstruct the decision later.
- Rung 5 establishes who can and cannot manipulate that process.

You might require Rung 2 for operational dashboards, Rung 3 for payroll cutoff, Rung 4 for finalized payroll evidence and Rung 5 for the database operations that protect critical payroll decisions.

I would not impose Rung 5 on every operation in the application. I would apply it to the boundaries whose compromise could materially corrupt money, legal obligations or authoritative business records.

One qualification: your existing WorkforceOps engineering standard treats direct SQL bypasses as a serious threat. That remains the current assumption. Moving to a lower rung requires an explicit change to the threat model, not merely a desire to reduce implementation work.

# 2. The three decisions that determine everything else

Your three questions are exactly the right starting point:

1. What is the cutoff?
2. Who can write?
3. Who relies on the output?

I would expand them slightly so that they become actual business decisions rather than general discussion points.

## Decision 1 — What exactly is the cutoff?

There are several meanings of cutoff, and they are often confused.

| Possible cutoff       | Meaning                                                                 |
| --------------------- | ----------------------------------------------------------------------- |
| Event cutoff          | Include events that occurred before a specified time                    |
| Effective-date cutoff | Include facts that apply to the business period                         |
| Recording cutoff      | Include facts recorded in the system before a deadline                  |
| Approval cutoff       | Include only records approved by a deadline                             |
| Snapshot cutoff       | Include the authoritative database state observed at a defined boundary |
| Financial close       | No ordinary postings into the closed accounting or payroll period       |

For example:

An employee worked eight hours on October 30. The attendance system did not receive the clock-out event until November 2. The manager approved the timesheet on November 3. Payroll closes on November 5.

Which payroll period should include those eight hours?

There is no purely technical answer. The business must decide.

You need to distinguish:

- When the work happened.
- When the system learned about it.
- When it was approved.
- When it was included in payroll.

Those dates can differ.

Questions you must decide:

- Is the payroll cutoff determined by the event date, approval date, recorded date or an explicit closing date?
- Can a timesheet approved after cutoff enter the original run?
- Is there a grace period for late submissions?
- Can an operator extend the cutoff?
- Who is authorized to extend or close it?
- Is the cutoff applied per employer, pay group, employee, client or payroll run?
- What timezone governs the cutoff?
- What happens when a cutoff falls on a weekend or public holiday?
- Can the period be reopened?
- Who can authorize reopening?

My recommended default

Define an explicit, employer-scoped payroll input cutoff, governed by `Asia/Singapore` for the Singapore employer. Specify the treatment of late-arriving and retroactively effective records separately.

Do not equate the payroll period end date with the operational cutoff time.

## Decision 2 — What happens when something changes after the cutoff?

This is probably the single most valuable business decision for reducing accidental concurrency complexity.

Consider two approaches.

Approach A — Reopen and recalculate

A correction arrives after the payroll cutoff. The system reopens or recalculates the original payroll run using corrected data.

Advantages: the original period can reflect the corrected business facts.

Costs: prior calculations and approvals may need invalidation, versioning and revalidation. Payment and accounting consequences become more complicated.

Approach B — Keep the period closed and create an adjustment

A correction arrives after cutoff. The historical period remains closed. The system creates a linked adjustment that is processed in a later payroll run, while preserving the original effective date and correction history.

Advantages: historical runs remain stable, and late changes have a predictable processing path.

Costs: the system needs retroactive adjustment accounting and a way to explain differences between original and corrected entitlements.

These approaches can coexist at different lifecycle stages. A correction received before final approval might permit recalculation; a correction received after payment might require an adjustment run.

Questions you must decide:

- When is a payroll run considered closed?
- Is the period closed before snapshot capture, after validation or after payment?
- Can ordinary users reopen a closed period?
- Are corrections allowed to alter the original payroll run?
- When must corrections become separate successor records?
- Does a late correction retain its original effective date?
- Which payroll run receives the financial adjustment?
- What happens if the correction increases pay?
- What happens if it decreases pay?
- How are already-paid amounts reconciled?
- What happens when a correction affects a previous tax, contribution or reporting period?
- Does a correction require new approval?
- Can corrections be batched into a single adjustment run?
- Can an adjustment itself be corrected?

My recommended default

Use a lifecycle with different correction policies:

- Before cutoff: ordinary operational correction.
- After cutoff but before payroll finalization: controlled reopening or a replacement run.
- After finalization or payment: immutable historical result plus a linked adjustment or correction process.

This is a business-process decision that should drive the database model, not the other way around.

Important: "Late changes go into the next payroll run" must not mean silently changing the financial period to which the obligation belongs. Preserve the original effective period, the correction date and the period in which the financial adjustment is processed.

## Decision 3 — Who relies on the output, and how much do they trust it?

Not all outputs deserve the same protection.

Consider four consumers:

| Consumer                     | Example                                    | Required confidence |
| ---------------------------- | ------------------------------------------ | ------------------- |
| Internal operator            | Preview of expected payroll                | Moderate            |
| Manager                      | Approval of time and attendance            | High                |
| Finance/payroll              | Authoritative payroll calculation          | Very high           |
| Employee, auditor, regulator | Payslip, statutory records, audit evidence | Very high           |

A draft payroll preview can be recalculated. A validated payroll run may authorize further financial processing. A paid payroll result may become legally or commercially consequential.

Questions you must decide:

- Is the output merely informational?
- Can users make operational decisions based on it?
- Does it authorize a payment or financial posting?
- Does it create a legal obligation?
- Can it be externally relied upon?
- Must it be reproducible years later?
- Can the result be restated?
- Who can certify or approve it?
- What evidence is necessary to defend it?
- What damage results if it is wrong?

My recommended default

Give each output an explicit trust classification. Do not treat a preview, validated snapshot, approved payroll and paid result as the same artifact.

# 3. Major architectural decisions you should make now

The previous three decisions establish business meaning. The following decisions translate that meaning into a system architecture.

I have ordered them by how many downstream decisions they affect.

## Decision 4 — What is the authoritative unit of closure?

This is more significant than it first appears.

What exactly are we closing?

- A payroll period?
- An employer?
- A pay group?
- A client program?
- An individual employee's timesheet?
- A payroll run?
- A collection of approved input records?

For WorkforceOps, I recommend distinguishing three boundaries:

Operational period: The dates during which work, attendance and employment events happen.

Payroll input cutoff: The deadline after which ordinary changes cannot enter the payroll population without a controlled exception.

Payroll run: The particular calculation, validation, approval and financial outcome derived from the eligible inputs.

Why does this matter?

An employee can work for multiple client programs. A compensation correction may affect several payroll periods. A cross-midnight attendance event may span two business dates. Closing one client program should not accidentally close the employee's entire employment record.

Your decision: Is the payroll cutoff employer-wide for the MVP, or can you have different cutoffs by pay group or employee population?

My recommendation: employer-wide initially, with pay-group-specific policies only when a genuine business requirement exists. Avoid implementing flexible cutoff dimensions prematurely.

## Decision 5 — Which data is operational, and which data is authoritative?

One of the common architectural mistakes is treating every table as equally trustworthy.

For example:

- A raw clock event is evidence that a device recorded something.
- A paired attendance interval is an interpretation.
- An approved timesheet is an authorized business record.
- A payroll input snapshot is a frozen calculation input.
- A calculated payroll line is a financial result.

These are not interchangeable.

The same principle applies elsewhere:

- A bank transaction request is not a posted ledger transaction.
- A warehouse scan is not necessarily confirmed inventory ownership.
- An insurance claim submission is not an approved claim.
- An access request is not an assigned permission.

Questions you must decide:

- What is the original evidence?
- What is derived from that evidence?
- What is reviewed or approved?
- What becomes authoritative?
- What is allowed to change after approval?
- What must remain immutable?
- What is the authoritative source when two systems disagree?

My recommendation: explicitly classify every important record into one of four categories:

1. Raw evidence.
2. Interpreted or calculated data.
3. Approved business records.
4. Frozen decision artifacts.

This distinction should be reflected in domain models and database permissions.

## Decision 6 — Who is trusted to write?

This is the architecture decision most closely related to Prompt17 and Gate 2.

There are three possible trust models:

| Model                         | Trusted party                                                           | Consequence                                                 |
| ----------------------------- | ----------------------------------------------------------------------- | ----------------------------------------------------------- |
| Application-trusted           | Go services enforce the business rules                                  | Simpler database permissions and application architecture   |
| Database-enforced             | PostgreSQL enforces critical invariants against the ordinary `app` role | More triggers, functions, constraints and privilege testing |
| Separate privileged execution | A restricted process or role owns especially sensitive operations       | Stronger capability isolation, more operational complexity  |

Your current architecture uses the second model for critical invariants. I recommend retaining it for authoritative payroll inputs, snapshot creation and validation.

But make one important distinction: do we need protection against accidental bugs in our own Go services, arbitrary SQL executed as `app`, or compromise of the entire application host? These are different threat models.

Questions you must decide:

- Is `app` trusted to follow all application protocols?
- Is direct SQL by `app` considered a supported or hostile write path?
- Can support engineers execute SQL in production?
- Can administrators manipulate authoritative records?
- Who can create a payroll snapshot?
- Who can mark it validated?
- Who can approve and finalize it?
- Can a database administrator alter the audit trail?
- What level of privileged access must be independently audited?

My recommendation: make the database enforce the invariants that must remain true regardless of which Go service, CLI command, trigger or authorized direct-SQL operation attempts the write. Retain separate operational permissions for ordinary CRUD.

Do not build a separate privileged service for every business operation.

## Decision 7 — What is the history and correction model?

This determines the foundational data schema.

You have three broad options:

Mutable records: Update a record in place and preserve only limited audit information.

Versioned records: Each material change produces a new version, with explicit successor relationships.

Append-only event history: Preserve every relevant event, then derive the current business view from the history.

These are not mutually exclusive. A system can use mutable operational projections, append-only correction history and immutable financial snapshots.

For WorkforceOps, I recommend versioned and correction-aware records for legally or financially meaningful information, while keeping ordinary operational fields mutable where that is safe.

Do not introduce full event sourcing across every module merely because it sounds more auditable.

Questions you must decide:

- Which records require full historical reconstruction?
- Which updates can be overwritten?
- Which corrections need successor records?
- Which relationships must be preserved?
- Must the system reconstruct what was known at a previous time?
- Must it reconstruct what was effective at a previous time?
- How are historical snapshots linked to their source versions?
- How are old records retained, archived or anonymized?

## Decision 8 — What is the unit of transaction and synchronization?

This is where the current Gate 2 architecture belongs.

Once cutoff semantics and correction policy are agreed, we can determine whether a technical gate is necessary.

For example, if the business closes the payroll input period first, rejects ordinary late postings, and routes later changes into controlled adjustment records, the snapshot might not need the stronger "include every change committed before validation" requirement that originally motivated Prompt17.

But the closing operation itself must be atomic with respect to concurrent writers. A write that starts before closure must either commit before closure and be included, or be rejected/handled as a post-closure correction. It must not slip through after the close.

Questions you must decide:

- Is the technical gate required only when closing the period?
- Must it remain held throughout snapshot creation and validation?
- Is a draft preview allowed to use a consistent but potentially older snapshot?
- Is validation a separate business transaction?
- Can an input change between capture and validation?
- What happens if it does?
- Can a writer move data between employers or pay groups?
- Are cross-employer transactions permitted?

My recommendation: keep the selected Option B architecture as the current implementation target until a business cutoff design is formally approved. Do not simply remove the gate based on the existence of a period-close status.

A closed-period policy and the database gate solve complementary problems.

## Decision 9 — Which business rules change over time?

This is particularly important for systems operating under legislation, contracts, organizational policies or customer-specific rules.

For example, Singapore payroll rules are not just formulas. They have applicability conditions, effective dates, exemptions, limits, evidence and interpretation.

Questions you must decide:

- Which rules are external legal requirements?
- Which are internal policies?
- Which are contractual agreements?
- Who owns each rule?
- Which source is authoritative?
- When does a new rule become effective?
- Does a new rule apply to existing historical periods?
- Can a policy be overridden?
- Who approves exceptions?
- Can calculations be reproduced under the old rule version?
- What happens when the source is unclear or contradictory?
- What must happen before a rule is activated?

My recommendation: retain versioned rule packages with evidence and independent verification. Do not mix a rule's publication date, effective date and activation date.

This is particularly important when a correction received in November changes an entitlement effective in October. The system must know which rule applied to October, not simply use whatever rule is currently active.

## Decision 10 — What happens when external systems disagree or fail?

This decision is commonly postponed until integration development, when it is already expensive.

Consider a WFM platform integrating with HR, attendance devices, payroll, banking and accounting.

Suppose the payroll system sends a payment instruction to the bank. The bank processes it, but the acknowledgment is lost.

Should the system send the instruction again?

The answer cannot be "just retry." It needs a defined idempotency and reconciliation model.

Questions you must decide:

- Which system owns employee identity?
- Which system owns attendance evidence?
- Which system owns approved timesheets?
- Which system owns the financial result?
- What happens when an integration is unavailable?
- Can messages arrive late or out of order?
- Can the same message be delivered twice?
- What happens when a request succeeds remotely but our response is lost?
- Who resolves conflicting records?
- How is an external transaction reconciled?

My recommendation: establish system-of-record ownership for each important data category, and design idempotency and reconciliation before building integrations.

## Decision 11 — What is the organizational and legal boundary?

This is one of the most expensive decisions to change later.

A BPO employer may serve multiple clients. Those clients may have independent programs, service windows, contractual obligations, reporting requirements and access restrictions.

But the employer remains responsible for employment and payroll obligations.

Do not confuse a client boundary with an employer boundary.

Questions you must decide:

- Is the system for one legal employer or multiple legal employers?
- Can one employer operate multiple branches or legal entities?
- Can one employee work across several clients or programs?
- Can employees transfer between employers?
- Can records be shared between organizations?
- Can customers inspect operational records without seeing employment or compensation details?
- Are different countries or jurisdictions in scope?
- Are legal rules attached to the employer, employee, location or work performed?
- Can a customer relationship change without changing the employment relationship?
- What happens when an employer is acquired, merged or dissolved?

My recommendation: preserve the employer as the primary legal boundary. Treat BPO clients, programs, campaigns and queues as operational boundaries.

Your current one-employer MVP is reasonable. However, ensure that the data model does not accidentally equate `client_id` with the legal employer identifier.

## Decision 12 — What must be calculated, reconciled and reproduced?

A calculated result is not automatically trustworthy because its arithmetic is correct.

An inventory valuation, payroll calculation, insurance assessment or accounting close must be explainable in terms of inputs and rules.

Questions you must decide:

- Which calculations are authoritative?
- Which calculations are previews?
- Which calculations require exact decimal arithmetic?
- Where is rounding applied?
- Which rule version is used?
- Can a result be independently recalculated?
- What is the permitted variance?
- What happens when independently calculated results disagree?
- Can a result be corrected without rewriting its source evidence?
- Does a recalculation produce a new version?
- Must historical financial outputs remain reproducible?
- What is the reconciliation evidence?

My recommendation: treat every consequential calculation as a versioned decision artifact with a defined input set, rule provenance and reconciliation path.

## Decision 13 — What are the audit and evidence requirements?

This is not simply a logging question.

An application log tells you something happened. A business audit trail must establish which authorized actor or process performed an action, what business state changed, why it was permitted, what evidence supported it and what resulted.

Questions you must decide:

- Which operations require audit records?
- Which fields must be captured?
- Does the audit record identify both the human actor and the executing service?
- Can audit records be modified by the application?
- Must before-and-after values be retained?
- Are approvals independently recorded?
- Can an auditor reconstruct an earlier decision?
- Are hashes or digital signatures required?
- How long must records be preserved?
- Can evidence be exported independently?
- What happens if audit persistence fails?

My recommendation: distinguish operational logs, business audit events and immutable evidence. They serve different purposes.

## Decision 14 — What must happen when things go wrong?

This is where business analysis and operational engineering must meet.

Questions you must decide:

- What happens when an approval process fails halfway?
- What happens when a financial operation succeeds but the user receives a timeout?
- Which operations may be safely retried?
- Which operations require manual intervention?
- Can a failed workflow be resumed?
- What happens when a job remains pending beyond its expected duration?
- Who owns unresolved exceptions?
- What is the escalation process?
- How long can the system remain unavailable?
- How much data loss is acceptable?
- How do we prove that a backup can actually be restored?
- How does support investigate a failure without altering the evidence?

My recommendation: define recoverability alongside the normal lifecycle, not after the first production incident.

# 4. The decisions I would put on your desk now

The previous sections are the full discovery checklist. You do not need to make every decision immediately.

These are the decisions I consider foundational for WorkforceOps. Each one influences several downstream architectural choices.

## Priority decision register

Recommended defaults are my starting recommendations, not decisions already approved by you.

Decision 1 · Highest priority

What defines payroll cutoff?

Choose among event date, effective date, approval date, recording deadline or explicit period close.

My recommendation: Explicit employer-scoped cutoff with a defined local timestamp and separate rules for late arrivals.

Decision 2 · Highest priority

What happens to corrections after cutoff?

Choose ordinary reopening/recalculation, controlled replacement run, or a subsequent adjustment run according to lifecycle stage.

My recommendation: Recalculate before finalization under control; after finalization, use linked adjustment records.

Decision 3 · Highest priority

When does payroll become final?

Distinguish draft, captured, validated, approved, finalized and paid.

My recommendation: Treat these as separate lifecycle transitions, with immutable decision artifacts after validation and finalization.

Decision 4 · Highest priority

How much adversarial protection is required?

Confirm whether direct SQL under `app` is within the threat model for authoritative payroll records.

My recommendation: Retain database-enforced protection for critical writes and decisions.

Decision 5 · High priority

What constitutes authoritative payroll input?

Define whether raw attendance, interpreted attendance, approved timesheets or another record becomes eligible.

My recommendation: Approved business records feed payroll; raw evidence remains available for provenance and correction.

Decision 6 · High priority

How are retroactive changes represented?

Define effective date, recorded date, affected period, adjustment period and approval requirements.

My recommendation: Preserve both effective and recorded time, with successor/correction lineage.

Decision 7 · High priority

What is the legal organizational boundary?

Confirm one legal employer for MVP, and distinguish employers from BPO clients and programs.

My recommendation: One operational employer for MVP; employer-scoped design; no premature multi-employer processing.

Decision 8 · High priority

What evidence must be reproducible?

Define whether historical payroll outputs, source records, rule versions and approvals must be reconstructed.

My recommendation: Reproduce every validated/finalized payroll decision from immutable evidence and pinned rule versions.

These eight decisions should be made before we finalize the production payroll snapshot schema and migration scope.

# 5. The most important connection: business policy can eliminate technical complexity

Let's compare two designs.

Design A — Continuous freshness requirement

Business requirement:

> Include every relevant change committed before the payroll validation decision, regardless of when it was submitted.

Architectural consequences:

- Synchronize all participating writers with capture.
- Control the transaction boundary.
- Protect capture and validation atomically.
- Verify the complete population.
- Protect every direct database write path.
- Preserve the historical decision artifact.

This is the stronger technical requirement that motivated Prompt17 and Gate 2.

Design B — Business-defined closing process

Business requirement:

> Include every eligible and approved input received by the payroll cutoff. After closing, changes follow a controlled correction or adjustment process.

Architectural consequences:

- Define and enforce the cutoff.
- Establish a race-safe period-closing operation.
- Reject or redirect ordinary post-close writes.
- Preserve late corrections and their original effective dates.
- Produce a snapshot from the closed population.
- Validate the resulting payroll artifact.

This can substantially reduce the duration and scope of synchronization.

Here is the crucial qualification:

Design B does not eliminate concurrency control. It relocates it.

The period-close operation must still coordinate with writers. A transaction that checked that the period was open must not be allowed to commit an ordinary posting after the close has succeeded.

The difference is that you are controlling a specific business transition instead of continuously trying to guarantee an increasingly broad freshness condition throughout an open-ended workflow.

That is why I favor making the business cutoff explicit before further production implementation.

## 6. What I would do in our next architecture review

I would conduct the next review in this order:

1. Define payroll business semantics.

   Document cutoff, late submission, correction, reopening, finalization and adjustment policies.
2. Define the payroll artifact lifecycle.

   Separate draft preview, captured snapshot, validation, approval, finalization and payment.
3. Map the actual authoritative input population.

   Decide which evidence and approved records feed payroll, including exclusions and exceptions.
4. Determine the required rigor per operation.

   Decide which workflows need Rung 2, 3, 4 or 5. Do not impose the same guarantee on every module.
5. Revisit the synchronization contract.

   Keep Option B as the selected technical direction, but determine whether the business cutoff can narrow its scope.
6. Close Gate 2.1.

   Fix the hash-to-content binding, snapshot/run identity, direct decision-table insertion, run lifecycle, transaction-marker XID and complete production writer coverage. These remain implementation blockers regardless of the business cutoff decision.
7. Only then approve production migration `00013`.

   The schema should reflect settled business semantics, not force the business to adopt whatever lifecycle the experiment happened to implement.

## 7. My overarching recommendation

I would establish one additional principle alongside our previously locked Invariant-First, Time-Aware Architecture:

# Business Closure Before Technical Synchronization

Define when a business operation becomes closed, what changes remain permissible afterward, and how late changes are represented before choosing the database machinery that enforces those decisions.

The system should synchronize only what the business actually requires it to synchronize. It should preserve more history only where the business, audit, legal or operational requirements justify it.

This is how we avoid accidental complexity: not by making correctness weaker, but by making the business rules precise enough that the technical solution becomes smaller.

The first decision I would ask you to settle is this:

Should WorkforceOps treat a payroll input cutoff as a genuine business close—after which ordinary changes are prohibited and late corrections flow through a controlled adjustment process—or must a payroll run always include every relevant change committed before its validation transaction completes?

That one decision will determine whether we can substantially narrow the synchronization machinery, or whether the stronger Gate 2 boundary must remain essential to the first release.