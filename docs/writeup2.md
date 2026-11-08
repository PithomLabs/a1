The most important story so far is that WorkforceOps has evolved from a proposed Singapore HR/WFM/payroll application into a functioning backend, while the engineering work has uncovered what it actually takes to make its payroll inputs trustworthy.

I would frame the progress around three things: what we built, what we learned by trying to break it, and how those discoveries changed the architecture and implementation decisions.

One status distinction is particularly important: Milestone 1.2's implementation report declares PASS, but the subsequent independent adversarial review returns FAIL. Prompt13 has been prepared and amended, but its implementation has not yet been completed. The writeup below reflects that distinction rather than presenting planned work as delivered.

# WorkforceOps Lab

# Technical Progress, Engineering Challenges and Lessons Learned

**Organization:** Pithom Labs\
**Repository:** `github.com/PithomLabs/a1`\
**Project:** WorkforceOps Lab\
**Initial jurisdiction:** Singapore\
**Reporting point:** 9 November 2026\
**Architecture:** Go modular monolith, PostgreSQL, REST API and CLI\
**Current development stage:** Milestone 1.2 implemented; final integrity remediation planned under Milestone 1.3

---

# 1. Executive Summary

WorkforceOps Lab began with a practical objective: build a functioning enterprise HR, Workforce Management (WFM) and payroll backend for a realistic Singapore BPO company.

The initial idea was to reproduce enough of a real enterprise workforce system to support employee administration, multi-client workforce operations, scheduling, attendance, timesheets and payroll.

The project deliberately avoids a frontend and distributed architecture. It uses one Go application, one PostgreSQL database, a REST API and a CLI. Its initial operational configuration is for one Singapore employer, while its database design supports future multiple-employer operation.

What appeared initially to be a relatively conventional enterprise application has developed into a much more interesting engineering exercise.

The difficult parts were not creating REST endpoints, writing SQL queries or implementing CRUD operations. They were establishing the meaning and integrity of business information as it moves between HR, WFM, attendance, timesheets and payroll.

Several challenges emerged:

- Singapore employment legislation contains applicability conditions, historical changes and calculation details that cannot safely be inferred from simplified summaries.
- Employee employment obligations and BPO service-delivery requirements belong to different business domains.
- Scheduled time, actual attendance, paid time and approved payroll time are different quantities.
- Cross-midnight work requires a proper temporal model rather than a simple date and duration.
- Clock events are evidence that must be interpreted, not automatically accepted as hours worked.
- Correcting historical information must not silently rewrite approved records.
- PostgreSQL triggers and application services must enforce the same business invariants.
- Transaction ordering matters when compensation, attendance and approval occur concurrently.
- Financial calculations must preserve exact monetary values rather than rely on binary floating-point or implicit database rounding.
- A successful demonstration does not establish that a system remains correct when events arrive late, transactions fail or multiple operations execute concurrently.

We have made significant progress.

The project has established its architecture, database foundation, Phys-Research and Phys-Pseudo specification methodology, and the first working HR/WFM/timesheet workflow.

Milestone 1.1 and Milestone 1.2 also introduced substantial corrections to attendance, historical records, database privileges, onboarding and concurrency protection.

However, the latest independent adversarial review identified remaining database-enforcement and compensation-consistency problems. These are now being addressed through the proposed Milestone 1.3.

**The current achievement is not a completed Singapore payroll engine. It is an increasingly well-defined operational foundation for one, together with a much clearer understanding of the guarantees required before the application can be trusted to calculate money.**

# 2. Project Direction and Architectural Decisions

## 2.1 Why we chose a Singapore BPO employer

The original model employer is a fictional Singapore BPO company, Meridian CX Services Pte. Ltd.

The model includes:

- Multiple BPO clients.
- Multiple programmes and service operations.
- Teams and supervisors.
- Different employee skills.
- Day, evening and overnight shifts.
- Full-time and part-time working arrangements.
- Rotating schedules.
- Cross-midnight attendance.
- Time and attendance exceptions.
- Payroll processing requirements.

This was a deliberate choice.

A generic employee attendance application would not expose the complexities that a real BPO workforce system encounters.

A multi-client BPO model forces the design to represent two distinct operational dimensions.

**Employment operations:** the employer's relationship with its employees, including employment terms, working obligations, attendance and remuneration.

**Client service operations:** the employer's contractual obligations to its clients, including programme coverage, queue staffing, skills, service hours and service-level targets.

These domains interact, but they are not legally interchangeable.

A client SLA must not become an employment-law rule. An employee's contractual working arrangement must not be inferred from a client service window.

The distinction is now a foundational part of the domain model.

## 2.2 Why we chose a modular monolith

The application uses:

- One Go executable.
- One PostgreSQL database.
- One REST API.
- One CLI.
- One shared application-service layer.

The CLI and REST handlers are adapters, not separate implementations of business logic.

This avoids:

- Duplicated rules.
- Distributed transactions.
- Synchronization between separate databases.
- Deployment complexity.
- Unnecessary messaging infrastructure.

The architecture remains modular at the domain and package level without becoming a collection of independent services.

This decision has held through the implementation and adversarial reviews.

## 2.3 One employer operationally, multiple employers structurally

The MVP operates with one employer, but its database has employer-scoped records and relationships.

This allows us to validate ownership, composite foreign keys and isolation boundaries without prematurely implementing multi-employer authentication or RLS.

It also establishes a useful design principle:

The legal employer is the data ownership boundary. BPO clients and programmes are operational entities belonging to that employer, not additional tenants.

## 2.4 Singapore-first, jurisdiction-extensible

The current application is Singapore-specific in its business-rule configuration, not permanently tied to a Singapore-only architecture.

The emerging product direction is:

- Shared operational core.
- Separate jurisdiction-specific rule packages.
- Employer policies distinct from statutory rules.
- Customer-specific service operations distinct from employment obligations.

Singapore remains the first implementation. We are not building a universal legal rules engine or adding other country packages before the first Singapore payroll works.

# 3. Establishing the Database and Engineering Foundation

An important early milestone was the real PostgreSQL design gate.

The original baseline contained seven migration versions and approximately 50 tables.

The integrity work demonstrated:

- Employer-scoped relationships.
- Composite foreign-key protection.
- Unique business identifiers.
- Published and approved record immutability.
- Runtime database-role boundaries.
- Audit and approval controls.
- Tests executing against real PostgreSQL rather than relying exclusively on mocked repositories.

The first operational implementation introduced migration `00008_break_events`.

Milestone 1.1 introduced `00009_m11_integrity.sql`.

Milestone 1.2 introduced `00010_m12_integrity.sql`.

The current architecture therefore has a real and evolving PostgreSQL schema rather than a collection of illustrative database diagrams.

Another important decision was separating:

- `migration_admin`, which performs schema migration and protected database operations.
- `app`, which performs normal application operations.

This distinction became especially important during the adversarial reviews. Testing business behavior under a superuser can conceal privilege problems that an ordinary runtime application would encounter.

We learned that database correctness must be demonstrated under the same role the application actually uses.

# 4. Phys-Research and Phys-Pseudo: Establishing Business-Rule Correctness

One of the most valuable developments has been introducing the Phys-Research and Phys-Pseudo methodology into business-rule engineering.

This is not a physics dependency in the payroll application. It is an adaptation of the research and formalization practices developed by Pithom Labs.

## 4.1 Phys-Research: establish the evidence

The methodology provides a process for identifying what authoritative sources actually establish.

For Singapore employment and payroll rules, the research process is:

`Acquire → Preserve → Extract Claims → Investigate → Formalize → Derive → Independently Verify → Implement`

For each important rule, we want:

- Source identity.
- Issuing authority.
- Official URL.
- Source snapshot.
- SHA-256 digest.
- Retrieval timestamp.
- Publication and effective dates.
- Relevant statutory location.
- Extracted claims.
- Applicability conditions.
- Assumptions.
- Contradictions.
- Independent review.

An important distinction emerged during the first MOM retrieval work.

We retrieved official government content through a web-fetch mechanism because the sandbox's ordinary network access was unavailable.

We preserved the exact transcripts and independently verified their hashes.

Those hashes establish the integrity of the captured transcripts, not byte identity with the original official HTML.

Consequently, we did not promote them as byte-grade legislative snapshots.

That distinction is central to the research methodology: evidence must never be assigned a stronger provenance status than the retrieval process actually supports.

## 4.2 Phys-Pseudo: make the rule explicit

The actual local Phys-Pseudo repository was inspected and its authoritative specification adopted.

Its seven constructs are:

- `given`
- `define`
- `assume`
- `claim`
- `derive`
- `if/then/else`
- `?`

These allow a business rule to distinguish source-backed facts, definitions, assumptions, claims, derivations, conditional outcomes and unresolved questions.

No interpreter, compiler or code generator was introduced.

The resulting relationship is:

| Component            | Responsibility                                                 |
| -------------------- | -------------------------------------------------------------- |
| Phys-Research        | Establish the evidential foundation                            |
| Phys-Pseudo          | Represent the rule and its reasoning                           |
| Independent reviewer | Challenge premises and derivations                             |
| Go                   | Implement approved business logic                              |
| PostgreSQL           | Enforce data and relationship integrity                        |
| Tests                | Compare implementation with independently established outcomes |

This separation has been particularly useful in Singapore overtime research.

## 4.3 The overtime example exposed semantic problems before implementation

The original Singapore research PDF contained historical values:

- S$2,500 for a non-workman coverage threshold.
- S$2,250 for a non-workman overtime cap.

The provisional MOM captures consistently showed a S$2,600 non-workman threshold.

A second discrepancy appeared in the monthly-rated hourly-rate example.

Using the monthly-rated formula at S$2,600:

[\
R=\frac{12\times2600}{52\times44}\
]

[\
R=13.63636\ldots\
]

The government guidance displays S$13.60 in its example.

The legal rounding method and stage have not been established.

We also found an incorrect interpretation of the 72-hour monthly overtime limit: a restriction on permitted working hours must not become an automatic cap on payable work.

These discoveries happened before a Go overtime calculator was implemented.

The overtime package therefore remains BLOCKED.

The lesson is significant:

**Correct arithmetic does not guarantee a correct business result. The eligibility conditions, legal interpretation, formula inputs, effective date and rounding method must also be correct.**

# 5. The First Working Backend: Milestone 1

Milestone 1 produced the first integrated employee-to-timesheet workflow.

The delivered capabilities include:

## HR

- Employee creation and listing.
- Status management.
- Employment-term management.
- Compensation information.

## BPO configuration

- Clients.
- Programmes.
- Teams.
- Team memberships.
- Skills.
- Programme-required skills.

## WFM

- Shift templates.
- Shift segments.
- Rotation patterns and assignments.
- Draft rosters.
- Employee assignments.
- Roster publication.

## Attendance

- CSV clock-event import.
- Idempotent source-event handling.
- Clock pairing.
- Break-event evidence.
- Attendance review.
- Audited corrections.

## Timesheets

- Timesheet generation.
- Submission.
- Approval.
- Rejection.

## Payroll readiness

- Required rule-pack evaluation.
- Missing-input detection.
- Explicit blocked results.
- No invented financial outputs.

The demonstration executed the operational workflow through both CLI and REST using real PostgreSQL.

One reported scenario involved:

- An employee with employment terms.
- BPO team and skill assignment.
- A published roster.
- Twenty clock events.
- Attendance review and correction.
- A generated timesheet.
- Approval of 39.5 regular hours.
- Correct exclusion of an unmatched punch.
- A blocked payroll-readiness result because statutory and employee-input requirements remained incomplete.

CLI and REST business results matched across the demonstrated comparison.

This was the first point at which WorkforceOps became a functioning backend rather than an architectural project.

# 6. The Adversarial Reviews Changed Our Understanding

The most significant engineering lessons emerged when we asked independent reviewers to examine the implementation adversarially.

The purpose was not to criticize the development work. It was to discover what a normal successful workflow might fail to reveal.

## 6.1 Milestone 1.1: the first integrity problems

The first adversarial review identified problems involving:

- Approved timesheets and corrected attendance.
- Duplicate and overlapping intervals.
- Cross-midnight attribution.
- Employee historical status.
- Payroll population.
- Concurrent attendance corrections.
- Audit completeness.
- Financial precision.
- Database privileges.
- API workflow completeness.

Some original approved timesheets could remain linked to historical attendance after the underlying attendance had been corrected.

The important discovery was that protecting a row from UPDATE and DELETE does not automatically protect the business meaning of that row.

Its relationships to the evidence it represents must also remain trustworthy.

## 6.2 Milestone 1.2: substantial remediation

The implementation agent reported completing:

- Eight service-reachable defect corrections.
- Ten application-role database protection areas.
- Fresh-install role provisioning.
- Employer creation through CLI and REST.
- Audit gaps.
- Transaction and concurrency protections.
- Exact duration representation.
- Reset safety.
- New PostgreSQL integrity coverage.

Migration `00010_m12_integrity.sql` introduced significant database protections.

In particular:

- Attendance correction groups.
- Current-interval maintenance.
- Supersession guards.
- Timesheet identity protection.
- Roster publication and revision protection.
- Controlled employment-term and status transitions.
- Restricted database functions.

The report recorded 24 Go packages, race tests across five service packages, application-role SQL checks and fresh-install tests.

These are meaningful engineering achievements.

## 6.3 The final independent review exposed another layer

The subsequent Prompt 12 adversarial review synthesized eight review contributions.

It confirmed that the normal service paths were considerably stronger, but found remaining problems in database enforcement and compensation consistency.

The final verdict was FAIL, notwithstanding the implementation agent's reported PASS.

This distinction is important.

The latest review did not conclude that the architecture was wrong. It concluded that the database could still accept certain invalid states or allow transactions to bypass consistency checks.

The most significant findings were:

- Correction segments were not fully bound to their predecessor groups.
- Correction-group ownership was not completely protected.
- Re-correcting one segment could create partial lineage.
- Direct SQL could misdate attendance segments.
- Backdated evidence could escape readiness checks.
- Concurrent duplicate dispositions could potentially form cycles.
- Compensation could escape readiness checks through a transaction-ordering race.
- Compensation could be inserted outside its employment-term boundaries.
- PostgreSQL could silently round excess monetary precision.

This is why the current project status needs to be described carefully:

**Milestone 1.2 was implemented and reported PASS by its implementation agent, but its subsequent independent acceptance review returned FAIL.**

We should not mistake successful delivery of the remediation code for independent proof that every required invariant now holds.

# 7. The Technical Challenges We Have Encountered

The following challenges provide the most useful insights into building a realistic enterprise workforce system.

## 7.1 A clock event is not an attendance interval

A raw clock-in and clock-out are pieces of source evidence.

They still need to be interpreted against:

- Employee identity.
- Source event identity.
- Shift assignment.
- Event ordering.
- Break evidence.
- Business-date boundaries.

An unpaired event must not silently disappear or become an arbitrary interval.

A duplicate event must not become duplicate work.

A valid overlap must not be dismissed merely to unblock timesheet generation.

We learned that importing clock events and producing trustworthy attendance are two different technical responsibilities.

## 7.2 Time has multiple meanings

A BPO system needs to understand at least:

- Source/event timezone.
- Employee employment timezone.
- Client/service timezone.
- UTC instant.
- Employee-local business date.
- Scheduled shift.
- Actual interval.
- Payroll period.

These cannot be collapsed into one timezone or one date field.

An overnight shift from 22:00 to 06:00 is one operational shift, but it crosses two employment business dates.

Breaks can also cross midnight.

A corrected interval may need to be split across multiple local dates and assigned to separate roster records.

We learned that temporal attribution is a domain-model problem, not merely a date-conversion problem.

The current Singapore profile uses `Asia/Singapore` for employment attribution, while vendor timestamps and client service windows remain separate inputs.

## 7.3 Corrections are part of the normal business model

Historical information is not always correct.

Clock events can arrive late. Breaks can be recorded after an interval has been reviewed. Compensation can be entered incorrectly. Employment terms can require correction.

The solution cannot simply be to make every historical row immutable and offer no way to fix it.

The emerging model is append-only succession:

- Preserve the original record.
- Create a successor or correction group.
- Preserve the reason and actor.
- Maintain lineage.
- Resolve the effective version explicitly.
- Prevent double-counting.

This has become a central concept across attendance, timesheets and compensation.

## 7.4 A correction group is not merely a collection of intervals

The cross-midnight correction problem revealed a subtle data-model challenge.

One original interval may need to produce multiple date-segmented intervals.

A simple one-successor foreign key cannot represent that relationship properly.

We therefore explored a correction-group predecessor relationship.

This permits a logical correction to replace multiple related intervals while preserving one coherent successor operation.

The next design must ensure that:

- Every predecessor is included.
- Every successor belongs to the group.
- A correction cannot partially replace a multi-segment group.
- No segment is duplicated or orphaned.
- All segments commit atomically.

This is an example of how a seemingly minor attendance feature exposed a more general data-modeling principle.

## 7.5 Database triggers are not automatically secure

A further lesson came from PostgreSQL security review.

An application service might correctly validate a correction, but the `app` role can also execute SQL directly.

If the database allows an invalid relationship to be inserted, the invariant is not fully protected.

We encountered:

- Trigger re-parenting issues.
- `SECURITY DEFINER` function hardening.
- `search_path` and temporary-table concerns.
- Direct modification of current-state metadata.
- Cross-employee and cross-employer lineage.
- Historical-record update privileges.

The lesson is that database integrity must be independently tested under the ordinary application role.

A green test executed as a superuser does not prove that the actual application privilege model is safe.

## 7.6 Transaction-start time is not commit order

This is arguably our most interesting PostgreSQL discovery.

PostgreSQL `now()` represents the start time of the current transaction.

That means a row can be created in a transaction that began before approval but committed afterwards.

A readiness query comparing `created_at` to `approved_at` can miss the new evidence.

This is not a clock synchronization error.

It is a concurrency and transaction-ordering problem.

The lesson is that historical approval must be protected by transaction ordering, not merely timestamp comparison.

The proposed Milestone 1.3 solution introduces a shared employee-level advisory lock and a per-employee basis sequence.

The objective is to make approval and pay-basis changes serialize consistently.

That implementation is still planned, not completed.

## 7.7 Financial precision requires database-level protection

The system uses exact money representations.

However, PostgreSQL can round values when they are assigned to a fixed-scale `NUMERIC` column.

The adversarial review demonstrated that inserting `199.999` into `NUMERIC(12,2)` can result in `200.00`.

A Go validation rule cannot protect the database against direct SQL.

The planned correction is unconstrained numeric storage with exact-scale checks, alongside integer minor units at the Go application boundary.

The broader insight is that numerical precision must be enforced before lossy conversion, not after it.

## 7.8 Payroll readiness is itself a business-critical process

The project initially treated payroll readiness as a relatively simple status response.

The reviews showed that readiness must account for:

- Expected employees.
- Employment applicable to the period.
- Approved timesheets.
- Corrected timesheet versions.
- Compensation.
- Attendance evidence.
- Explicit exclusions.
- Required statutory rule-pack versions.
- Historical conflicts.

The population cannot be defined only by employees who already have approved timesheets. Otherwise, an employee who should have been paid but is missing a timesheet may disappear from the assessment.

Readiness must explain not only who is included, but also who should have been included and why someone is absent.

This is an important foundation for future payroll reconciliation.

# 8. What the Project Has Achieved So Far

The following is the consolidated status at the current reporting point.

| Capability                        | Status                                                                     |
| --------------------------------- | -------------------------------------------------------------------------- |
| Single Go application             | Implemented                                                                |
| PostgreSQL foundation             | Implemented                                                                |
| Employer-scoped database design   | Implemented                                                                |
| CLI and REST shared services      | Implemented                                                                |
| HR employee administration        | Implemented                                                                |
| Effective-dated employment terms  | Implemented, further integrity hardening planned                           |
| BPO clients and programmes        | Implemented                                                                |
| Teams and employee skills         | Implemented                                                                |
| Shift templates and rosters       | Implemented                                                                |
| Roster revisions                  | Implemented                                                                |
| Clock-event imports               | Implemented                                                                |
| Break-event evidence              | Implemented                                                                |
| Attendance pairing and correction | Implemented, additional group-integrity work planned                       |
| Timesheet generation and approval | Implemented, further correction-chain verification planned                 |
| Payroll readiness                 | Implemented, additional compensation and population-integrity work planned |
| Phys-Research methodology         | Adopted                                                                    |
| Phys-Pseudo specification layer   | Established                                                                |
| Singapore overtime calculation    | Not implemented; BLOCKED                                                   |
| CPF calculation                   | Not implemented                                                            |
| SDL calculation                   | Not implemented                                                            |
| SHG calculation                   | Not implemented                                                            |
| Gross/net payroll calculation     | Not implemented                                                            |
| Financial payslips                | Not implemented                                                            |

## Test and implementation evidence

Milestone 1.2's remediation report records:

- 24 Go packages.
- Full test-suite execution.
- Full-suite execution under UTC.
- Race tests across attendance, timesheet, payroll, HR and WFM.
- PostgreSQL-backed integrity tests.
- Application-role SQL probes.
- Provisioning tests.
- Employer-onboarding tests.
- CLI/REST parity checks.
- No statutory calculations.

The subsequent independent review found that some claims rested on static inspection rather than live database probes, and that certain compensation and correction-group defects remained.

Therefore, these test results demonstrate substantial implementation and testing progress, but not final acceptance of the entire integrity model.

# 9. Milestone 1.3: The Remaining Integrity Work

The next planned milestone is narrowly focused on the defects identified by the final review.

It has five main technical objectives.

## 9.1 Correction-group enforcement

Add database-level enforcement linking:

- Correction group.
- Predecessor intervals.
- Successor segments.
- Employer.
- Employee.
- Local business date.

Ensure that multi-segment corrections cannot be partially replaced.

## 9.2 Compensation/approval serialization

Introduce a shared lock and per-employee basis sequence.

Make compensation insertion/correction and approval follow the same ordering protocol.

Prove both possible lock-winner orders using separate PostgreSQL connections.

## 9.3 Compensation lifecycle

Move compensation writes behind controlled database functions.

Enforce:

- Employment-term containment.
- Same-code overlap rules.
- Exact monetary precision.
- Immutable compensation succession.
- Atomic audit events.
- Explicit readiness conflicts.

## 9.4 Duplicate-event concurrency

Ensure that a canonical clock event cannot become a duplicate in a way that produces cycles or makes legitimate evidence unavailable.

Use deterministic locking and direct-SQL concurrency tests.

## 9.5 Reliable testing and migration policy

Make clean-server database provisioning reliable.

Make the application-role privilege matrix fail closed when an unexpected table is introduced.

Define an explicit forward-only policy where rollback would destroy meaningful historical correction data.

The objective is to close the integrity defects without expanding the application's feature set.

The Prompt 13 implementation plan has been prepared and reviewed, including additional clarifications for transaction ordering, correction groups and migration rollback. It has not yet been executed.

# 10. Major Insights and Lessons Learned

The development experience so far has produced several lessons that should influence the rest of the project.

## Lesson 1: Model business meaning before modeling tables

A field such as `hours_worked` appears convenient but conceals several different quantities.

We now distinguish:

- Scheduled hours.
- Recorded attendance.
- Actual work.
- Paid time.
- Approved timesheet hours.
- Statutory overtime hours.

The correct data model emerges from understanding these meanings, not from starting with a generic employee attendance table.

## Lesson 2: A business system is a network of evidence

A payroll result depends on earlier business events.

Each result should be traceable to the records that support it.

For example:

`Clock Event → Attendance → Correction → Timesheet → Payroll Input`

A defect in any relationship can make the final amount unreliable even if the calculation formula is correct.

## Lesson 3: Immutability and correction must be designed together

Immutability without correction creates operational dead ends.

Correction without immutability destroys historical trust.

The solution is controlled succession with explicit lineage, approval and effective-version resolution.

This pattern is relevant beyond timesheets. It applies to employment terms, compensation, rosters and payroll records.

## Lesson 4: Application validation and database enforcement serve different purposes

Go services make the system easier to use and provide meaningful business errors.

PostgreSQL protects the data even when a service is bypassed.

Neither should substitute for the other.

A critical invariant should be enforced at the strongest appropriate database boundary and also validated in the application service.

## Lesson 5: Concurrency is business logic

The compensation approval race demonstrated that transaction ordering is not an infrastructure detail.

It changes whether a financial result represents the correct historical inputs.

Locks, sequence numbers and transaction boundaries are part of the business semantics.

## Lesson 6: Timezones are part of the domain model

Host timezone, event timezone, employee timezone and client timezone cannot be treated as interchangeable.

The same physical instant must produce the same employment business date regardless of where the Go process runs.

Timezone rules must be explicit from ingestion through approval.

## Lesson 7: Formal specifications are useful because they expose uncertainty

Phys-Pseudo has helped distinguish verified claims from assumptions and unresolved questions.

The overtime discrepancy demonstrated that a formal-looking formula can still rest on an unverified legal premise.

The correct response is to preserve uncertainty, not hide it behind a convenient implementation.

## Lesson 8: Independent tests must have independent expected results

Tests that reproduce the implementation's own assumptions can provide false confidence.

For statutory calculations, expected outcomes should originate from authoritative evidence and independent derivations.

For database integrity, tests must inspect persisted state under the actual runtime role.

For concurrency, tests must use concurrent transactions, not only mocked timestamps.

## Lesson 9: The happy path is only the beginning

The first employee-to-timesheet workflow worked.

The most consequential defects appeared when we introduced:

- Late events.
- Duplicate feeds.
- Cross-midnight shifts.
- Post-approval corrections.
- Historical employment changes.
- Concurrent transactions.
- Direct SQL.
- Fresh installations.

These are not unusual edge cases in a workforce system. They are normal operational conditions that the product must handle.

## Lesson 10: Resist premature generalization

Singapore provides a concrete environment for developing and testing the business model.

A Singapore-first implementation allows us to establish real requirements before abstracting other jurisdictions.

The shared operational core should remain neutral, but CPF, SDL, SHG, overtime and other statutory concepts should retain their jurisdiction-specific semantics.

The goal is one extensible product, not one fictional universal payroll formula.

# 11. What We Should Preserve Going Forward

Several engineering decisions should remain stable:

1. Keep the single Go modular monolith.
2. Keep PostgreSQL as the authoritative business-data store.
3. Keep CLI and REST on shared application services.
4. Keep employer-scoped data relationships.
5. Keep operational and statutory rules separate.
6. Keep Phys-Research and Phys-Pseudo for high-consequence business rules.
7. Keep exact monetary representations.
8. Keep historical records immutable and corrections traceable.
9. Keep the application-role database integrity tests.
10. Keep implementation scope focused on a working Singapore backend.

There is no evidence that we need another architectural rewrite.

The remaining problems are concentrated in specific integrity relationships and transaction boundaries.

# 12. What Comes After Milestone 1.3

Once the integrity gate passes, the next objective should be the first independently verified Singapore payroll calculation.

I recommend a deliberately restricted employee profile:

- Singapore Citizen.
- Monthly-rated employment.
- Full salary period.
- No overtime.
- No unpaid leave.
- No variable earnings.
- Complete and verified statutory inputs.

The initial calculations should cover the applicable CPF and SDL requirements, together with SHG treatment where required by the supported profile.

Each statutory calculation must first pass its Phys-Research and Phys-Pseudo verification lifecycle.

Only after those results are verified should we connect them to the shared payroll orchestration layer and generate the first reproducible payroll run.

The overtime package remains independent and blocked until its outstanding legislative and mathematical questions are resolved.

This sequencing allows the product to make progress without allowing unresolved overtime research to hold back every other payroll component.

# 13. Conclusion

WorkforceOps has progressed through several stages of technical learning.

We began with a specific business problem: build an integrated Singapore HR, WFM and payroll backend.

We now have:

- A functioning Go application.
- A real PostgreSQL schema.
- HR and BPO configuration.
- Shift and roster management.
- Clock and break evidence.
- Attendance interpretation and correction.
- Timesheet generation and approval.
- Payroll-readiness assessment.
- A formal research and business-rule specification methodology.
- A substantial adversarial testing process.

The project has also exposed deeper problems involving historical records, correction lineage, timezone attribution, direct database access, transaction ordering and monetary precision.

These findings have improved our understanding of the product far more than adding another collection of endpoints would have.

The central engineering lesson is that a reliable payroll system is not merely a collection of formulas.

It is a system that must preserve the integrity of every business decision and every piece of evidence leading to a financial result.

A correct formula applied to incorrect attendance is still an incorrect payroll result. An immutable timesheet connected to stale evidence is still an unreliable financial record. A green service test does not establish database safety if the runtime role can bypass the same invariant through SQL.

The next objective is therefore to finish the remaining integrity work, establish one trustworthy historical evidence chain, and then produce the first independently verified payroll amount.

**We are moving from building a workforce application to engineering a system whose business results can be explained, traced, reproduced and independently verified.**

That is the most valuable progress WorkforceOps has made so far.

## My assessment

The biggest achievement so far is not the number of features delivered. It is the change in our understanding of the system's correctness requirements.

We have learned that four things must agree:

1. The business meaning — what actually happened to the employee.
2. The evidence — what the source systems recorded.
3. The database state — what the application has preserved and approved.
4. The financial interpretation — how verified rules turn those inputs into money.

The latest adversarial review demonstrates that the normal service workflows are much stronger, but the database and transaction-ordering guarantees still need work. Prompt13 is intended to close that gap; it is not yet a completed milestone.&#x20;

milestone1_2_final_adversarial_review\.md

plan_prompt13.md



I would keep this report as the technical development retrospective for WorkforceOps through 9 November 2026. It records not just what we built, but why the engineering decisions evolved as they did.