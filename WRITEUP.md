# WorkforceOps Lab: Applying Phys-Research and Phys-Pseudo to Engineering an Evidence-Driven HR, WFM and Payroll Backend

**Technical Architecture and Engineering Methodology**

**Project:** WorkforceOps Lab  
**Organization:** Pithom Labs  
**Repository:** `github.com/PithomLabs/a1`  
**Date:** 8 November 2026  
**Status:** Methodology established; first operational backend implementation underway

---

# 1. Executive Summary

WorkforceOps Lab is an open-source Go-based integrated HR, Workforce Management (WFM) and payroll backend, initially modeling a fictional Singapore BPO employer.

Its purpose is to provide a functioning business system with realistic employment records, multi-client workforce operations, attendance, timesheets and payroll workflows, based on an explicitly defined Singapore business-rule coverage profile.

The difficult part is not creating database tables or implementing payroll formulas. It is ensuring that the business rules encoded in the software actually represent the applicable requirements, including their assumptions, eligibility conditions, exceptions, effective dates and boundary cases.

We are addressing this problem by adapting two complementary methodologies from the Pithom Labs research and formalization work:

- **[Phys-Research](https://github.com/PithomLabs/phys-research):** An evidence-driven research methodology for acquiring authoritative sources, preserving provenance, extracting claims, investigating contradictions, establishing derivations and independently verifying conclusions.
- **[Phys-Pseudo](https://github.com/PithomLabs/phys-pseudo):** A formal specification language for expressing the meaning and logical structure of those conclusions before they are implemented in Go.

Their responsibilities are deliberately different.

Phys-Research establishes the evidential basis for a business rule. Phys-Pseudo expresses the rule and its reasoning in a structured, reviewable form.

The resulting engineering sequence is:

`Authoritative source → Evidence capture → Claims → Phys-Pseudo specification → Independent derivation → Verified fixtures → Go implementation → Automated verification`

This approach improves the entire software lifecycle:

1. **Design:** Business requirements become explicit, bounded and traceable rather than informal assumptions.
2. **Development:** Developers receive precise inputs, outputs, eligibility conditions, invariants and unresolved cases.
3. **Implementation:** Go code implements an approved specification rather than an interpretation improvised during coding.
4. **Testing:** Independent mathematical derivations and boundary fixtures test the implementation without using the implementation itself as the source of expected results.
5. **Maintenance:** Effective-dated rules, immutable evidence and calculation-version records make historical results more reproducible and auditable.

The methodology has already demonstrated practical value through the discovery of material inconsistencies in Singapore overtime research before implementation.

Importantly, this does not mean every feature requires formalization. We apply the greatest rigor to statutory, financial and high-consequence rules, selective formalization to complex operational workflows, and conventional engineering practices to ordinary infrastructure and CRUD operations.

The objective is not to build an elaborate research framework. It is to prevent incorrect business rules from becoming executable software while keeping the MVP small enough to deliver.

# 2. The Engineering Problem We Are Solving

A conventional HR or payroll application often follows a straightforward development sequence:

1. Read a business requirement.
2. Interpret the requirement.
3. Create database fields.
4. Write calculation functions.
5. Generate test cases.
6. Deploy.

This works reasonably well for ordinary application functionality. It is dangerous when the requirement depends on legislation, statutory formulas, eligibility classifications, effective dates or legally prescribed rounding.

A plausible interpretation can still be wrong.

Consider a payroll developer implementing Singapore overtime calculations.

The developer might encounter:

- An employee salary threshold.
- An hourly basic-rate formula.
- A statutory cap.
- An overtime multiplier.
- A monthly maximum-hours provision.

The developer may translate these into a formula that looks mathematically sound and passes all ordinary unit tests.

But what if:

- The threshold came from an obsolete regulatory provision?
- The cap is stated differently in the official guidance and statutory formula?
- A maximum working-hours restriction is incorrectly applied as a maximum payable-hours restriction?
- The employee classification is wrong?
- An allowance is incorrectly included in basic salary?
- The formula changes partway through a historical payroll period?
- A worked example reflects a rounding convention that is not explicitly documented?

The software can be internally consistent and still produce an incorrect payroll result.

This is the fundamental problem addressed by the two methodologies.

**We must establish the correctness of the business rule independently of the correctness of its implementation.**

# 3. Phys-Research: Establishing the Evidential Foundation

## 3.1 Role in WorkforceOps

Phys-Research provides the methodological foundation for treating business rules as research objects with explicit provenance, claims, assumptions, derivations and verification records.

For WorkforceOps, we adapt this research discipline to Singapore employment and payroll regulations.

The source of authority is not an AI model, generated answer or third-party explanation.

It is the applicable authoritative legislation, government publication or recognized issuing authority for the specific rule.

The research process is:

`Acquire → Pin → Extract → Formalize → Derive → Verify → Implement`

Each stage has a different responsibility.

### Acquire

Obtain the relevant official material.

Examples include:
- Singapore Employment Act.
- Subsidiary legislation and schedules.
- Ministry of Manpower guidance.
- CPF Board contribution rules.
- Skills Development Levy guidance.
- Applicable self-help-group contribution schedules.

### Pin

Preserve the source and its identity.

Record:
- Issuing authority.
- Document identifier and title.
- Official URL.
- Retrieval timestamp.
- Publication and effective dates.
- Relevant section, subsection or table.
- Original source artifact.
- SHA-256 digest.
- Retrieval method and limitations.

A source must be distinguishable from the extracted representation of that source.

This distinction has already mattered in our research.

Our first MOM retrieval pass produced processed-text transcripts. Their SHA-256 hashes were independently recomputed and matched, but those hashes only establish the integrity of the transcripts themselves. They do not establish byte identity with the original official HTML.

We therefore classified them as provisional evidence rather than claiming they were byte-grade pinned sources.

### Extract

Convert source material into a claim ledger.

A claim identifies a proposition supported by a source or identifies something that remains unresolved.

For example:

- The employee category to which a provision applies.
- The relevant salary threshold.
- The formula defining an hourly rate.
- The effective date of an amendment.
- An exception to a working-hours restriction.
- The rounding convention applicable to a monetary calculation.

Each claim has provenance and a verification state.

A search-result snippet does not become evidence merely because it contains the expected number.

### Investigate

Compare provisions, versions and independent sources.

The objective is to identify:
- Contradictions.
- Historical changes.
- Ambiguous language.
- Incomplete formula definitions.
- Hidden assumptions.
- Different meanings of apparently similar terms.

This is particularly important where different legal provisions govern different dimensions of the same calculation.

For example, eligibility for overtime, the formula for overtime remuneration and the maximum permitted overtime hours should not be collapsed into one undifferentiated rule.

### Derive

Independently establish the mathematical or logical consequences of verified source claims.

For monetary calculations, use exact arithmetic rather than binary floating-point approximations.

Where an interpretation is disputed, the derivation must preserve the competing possibilities rather than select one to make the implementation convenient.

### Verify

Subject the claims and derivations to independent review.

The reviewer should receive the source evidence and the reasoning to be evaluated, not merely the original author's preferred answer.

Golden, anti-golden and boundary fixtures must reflect independently established outcomes.

The implementation must not generate its own expected answers and then use them to certify itself.

### Implement

Only after the required evidence, derivation, fixture and review gates have been satisfied should a statutory rule become executable business logic.

This is how Phys-Research prevents research uncertainty from silently becoming production behavior.

## 3.2 Rule lifecycle

Every L1 business-rule package follows the established lifecycle:

`DISCOVERED → SOURCES_PINNED → CLAIMS_EXTRACTED → FORMALIZED → INDEPENDENTLY_CHECKED → GOLDENS_VERIFIED → IMPLEMENTABLE`

The lifecycle is deliberately conservative.

A syntactically complete specification is not necessarily a verified specification.

A verified transcript is not necessarily a pinned original source.

A mathematically correct derivation from an obsolete premise is not a legally correct calculation.

A passing Go test does not prove that the statutory interpretation was correct.

Each status represents a distinct evidence requirement.

# 4. Phys-Pseudo: Making Business Rules Explicit

## 4.1 Why a formal specification layer is necessary

Research findings are commonly communicated in prose.

Prose is useful for explanation, but it makes it easy to conceal ambiguity.

For example:

"Calculate overtime using the employee's hourly basic rate, subject to the applicable statutory salary cap."

This sentence sounds clear until someone asks:

- Which employee categories are eligible?
- Which salary components are included?
- Which effective date determines the applicable threshold?
- What does the cap apply to?
- At what stage is rounding performed?
- What happens when source provisions conflict?
- What happens when required employee data is missing?

Phys-Pseudo gives us a structured way to express these matters.

We inspected the actual local Phys-Pseudo repository at commit `b9bb9a4`, including its authoritative `PSEUDO.md`.

The language has seven constructs:

- `given`
- `define`
- `assume`
- `claim`
- `derive`
- `if/then/else`
- `?`

The original syntax and semantics are adopted without introducing new language constructs.

The specification also preserves an important invariant:

**Representing a proposition does not mean endorsing it.**

That is particularly valuable in regulatory research, where a disputed interpretation must remain visible rather than acquire authority simply because someone wrote it in a formal-looking document.

## 4.2 How we use the seven constructs

### `given` — Evidence and supplied facts

Captures source-backed information or explicitly supplied scenario inputs.

Example categories:
- A statutory salary threshold.
- An employee's monthly basic salary.
- A documented working arrangement.
- A source-defined calculation formula.

The source and its locator should accompany the relevant factual statement.

### `define` — Precise terminology

Defines the variables and concepts used in the rule.

Examples:
- Monthly basic rate.
- Statutory basic-pay basis.
- Overtime hours.
- Covered employee.
- Applicable contribution period.

This prevents different modules from assigning different meanings to the same business term.

### `assume` — Explicit assumptions

Identifies conditions accepted for a specific scoped analysis.

An assumption is not a verified legal fact.

For example, a reference-employer scenario may specify that the employee is monthly-rated. That is a scenario assumption, not a statutory conclusion.

Uncertain legal interpretations should not be disguised as assumptions to make a specification appear complete.

### `claim` — A proposition to establish

Identifies the result or rule assertion being examined.

Examples:
- An employee satisfies a particular eligibility condition.
- A certain wage component belongs to a specific CPF wage classification.
- A proposed attendance interval satisfies an operational constraint.

The existence of a claim does not mean it has been proven.

### `derive` — The reasoning chain

Shows how a result follows from previously established facts, definitions, assumptions and propositions.

The derivation must make its premises explicit.

It should expose:
- Formula inputs.
- Intermediate values.
- Logical dependencies.
- Relevant conditions.
- Mathematical transformations.
- The precise result being established.

### `if/then/else` — Conditional applicability

Expresses alternative outcomes under different conditions.

This is especially important for employment rules because a calculation that applies to one employee category may be inapplicable to another.

### `?` — Explicit uncertainty

Marks an unresolved question at the appropriate point in the specification.

The question must not disappear merely because an implementation deadline approaches.

A load-bearing unresolved question blocks the corresponding implementation path until the uncertainty is resolved or explicitly represented as an unsupported scenario.

## 4.3 Business-rule package structure

Each L1 rule has a reusable, inspectable package:

```text
rules/
├── _template/
└── SG-MOM-OT-001/
    ├── manifest.json
    ├── sources/
    │   └── SOURCES.md
    ├── claims.md
    ├── SPEC.pseudo.md
    ├── derivation.md
    ├── edge-cases.md
    ├── fixtures/
    │   ├── golden/
    │   ├── anti-golden/
    │   └── boundary/
    ├── REVIEW.md
    └── contract.md
```

Each file has a specific responsibility.

| Artifact | Responsibility |
|---|---|
| `manifest.json` | Rule identity, applicability, version and lifecycle status |
| `SOURCES.md` | Source provenance and evidence |
| `claims.md` | Extracted propositions and their evidence |
| `SPEC.pseudo.md` | Formal representation of the rule |
| `derivation.md` | Independent reasoning and arithmetic |
| `edge-cases.md` | Boundary conditions and exceptional paths |
| `fixtures/` | Independently established expected outcomes |
| `REVIEW.md` | Independent verification findings |
| `contract.md` | Requirements for the eventual Go implementation |

This makes the rule package portable, reviewable and usable by both developers and coding agents.

# 5. What We Have Learned: SG-MOM-OT-001 as a Practical Case Study

The first exemplar is ordinary overtime for the restricted monthly-rated employee profile.

It has already shown why the methodology matters.

## 5.1 A historical source discrepancy

The original `SG_research.pdf` contained:
- S$2,500 non-workman coverage threshold.
- S$2,250 overtime calculation cap.

The provisional MOM captures consistently state:
- S$2,600 non-workman threshold.
- S$4,500 workman threshold.

The earlier research therefore contained historical values that could have been carried directly into Go if we had followed a conventional implementation process.

The methodology forced us to distinguish:
- What the old document says.
- What current captured MOM guidance says.
- What the legislative amendment history still needs to establish.
- Which effective date applies.

The result was not an immediate formula. It was a better-defined research problem.

## 5.2 A mathematical discrepancy that ordinary testing might miss

The MOM material states:
- Monthly basic salary cap: S$2,600.
- Hourly figure: S$13.60.
- Worked example: S$13.60 × 1.5 × 2 = S$40.80.

However, applying the published monthly-rated formula directly gives:

\[
R=\frac{12\times2600}{52\times44}
\]

\[
R=\frac{31200}{2288}
=13.63636\ldots
\]

This produces a different result from the S$13.60 figure.

At two overtime hours, the unrounded calculation gives approximately S$40.91, while the MOM example gives S$40.80.

The difference is material.

The current evidence does not establish:
- The exact rounding rule.
- Whether rounding occurs at the hourly-rate stage or later.
- Whether the hourly figure is a prescribed rate or a rounded expression of the monthly cap.
- The relevant statutory derivation.

The package therefore preserves the discrepancy rather than selecting a mathematically convenient interpretation.

## 5.3 An incorrect interpretation of the 72-hour limit

The original specification treated 72 hours as a maximum payable-hours quantity.

The MOM guidance instead describes a maximum number of overtime hours an employee can work in a month, subject to an exemption process and specific treatment of rest-day and public-holiday hours.

These are different business concepts.

A maximum working-time limit cannot automatically be used as a ceiling on remuneration.

This finding has implications for the entire system:
- Attendance must retain actual work performed.
- Timesheets must preserve actual work evidence.
- Statutory working-time violations must be represented separately.
- Payroll cannot silently delete recorded hours.

The distinction was discovered before the overtime calculation was implemented.

## 5.4 Eligibility must be derived, not assumed

A calculation is only meaningful after eligibility has been established.

The specification must trace:

`Employee category → Monthly basic salary → Effective date → Part IV eligibility → Applicable formula`

MOM's coverage guidance also distinguishes basic salary from allowances, bonuses, overtime and certain other payments.

This means the underlying employment and compensation model must retain enough information to identify the correct salary basis.

A field named `salary` is not sufficient.

## 5.5 Independent review found defects before implementation

The independent review detected:
- Incorrect intermediate arithmetic.
- A missing premise in a derivation.
- Reliance on an unestablished claim.
- Overstated evidence concerning the 72-hour limit.
- An assumption incorrectly implying the effective date was pinned.
- Specification wording that approached prohibited status labeling.

These corrections improved the quality of the research package without requiring a single Go payroll calculation.

This is an important demonstration: formal specification plus adversarial review can identify defects that ordinary application testing would not discover.

# 6. How the Combined Methodology Improves the Software Lifecycle

## 6.1 Design: Better domain models and system boundaries

A conventional database design often begins with entities and relationships, then accommodates special rules later.

Our process works differently.

We identify the required business concepts and invariants from the research and formalization process before deciding how they should be represented in the database.

Examples:

**Employment eligibility and salary basis**

The Part IV research makes it clear that employee classification and monthly basic salary are separate inputs. This is reflected in effective-dated employment terms rather than inferred from a job title.

**Scheduled time versus actual time**

WFM rules require us to distinguish rostered activity, actual attendance, paid time and approved payroll inputs.

**Cross-midnight attendance**

Business-date attribution and actual timestamps must be represented separately. A shift crossing midnight cannot simply be treated as one calendar-day attendance record.

**CPF Ordinary Wages versus Additional Wages**

The CPF research makes it clear that a generic `cpf_payable` Boolean does not adequately represent the wage classification and contribution-period requirements.

**Employer versus BPO client**

The employer is the legal employing entity. Clients and programmes are operational relationships within that employer, not separate tenants.

The resulting domain model is more faithful to the business system we intend to build.

The important point is that formalization informs data design rather than being added after the database has already become difficult to change.

## 6.2 Development: Clearer responsibilities and less interpretation during coding

The methodology gives developers an explicit implementation contract.

A developer should not have to decide independently:
- Which source to trust.
- Which salary components are eligible.
- What constitutes overtime.
- Whether an exception can be treated as zero.
- How historical rule changes apply.
- What happens when a required input is missing.

Those decisions should be in the rule package.

The Go application then has a much cleaner responsibility:

- Parse and validate business inputs.
- Load the applicable verified rule version.
- Execute the approved calculation.
- Return a defined result or an explicit indeterminate/error state.
- Preserve the input and output lineage.

This reduces the amount of legal interpretation embedded inside application services.

It also makes code reviews more productive. Reviewers can compare implementation behavior against an established specification instead of debating the meaning of legislation while simultaneously reviewing Go code.

## 6.3 Implementation: Stronger boundaries between infrastructure and statutory rules

WorkforceOps retains a conventional Go architecture.

The core application provides:
- Employer context.
- Database access.
- Application services.
- API handlers.
- CLI commands.
- Audit and workflow orchestration.

Statutory rule packages remain pure.

The existing engineering agreements require:
- Standard library and `math/big` for rule packages.
- No database or HTTP dependency inside calculations.
- No binary floating-point arithmetic for money.
- Effective-dated rule parameters.
- Version-pinned calculations.

This separation is essential.

A payroll calculation should not depend on which HTTP endpoint called it, which database connection happened to be used, or which CLI command invoked it.

The same rule function should produce the same result from the same verified inputs.

Phys-Pseudo does not generate the Go implementation. It specifies what the implementation must mean.

This keeps the architecture simple while improving the quality of the business logic.

## 6.4 Testing: Independent expected results rather than self-confirming tests

This is arguably the most significant engineering improvement.

Traditional unit testing can produce false confidence when the implementation and the expected result are derived from the same mistaken interpretation.

Our process requires a different chain:

`Authoritative source → Formal derivation → Expected fixture → Go implementation → Test comparison`

The expected result must not originate from the implementation being tested.

For L1 rules, fixtures are separated into:

**Golden cases**

Inputs and outputs established by authoritative evidence and independently checked derivations.

**Anti-golden cases**

Inputs that should not be accepted or should produce an explicitly different result.

**Boundary cases**

Values around thresholds, caps, effective dates, rounding points and applicability transitions.

For example, the overtime rule should eventually test:
- Salary immediately below the eligibility threshold.
- Salary exactly at the threshold.
- Salary immediately above the threshold.
- Values around the statutory cap.
- Zero overtime hours.
- Multiple overtime intervals.
- Historical effective-date transitions.
- Break exclusions.
- Unsupported employee categories.
- Missing or ambiguous inputs.

Until the relevant rules are verified, these are test-design requirements, not fabricated golden results.

The same philosophy applies to WFM:
- An employee without a required skill cannot be assigned to a programme.
- A published roster cannot be silently edited.
- An attendance interval cannot reference another employee's roster assignment.
- Repeated clock-event delivery must not create duplicate records.
- An approved timesheet cannot be changed in place.

These are operational invariants that can be tested directly against PostgreSQL.

## 6.5 Maintenance: Historical reproducibility and explainable changes

Employment and payroll rules change over time.

A calculation made under one rule version must not silently change because a new threshold or rate becomes effective.

The design therefore preserves:
- Effective-dated rule data.
- Rule-pack versions.
- Employment-term versions.
- Approved timesheets.
- Payroll input references.
- Calculation implementation versions.
- Rounding conventions.
- Immutable approved payroll outputs.

The reproducibility contract is:

`Same immutable inputs + same rule versions + same calculation version = same payroll output`

End-to-end reproducibility has not yet been executed because the payroll calculation engine has not been implemented. However, its data and verification requirements have been specified.

This is how the methodology prepares the application for regulatory change without turning every update into a rewrite of historical payroll results.

# 7. Applying Different Levels of Rigor

We do not want Phys-Pseudo to become mandatory paperwork for everything.

The project uses three rigor levels.

## L1 — Statutory, financial and high-consequence rules

Require a complete research package, formal specification, independent derivation, golden/anti-golden/boundary fixtures and independent review.

Examples:
- Singapore overtime.
- CPF contributions.
- Skills Development Levy.
- Self-Help Group deductions.
- Statutory leave calculations.
- Public-holiday pay.

## L2 — Complex operational workflows and integrations

Require explicit state transitions, preconditions, postconditions, invariants and exception handling.

Use Phys-Pseudo selectively for complex calculations or critical decisions.

Examples:
- Roster publication.
- Attendance corrections.
- Timesheet approval.
- Payroll exclusions.
- WFM-to-payroll attribution.

## L3 — Ordinary application functionality

Use standard Go validation, database constraints and conventional automated tests.

Examples:
- Basic employer lookup.
- Simple employee listing.
- Configuration metadata.
- Health endpoints.
- Migration bookkeeping.

This division concentrates engineering effort where a mistake could cause financial, legal or operational harm.

# 8. How This Fits the Actual WorkforceOps MVP

The methodology is not intended to delay the whole project until every statutory rule has been researched.

It allows us to separate independent delivery tracks.

## Operational implementation track

The first implementation milestone follows this workflow:

`HR → BPO configuration → Rosters → Attendance → Timesheets → Payroll readiness`

This can produce real application functionality using the existing PostgreSQL foundation.

It includes:
- Employee and employment-term administration.
- Multiple BPO clients and programmes.
- Teams and employee skills.
- Shift templates and rotating rosters.
- Cross-midnight attendance.
- Clock-event imports.
- Attendance exceptions and corrections.
- Timesheet generation and approval.
- A payroll-readiness assessment.

## Statutory research track

In parallel, we continue qualifying the required business-rule packages.

The overtime package remains blocked until its source and mathematical uncertainties are addressed.

CPF, SDL and SHG require their own distinct verified packages.

A rule that has not passed its gate must not produce a supposedly compliant payroll figure.

This is a deliberate separation of concerns: we can build the operational system while the evidence needed for statutory calculations is being established.

The immediate implementation milestone therefore produces a working HR/WFM/timesheet backend and an honest payroll-readiness result. It does not pretend that the statutory payroll engine is complete.

# 9. Evidence of Technical Value Already Demonstrated

The methodology is not purely theoretical. Several results have already been obtained.

## Database and architecture verification

The plan4.3 design-gate execution reports:

- Actual PostgreSQL 16.15.
- The real seven-version baseline migration.
- Tests running under the unprivileged `app` role.
- Multi-employer integrity tests.
- Draft-versus-published immutability tests.
- Payroll exclusion and approval-blocking tests.
- Lineage constraints.
- Twenty integration tests passing and one explicitly deferred reproducibility test.
- Seven Go packages passing in repeated test runs.

The test process also caught real implementation bugs, including a trigger incorrectly returning `OLD` after an update and a database test silently using the wrong database identity.

These results are evidence that the project's engineering discipline is extending beyond research documents into executable database verification.

## Phys-Pseudo research verification

The local Phys-Pseudo assessment established the actual seven-construct language and its semantics. The project adopted the authoritative specification without creating a new grammar, parser or compiler.

The first overtime exemplar exposed:
- An obsolete threshold in prior research.
- An unresolved difference between a formula-derived rate and MOM's published hourly figure.
- Incorrect intermediate arithmetic.
- Missing premises.
- Overstated claims about the 72-hour limit.
- A distinction between source evidence and independent mathematical verification.

The package remained blocked rather than being promoted for the sake of completing a demonstration.

These are meaningful improvements because the defects were discovered before statutory calculation code existed.

# 10. What the Methodology Does Not Guarantee

It is important not to overstate the approach.

Phys-Research does not make a source authoritative merely because it has been captured and hashed.

Phys-Pseudo does not prove that a legal interpretation is correct merely because it is formally expressed.

An independent reviewer can also make mistakes.

A golden fixture can be wrong if its source or derivation is wrong.

A correct calculation can still be applied to the wrong employee population.

A system with rigorous rule packages can still contain infrastructure, security or data-integrity defects.

The methodologies therefore complement rather than replace:
- Authoritative sources.
- Independent legal and mathematical scrutiny.
- Conventional software engineering.
- PostgreSQL integration tests.
- Adversarial reviews.
- Operational acceptance testing.

The key benefit is that uncertainty and evidence limitations become visible and actionable instead of being hidden inside code.

# 11. Engineering Principles We Are Establishing

The combined methodology leads to several durable project principles.

**1. Evidence precedes statutory implementation.**

No high-consequence rule is implemented solely from an AI-generated interpretation, a secondary summary or an undocumented assumption.

**2. Formalization precedes coding.**

The rule's applicability, inputs, assumptions, claims, derivations and unresolved questions must be explicit.

**3. Representation does not equal endorsement.**

A formal specification can contain competing claims and unresolved questions without silently endorsing one.

**4. Independence matters.**

The expected result must be established independently of the implementation under test.

**5. Uncertainty must have an executable boundary.**

An unresolved rule must produce an explicit blocked or indeterminate path, not an invented default.

**6. Historical rules must remain reproducible.**

Effective dates and rule versions are essential inputs, not administrative metadata.

**7. Formal rigor must be proportional to risk.**

L1 receives full formalization; L2 selective formalization; L3 ordinary engineering.

**8. Research must not become an excuse for delaying unrelated implementation.**

The operational backend should continue progressing while statutory research qualifies each calculation independently.

**9. The specification and implementation have separate responsibilities.**

Phys-Pseudo expresses the intended rule. Go implements the approved rule. Fixtures independently verify the implementation.

**10. The MVP remains small.**

No interpreter, compiler, generic rules engine, microservices architecture or unnecessary research infrastructure is required to obtain these benefits.

# 12. Conclusion: From Application Development to Evidence-Driven Business-System Engineering

The greatest change introduced by Phys-Research and Phys-Pseudo is not a new programming language or a new software framework.

It is a change in how we establish correctness.

We no longer assume that understanding a requirement is equivalent to knowing its correct implementation.

We separate source evidence from extracted claims, claims from interpretations, interpretations from assumptions, and derivations from executable code.

That separation allows us to discover incorrect assumptions before they become database structures, Go functions or payroll results.

It also improves the usefulness of testing. Tests no longer merely demonstrate that code behaves consistently with itself; they can demonstrate that the implementation agrees with independently established business-rule outcomes.

For WorkforceOps, this is particularly important because the system combines several distinct domains:

- Employment law.
- HR records.
- Workforce scheduling.
- Time and attendance.
- Payroll.
- Statutory contributions.
- Financial reconciliation.

These domains interact, but their concepts and rules cannot be casually conflated.

The emerging engineering process gives each domain explicit semantics and preserves the evidence needed to connect its requirements to the software.

We should preserve the methodology while resisting the temptation to overbuild it.

The immediate objective is still a functioning Go backend: employees, programmes, rosters, attendance, timesheets and eventually a verified monthly payroll run.

Phys-Research and Phys-Pseudo are valuable because they make that small system more trustworthy, not because they give us another large framework to maintain.

**The ultimate principle is simple: make business rules explicit, make their evidence traceable, make their assumptions inspectable, and make their implementation independently testable.**

That is how WorkforceOps can become a credible reference implementation of Singapore HR, WFM and payroll workflows rather than another application that merely encodes someone's interpretation of the law.