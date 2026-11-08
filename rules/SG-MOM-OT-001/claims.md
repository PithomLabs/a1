# Claims ledger — SG-MOM-OT-001

| # | claim | source scope | evidence_class | spec ref | state |
|---|-------|--------------|----------------|----------|-------|
| C1 | Workmen covered to S$4,500 | S1 p.1 | SPEC_INFERENCE | `given cov_workman` | unresolved |
| C2 | Non-workmen covered to S$2,500 | S1 p.1 | SPEC_INFERENCE | `given cov_nonworkman_pdf` | obsolete for current law (provisional); historical applicability open |
| C3 | Non-workmen covered to S$2,600 | S2 + P1–P4 | SPEC_INFERENCE | `given cov_nonworkman_register` | corroborated current value (provisional); effective date open |
| C4 | Manager/executive/domestic/seamen excluded | S1 p.1 | SPEC_INFERENCE | `given excluded` | unresolved |
| C5 | Standard hours 8/9/44 | S1 p.1 | SPEC_INFERENCE | `given std_hours` | unresolved |
| C6 | OT = beyond-standard (PDF adds employer-request) | S1 p.1 | SPEC_INFERENCE | `given ot_def_pdf` + `?` | unresolved; request condition absent from P1/P4, needs legislation |
| C7 | OT rate >= 1.5x hourly basic | S1 p.1 (s38(4)) | SPEC_INFERENCE | `given ot_mult` | unresolved |
| C8 | R_hourly = (12 x B_stat)/(52 x 44) | S1 p.2 (4th Sch) | SPEC_INFERENCE | `given hourly_formula` | unresolved |
| C9 | Statutory basic capped at 2250/2500 | S1 p.2 (4th Sch It.3) | SPEC_INFERENCE | `given cap_rule_pdf` | contested |
| C10 | 72 overtime hours/month cap (figure only) | S1 p.2 + P1 L167 | SPEC_INFERENCE | `given limit_72_figure` | corroborated (provisional) for the 72h figure |
| C22 | 72h cap per calendar month (PDF) | S1 p.2 only | SPEC_INFERENCE | `given limit_72_calendar_pdf` + `?` | unresolved (absent from P1–P4) |
| C23 | 72h cap excludes emergency work (PDF) | S1 p.2 only | SPEC_INFERENCE | `given limit_72_emergency_pdf` + `?` | unresolved (P1 exception text covers the 12h daily limit, not the 72h cap) |
| C24 | Basic salary excludes OT/bonus/AWS/incentives/reimbursements/allowances | P2 L102 | SPEC_INFERENCE | `given basic_salary_def` | corroborated (provisional) |
| C11 | OT payable within 14 days | S2 + P1 | SPEC_INFERENCE | `given pay_deadline` | corroborated (provisional) |
| C12 | min P_OT = 1.5 x R_hourly for covered staff | C7+C8 | SPEC_INFERENCE | `claim pot`, `derive pot_covered` | unresolved (minimum only; contractual rates outside package) |
| C13 | R1 = (12x2600)/2288 = 13.6364 candidate | S2 shape + P1 formula | SPEC_INFERENCE | `derive rate_from_formula` | contested (rounding open) |
| C14 | R2 = 13.60 stated candidate (-> S$20.40) | P1 cap sentence + example | SPEC_INFERENCE | `derive rate_stated` + `?` | contested (rounding open) |
| C15 | MOM: cap at $2,600 salary level or $13.60 hourly | P1 | SPEC_INFERENCE | `given mom_cap_sentence` | corroborated (provisional) |
| C16 | MOM: $2,600 + 2 OT hours = $40.80 | P1 | SPEC_INFERENCE | `given mom_example` | corroborated (provisional) |
| C17 | Monthly formula (12xB)/(52x44) per MOM | S1 p.2 + P1 | SPEC_INFERENCE | `given mom_formula_monthly` | corroborated (provisional) |
| C18 | Overtime excludes breaks | P1+P4 | SPEC_INFERENCE | `given break_excluded` | corroborated (provisional) |
| C19 | 72h exemption application path exists | P1 | SPEC_INFERENCE | `given cap_72_exemption` + `?` | corroborated (provisional); scope open |
| C20 | Rest-day/PH hours mostly outside 72h limit | P1 | SPEC_INFERENCE | `given restday_72_interaction` | corroborated (provisional); boundary note |
| C21 | 13.636 -> 13.60 rounding rule unstated | P1 by omission | UNRESOLVED | `derive mom_1360_gap` + `?` | unresolved |

Nothing in this ledger is byte-grade pinned. "Corroborated (provisional)"
means MOM pages state it in captured transcripts — not that the source is
pinned. Contested rows quote every known value; the SPEC endorses none.
