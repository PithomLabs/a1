## prompt

this project is Singapore-specific, what does it take to make it agnostic to country-specific laws and business rules, does it make sense to make one monolithic app initially configured that is SG-specific or customize it altogether for country-specific dropping SG-specific and tailored to per country?


## answer

My recommendation is to make WorkforceOps Singapore-first, but jurisdiction-extensible from the beginning. Do not remove Singapore-specific functionality, and do not build a universal HR/payroll engine before the Singapore MVP works.

The right architecture is a modular monolith with a country-neutral operational core and independently developed jurisdiction-specific rule packages.

This gives us a path to support other countries without maintaining a separate application for every country or pretending that employment laws can be reduced to one universal set of formulas.

# 1. First, distinguish what is universal from what is country-specific

An integrated HR/WFM/payroll system contains several fundamentally different categories of business logic.

I would divide them into five layers.

Layer 1 — Platform foundations

Go · PostgreSQL · API · CLI · audit · transactions · exact money · effective dating · evidence lineage

Layer 2 — Common business operations

Employers · employees · employment relationships · teams · clients · programmes · shifts · attendance · timesheets · payroll periods

Layer 3 — Jurisdiction-specific rules

Working hours · overtime eligibility · leave entitlements · public holidays · minimum wages · social contributions · income tax · statutory deductions · payslip requirements

Layer 4 — Employer policies

Contractual salary · additional overtime rates · workweek · allowances · company leave policies · approval policies

Layer 5 — Customer and service operations

BPO client contracts · service windows · queue staffing · service-level targets · programme skills · operational schedules

These layers must interact, but they must not be confused.

For example:

- Singapore statutory overtime rules belong to Layer 3.
- Meridian CX's contractual overtime rate belongs to Layer 4.
- A client's requirement for 20 agents on a support queue belongs to Layer 5.
- The employee's scheduled shift belongs to Layer 2.

A client's service-level target must never become an employment-law rule. Similarly, an employer's generous overtime policy must not be mistaken for a statutory minimum.

This separation is the foundation of country extensibility.

# 2. Why I would not drop Singapore-specific functionality

There are two possible approaches, and I would reject one of them.

## Approach A — Build a universal HR/payroll application immediately

This would mean trying to create generic abstractions for every country's employment rules before we have completed a functioning Singapore payroll system.

It sounds attractive, but it creates several problems:

- We do not yet know which abstractions genuinely generalize.
- Some legal concepts have no direct equivalent between countries.
- Different jurisdictions classify wages, contributions and employment arrangements differently.
- We risk designing a generic abstraction that does not accurately represent Singapore law.
- Development gets diverted from finishing the actual product.

We would effectively be building a framework for systems we have not yet validated.

## Approach B — Build a Singapore application, then clone it for every country

This is also undesirable.

It creates:

- Separate codebases.
- Duplicated HR and WFM functionality.
- Divergent bug fixes.
- Different API contracts.
- Repeated database migrations.
- Increasingly expensive maintenance.

A correction to attendance handling might have to be implemented independently in five applications.

That is unnecessary.

## Approach C — Singapore-first modular monolith

Recommended

Keep one application, one database and one API.

The operational foundation is shared. Singapore's laws and business rules are implemented as a country-specific package.

Other countries eventually provide their own packages.

```
                 WORKFORCEOPS
                       │
            ┌──────────┴──────────┐
            │                     │
       Shared Core           Application APIs
            │                     │
            └──────────┬──────────┘
                       │
              Business Operations
                       │
             Jurisdiction Context
                       │
          ┌────────────┼────────────┐
          │            │            │
         SG            PH           AU
      Singapore    Philippines   Australia
       Rules         Rules        Rules
          │            │            │
          └────────────┼────────────┘
                       │
               Shared Payroll
                 Orchestration
                       │
               Payroll Outputs
```

The diagram shows a future architecture, not a requirement to implement three country packages now.

We begin with Singapore. The other packages do not need to exist until there is a concrete reason to add them.

# 3. What should actually be country-neutral?

This is where I would be deliberate.

The following capabilities should be shared because they represent common operational concepts rather than one country's legislation.

| Domain       | Shared capability                                        |
| ------------ | -------------------------------------------------------- |
| Employer     | Legal entity and employer identity                       |
| HR           | Employee, employment relationship, effective-dated terms |
| Organization | Departments, teams, supervisors                          |
| BPO          | Clients, programmes, campaigns, queues and skills        |
| WFM          | Shifts, rosters, rotations and assignments               |
| Attendance   | Raw clock events, breaks, intervals and corrections      |
| Timesheets   | Time attribution, review, submission and approval        |
| Payroll      | Periods, runs, line items and immutable outputs          |
| Audit        | Actor, reason, source and history                        |
| Integration  | Import, export, idempotency and reconciliation           |
| Evidence     | Source references, snapshots and calculation lineage     |

These concepts can be represented consistently across countries, provided we avoid embedding local legal assumptions into their generic structures.

For example, the shared attendance domain should understand:

- Employee worked from 22:00 to 06:00.
- There was an unpaid break from 01:00 to 01:30.
- The shift crossed local midnight.
- An attendance correction superseded the original interval.

It should not independently decide whether that work arrangement complies with Singapore's daily hours, Australia's applicable award provisions or another country's overtime rules.

That responsibility belongs to the applicable jurisdiction package.

# 4. What must remain country-specific?

Payroll is particularly important here.

There is no reason to expect the same calculation model to work across all jurisdictions.

Consider the differences between these categories:

| Business rule        | Singapore example                                      | Other jurisdictions                                        |
| -------------------- | ------------------------------------------------------ | ---------------------------------------------------------- |
| Social contributions | CPF                                                    | Different social insurance systems                         |
| Employer levy        | SDL                                                    | Different employer levies                                  |
| Employee deductions  | Applicable SHG contributions                           | Different statutory and voluntary deductions               |
| Income taxation      | Singapore income-tax and withholding requirements      | Different national and possibly regional tax systems       |
| Overtime             | Employment Act eligibility and calculation provisions  | Different coverage, thresholds and rates                   |
| Leave                | Singapore statutory leave provisions                   | Different entitlement and accrual models                   |
| Public holidays      | Singapore holiday calendar and remuneration provisions | Different calendars and work rules                         |
| Employment records   | Singapore-specific record and KET requirements         | Different statutory disclosures and retention requirements |

Do not build generic formulas such as:

```
overtime = hourly_rate * 1.5
```

That may describe one narrowly scoped calculation, but it is not a universal overtime engine.

Instead, the shared payroll orchestrator should ask the applicable jurisdiction package to evaluate the relevant earning or deduction.

Conceptually:

```
type RuleContext struct {
    EmployerID       EmployerID
    EmploymentID     EmploymentID
    JurisdictionID   JurisdictionID
    EffectiveDate    time.Time
    PayPeriod        PayPeriod
    Currency         CurrencyCode
}

type RuleAssessment struct {
    Status         AssessmentStatus
    Components     []PayrollComponent
    Blockers       []RuleBlocker
    RulePack       RulePackVersion
    CalculationID  string
}
```

This is an illustrative interface, not a mandate to implement a generic rules engine now.

The important idea is that the core supplies the business context, while the jurisdiction package decides which legal rules apply and what they produce.

The package must be able to return:

- Applicable.
- Not applicable.
- Blocked.
- Indeterminate.
- Unsupported.

It must not invent a result when the required evidence or rule interpretation is missing.

# 5. An important distinction: country is not always the same as legal jurisdiction

I would avoid designing the application around a single global `country` field.

The legal jurisdiction governing an employee's employment may depend on more than the employer's registered country.

Relevant context can include:

- Legal employing entity.
- Employee's work location.
- Employment arrangement.
- Period of work.
- Tax residence.
- Social security coverage.
- Applicable subnational jurisdiction.

For example, a company registered in one country might employ someone working in another country. Another country may have state or provincial differences.

Currency and timezone also need to be independent concepts:

- Country code: ISO 3166 where applicable.
- Currency: ISO 4217.
- Timezone: IANA timezone identifier.
- Legal jurisdiction: a stable internal jurisdiction identity, optionally associated with external codes.

A country does not necessarily have one timezone, one currency or one set of employment rules.

For our current MVP, do not implement complex cross-border employment. But avoid assumptions such as:

```
employer.country = employee.payroll_country = work_timezone
```

Those concepts should not be permanently coupled.

The minimal Singapore profile can simply identify Singapore as the employer's and employment's applicable jurisdiction.

# 6. How Phys-Research and Phys-Pseudo become much more powerful

This is where our existing methodology gives us a major advantage.

We do not need a different research methodology for every country.

We need a shared methodology with country-specific evidence and specifications.

Consider the future structure:

```
rules/
├── common/
│   └── ...
│
├── SG/
│   ├── employment/
│   │   ├── overtime/
│   │   ├── working-hours/
│   │   └── leave/
│   ├── payroll/
│   │   ├── cpf/
│   │   ├── sdl/
│   │   └── shg/
│   └── records/
│
├── PH/
│   ├── employment/
│   ├── payroll/
│   └── records/
│
└── AU/
    ├── employment/
    ├── payroll/
    └── records/
```

Only the Singapore packages need to be populated now.

Each jurisdiction package uses the same research discipline:

1. Acquire authoritative local sources.
2. Preserve provenance.
3. Extract claims.
4. Establish applicability.
5. Formalize with Phys-Pseudo.
6. Derive expected outcomes independently.
7. Generate fixtures.
8. Perform adversarial review.
9. Implement only after verification.

This means the research methodology is reusable even when the legal content is not.

For instance, Singapore CPF and another country's social-security contribution system may serve similar business purposes, but their contribution bases, employee classifications, ceilings and rates are different.

We should not force them into one CPF-like abstraction.

Instead, both can produce standardized payroll outputs while retaining jurisdiction-specific calculation logic and evidence.

That is the useful abstraction: standardize the interface and provenance, not the laws or formulas.

# 7. The database should support extensibility without becoming generic for its own sake

I would retain the existing employer-scoped database design.

There are a few principles to follow when we eventually extend it.

### A. Jurisdiction identity

A stable jurisdiction catalogue should identify legal jurisdictions.

An employer should reference its legal jurisdiction.

An employment relationship or employment-term version should identify its applicable work/legal jurisdiction where needed.

Do not require every business entity to duplicate jurisdiction information unnecessarily.

### B. Effective-dated rule activation

The employer should have a record of which jurisdiction rule-pack versions apply to its payroll operations.

For example:

```
Employer: Meridian CX Services Pte. Ltd.
Jurisdiction: SG
Rule pack: SG-CPF
Version: 2026.01
Effective from: 2026-01-01
Status: IMPLEMENTABLE
```

This is illustrative.

The important point is that rule-pack activation is effective-dated, auditable and employer-specific.

### C. Shared payroll output model

The common payroll model should understand generic financial items:

- Earnings.
- Employee deductions.
- Employer contributions.
- Employer levies.
- Taxes.
- Reimbursements.
- Adjustments.

But it should not force every jurisdiction to use the same internal classification system.

A Singapore wage classification such as Ordinary Wages and Additional Wages must remain part of the Singapore-specific calculation model.

The common payroll layer can store the resulting item, its financial value, its classification references and its provenance without assuming that every country has equivalent concepts.

### D. Country-specific extensions

Some countries will have fields, declarations or statutory record requirements that others do not.

Do not introduce hundreds of nullable columns into generic employee or payroll tables.

Use a deliberate extension strategy:

- Shared fields for genuinely common concepts.
- Typed jurisdiction-specific structures where needed.
- Effective-dated country-specific records.
- Explicit schemas for statutory documents and outputs.

Avoid hiding all business logic in arbitrary JSON blobs. Extensibility must not destroy database integrity.

# 8. What this means for the current Go architecture

I would not introduce dynamic plugins or separate country services.

Retain the current single executable and static package registration.

The conceptual Go structure could eventually look like:

```
internal/
├── employer/
├── hr/
├── bpo/
├── wfm/
├── attendance/
├── timesheet/
├── payroll/
├── audit/
└── jurisdiction/
    ├── registry/
    └── contract/

modules/
├── sg/
│   ├── employment/
│   ├── payroll/
│   └── records/
├── ph/
└── au/
```

This is a future organizational direction, not a request to reorganize the repository during Milestone 1.1.

The shared core should never import Singapore-specific business logic directly.

The application composition root registers the desired packages.

For now, only Singapore is registered.

When another country is added, the application still builds one executable with the additional package.

This avoids:

- Go dynamic plugin complexity.
- Runtime plugin loading.
- Cross-service communication.
- Separate databases.
- Country-specific forks.

It also fits our preference for a seamless Go backend development experience.

# 9. How I would transition the existing project

I would not interrupt Milestone 1.1 to perform a large localization redesign.

There is a better sequence.

1. Milestone 1.1 — Fix operational integrity

   Complete attendance, timesheet, temporal, employment-history and database-role remediation. Preserve the current Singapore implementation.
2. Milestone 2 — Complete the first Singapore payroll calculation

   Verify and implement a restricted monthly payroll scenario, beginning with CPF and the associated required calculations. Preserve Phys-Research and Phys-Pseudo gates.
3. Milestone 3 — Establish the jurisdiction boundary

   Extract Singapore-specific legal calculations and statutory record requirements into a clearly identifiable package. Keep generic payroll orchestration in the common core.
4. Milestone 4 — Add a second jurisdiction as an architectural test

   Only when there is a real market reason. Use the experience to identify what genuinely generalizes and what must remain local.

The second jurisdiction is important because an abstraction that works only for Singapore has not yet demonstrated that it is country-extensible.

But do not invent a second implementation merely to satisfy an architectural ambition.

Choose the next country based on actual customer or business demand.

# 10. What I would explicitly avoid

These mistakes could make the project considerably more complicated than necessary.

Do not build a universal legal rules engine. The common core should orchestrate verified country packages, not interpret every country's laws through a generic formula language.

Do not make the database country-neutral by deleting useful Singapore fields. CPF-specific classifications and statutory requirements are legitimate domain concepts. They belong in the Singapore package, not in a fictional universal model.

Do not implement country selection as a simple configuration toggle. Selecting a country must activate the appropriate rule and data model dependencies, not just change currency or holiday calendars.

Do not maintain separate country forks. They will accumulate duplicated bugs and divergent functionality.

Do not implement all countries at once. Each country introduces legal research, source provenance, fixtures, payroll behavior and jurisdiction-specific testing.

Do not confuse configurability with law. An employer should be able to configure legitimate policies, but it must not be allowed to configure away a statutory requirement.

# 11. My recommended architectural decision

I would formally adopt this product direction:

Recommended architecture

# Singapore-first, jurisdiction-extensible modular monolith

Shared operational core

HR, BPO, WFM, attendance, timesheets, payroll orchestration, database, CLI, REST and audit.

Independent jurisdiction packages

Employment law, payroll calculations, statutory contributions, deductions, records and local reporting.

One application and one deployment model

Country packages are statically registered Go modules. No microservices or dynamic plugin system.

I would also establish four engineering rules:

1. Country-specific statutory rules must never leak into the generic core.
2. The core must never assume that one country's salary, leave or contribution model is universal.
3. All jurisdiction-specific calculations must use the Phys-Research and Phys-Pseudo approval methodology.
4. A new country must be introduced through a separate jurisdiction package, not by modifying existing countries' calculations to accommodate it.

## Final thought

There is a strategic advantage in what we have already done.

We started with Singapore because it provides a concrete legal and business environment against which we can test the architecture. That was the right decision.

The mistake would be to interpret that starting point as a requirement to build a permanently Singapore-only application—or, conversely, to abandon Singapore-specific detail in pursuit of premature universality.

Build a genuinely functional Singapore product first. Keep the operational core neutral, preserve the Singapore rule package, and establish a strict boundary between common business operations and jurisdiction-specific law.

That gives Pithom Labs one product, one codebase and one engineering methodology that can eventually support multiple legal jurisdictions without diluting the correctness of any of them.

I would describe the product strategy as: Singapore-first, globally extensible—not globally generic.