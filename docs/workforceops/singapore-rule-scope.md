# Singapore Rule Scope — v0.1 packs and boundaries

**Status:** design gate candidate (rev. 2 — plan_prompt4 amendments) · **Date:** 2026-11-08
**Rule:** no pack enters implementation before reaching IMPLEMENTABLE through
the research pipeline (acquire → extract → formalize → derive → goldens →
review). All packs start `unverified`.

## 1. MVP packs

| Pack | Authority | Scope | Spec | Load-bearing TBDs |
|---|---|---|---|---|
| SG-MOM-OT | MOM/EA Part IV | Ordinary overtime, monthly-rated, ordinary 5/6-day arrangements: eligibility, formula, cap, 72h limit, 14-day deadline | `rules/SG-MOM-OT-001/` BLOCKED (threshold + cap-mechanism conflicts, no MOM pinned) | S$2,600 cap and S$13.60-vs-formula discrepancy; rounding treatment; break exclusion |
| SG-MOM-SHIFT | MOM/EA | Narrow validation of supported shift hours/rest conditions for rosters we build (no rest-day work pay, no optimization) | (no package yet) | Which averaging/rest provisions apply to our arrangements; values and boundaries |
| SG-CPF-2026 | CPF Board | SC monthly contributions: age bands, OW/AW classification with performance-month apportionment, S$8,000 OW ceiling, annual AW ceiling, rounding, S$50 threshold | (no package yet) | Full rate tables; reconciliation procedure; 3-step rounding; 2027 rows only if verified |
| SG-SDL | CPF Board/SSG | 0.25% of monthly wages, S$2 min, S$11.25 max, aggregate rounded down; employer liability only | (no package yet) | Confirm current parameters |
| SG-SHG | CDAC/MBMF/SINDA/ECF | Instruction-based deductions by wage band | (no package yet) | Applicable groups; band tables; instruction format |
| SG-MOM-LEAVE | MOM/EA | Annual and sick leave entitlement, accrual, usage, balances; paid and unpaid treatment | (no package yet) | Service-year schedule; sick caps; unpaid deduction basis |
| SG-MOM-KET | MOM/EA | KET contents, itemised payslip contents, record requirements, retention periods | (no package yet) | Exact field lists and periods |
| SG-MOM-PH | MOM/EA | Holiday calendar, standard paid-holiday treatment | (no package yet) | Paid-treatment basis |
| SG-MOM-PAYDEAD | MOM/EA | 7-day ordinary / 14-day overtime payment deadlines in workflow | (no package yet) | Representation only |

## 2. Pack-to-component blocking matrix

Approval is all-or-nothing per regular run: a blocked employee cannot be
included in an approved payroll calculation. An operator may explicitly
exclude that employee before validation, with a recorded reason, into
`payroll_run_exclusion`; the employee is then not considered paid by that run,
stays visible in the register and audit history, and must be handled through
a separately controlled process. An approved run discloses its exclusions and
is never described as a complete payroll for all employees without
qualification. A pack that has not reached IMPLEMENTABLE blocks every
component depending on it:

| Pack | Blocks when unverified |
|---|---|
| SG-MOM-OT | `OT_STANDARD` lines, overtime pay, any scenario asserting overtime amounts |
| SG-MOM-SHIFT | Legal validation of rotating/shift rosters (scheduling allowed, labeled per mvp-scope §5) |
| SG-CPF-2026 | CPF employee/employer lines, OW/AW classification, net-pay completion |
| SG-SDL | Statutory summary completion (never blocks net pay — employer cost) |
| SG-SHG | Net-pay completion for employees with missing/ambiguous SHG instructions |
| SG-MOM-LEAVE | Leave pay lines, unpaid-leave deductions, balance correctness |
| SG-MOM-KET | KET issuance, payslip issuance, retention attestation |
| SG-MOM-PH | Paid-holiday lines |
| SG-MOM-PAYDEAD | Deadline display in workflow (representational; blocks nothing computational) |

A pack reaches IMPLEMENTABLE only through the research lifecycle in
`docs/workforceops/rules/adaptation-spec.md` §5. The matrix's
IMPLEMENTABLE gate and the package manifest's `status` field are the same
gate, and `rule_pack_activation.status` mirrors the manifest — it never
leads it.

## 3. Runtime rules

- Unverified rule data may be stored for research but can never activate for
  payroll: only an IMPLEMENTABLE pack version feeds a calculation.
- Each payroll run records the exact rule-pack versions consumed.
- A blocked calculation prevents approval of the payroll run: affected
  employees must be explicitly excluded first, and the run discloses its
  exclusions (machine-readable code, never silent).
- A rule update must never change the result of an already approved run;
  corrections flow through corrective runs.
- Unpaid-leave deductions and public-holiday calculations stay gated until
  their treatment verifies, even though leave requests and calendars work.

## 4. Deferred (explicitly unsupported in v0.1)

Rest-day work pay; public-holiday work arrangements; PR-specific CPF; IR21;
overtime for uncovered classifications; non-monthly-rated pay; part-time
arrangements; shift-worker averaging beyond the SG-MOM-SHIFT boundary;
maternity/paternity/childcare leave; allowances inside the OT base beyond the
verified basic-salary definition.

## 5. Jurisdiction scoping and activation

Statutory rule-pack definitions and versions are jurisdiction-global (not
duplicated per employer). Employer-specific activation — which pack version an
employer operates under — is an employer-owned record
(`rule_pack_activation`). A pack version activates for an employer only at
IMPLEMENTABLE or above. The package manifest's `applicability` block is the
machine-readable form of the scope prose in §1.

## 6. Method

Each L1 pack ships a Business Rule Research Package (sources, formalization,
verification, implementation contract, manifest) and promotes through
DISCOVERED → SOURCES_PINNED → CLAIMS_EXTRACTED → FORMALIZED →
INDEPENDENTLY_CHECKED → GOLDENS_VERIFIED → IMPLEMENTABLE. Rates and thresholds
are effective-dated rule data, never hardcoded constants. MOM employment rules
and CPF Board contribution rules are separate sources and must never be
conflated. Unresolved load-bearing assumptions block only the affected pack.
