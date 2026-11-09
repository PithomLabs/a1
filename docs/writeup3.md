The easiest way to understand WorkforceOps is to think of it as one business database maintaining the complete history of an employee's working relationship, the work that was scheduled, the work that actually happened, and the financial obligations that eventually result.

HR, WFM and Payroll are three distinct business domains, but they share the same employees, employer, dates, operational records and evidence.

The important architectural decision is that we are not building three separate applications. We are building one Go modular monolith with PostgreSQL as the authoritative data store, with each domain responsible for a different part of the business.

Below is a technical guide to how the whole system fits together, including the database concepts, relationships, transactions, rule packs, population barriers and payroll snapshots.

# WorkforceOps Lab

# Understanding the Integrated HR, WFM and Payroll Backend

## A Technical Guide to Its Database Architecture, Business Domains and Data Flow

**Organization:** Pithom Labs\
**Repository:** `github.com/PithomLabs/a1`\
**Initial jurisdiction:** Singapore\
**Architecture:** Go modular monolith, PostgreSQL, REST API and CLI\
**Product model:** Single employer operationally; multi-employer-ready database design\
**Reporting point:** 9 November 2026

---

# 1. The Big Picture

WorkforceOps integrates three business systems:

1. Human Resources (HR).
2. Workforce Management (WFM).
3. Payroll.

In a conventional enterprise environment, these might be separate software products with separate databases and integrations.

We deliberately combine them into one application and one database.

This is not because the three domains are identical. They are not.

It is because they operate on the same underlying business reality: employees working for an employer under particular employment conditions, performing scheduled work, recording actual attendance and eventually receiving remuneration.

The complete business relationship looks like this:

```
                  EMPLOYER
                     |
          +----------+----------+
          |                     |
          v                     v
         HR                    BPO
          |                 OPERATIONS
          |                     |
          v                     v
    EMPLOYMENT              WFM
      TERMS              SCHEDULING
          |                     |
          |                     v
          |                 PUBLISHED
          |                  ROSTER
          |                     |
          +----------+----------+
                     |
                     v
                ATTENDANCE
                     |
                     v
                TIMESHEETS
                     |
                     v
               PAYROLL INPUTS
                     |
                     v
              PAYROLL READINESS
                     |
                     v
              PAYROLL GOVERNANCE
                     |
                     v
              IMMUTABLE SNAPSHOT
                     |
                     v
              PAYROLL CALCULATION
                     |
                     v
                PAYSLIP / OUTPUT
```

The diagram is a business-data flow, not a claim that every stage is already implemented.

**HR establishes who the employee is and under what employment conditions they work.**

**WFM establishes what work the employer needs and schedules the employee to perform.**

**Attendance establishes what actually happened.**

**Timesheets establish which work has been reviewed and approved.**

**Payroll determines what financial obligations result from the approved information and applicable rules.**

This is the central relationship.

# 2. The Application Architecture

## 2.1 One Go application, not three independent systems

The current architecture is a modular monolith.

```
                  WORKFORCEOPS
                 Go Executable
                       |
          +------------+------------+
          |            |            |
          v            v            v
         CLI        REST API      Internal
                                  Services
                                     |
              +----------------------+------------------+
              |                      |                  |
              v                      v                  v
             HR                     WFM               Payroll
              |                      |                  |
              v                      v                  v
           Employee              Scheduling          Readiness
           Employment            Attendance          Snapshots
           Compensation          Timesheets          Rule Packs
           SHG Instructions                          Runs
              |                      |                  |
              +----------------------+------------------+
                                     |
                                     v
                                PostgreSQL
```

There is one executable and one database connection architecture.

The CLI and REST API are two ways to interact with the same application services.

For example:

```
CLI:
workforceops attendance import ...

REST:
POST /api/v1/attendance/import
```

Both should invoke the same attendance service.

This is important because we do not want the CLI to interpret attendance differently from the REST API.

The business logic belongs in the application services, not in the HTTP handlers or command-line code.

## 2.2 What PostgreSQL actually does

PostgreSQL is more than a storage location.

It is the authoritative system of record and an important part of the application's integrity model.

It provides:

- Persistent business records.
- Primary keys and foreign keys.
- Employer-scoped relationships.
- Unique constraints.
- Transaction boundaries.
- Row locking.
- Advisory locking.
- Triggers.
- Exact monetary storage.
- Historical immutability.
- Audit persistence.
- Application-role privilege enforcement.

Go provides the normal business operations and ergonomic validation.

PostgreSQL independently enforces important invariants, including those that could otherwise be bypassed by direct SQL.

This is why the project has invested so much effort in PostgreSQL-backed adversarial tests.

# 3. The Shared Foundation: Employer, Employee and Employment

Before understanding HR, WFM or Payroll separately, we need to understand their common identities.

## 3.1 Employer

The employer is the legal employing entity.

Our fictional model employer is:

`Meridian CX Services Pte. Ltd.`

The employer is the primary data ownership boundary.

Most important business records carry an `employer_id`.

For example:

```
employer
   |
   +---- employee
   |
   +---- client
   |
   +---- programme
   |
   +---- team
   |
   +---- shift
   |
   +---- attendance
   |
   +---- timesheet
   |
   +---- payroll
```

Why?

Because Employee 100 belonging to Employer A must never accidentally be linked to a timesheet belonging to Employer B.

A normal foreign key checks that a referenced record exists.

An employer-scoped composite foreign key can additionally check that both records belong to the same employer.

Conceptually:

```
FOREIGN KEY (employer_id, employee_id)
REFERENCES employee (employer_id, id)
```

This is stronger than checking `employee_id` alone.

The current MVP operates with one employer, but its schema supports future multiple-employer operation.

That does not mean multiple employers can already use the application as isolated authenticated SaaS tenants. Authentication, RLS and full multi-employer operation remain future work.

## 3.2 Employee

An employee is the common identity used by all three domains.

The same employee may appear in:

- HR records.
- Employment terms.
- Compensation.
- Team membership.
- Skill assignments.
- Shift assignments.
- Attendance events.
- Attendance intervals.
- Timesheets.
- Payroll snapshots.
- Payroll records.

The employee identity must remain consistent across every relationship.

## 3.3 Employment terms

An employee record answers:

"Who is this person?"

An employment-term record answers:

"Under what employment arrangement does this person work during a particular period?"

Employment terms may contain information such as:

- Employment type.
- Start date.
- End date.
- Monthly or hourly salary basis.
- Standard working arrangement.
- Relevant employment classification.
- Other contractual conditions.

This information is effective-dated.

That brings us to one of the most important database concepts in the project.

# 4. Effective Dating: Understanding Historical Business Truth

Effective dating means that a business record describes when something is true in the real world, independently of when the database learned about it.

Consider an employee who earns S$3,000 per month until 15 November and S$3,200 starting 16 November.

The database must preserve both arrangements.

```
Employment / Compensation History

1 Nov ---------------- 15 Nov
         S$3,000

16 Nov --------------- 30 Nov
         S$3,200
```

The application must not simply overwrite S$3,000 with S$3,200.

Otherwise, asking what the employee earned during the earlier period becomes impossible.

There are two distinct notions of time:

**Effective time:** When the information applies in the business world.

**Record-creation time:** When the system recorded that information.

For example:

- Compensation becomes effective on 16 November.
- The administrator records it on 12 November.

The effective date and recorded date are different.

This becomes especially important when somebody enters a correction after payroll has already been approved.

The system needs to know both when the information was supposed to apply and when it became known to the system.

Effective dating is used throughout HR, attendance corrections, compensation and eventually payroll snapshots.

# 5. System One: HR

HR is the source of employee and employment information.

Its responsibilities are not limited to employee registration.

It establishes the facts that determine who belongs to the employer and which employment conditions apply.

## 5.1 HR's principal responsibilities

The current HR domain includes:

- Employee administration.
- Employment terms.
- Employee status.
- Compensation information.
- SHG instructions.
- Effective-dated employment history.

Conceptually:

```
                 EMPLOYER
                     |
                     v
                  EMPLOYEE
                     |
          +----------+----------+
          |                     |
          v                     v
     EMPLOYMENT              STATUS
       TERMS                HISTORY
          |
          +----------+
          |          |
          v          v
     COMPENSATION   SHG
       RECORDS   INSTRUCTIONS
```

These records are related, but they have different meanings.

### Employee status

Examples include:

- Active.
- Suspended.
- Resigned.
- Terminated.

Status changes must have effective dates.

An employee terminated on 31 October may still have legitimate payroll obligations for October.

Current employee status must not erase historical employment.

### Compensation

Compensation records describe financial components of the employment arrangement.

Examples include:

- Monthly basic salary.
- Fixed allowances.
- Other supported compensation classifications.

These are stored using exact monetary representations.

They are not simply fields that can be overwritten whenever an administrator changes salary.

Compensation has its own effective-date and correction history.

### SHG instructions

SHG refers to Singapore Self-Help Group contribution instructions.

The application must distinguish:

- No instruction supplied.
- Pending instruction.
- Applicable instruction.
- Verified non-applicability.
- Superseded instruction.

An absent instruction is not the same as verified non-applicability.

The employee's SHG information is a payroll input that must be supported by evidence.

The current implementation has established a more complete instruction lifecycle, but SHG contribution calculations remain separate and are not yet implemented.

# 6. System Two: BPO Operations and WFM

WFM is where the employee's employment relationship meets the employer's operational obligations.

For our reference employer, Meridian CX operates multiple client programmes.

An employee may work on a voice-support programme during one shift and a chat-support programme during another.

WFM needs to understand both the employee and the service operation.

## 6.1 The BPO operational hierarchy

Conceptually:

```
Employer
   |
   +---- Client A
   |       |
   |       +---- Programme A1
   |       |       |
   |       |       +---- Campaign
   |       |       +---- Queue
   |       |       +---- Channels
   |       |
   |       +---- Programme A2
   |
   +---- Client B
           |
           +---- Programme B1
```

The employer has a contractual relationship with each client.

Each programme may have different:

- Service hours.
- Languages.
- Skills.
- Shift requirements.
- Queue coverage.
- Operational service targets.

These are business operations, not employee employment-law rules.

## 6.2 Teams and skills

An employee can belong to one or more operational teams.

Skills determine what work an employee is qualified or authorized to perform.

For example:

```
Employee: Agent 100

Team: Customer Care

Skills:
- English Support
- Billing Support
- Voice Support
- Product X
```

A programme may require:

- English Support.
- Voice Support.
- Valid product certification.

The roster should reject an assignment when the employee is not eligible.

Importantly, the system distinguishes being an employee of Meridian CX from being qualified to work on a particular client programme.

## 6.3 Rostering

A roster represents planned work.

A roster assignment identifies an employee's scheduled shift and the relevant operational activity segments.

Example:

```
Employee: Agent 100
Shift: 22:00–06:00

22:00–01:00  On queue
01:00–01:30  Meal
01:30–04:00  On queue
04:00–04:15  Paid break
04:15–06:00  On queue
```

This is the plan.

It is not proof that the employee actually worked these hours or took these breaks.

A published roster is also historically significant.

A later scheduling correction creates a new revision rather than silently rewriting the published version.

## 6.4 WFM and attendance are connected but distinct

WFM answers:

"What should the employee be doing?"

Attendance answers:

"What did the employee actually do?"

This distinction prevents one of the most dangerous mistakes in workforce applications: treating scheduled time as actual working time.

A scheduled 30-minute break is not evidence that the employee took that break.

A scheduled 06:00 finish does not prove the employee clocked out at 06:00.

Attendance must be based on actual evidence.

# 7. Attendance: Turning Raw Events into Business Facts

Attendance is the bridge between WFM and Payroll.

It is one of the most important domains in the entire application.

## 7.1 Clock events

A clock event is a raw observation received from a source system.

For example:

```
Employee 100

22:00  Clock In
01:00  Break Start
01:30  Break End
06:00  Clock Out
```

The raw clock event records should be retained.

The system must not erase the original evidence when correcting an interpretation.

A source event has an identity, source and timestamp.

This allows duplicate deliveries to be recognized and investigated.

## 7.2 Pairing and interpretation

The attendance service interprets the events using:

- Employee identity.
- Event type.
- Source.
- Applicable roster.
- Expected shift.
- Event ordering.
- Timezone.
- Break evidence.

It may produce an attendance interval.

A simplified example:

```
Work interval:
22:00–01:00

Unpaid meal:
01:00–01:30

Work interval:
01:30–06:00
```

The resulting total work is 7.5 hours, not the eight-hour span between clock-in and clock-out.

The actual calculation must account for the correct segment and duration semantics.

## 7.3 Why attendance needs exceptions

Events are not always complete or reliable.

Examples:

- Missing clock-out.
- Duplicate clock-in.
- Clock event outside an expected shift.
- Break without a matching end.
- Ambiguous overlap.
- Event received late.
- Conflicting replay.
- Employee clocking into another shift.

These must become explicit exceptions.

An exception is a persisted business condition requiring review. It is not simply an error message displayed once to the user.

The application needs to retain:

- What happened.
- Which evidence is affected.
- Why it is considered ambiguous.
- Whether an operator resolved it.
- Who made the decision.
- What the resulting attendance interpretation is.

## 7.4 Attendance intervals

The attendance interval is the interpreted representation of work or a break.

Unlike a raw event, an interval has a duration and a business-date identity.

The current model uses records such as:

- `clock_event`
- `attendance_interval`
- `attendance_correction_group`

The interval carries source lineage and correction relationships.

For payroll, the important distinction is:

Raw event evidence → interpreted attendance → reviewed attendance.

A raw clock event must never become payable time merely because it exists.

## 7.5 Cross-midnight shifts

Consider:

22:00 on Monday until 06:00 on Tuesday.

The shift is one operational shift, but its attendance spans two local business dates.

The system must split work and breaks at the employee's local midnight while preserving the original shift identity and source evidence.

This is why we maintain three distinct timezone concepts:

| Timezone                | Meaning                                                 |
| ----------------------- | ------------------------------------------------------- |
| Source/event timezone   | How the external clock system represented the timestamp |
| Employment timezone     | Determines employee business dates                      |
| Client/service timezone | Determines client service windows                       |

For our Singapore employment profile, the employment timezone is `Asia/Singapore`.

The host computer's timezone must never decide the employee's payroll date.

## 7.6 Corrections and correction groups

Suppose an employee originally clocked out at 06:00, but later evidence shows 06:15.

The system must preserve the original record and create an auditable correction.

For a cross-midnight correction, one original interval may need to become multiple date-specific successor intervals.

The correction group represents the logical correction operation.

```
Original interval
       |
       v
Correction group
       |
       +--------+--------+
       |                 |
       v                 v
Monday segment      Tuesday segment
```

This is important because correcting only one segment could leave an inconsistent representation.

The correction group ensures that a multi-segment correction is treated as one complete operation.

Original evidence remains visible. Superseded intervals are historical, not current working time.

# 8. Timesheets: Turning Attendance into Approved Time

An attendance interval is an interpreted operational fact.

A timesheet is an approved business record.

These are not interchangeable.

The timesheet service gathers eligible attendance intervals for an employee and payroll period.

It then creates a reviewable record containing the appropriate time allocations.

Conceptually:

```
Attendance intervals
        |
        v
Timesheet generation
        |
        v
Draft timesheet
        |
        v
Submitted
        |
        +------> Rejected
        |            |
        |            v
        |       Correction version
        |
        v
Approved
```

The approved timesheet is an important financial boundary.

Once approved, its original contents and evidence references must remain immutable.

If new evidence arrives afterwards, the application must not rewrite the approved sheet.

Instead, it uses a controlled reissue/correction process.

The system preserves:

- Original timesheet.
- Correction version.
- Approval decisions.
- Attendance lineage.
- Effective approved version.

This makes it possible to explain why an employee's originally approved hours differ from a later corrected result.

# 9. System Three: Payroll

Payroll consumes the approved business information produced by HR, WFM, attendance and timesheets.

It is tempting to think of payroll as a formula:

```
Gross Pay - Deductions = Net Pay
```

But the hard part is determining which inputs and rules are valid for a particular employee and period.

Payroll must know:

- Who should be included.
- Which employment terms applied.
- Which compensation records were effective.
- Which approved timesheet version applies.
- Which statutory instructions are complete.
- Which jurisdiction rules apply.
- Which rule versions were approved for use.

This is why Payroll has its own governance and readiness process.

## 9.1 Payroll readiness

Readiness checks whether the inputs required for a payroll run are complete and supported.

It is not a payroll calculation.

For example:

```
Employee 100

Employment terms:          Complete
Compensation:              Complete
Approved timesheet:        Complete
SHG instruction:           Missing
CPF rule pack:             Blocked
SDL rule pack:             Blocked

Result: NOT READY
```

The system must explain exactly why the employee or payroll run is not ready.

It must not substitute zero for missing information.

It must not silently omit an employee because an approved timesheet is absent.

## 9.2 Expected employee population

This is an especially important concept.

Suppose Meridian CX has ten employees who should be included in November payroll.

Only nine have approved timesheets.

If payroll readiness simply starts with approved timesheets, the tenth employee disappears from consideration.

That would be dangerous.

Instead, payroll must first establish the expected population from effective-dated employment relationships.

Conceptually:

```
Expected employees: 10

Approved timesheets: 9
Missing timesheets:   1

Payroll readiness: NOT READY
```

The system must distinguish:

- Employees expected for the period.
- Employees with approved evidence.
- Employees missing evidence.
- Employees blocked by incomplete inputs.
- Explicitly excluded employees.

This is the reason population consistency matters.

# 10. What Is a Population Barrier?

A population barrier is a database synchronization mechanism that prevents the set of employees being evaluated for a payroll snapshot from changing halfway through that evaluation.

It is a technical coordination mechanism, not a legal concept.

Imagine this scenario:

1. Payroll begins building November's expected employee population.
2. It identifies 80 employees.
3. At the same time, HR creates an employment record for another employee effective during November.
4. Payroll finishes capturing its original 80 employees.
5. The new employee is missing from the payroll input snapshot.

Each query might have been valid independently, but the resulting snapshot would be incomplete.

A population barrier prevents this race.

## 10.1 How it works

The planned mechanism is an employer-level PostgreSQL advisory lock.

Conceptually:

```
Snapshot transaction:
    Acquire employer population lock
    Read expected employees
    Acquire employee locks in sorted order
    Revalidate employee population
    Capture all employee inputs
    Persist snapshot
    Commit
```

A population-affecting operation follows the same lock convention:

```
Employee creation:
    Acquire employer population lock
    Acquire employee-related lock if required
    Validate
    Insert employee/employment records
    Commit
```

The population lock ensures that a snapshot capture and an employee-population change cannot pass each other unnoticed.

One important distinction:

**The employer population barrier protects who belongs in the payroll population.**

**The employee payroll-input barrier protects the changing financial or operational basis of an individual employee.**

These are related but different locks.

## 10.2 Population barrier versus employee lock

| Mechanism                   | Protects                                                           |
| --------------------------- | ------------------------------------------------------------------ |
| Employer population barrier | The set of employees expected in a period                          |
| Employee pay-basis lock     | One employee's employment, compensation and related payroll inputs |
| Disposition pair-lock       | Canonical and duplicate clock-event relationships                  |
| Row lock (`FOR UPDATE`)     | A particular database record being modified                        |

The locks must have a consistent acquisition order.

For example:

```
Employer population lock
          |
          v
Employee locks (UUID order)
          |
          v
Secondary locks
```

This deterministic order reduces deadlock risk.

## 10.3 Why this matters

A population barrier guarantees that the snapshot does not accidentally contain 80 employees from one moment and one more employee from another.

Without it, payroll can be internally consistent at the employee level but incomplete at the employer level.

The population barrier is part of the planned P2 snapshot implementation; it should not be treated as fully delivered merely because the earlier employee-level barriers exist.

# 11. What Is pay_basis_seq?

Another important database concept is `pay_basis_seq`.

It is a per-employee sequence used to establish the ordering of changes to that employee's payroll basis.

Consider:

```
Employee 100

Sequence 101: Compensation created
Sequence 102: Employment terms changed
Sequence 103: Timesheet approved
Sequence 104: New compensation correction
```

The sequence helps determine which relevant business inputs existed before or after the approval boundary.

It is assigned through the shared payroll-input locking protocol.

Why not just compare timestamps?

Because PostgreSQL `now()` returns the transaction-start timestamp.

A transaction might start before approval but commit afterwards.

Its creation timestamp can therefore look older even though the change becomes visible after approval.

The sequence helps avoid that problem.

Important limitations:

- It is per employee, not a global sequence.
- Sequence 104 for Employee A cannot be compared with Sequence 104 for Employee B as if both share one global clock.
- It is not a universal transaction commit timestamp.

For a multi-employee payroll snapshot, the system needs both the employer-level population boundary and the employee-level sequence references.

# 12. What Is a Rule Pack?

A rule pack is a versioned, evidence-backed collection of jurisdiction-specific business rules.

It tells the application which verified legal or statutory requirements apply to a particular calculation domain.

For Singapore, examples include:

- CPF.
- SDL.
- SHG.
- Overtime.
- Leave and employment requirements.

These are not all one universal formula.

Each has its own legislation, eligibility, inputs, calculation requirements, effective dates and exceptions.

## 12.1 Example: CPF rule pack

Conceptually:

```
Rule Pack
    |
    +---- Identity
    |       SG-CPF
    |
    +---- Version
    |       Specific reviewed version
    |
    +---- Jurisdiction
    |       Singapore
    |
    +---- Sources
    |       Official CPF materials
    |
    +---- Claims
    |       Applicability and definitions
    |
    +---- Phys-Pseudo
    |       Formal rule specification
    |
    +---- Derivation
    |       Independent reasoning
    |
    +---- Fixtures
    |       Golden / anti-golden / boundary
    |
    +---- Review
    |       Independent verification
    |
    +---- Activation
            Controlled approval
```

The rule pack is not just a Go package.

It includes research evidence, formal specification, versions, verification records and an activation decision.

## 12.2 Rule-pack lifecycle

A simplified lifecycle is:

```
DISCOVERED
    |
    v
SOURCES PINNED
    |
    v
CLAIMS EXTRACTED
    |
    v
FORMALIZED
    |
    v
INDEPENDENTLY VERIFIED
    |
    v
GOLDENS VERIFIED
    |
    v
IMPLEMENTABLE
    |
    v
ACTIVATED
```

`IMPLEMENTABLE` means the package has satisfied the project's implementation requirements.

`ACTIVATED` means the verified version has been explicitly approved for use by the employer's payroll system.

These are separate decisions.

A rule pack must not become active merely because somebody inserted a version string into PostgreSQL.

## 12.3 Why activation requires special privileges

Our application role `app` must not be able to change a blocked statutory package into an active one.

Otherwise, an application user could effectively bypass legal research and independent verification.

The latest P5 implementation introduces:

- A registry of known rule-pack identities.
- Append-only activation history.
- A controlled database activation function.
- Manifest and evidence checks in the administrative CLI.
- Direct application-role write restrictions.

The normal REST server does not expose statutory activation.

The `app` role can read the relevant statuses, but cannot simply invent an activation.

The administrative activation operation has elevated privileges and should be treated as a controlled operation, not ordinary application functionality.

## 12.4 Why this is important for multiple countries

Singapore CPF and another country's social-insurance system may serve comparable purposes, but their eligibility, contribution bases and formulas may be entirely different.

We standardize how rule packs are identified, verified, activated and pinned.

We do not standardize the actual laws into one fictional universal formula.

That is how we remain jurisdiction-extensible without losing country-specific correctness.

# 13. What Is a Payroll Snapshot?

A payroll snapshot is an immutable record of the exact business inputs a payroll run intends to use.

It answers:

**Which employee population, employment terms, compensation, approved time, SHG instructions and rule versions were used for this particular payroll run?**

Without a snapshot, the application might calculate payroll from current records each time.

That creates a problem.

Suppose an employee's salary changes after a payroll run was calculated.

If the run simply queries current compensation, it might now produce a different result.

A snapshot prevents the historical calculation basis from moving.

## 13.1 Conceptual snapshot structure

The planned P2 schema includes a payroll snapshot header and employee-level snapshot records.

Conceptually:

```
                  PAYROLL RUN
                       |
                       v
                PAYROLL SNAPSHOT
                 /      |       \
                /       |        \
               v        v         v
           EMPLOYEE  RULE PACKS  PERIOD
            INPUTS
               |
     +---------+----------+
     |         |          |
     v         v          v
  EMPLOYMENT COMPENSATION TIMESHEET
  TERMS                     |
     |                      |
     v                      v
  STATUS                  ATTENDANCE
     |
     v
    SHG
 INSTRUCTION
```

Each employee snapshot should retain the exact references to the source records used.

For example:

| Snapshot field        | What it identifies             |
| --------------------- | ------------------------------ |
| Employee ID           | Employee being assessed        |
| Employment-term ID    | Applicable employment version  |
| Status reference      | Relevant employment status     |
| Compensation IDs      | Effective compensation records |
| Timesheet ID          | Effective approved version     |
| Attendance references | Underlying time evidence       |
| SHG instruction ID    | Verified instruction           |
| `basis_seq`           | Employee input-order reference |
| Inclusion decision    | Expected, included or excluded |
| Blockers              | Unresolved input conditions    |

At the run level, the snapshot also pins the exact statutory rule-pack versions.

## 13.2 Snapshot immutability

Once validated, the snapshot's contents cannot simply be edited.

Suppose the source compensation changes after snapshot creation.

The existing snapshot remains unchanged.

Instead, the application detects source drift and produces an invalidation event or a new snapshot version.

Conceptually:

```
Payroll Run
    |
    +---- Snapshot V1
    |         |
    |         +---- VALIDATED
    |         |
    |         +---- INVALIDATED
    |
    +---- Snapshot V2
              |
              +---- VALIDATED
```

The previous snapshot remains available for history.

The planned design separates snapshot contents from lifecycle events.

That means the application can record that a snapshot became invalid without rewriting the original information.

## 13.3 Content hash

The snapshot also receives a SHA-256 content hash.

This is a fingerprint of its substantive contents.

For example:

```
Employer
Payroll period
Expected employee population
Effective compensation records
Approved timesheet versions
SHG evidence
Rule-pack identities and versions
Correction lineage
Inclusion/exclusion decisions
Snapshot schema version
```

The information is serialized in a canonical, deterministic order before hashing.

Two snapshots with identical substantive information and versions should produce the same hash.

Different underlying inputs should produce different hashes.

The hash does not replace the snapshot itself. It helps verify that the persisted snapshot has not changed.

The snapshot implementation is the next major part of Milestone 2; do not confuse its planned structure with an already completed payroll feature.

# 14. Payroll-Run Lifecycle

A payroll run represents one employer's payroll processing operation for a defined period and run type.

For example:

```
Employer: Meridian CX
Period: November 2026
Run kind: Regular
```

The current implementation supports basic run creation and exclusions.

Milestone 2 is introducing a governed lifecycle.

## 14.1 Intended lifecycle

```
DRAFT
  |
  v
VALIDATED
  |
  v
CALCULATED
  |
  v
APPROVED
  |
  v
FINALIZED
```

The meanings are different:

| State      | Meaning                                                        |
| ---------- | -------------------------------------------------------------- |
| Draft      | Run created, inputs not yet frozen                             |
| Validated  | Readiness checks passed and immutable input snapshot captured  |
| Calculated | Real financial calculation artifact exists                     |
| Approved   | Authorized financial result has been approved                  |
| Finalized  | Financial result has been finalized under the supported policy |

There may also be an invalidated or superseded state for version management.

However, during Milestone 2, we are not implementing the calculation engine.

Therefore, only the draft-to-validated transition should become operationally reachable.

The system must reject direct SQL attempts to approve or finalize a run without genuine downstream artifacts.

A validated snapshot is not a completed payroll.

## 14.2 Why the database also enforces the lifecycle

It is not enough to have Go code that checks:

```
if run.Status != "validated" {
    return errors.New("cannot approve")
}
```

A direct SQL operation might bypass that check.

The database therefore also needs state-transition protection.

This is a recurring architectural principle:

Go makes the workflow usable.

PostgreSQL protects the invariant.

# 15. The Database Transaction and Locking Model

This section brings together the different kinds of database coordination used throughout WorkforceOps.

## 15.1 Ordinary transactions

A transaction groups database changes into one logical operation.

For example, attendance correction might require:

1. Insert correction-group record.
2. Insert successor intervals.
3. Mark predecessor intervals superseded.
4. Recalculate daily aggregates.
5. Record audit events.

These must either all succeed or all roll back.

Otherwise, the database might contain a correction group without its required segments.

## 15.2 Row locking

`SELECT ... FOR UPDATE` locks specific records while a transaction modifies them.

For example, two users might attempt to approve the same timesheet.

A row lock allows one transaction to operate first while the other waits and rechecks the state.

It protects a particular record.

## 15.3 Advisory locking

An advisory lock coordinates operations using an application-defined logical key.

PostgreSQL does not automatically know that two different database rows represent the same employee's payroll input.

The application defines a shared lock identity.

For example:

```
pay-input:<employer-id>:<employee-id>
```

Every supported operation affecting that employee's payroll basis must use the same lock protocol.

The lock is released automatically at transaction completion when using a transaction-scoped advisory lock.

## 15.4 Employee payroll-input barrier

This lock serializes changes to one employee's payroll-relevant information.

Examples:

- Compensation change.
- Employment-term change.
- Attendance correction.
- Timesheet approval.
- SHG instruction change.

The intended principle is:

If the change happens first, approval sees it.

If approval happens first, the change must recognize the approved period and produce an explicit conflict or controlled correction.

## 15.5 Employer population barrier

This is the additional synchronization layer for P2.

It coordinates operations that can change the expected employee population against snapshot capture.

The sequence is conceptually:

```
Acquire employer population lock
             |
             v
Determine expected employee set
             |
             v
Acquire employee locks in sorted order
             |
             v
Revalidate population
             |
             v
Capture employee inputs
             |
             v
Persist snapshot
             |
             v
Commit
```

This protects the completeness of the population.

It does not replace employee-level locks.

## 15.6 Advisory lock ordering

A consistent order is important to avoid deadlocks.

For operations that require multiple locks, the application should follow one documented convention.

For example:

```
Employer population lock
          |
          v
Employee locks, sorted by UUID
          |
          v
Secondary operation locks
```

Never have one transaction acquire an employee lock and then wait for an employer lock while another transaction does the reverse.

That is a classic deadlock pattern.

# 16. Database Roles and Security Boundaries

WorkforceOps deliberately uses two important database identities.

## `migration_admin`

This role is responsible for:

- Database migrations.
- Schema changes.
- Protected database object ownership.
- Controlled administrative operations.

## `app`

This is the normal runtime application role.

It handles:

- Employee operations.
- Attendance.
- Timesheets.
- Payroll-readiness queries.
- Normal permitted business mutations.

It should not be able to:

- Perform arbitrary DDL.
- Bypass immutable historical records.
- Directly activate statutory rule packs.
- Rewrite approved compensation.
- Change frozen snapshots.
- Fabricate calculation artifacts.

## SECURITY DEFINER functions

You will encounter PostgreSQL functions marked `SECURITY DEFINER`.

These execute with the privileges of their owner rather than those of the calling application role.

They are useful for narrowly controlled operations.

For example, `app` may be prohibited from directly updating compensation, but allowed to execute a specific function that:

- Validates the employee.
- Acquires the required lock.
- Checks historical conflicts.
- Inserts the compensation.
- Writes the audit event.
- Returns the result.

This is more restrictive than granting `app` unrestricted INSERT and UPDATE privileges.

These functions require careful protection:

- Restricted `search_path`.
- Schema-qualified table references.
- Controlled ownership.
- Explicit EXECUTE privileges.
- Validation of all input parameters.

A SECURITY DEFINER function is powerful. It should behave like a narrowly scoped database capability, not an unrestricted back door.

# 17. Audit and Evidence Lineage

Audit records answer:

- Who performed an operation?
- What changed?
- Why?
- When?
- Which business record was affected?

Evidence lineage answers a different question:

- Which source records produced this business result?

For example:

```
Clock Event E1
       |
Clock Event E2
       |
       v
Attendance Interval A1
       |
       v
Correction Group G1
       |
       v
Successor Intervals A2, A3
       |
       v
Timesheet Line T1
       |
       v
Payroll Snapshot S1
```

An audit event might record that an operator corrected interval A1 and created G1.

The lineage connects the resulting records to their predecessors and source evidence.

Both are necessary.

An audit record alone does not prove which clock events contributed to payroll.

A foreign key alone does not explain why an operator approved a correction.

The application should preserve both.

One important limitation of the current v0.1 design is that operator attribution is not authentication. An operator label identifies the claimed actor but does not independently establish their identity.

# 18. Exact Money and Duration

Payroll systems must distinguish monetary precision from measured time.

## Money

Monetary inputs use exact representations.

The intended path is:

```
CLI / REST decimal string
          |
          v
Go validation
          |
          v
Integer minor units
          |
          v
PostgreSQL NUMERIC
```

For example:

`199.99`

must remain exactly `199.99`.

It must not become a binary floating-point approximation or be silently rounded from `199.999`.

## Time

Actual attendance duration is represented separately, using integer microseconds in the current M1.3 implementation.

This preserves the measured duration.

Payroll-specific rounding should happen only when an independently verified jurisdiction rule establishes the applicable calculation boundary.

Do not round raw attendance simply because a payroll formula may eventually operate in whole minutes.

# 19. How Singapore Law, Employer Policy and Client Rules Interact

This is particularly important for our BPO model.

There are three distinct kinds of business requirements.

## Statutory rules

These are imposed by applicable Singapore legislation and regulatory requirements.

Examples:

- Overtime eligibility and calculations.
- CPF obligations.
- SDL.
- SHG contributions where applicable.
- Statutory leave.
- Working-hours requirements.

These belong in jurisdiction-specific rule packs.

## Employer contractual policies

These are the conditions Meridian CX has agreed with employees.

Examples:

- Contractual overtime rate.
- Additional leave.
- Salary and allowance terms.
- Internal approval rules.

These are employer-specific, not automatically national law.

## Client operational rules

These arise from BPO service agreements.

Examples:

- A client requires ten agents on a voice queue.
- A programme operates 24 hours.
- A specific language skill is required.
- Certain service windows need staffing coverage.

These belong to BPO/WFM operations.

The distinction matters because a client SLA cannot authorize an employer to violate employment requirements.

# 20. The Current Status of the Integrated Application

The project has progressed through several implementation milestones.

The latest explicit development update confirms that P4 and P5 of Milestone 2 have been implemented and tested:

- SHG instruction lifecycle.
- Rule-pack registry and controlled activation.

The planned work remaining in the current milestone is:

- P2: Immutable payroll snapshots.
- P1: Governed payroll-run lifecycle.
- P6: Full verification.

The earlier compensation resolver was also an explicit prerequisite in the execution plan.

The following distinction should be maintained:

| Capability                             | Current project position                                     |
| -------------------------------------- | ------------------------------------------------------------ |
| HR administration                      | Implemented                                                  |
| BPO client/programme configuration     | Implemented                                                  |
| WFM scheduling and rosters             | Implemented                                                  |
| Attendance and break evidence          | Implemented                                                  |
| Attendance correction groups           | Implemented                                                  |
| Timesheet generation and approval      | Implemented                                                  |
| Compensation correction and resolution | Implemented through preceding milestones/planned integration |
| SHG lifecycle                          | Reported implemented and green in current M2 work            |
| Rule-pack registry and activation      | Reported implemented and green in current M2 work            |
| Immutable payroll snapshots            | Next major implementation task                               |
| Governed payroll-run validation        | Pending                                                      |
| Financial payroll calculations         | Not implemented                                              |
| CPF calculation                        | Not implemented                                              |
| SDL calculation                        | Not implemented                                              |
| SHG contribution calculation           | Not implemented                                              |
| Overtime calculation                   | Not implemented; OT-001 BLOCKED                              |
| Payslip financial generation           | Not implemented                                              |

In particular, do not confuse a verified SHG instruction lifecycle with an implemented SHG contribution calculation.

Likewise, registering or activating a rule pack is not the same as calculating a payroll component.

# 21. A Complete Example: One Employee Through the System

Consider an employee named Agent 100 working for Meridian CX.

The following example illustrates how information moves through the database.

## Step 1 — HR creates the employee

An employee record is created under Meridian CX.

An employment-term record establishes:

- Start date.
- Employment arrangement.
- Monthly salary basis.
- Applicable jurisdiction.
- Other supported employment details.

The database establishes the employee's identity and employer ownership.

## Step 2 — HR records compensation

A monthly basic salary and any relevant compensation components are recorded.

Every record has:

- Employee.
- Employer.
- Classification.
- Exact amount.
- Effective dates.
- Provenance.
- Historical succession if corrected.

The compensation resolver determines which compensation records actually apply to the payroll period.

## Step 3 — BPO configures operations

Meridian CX has a client and programme requiring English-speaking voice agents.

Agent 100 belongs to the appropriate team and has the required skill.

## Step 4 — WFM publishes a roster

The roster schedules Agent 100 for an overnight shift.

The published roster becomes a protected historical business record.

## Step 5 — Attendance imports clock evidence

The employee clocks in, takes a recorded break and clocks out.

The system stores the original clock events.

The attendance service pairs and interprets them against the relevant roster, employee timezone and break evidence.

## Step 6 — Attendance is reviewed

Any ambiguity is retained as an exception.

An authorized operator reviews valid evidence.

Corrections create successors rather than silently overwriting history.

## Step 7 — A timesheet is generated

Eligible attendance intervals become timesheet lines.

The employee submits the timesheet.

A supervisor reviews and approves it.

The approved version becomes an immutable financial input.

## Step 8 — Payroll readiness evaluates the employee

Readiness determines:

- Whether the employee should be included.
- Whether an approved timesheet exists.
- Whether compensation is complete.
- Whether SHG instructions are complete.
- Whether required rule packs are usable.
- Whether attendance or historical conflicts remain.

It returns an explicit readiness result.

## Step 9 — Payroll snapshot is captured

This is the next major Milestone 2 capability.

The application captures the expected employee population and exact source versions.

It stores the snapshot, including its content hash.

Source changes afterwards cannot silently rewrite it.

## Step 10 — Payroll calculation

This will be implemented in the subsequent financial milestone.

The calculation consumes the approved snapshot and the exact applicable statutory rule-pack versions.

The resulting payroll should be reproducible and explainable.

---

# 22. The Most Important Database Concepts to Remember

| Term                    | Plain technical meaning                                                    |
| ----------------------- | -------------------------------------------------------------------------- |
| Employer boundary       | Determines which legal employer owns a business record                     |
| Employee identity       | Common identity linking HR, WFM and Payroll                                |
| Effective dating        | When a business record applies in the real world                           |
| Supersession            | Creating a replacement while preserving the original                       |
| Correction group        | One logical correction that can produce multiple dated attendance segments |
| Attendance interval     | Interpreted period of work or break                                        |
| Timesheet               | Reviewed and approved allocation of attendance                             |
| Rule pack               | Versioned, evidence-backed jurisdiction-specific rules                     |
| IMPLEMENTABLE           | Rule package has satisfied implementation prerequisites                    |
| Activated               | A verified rule version has been approved for use                          |
| Readiness               | Determines whether required payroll inputs are complete                    |
| Population barrier      | Prevents the expected employee set changing during snapshot capture        |
| Employee pay-basis lock | Serializes payroll-relevant changes for an individual employee             |
| `basis_seq`             | Per-employee ordering reference for relevant business changes              |
| Payroll snapshot        | Immutable record of the exact input versions used by a run                 |
| Content hash            | Fingerprint of canonical snapshot contents                                 |
| Payroll run             | A governed processing operation for an employer and period                 |
| `app`                   | Ordinary database runtime identity                                         |
| `migration_admin`       | Privileged schema/administrative database identity                         |
| SECURITY DEFINER        | PostgreSQL function that executes with its owner's privileges              |
| Audit event             | Records who performed a business operation and why                         |
| Evidence lineage        | Shows which original records support a derived business record             |

# 23. Final Mental Model

There is one way I would summarize the whole architecture:

**HR establishes the employment relationship. WFM plans the work. Attendance records what happened. Timesheets approve the work. Payroll establishes the financial consequence under the applicable verified rules.**

PostgreSQL connects and protects that history.

Go provides the application services that move the business through its lifecycle.

Phys-Research establishes the evidence behind statutory requirements.

Phys-Pseudo expresses those requirements precisely.

Rule packs identify which verified versions may be used.

Payroll snapshots preserve exactly which records and rule versions a payroll run consumed.

The final financial result should therefore never be an unexplained number.

It should be possible to trace it backwards:

```
                 PAYSLIP
                    |
                    v
            PAYROLL CALCULATION
                    |
                    v
             PAYROLL SNAPSHOT
                    |
          +---------+---------+
          |                   |
          v                   v
       EMPLOYEE            RULE PACKS
       INPUTS              + VERSIONS
          |
    +-----+------+---------+
    |            |         |
    v            v         v
  COMPENSATION TIMESHEET   SHG
                 |
                 v
             ATTENDANCE
                 |
                 v
          CLOCK / BREAK EVENTS
                 |
                 v
             PUBLISHED ROSTER
                 |
                 v
          EMPLOYMENT RELATIONSHIP
```

That is the essence of WorkforceOps.

It is not merely an HR application connected to a scheduling application and a payroll calculator.

It is an integrated business-record system in which every important financial decision can ultimately be traced to an employee, an employment arrangement, operational evidence and a verified set of jurisdiction-specific rules.

**Our long-term engineering objective is to make that entire chain traceable, immutable where appropriate, correctable through controlled succession, and reproducible from the database.**

## The three concepts worth remembering most

1\. Population barrier: protects who is supposed to be included in a payroll period while a snapshot is being captured.

2\. Employee payroll-input barrier and `basis_seq`: protect which version of an individual's employment, compensation and other relevant inputs is being used.

3\. Rule pack and snapshot: the rule pack establishes which verified legal rules may be applied; the snapshot establishes exactly which business records and rule versions a particular payroll run used.

Together, these provide the foundation for a reproducible payroll result.

One status reminder: P4 and P5 are reported green; P2, P1 and P6 are still the next work in Milestone 2. The snapshot and complete run-governance design above describes the intended system, not functionality we should yet claim is finished.