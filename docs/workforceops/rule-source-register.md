# Rule Source Register — Singapore statutory provenance

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Every implemented statutory rule traces to a pinned official source. **All
entries are `unverified` until the Phase 2 retrieval pass re-opens each URL,
records exact sections/tables/hashes and derives golden tests.** No statutory
code may be written from model memory or summary pages.

## 1. Record shape

`rule_id | authority | source_url | section_or_table | retrieved_at |
source_hash | effective_from | effective_until | population | exclusions |
rounding | review_status | golden_tests`

## 2. MVP rule pack: SG-MOM-OT (overtime, first and only v0.1 pack)

| rule_id | authority | source_url | status |
|---|---|---|---|
| SG-MOM-OT-APPLICABILITY | MOM | https://www.mom.gov.sg/employment-practices/hours-of-work-overtime-and-rest-days | unverified |
| SG-MOM-OT-RATE | MOM | (same page; hourly basic-rate formula + 1.5× + S$2,600 cap) | unverified |
| SG-MOM-OT-LIMIT | MOM | (same page; 72-hour monthly limit + payment within 14 days) | unverified |
| SG-MOM-PAYSLIP | MOM | https://www.mom.gov.sg/employment-practices/salary/itemised-payslips | unverified |

Coverage notes (to verify on retrieval): Part IV excludes managers and
executives; non-workman threshold S$2,600, workman S$4,500; overtime excludes
breaks; monthly-rated hourly rate `(12 × monthly basic) / (52 × 44)`; non-workman
rate capped at the S$2,600 level. Anything outside the verified subset returns
`indeterminate` / `needs_review`.

Stage 1 (2026-11-08): provisional MOM captures corroborate the S$2,600 /
S$4,500 current guidance and surface the S$13.60 cap figure with an
unstated rounding step — see
`rules/SG-MOM-OT-001/sources/provisional/CAPTURES.md`. Provisional grade
only; byte-grade pinning still required before any entry leaves
`unverified`.

## 3. Gated second pack: SG-CPF (deferred)

CPF needs complete official rate tables (citizenship/SPR-year/age/wage-band),
OW/AW ceilings (OW S$8,000 from 2026-01-01 — to verify), rounding rules and
the 2027 senior-rate schedule. Sources to retrieve when gated in:

- https://www.cpf.gov.sg/employer/employer-obligations/how-much-cpf-contributions-to-pay
- https://www.cpf.gov.sg/service/article/what-is-the-ordinary-wage-ow-ceiling-mbr

No headline-rate shortcuts; no CPF answer keys until the validation gate in the
MVP plan §5.4 passes with independent review.

## 4. Deferred (reference only, do not implement)

Annual/sick/childcare leave, public-holiday/rest-day pay, IRAS AIS/IR8A/IR21,
SDL edge cases, work-pass administration, employment-records retention. URLs for
these live in the MVP plan §16 and are not repeated here to avoid implying scope.
