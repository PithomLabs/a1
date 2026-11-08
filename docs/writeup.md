## prompt

do a technical writeup of the progress as of this moment, what are the problems that were exposed during the design, development and implementation stages at this moment, the goal is to provide insights, not criticize the workflow

## answer

The most significant progress is that WorkforceOps has moved from an architectural idea to a working Go application, and we now have a much clearer understanding of what makes an integrated HR, WFM and payroll system difficult to get right.

The most valuable discoveries have not been about Go or PostgreSQL themselves. They have been about business semantics, temporal data, evidence lineage, statutory interpretation and the relationships between otherwise independent modules.

I have structured this as a technical progress and engineering-insights report. It distinguishes what has actually been implemented, what the adversarial reviews exposed, and what remains planned but not yet executed.

# WorkforceOps Lab

# Technical Progress, Engineering Discoveries and Architectural Insights

**Project:** WorkforceOps Lab\
**Organization:** Pithom Labs\
**Repository:** `github.com/PithomLabs/a1`\
**Reporting date:** 8 November 2026\
**Current milestone:** Milestone 1 completed; Milestone 1.1 remediation planned\
**Implementation:** Go, PostgreSQL, REST API and CLI\
**Status:** Operational vertical slice established; integrity remediation in preparation

---

# 1. Executive Summary

WorkforceOps Lab is an open-source integrated HR, Workforce Management (WFM) and payroll backend, initially modeled around a fictional Singapore BPO employer.

The objective is to build a functioning business system that represents realistic employment relationships, multi-client BPO operations, shift scheduling, attendance, timesheets and payroll workflows.

The project deliberately uses a single Go application, one PostgreSQL database, a REST API and a CLI. It has no frontend or microservices.

Since the initial design work, the project has passed several important milestones:

1. The initial architecture and database design were consolidated into a single-employer operational application with a multi-employer-ready database.
2. PostgreSQL integrity constraints, composite foreign keys, record immutability and employer isolation were tested against a real database.
3. Phys-Research and Phys-Pseudo were adapted to establish an evidence-driven methodology for statutory and financial business rules.
4. The first integrated HR/WFM/timesheet vertical slice was implemented and demonstrated through both CLI and REST.
5. Independent adversarial reviews exposed important defects involving attendance, temporal attribution, immutable records, workflow transitions, database privileges and payroll readiness.

The latest consolidated adversarial review identified:

- 2 critical findings (P0).
- 24 high-priority findings (P1), including 13 blocking Milestone 1 acceptance.
- 33 medium-priority findings (P2).
- 13 lower-priority findings (P3).
- 5 additional risks.
- 20 untested hypotheses.
- 17 areas where the reviewers found no defects in the examined implementation.

These findings do not indicate that the overall architecture is fundamentally unsuitable. In fact, several important architectural decisions were independently examined and found sound.

The discoveries instead reveal that the difficult part of an integrated workforce system is preserving the meaning and consistency of business events as information moves across HR, WFM, attendance, timesheets and payroll.

The current focus is therefore Milestone 1.1: repairing the integrity of the existing business workflow before implementing financial payroll calculations.

The project has reached an important point: **we now understand considerably better what a trustworthy payroll input system must guarantee before it is allowed to calculate money.**

# 2. Architectural Foundation Established

## 2.1 One application, one database, multiple business domains

The original architectural direction was intentionally reduced to avoid premature distributed-system complexity.

The current design consists of:

- One Go executable.
- One PostgreSQL database.
- One REST API.
- One CLI.
- One shared application-service layer.
- One migration system.
- Employer-scoped business records.

The CLI and REST API are adapters over the same application services.

The intended domain structure is:

```
WorkforceOps
│
├── Employer
│
├── HR
│   ├── Employee
│   ├── Employment Terms
│   ├── Compensation
│   ├── Employment Status
│   └── Leave
│
├── BPO Operations
│   ├── Client
│   ├── Programme
│   ├── Team
│   ├── Membership
│   └── Skills
│
├── WFM
│   ├── Shift Templates
│   ├── Rotation Patterns
│   ├── Rosters
│   └── Roster Assignments
│
├── Attendance
│   ├── Clock Events
│   ├── Break Events
│   ├── Attendance Intervals
│   ├── Exceptions
│   └── Corrections
│
├── Timesheets
│   ├── Generation
│   ├── Review
│   ├── Submission
│   └── Approval
│
└── Payroll
    ├── Readiness
    ├── Rule Activation
    ├── Payroll Runs
    └── Future Calculations
```

The important architectural decision is that these domains are logically distinct but operationally integrated.

There are no separate ShiftHub and PayLoop services or databases in the current MVP.

This reduces deployment, debugging, transaction and data-synchronization complexity while retaining clear domain boundaries.

## 2.2 Single-employer operation with multi-employer-ready data architecture

The initial application operates with one Singapore legal employer.

However, the database is designed around `employer_id` as the ownership boundary.

This affects:

- Employee identifiers.
- Client and programme records.
- Teams and memberships.
- Rosters.
- Clock events.
- Attendance.
- Timesheets.
- Payroll records.
- Audit information.

Composite foreign keys and employer-scoped uniqueness protect relationships between records belonging to different employers.

The distinction between an employer and a BPO client is also explicit.

An employer is the legal employing entity. Its clients and programmes are operational relationships within that employer. They are not separate database tenants.

The architecture therefore prepares the application for future multiple-employer operation without requiring multi-tenant SaaS authentication, RLS or enterprise identity in the current MVP.

## 2.3 Database integrity foundation

The original design gate established a PostgreSQL baseline with seven migrations and 50 tables. Milestone 1 subsequently added migration `00008_break_events`.

The design work established:

- Employer-scoped composite foreign keys.
- Unique business identifiers.
- Protected historical records.
- Published-roster and approved-timesheet immutability.
- Separate migration and runtime database responsibilities.
- Explicit application-level employer context.

The later review confirmed that employer isolation is a comparatively strong part of the current design.

It found no cross-employer data leakage in the examined access paths and confirmed that the main service queries use employer context.

However, the reviews also exposed weaknesses in immutability and database-role provisioning. These are part of the current remediation work.

# 3. Phys-Research and Phys-Pseudo: Improving Business-Rule Engineering

One of the most important developments has been adopting a formal methodology for understanding business rules before implementing them.

The project uses two complementary approaches.

## 3.1 Phys-Research: establish what the rule actually means

Phys-Research provides the research discipline.

For WorkforceOps, the methodology is adapted to authoritative Singapore government and legislative sources.

The research sequence is:

```
Acquire authoritative sources
        ↓
Preserve source provenance
        ↓
Extract claims
        ↓
Identify applicability and contradictions
        ↓
Formalize the rule
        ↓
Independently derive expected results
        ↓
Verify boundary cases
        ↓
Approve implementation
```

Every important statutory or financial rule should have:

- A stable rule identifier.
- Source provenance.
- Relevant statutory locations.
- Effective dates.
- Applicability conditions.
- Extracted claims.
- Assumptions.
- Derivations.
- Unresolved questions.
- Independent review.

This makes it possible to distinguish what a government source actually says from what an analyst believes it means.

It also prevents an AI-generated explanation from accidentally becoming the application's source of legal authority.

## 3.2 Phys-Pseudo: express the rule precisely

The local Phys-Pseudo repository was inspected directly.

Its authoritative `PSEUDO.md` defines seven constructs:

- `given`
- `define`
- `assume`
- `claim`
- `derive`
- `if/then/else`
- `?`

The language is used to make the logical structure of a business rule explicit.

In particular, it distinguishes:

- Facts supplied by sources.
- Defined quantities.
- Assumptions.
- Claims requiring proof.
- Derivations.
- Conditional outcomes.
- Unresolved questions.

No interpreter, compiler or code generator was introduced.

Phys-Pseudo is a specification layer, not a replacement for Go.

The separation of responsibilities is:

| Component              | Responsibility                                     |
| ---------------------- | -------------------------------------------------- |
| Phys-Research          | Research and establish evidence                    |
| Phys-Pseudo            | Formally express business rules                    |
| Independent derivation | Establish expected results independently           |
| Go                     | Execute approved business logic                    |
| PostgreSQL             | Preserve business data and integrity               |
| Tests                  | Verify implementation against independent outcomes |

## 3.3 The first practical insight: formal specifications expose semantic defects

The overtime research has already demonstrated why this methodology is useful.

The original research PDF contained historical salary thresholds and overtime caps that could easily have been implemented as current rules.

The subsequent MOM captures consistently showed a different non-workman salary threshold.

Research also exposed an unresolved mathematical discrepancy between:

- A monthly-rated formula producing approximately S$13.63636 per hour at a S$2,600 basic salary.
- MOM guidance displaying S$13.60 per hour.

The earlier specification also incorrectly treated the 72-hour monthly overtime restriction as a payable-hours cap.

These problems were identified before an overtime calculation was implemented in Go.

The significance is not simply that research found conflicting numbers.

The deeper discovery is that **a calculation can be mathematically correct while its legal applicability, inputs or interpretation are incorrect.**

This changes how we approach the implementation of every high-consequence business rule.

# 4. Integrated HR and WFM: What Has Been Built

The first implementation milestone produced a functioning backend vertical slice.

## 4.1 HR administration

Implemented capabilities include:

- Employee creation and listing.
- Employee status management.
- Effective-dated employment terms.
- Basic employment compensation information.

The system retains explicit employment classification information rather than inferring statutory eligibility from a job title.

This is important because employment rules can depend on employee category, salary basis and effective date.

## 4.2 BPO operations

The fictional reference employer is a Singapore BPO company operating multiple client programmes.

The backend supports:

- Clients.
- Programmes.
- Teams.
- Team memberships.
- Skill catalogue and employee skill assignments.
- Programme-required skills.

This establishes the distinction between employment relationships and service-delivery operations.

An employee has an employment relationship with the BPO employer while working on a particular client programme.

That relationship is operational, not a separate legal employment relationship.

## 4.3 Rostering and shift management

The implementation includes:

- Shift templates.
- Shift activity segments.
- Rotation patterns and assignments.
- Draft rosters.
- Employee shift assignments.
- Roster publication.
- Basic membership and skill validation.

The database distinguishes scheduled activities from actual attendance.

This is essential because an employee's scheduled shift is not evidence that the employee actually worked those hours or took a scheduled meal break.

An important remaining limitation is the published-roster revision lifecycle. The current review found that published rosters lack a usable correction/revision workflow through the application interfaces.

The next remediation introduces controlled revision creation while preserving the original published schedule.

# 5. Attendance: The Most Important Emerging Domain

The first implementation uses clock events and explicit break evidence to produce attendance intervals.

This is where the most revealing engineering problems have emerged.

## 5.1 Clock events are evidence, not hours worked

The system imports raw clock events containing:

- Source.
- Source event identifier.
- Employee number.
- Event timestamp.
- Event timezone.
- Event type.

The implementation preserves the original clock records.

Break start and end events were added through migration `00008_break_events`.

The key insight is that a clock event is evidence of an occurrence. It is not itself a completed attendance calculation.

Events must be interpreted and paired to produce intervals.

That interpretation can be ambiguous.

For example:

- A clock-in without a clock-out.
- A duplicated clock-in.
- A missing meal-break end.
- A clock-out belonging to another shift.
- A clock-in near a calendar boundary.
- An event delivered in a later batch.
- A conflicting replay from the source system.

These conditions require explicit exception handling.

## 5.2 Scheduled breaks and observed breaks are different facts

A roster may specify a one-hour unpaid meal break.

That does not prove the employee actually took it.

The system must distinguish:

- Planned break.
- Actual break.
- Paid break.
- Unpaid meal.
- Missing or ambiguous break evidence.

This distinction has consequences for actual hours, paid time and eventual payroll inputs.

The introduction of break events was therefore an important architectural step rather than just an additional CSV event type.

## 5.3 Cross-midnight work is a temporal modeling problem

Consider an employee working from 22:00 to 06:00.

The shift has one operational identity but spans two Singapore calendar dates.

The correct representation must preserve:

- Original UTC instants.
- Employee timezone.
- Local business date.
- Shift identity.
- Actual work intervals.
- Actual break intervals.
- Source-event lineage.

The implementation has exposed questions about whether an entire overnight span is being assigned to the starting business date.

The next remediation specifically investigates and corrects this.

The broader insight is that **a timestamp and a business date are not interchangeable**.

PostgreSQL's session timezone must never accidentally determine payroll attribution.

# 6. Timesheets: Connecting Operational Evidence to Financial Records

The first implementation supports:

- Timesheet generation.
- Submission.
- Review.
- Approval.
- Rejection.
- Attendance-source references.

The demonstrated workflow successfully generated and approved a regular timesheet with 39.5 hours after an attendance correction.

However, the adversarial review found a significant lifecycle issue.

An attendance interval could be corrected after a timesheet had already been approved, leaving the approved timesheet pointing to the old interval while attendance reflected the new value.

This exposed an important distinction:

**Immutability of a record is not enough. Its relationship to the evidence it represents must also remain trustworthy.**

The next design preserves an approved timesheet and its original content. A subsequent correction should create a new, linked version rather than silently changing the original financial source record.

The same concept applies to:

- Employment terms.
- Published rosters.
- Attendance intervals.
- Payroll runs.
- Payslips in the future.

Historical records must remain understandable after subsequent changes.

# 7. Payroll Readiness: A Deliberate Boundary

The first implementation includes payroll readiness assessment but not financial calculation.

The readiness process checks:

- Expected employee inputs.
- Approved timesheet availability.
- Required statutory rule packs.
- Missing instructions.
- Explicit exclusions.
- Current rule activation status.

The demonstrated scenario produced an approved regular timesheet but returned:

```
ready: false
```

This is expected under the current rule configuration.

The statutory packs remain blocked, and required employee inputs are not yet complete.

The implementation deliberately does not invent:

- Overtime pay.
- CPF contributions.
- SDL.
- SHG deductions.
- Gross salary.
- Net salary.
- Financial payslips.

The distinction is important because a system that produces no incorrect financial result is preferable to one that produces plausible but unverified amounts.

The latest adversarial review nevertheless exposed weaknesses in readiness population accounting. In particular, historical employees with approved timesheets and exclusions from unrelated pay periods must be represented correctly.

The next remediation strengthens that foundation before payroll calculation begins.

# 8. What the Adversarial Reviews Have Revealed

The independent reviews are especially valuable because they examined the system as a connected business workflow rather than judging each Go package independently.

The consolidated report contains 72 findings:

| Finding class       | Count |
| ------------------- | ----- |
| Critical (P0)       | 2     |
| High (P1)           | 24    |
| Medium (P2)         | 33    |
| Low (P3)            | 13    |
| Additional risks    | 5     |
| Untested hypotheses | 20    |
| Clean areas         | 17    |

Thirteen of the P1 findings were classified as blocking Milestone 1 acceptance.

These findings can be understood through several recurring engineering themes.

## 8.1 Temporal consistency

Effective dates, local business dates, timestamps and pay periods must agree.

The review surfaced:

- UTC versus Singapore-date filtering.
- Cross-midnight attendance attribution.
- Historical employee status.
- Effective-dated employment-term revisions.
- Potentially overlapping pay periods.

These are not isolated date-formatting problems.

They show that HR, WFM and payroll share a temporal model, even though each uses dates for different business purposes.

## 8.2 Evidence integrity

An attendance interval should never become reliable merely because it exists in the database.

The system needs to establish:

- Which clock events produced it.
- Whether all required break evidence exists.
- Whether the events were paired correctly.
- Whether the interval has been corrected.
- Whether the interval has already been allocated to a timesheet.

The review discovered potential double-counting from duplicated attendance intervals and conflicting source-event redelivery.

This demonstrates why source-event deduplication alone is insufficient. Two different event IDs can still describe the same underlying work.

## 8.3 Lifecycle integrity

Business records have states and permitted transitions.

The review found problems involving:

- Employee status changes.
- Employment-term succession.
- Timesheet rejection and recovery.
- Roster publication and revision.
- Approved timesheets and subsequent attendance corrections.

A database constraint may prevent an invalid update but still leave an operator with no valid recovery path.

The system therefore needs both:

1. Database-enforced invariants.
2. Application workflows that provide a legitimate way to move between states.

## 8.4 Transaction and concurrency integrity

Some operations consist of several dependent writes.

For example, attendance import involves:

1. Persisting raw events.
2. Identifying relevant employees.
3. Pairing events.
4. Creating intervals.
5. Updating daily aggregates.
6. Recording exceptions.

If a failure occurs between these steps, replay must recover the incomplete work rather than silently treating all original events as already processed.

Similarly, concurrent attendance corrections must not produce multiple live successors to the same interval.

The remediation design therefore introduces explicit transaction boundaries, consistent advisory locking and database uniqueness protections.

## 8.5 Database privileges and execution identity

The reviews also exposed a difference between testing PostgreSQL constraints under a restricted role and running the actual application through a correctly configured runtime role.

The intended model is:

- `migration_admin` performs database migrations and owns protected schema objects.
- `app` performs normal application operations.

The local setup and migration path must enforce that arrangement consistently.

Tests that pass under a superuser cannot demonstrate that the application behaves correctly under its real privileges.

## 8.6 Interface completeness

A business operation is not fully delivered just because its service function exists.

For example, attendance correction requires the operator to discover the relevant attendance interval ID.

The original API did not expose enough information to complete that task without SQL.

Other findings concerned roster revisions, rejected-timesheet recovery, API-only setup operations and response-envelope consistency.

This demonstrates that application-service correctness, API completeness and operator usability must be tested separately.

# 9. What Has Been Independently Established

Several parts of the implementation have received substantial technical scrutiny.

The consolidated review found evidence supporting:

### Architecture

- One executable.
- Shared CLI and REST application services.
- Employer-scoped data queries and composite relationships.
- No significant SQL injection exposure in the examined runtime queries.

### Database

- Employer isolation in the examined access paths.
- PostgreSQL integration tests using real databases.
- Protected historical records in the principal tested paths.
- Explicit source lineage for attendance and timesheet relationships.

### Business-rule boundaries

- No overtime, CPF, SDL, SHG or net-pay calculation in Go.
- `SG-MOM-OT-001` remains BLOCKED.
- Provisional MOM transcripts are not misrepresented as byte-grade legislative snapshots.
- The 72-hour rule is not used to truncate payable hours.
- The 1.5× overtime arrangement is recorded as a Meridian contractual assumption rather than a universal statutory conclusion.

The reviewers also identified areas where implementation tests were weaker than the reported happy-path demonstration.

For example, the parallel database-test setup failed on a fresh PostgreSQL environment because multiple packages raced while creating databases and extensions. Serial execution passed the full database suite.

This is useful evidence: the test system itself requires stronger deterministic initialization before it can reliably support continuous verification.

# 10. Current Remediation Direction: Milestone 1.1

The next planned milestone is an integrity-remediation pass.

Its purpose is to correct the relationships and workflows exposed by the adversarial reviews while preserving the existing architecture.

The approved plan has several major workstreams.

## 10.1 Attendance and temporal integrity

- Roster-anchored clock-event pairing.
- Defensive span limits.
- Persisted unmatched-event exceptions.
- Timezone-aware business-date filtering.
- Correct cross-midnight attribution.
- Duplicate and overlap prevention.
- Recoverable import replay.
- Atomic attendance corrections.

The 72-hour statutory working-time limit remains a separate compliance concern. It must not erase verified actual work.

## 10.2 Immutable correction workflows

- Preserve approved and rejected historical timesheets.
- Create linked correction versions.
- Prevent corrections from silently changing approved evidence.
- Support rejected-timesheet recovery through a new version.
- Enforce one successor per original artifact.
- Ensure payroll readiness resolves the effective version correctly.

## 10.3 HR and roster lifecycle

- Close previous effective-dated employment intervals correctly.
- Support valid employee status transitions.
- Protect approved historical payroll periods against unrecorded retrospective changes.
- Revalidate roster eligibility before publication.
- Create new roster revisions without changing published history.

## 10.4 Payroll readiness

- Derive the expected employee population from employment applicable to the period.
- Include historical employees who have approved timesheets.
- Make missing timesheets visible.
- Validate exclusions against their employer, pay period and run.
- Reject empty or incomplete populations.
- Require complete source evidence before treating a timesheet as payroll-ready.

## 10.5 Database and test foundation

- Correct migration/runtime role separation.
- Test normal operations as `app`.
- Protect frozen parent relationships against re-parenting.
- Add database constraints for duplicate allocation and attendance overlap.
- Resolve fresh-database parallel test setup races.
- Test crash recovery and replay.

The implementation plan preserves migrations `00001–00008` and proposes new forward migrations beginning at `00009`, subject to the existing-database check and migration preconditions.

No statutory payroll calculations are included in this remediation milestone.

# 11. Insights Gained So Far

The project has produced several useful engineering insights.

## Insight 1: The real unit of correctness is an evidence-linked business event

It is insufficient to have an employee record, an attendance interval or a timesheet line.

We must know what each record represents, where it came from and which earlier records justify it.

The payroll system ultimately needs a traceable chain:

```
Original clock evidence
        ↓
Attendance interpretation
        ↓
Reviewed attendance
        ↓
Approved timesheet
        ↓
Payroll input
        ↓
Payroll calculation
        ↓
Payslip
```

Every transition must preserve its provenance.

## Insight 2: Time is a first-class domain concept

A single `date` field or `hours_worked` value is insufficient for a multi-shift BPO application.

We have learned to distinguish:

- UTC instant.
- Employee local time.
- Singapore business date.
- Shift identity.
- Scheduled activity.
- Actual attendance.
- Paid duration.
- Payroll period.

This is fundamental to overnight shifts, leave, overtime, statutory hours and monthly payroll attribution.

## Insight 3: Immutable records require controlled succession

A system cannot be made trustworthy merely by preventing UPDATE and DELETE.

Business records sometimes require corrections.

The solution is to preserve the original record and create a controlled successor with an explicit relationship and approval history.

That gives us both historical integrity and operational flexibility.

## Insight 4: HR, WFM and payroll are separate domains but share invariants

A terminated employee may still have wages to be paid for an earlier period.

An approved timesheet may become inconsistent if its source attendance is corrected.

A roster may remain operationally valid but lack sufficient legal validation.

These examples show why a domain cannot make decisions independently of the surrounding business context.

Integration must be explicit.

## Insight 5: Legal compliance and operational correctness are different

A valid roster is not necessarily legally compliant.

A service-level staffing requirement is not an employment-law obligation.

A 72-hour overtime limit is not a payable-hours calculation.

A payroll calculation can be mathematically consistent but legally inapplicable to the employee.

The system needs separate representations for each of these dimensions.

## Insight 6: Research quality directly affects implementation quality

The overtime example demonstrated that outdated values, ambiguous formula conventions and missing statutory provisions can survive ordinary application testing.

Phys-Research and Phys-Pseudo provide a method for discovering these problems before Go code exists.

They also give the coding agent a precise boundary between established facts and unresolved interpretations.

## Insight 7: A successful happy path does not establish business-system correctness

The first vertical slice worked end to end.

The adversarial reviews then discovered problems in unusual but consequential conditions:

- Late or conflicting event delivery.
- Corrections after approval.
- Historical employee changes.
- Duplicate intervals.
- Cross-midnight shifts.
- Concurrent processing.
- Fresh database installations.

These are exactly the cases that distinguish a realistic backend from a convincing demonstration.

The lesson is to test the lifecycle and the negative paths, not only whether the system can complete one successful scenario.

## Insight 8: The MVP must optimize for trustworthy completeness, not feature count

We do not need advanced forecasting, automatic scheduling, telephony integrations or enterprise identity to build a useful first backend.

We do need correct employee records, attendance evidence, timesheets and payroll-input lineage.

A small system with those properties is more valuable than a larger system that cannot explain how it arrived at a payroll amount.

# 12. Current Status and Remaining Boundaries

| Capability                            | Current status                                        |
| ------------------------------------- | ----------------------------------------------------- |
| Single Go application                 | Implemented                                           |
| PostgreSQL data foundation            | Implemented                                           |
| Employer-scoped data architecture     | Established                                           |
| HR administration                     | Implemented, lifecycle remediation pending            |
| BPO client/programme/team model       | Implemented                                           |
| Skills and roster assignments         | Implemented                                           |
| Shift templates and attendance import | Implemented                                           |
| Break evidence                        | Implemented                                           |
| Attendance correction and review      | Implemented, integrity remediation pending            |
| Timesheet generation and approval     | Implemented, correction lifecycle remediation pending |
| Payroll readiness                     | Implemented, population remediation pending           |
| Overtime calculation                  | Not implemented; rule package blocked                 |
| CPF calculation                       | Not implemented                                       |
| SDL calculation                       | Not implemented                                       |
| SHG calculation                       | Not implemented                                       |
| Gross/net payroll calculation         | Not implemented                                       |
| Financial payslips                    | Not implemented                                       |
| Byte-grade MOM legislative provenance | Outstanding                                           |
| M1.1 integrity remediation            | Planned, not yet completed                            |

A number of adversarial hypotheses still need direct execution, especially concurrent processing, late-break attachment, overlapping pay periods, migration upgrade and rollback, and the complete CLI/REST workflow without SQL.

These should remain visible as unverified until demonstrated.

# 13. The Next Engineering Objective

The immediate target is Milestone 1.1.

Its success condition is a reliable operational evidence chain:

`Employee → Employment Terms → Roster → Clock Events → Attendance → Timesheet → Payroll Readiness`

The resulting system must preserve actual work, reject ambiguous or duplicated attendance, support controlled historical corrections, retain employer isolation and behave consistently under the intended PostgreSQL application role.

Only after this foundation passes its acceptance tests should the project move to the first independently verified payroll calculation.

A sensible first financial scenario is a deliberately restricted monthly-rated Singapore Citizen employee with a full salary period and no overtime, unpaid leave or other variable earnings. Its CPF, SDL and SHG requirements still need their own verified rule packages.

Overtime remains a separate research and implementation track. Its unresolved legal and mathematical questions should not hold back independent payroll components, but neither should they be silently bypassed.

# 14. Conclusion

WorkforceOps has progressed considerably beyond its original design stage.

We now have a real Go application with a PostgreSQL database, shared CLI/API services, HR administration, BPO configuration, shift management, attendance processing, timesheets and payroll readiness.

We also have a research and formal-specification methodology intended to prevent unsupported legal interpretations from becoming executable payroll logic.

The most important discoveries so far concern the deeper structure of an integrated workforce system:

- Employment relationships are effective-dated.
- Client operations and employment obligations are distinct.
- Schedules are not actual attendance.
- Actual attendance is not automatically paid time.
- Clock events are evidence, not completed hours.
- Approved timesheets are historical business decisions.
- Corrections must preserve rather than rewrite history.
- Payroll populations must be defined independently of which records happen to exist.
- Statutory calculations must be supported by evidence and independently verified derivations.

The adversarial reviews have made these relationships more explicit and have exposed where the first implementation does not yet preserve them reliably.

This is valuable progress. We have moved from designing an application around assumed requirements to learning, through actual PostgreSQL execution and adversarial testing, what the system must guarantee.

The next phase should preserve that direction without broadening the scope.

**The goal is to deliver a small, functioning Singapore HR/WFM/payroll backend whose results can be traced from their original business evidence through every decision and calculation.**

That is the foundation on which a reliable payroll system—and later a realistic production-support training environment—can be built.

---

**End of technical progress report**

## Source basis and status qualification

This writeup draws on the consolidated adversarial review, which records the 72 findings and distinguishes confirmed defects, risks, untested hypotheses and clean areas, and on the current Prompt 9 execution plan. The latter describes the intended Milestone 1.1 work; it is not evidence that those remediation steps have already been completed.&#x20;

milestone1_adversarial_review\.md

milestone1_adversarial_review\.md

milestone1_adversarial_review\.md

plan_prompt9.md



The most important message I would preserve from this report is: the project has not merely accumulated features; it has discovered the invariants that make those features trustworthy.