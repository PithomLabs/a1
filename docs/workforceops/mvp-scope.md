# MVP Scope — WorkforceOps Lab v0.1

**Status:** design gate candidate (rev. 2 — plan_prompt4 amendments) · **Date:** 2026-11-08
**Product:** WorkforceOps Lab — Integrated HR, WFM and Payroll Backend.

## 1. Definition

WorkforceOps Lab v0.1 is an open-source Go-based enterprise HR,
workforce-management and payroll application implementing realistic Singapore
business rules and operational workflows, backed by PostgreSQL and populated
with internally consistent synthetic business data. Its purpose is a
functioning, extensible business-system environment for a fictional Singapore
BPO employer that individuals and organizations can operate, inspect, test,
customize and extend.

It is a **MOM-informed reference implementation with a restricted
employee-record profile** — not a claim of complete statutory
employment-record compliance. Identity fields requiring real-PII protection
(NRIC handling in particular) are deliberately unsupported; the design says so
explicitly rather than presenting an incomplete record as legally complete.

Not a training platform yet. Not a WFM SaaS clone. Not microservices. First,
one functioning business system.

## 2. The end-to-end workflow

HR (employee records, employment terms, leave) → WFM (client programme,
shift roster, attendance) → Timesheets (actual hours, overtime, corrections,
approval) → Payroll (gross pay, statutory deductions, payslips) → Outputs
(payroll register, employee payslips, statutory summaries).

Everything is connected. No disconnected demonstrations.

## 3. MVP features

**HR:** employer and employee records (synthetic address and gender modeled;
NRIC deliberately unsupported); contracts and effective-dated terms; status
(active/suspended/resigned/terminated); title, category, basic salary, fixed
allowances/deductions; start/end dates and working arrangements; annual and
sick leave records, balances, approvals; employment-record and KET information
per the versioned KET schema. Excluded: recruitment, ATS, performance,
benefits, engagement, LMS.

**WFM:** `Client → Programme → Team → Employee`; skill assignment with
programme skill requirements enforced at roster time; multiple
clients/programmes; teams and assignments; day/evening/night templates;
weekly and rotating rosters via effective-dated rotation assignments;
cross-midnight shifts; paid/unpaid breaks; designated rest days;
leave/availability eligibility; published rosters with immutable revisions;
clock in/out; corrections with reasons and audit; supervisor-approved
timesheets; minimum headcount per programme/shift. The model distinguishes
scheduled, recorded, actual and paid time — never one `hours_worked` field.
Excluded: forecasting, Erlang C, auto-optimization, intraday monitoring,
telephony, adherence dashboards, multi-skill optimization, shift bidding.

**Payroll:** monthly basic, fixed allowances, approved overtime, supported
leave treatment, employee statutory deductions (CPF employee share, SHG per
employee instruction), employer contributions and levies (CPF employer share,
SDL as employer cost — never reducing net salary), gross/deductions/net,
approval and locking, itemised payslips per the versioned payslip schema,
register and statutory exports. Wage components carry OW/AW/NON_CPF_WAGE
classification with work period, payable date and contribution period. Inputs
and results preserved per run; post-approval changes require auditable
corrective runs. CPF, SDL and applicable SHG deductions are in scope with
effective-dated rates; submission, bank transfers and IRAS filing wait. MOM
employment rules and CPF Board contribution rules stay distinct.

## 4. Support boundary

One Singapore legal entity actively operated; direct employees; Singapore
Citizen reference population; monthly-rated full-time; basic salary plus fixed
allowances; day/evening/night shifts; cross-midnight and rotating rosters;
ordinary arrangements with explicit Part IV eligibility; verified ordinary
overtime; annual and sick leave; rest days recorded and protected (no
rest-day work); public-holiday calendar with standard paid treatment (no
work-on-holiday arrangements); monthly payroll; Singapore only.

The database schema and application-service boundaries are multi-employer
capable from the beginning (`employer_id` tenant boundary, composite keys,
employer context in every service call), while v0.1 operates exactly one
employer. `employer` means the legal employing entity — never an arbitrary
customer account; Meridian's client programmes remain children of Meridian's
record. Production multi-tenancy (identity, authorization, RLS-equivalent
isolation) is a separate release gate, not part of v0.1.

Unsupported — rejected or flagged with machine-readable codes, never silently
miscalculated: foreign workers, PR-specific CPF, IR21, rest-day work,
public-holiday work, part-time and non-monthly arrangements, special
employment situations. Ordinary salary payable within 7 days and overtime
within 14 days are represented in the payroll workflow.

## 5. Operational scheduling versus verified legal compliance

A roster the system can schedule is not automatically a roster the system has
validated against Singapore law. Rosters built on shift arrangements whose
rule pack (SG-MOM-SHIFT) has not reached IMPLEMENTABLE are labeled
`operationally scheduled, legal validation unavailable` — visibly, in API
responses, CLI output and exports. The same rule applies to any calculation
resting on an unverified pack: the system states the boundary instead of
implying compliance.

## 6. Explicitly out of v0.1

Any web UI; microservices; separate ShiftHub/PayLoop systems; event bus and
distributed sync; AI/ML; RAG/embeddings/vectors. Deferred: generic simulation,
scenario marketplace, advanced WFM optimization, multi-tenant SaaS
operations, SSO and enterprise identity, external integrations, banking/CPF-
submission automation, applicant/OJT training platform. Retained:
deterministic seeded cases and fixtures, independent expected-result
verification, the rule research pipeline as engineering methodology.

## 7. Completion criterion

A CLI/API user can hire an employee, schedule them, record actual work,
approve hours and produce a traceable monthly payroll result — demonstrated by
the 13-step acceptance test in the final prompt, including reproducibility of
the approved run. Additionally: employment and salary records satisfy the
two-year retention requirement (current employees; leavers' records retained
one year post-exit), verified by acceptance tests.
