# SPEC.pseudo.md — SG-MOM-OT-001

Ordinary overtime for the restricted monthly-rated employee profile.
Source-backed where pinned; unresolved where not. No reading is endorsed
by being written here.

```text
# source: SG_research.pdf pp.1-2 (secondary research artifact, not MOM) + rule-source-register.md (unverified notes) + provisional MOM captures P1-P5 (provisional_transcript, not source bytes)
given jurisdiction: employment in Singapore under Employment Act Part IV  # SG_research.pdf p.1
given cov_workman: workmen with monthly basic salary B <= S$4,500 are covered  # SG_research.pdf p.1
given cov_nonworkman_pdf: non-workmen with B <= S$2,500 are covered  # SG_research.pdf p.1
given cov_nonworkman_register: non-workmen are covered to S$2,600  # rule-source-register.md coverage notes, not MOM-pinned
    ? MOM-current pages P1-P4 state S$2,600 (provisional transcripts) while the research PDF states S$2,500; the amendment and effective date still need SSO legislation text
given excluded: managers, executives, domestic workers, seamen, and above-threshold non-workmen are not covered  # SG_research.pdf p.1
given basic_salary_def: MOM states basic salary excludes overtime, bonus, annual wage supplement, productivity incentives, reimbursements and allowances  # P2 Who is covered (provisional_transcript)
given classification_input: employee classification (workman, non-workman, manager, executive, other) is supplied by the scenario  # scenario input
given std_hours: standard hours are 8/day for a 6-day week or 9/day up to 44/week for a 5-day week  # SG_research.pdf p.1
given ot_def_pdf: the research PDF describes overtime as work beyond standard contractual hours at the employer's request  # SG_research.pdf p.1
    ? MOM P1/P4 define overtime as excess over normal hours excluding breaks, with no request condition; whether employer-request is a legal condition needs legislation text
given ot_mult: overtime is paid at no less than 1.5 x hourly basic rate  # SG_research.pdf p.1, cites s38(4); P1 states at least 1.5 (provisional_transcript)
given hourly_formula: R_hourly = (12 x B_statutory) / (52 x 44)  # SG_research.pdf p.2, cites Fourth Schedule
given cap_rule_pdf: B_statutory = B when B <= 2250; 2250 when 2250 < B <= 2500  # SG_research.pdf p.2, cites Fourth Schedule Item 3
given limit_72_figure: MOM states an employee can work up to 72 overtime hours in a month  # P1 Maximum hours of overtime (provisional_transcript)
given limit_72_calendar_pdf: the research PDF describes the 72-hour limit per calendar month  # SG_research.pdf p.2
    ? calendar-month interpretation is absent from P1-P4; needs legislation text
given limit_72_emergency_pdf: the research PDF excludes emergency work from the 72-hour limit  # SG_research.pdf p.2
    ? P1 exception text covers the 12-hour daily limit, not the 72-hour cap; the emergency exclusion needs legislation text
given pay_deadline: overtime is payable within 14 days  # rule-source-register.md coverage notes, not MOM-pinned
given mom_cap_sentence: MOM states the non-workman overtime rate is capped at salary level S$2,600, or an hourly rate of S$13.60  # P1 Overtime pay (provisional_transcript)
given mom_example: MOM computes S$2,600 with 2 overtime hours as 13.60 x 1.5 x 2 = S$40.80  # P1 Overtime pay (provisional_transcript)
given mom_formula_monthly: MOM states monthly hourly basic rate = (12 x monthly basic rate of pay) / (52 x 44)  # P1 How overtime pay is calculated (provisional_transcript)
given break_excluded: overtime is work in excess of normal hours, excluding breaks  # P1 Overtime pay + P4 (provisional_transcript)
given cap_72_exemption: employers may apply for exemption to exceed 72 overtime hours in a month  # P1 Maximum hours of overtime (provisional_transcript)
    ? grant criteria, scope, and effect on recorded hours are unstated; needs legislation or exemption instrument text
given restday_72_interaction: rest-day and public-holiday work is excluded from the 72-hour limit except hours beyond usual daily hours on those days  # P1 Maximum hours of overtime (provisional_transcript)
define statutory_basic := the monthly basic figure entering hourly_formula after any capping
    ? which cap value instantiates statutory_basic is unresolved: S$2,600 MOM-current provisional versus S$2,250 research-PDF historical; needs Fourth Schedule text
define ot_hourly_min := the statutory minimum overtime payment per hour
define covered := the employee satisfies Part IV classification, salary threshold, and effective date for this rule
assume profile: monthly-rated non-workman; effective date to be established by a pinned retrieval
assume mvp_rate: this package illustrates the statutory minimum rate only; contractual rates above 1.5x are outside this package
given monthly_basic: monthly basic salary B is supplied by the scenario  # scenario input
given ot_hours: overtime hours H actually worked are supplied by the scenario  # scenario input
claim pot: ot_hourly_min = 1.5 x (12 x statutory_basic) / (52 x 44)
derive divisor: 52 x 44 = 2288 from [hourly_formula] | 52 x 44 = 2288
derive eligibility: covered holds exactly when classification, monthly basic B with basic-salary composition, the applicable threshold, and the effective date jointly satisfy Part IV from [classification_input, monthly_basic, basic_salary_def, cov_workman, cov_nonworkman_pdf, cov_nonworkman_register, excluded, profile] | each input must hold; no input may be defaulted
    ? threshold value, basic-salary composition evidence per employee, and effective date are unresolved; the derivation is conditional on all three
derive pdf_cap_arithmetic: under cap_rule_pdf, B = 2250 gives R_hourly = 27000/2288 = 11.80 and ot pay factor 17.70 from [hourly_formula, cap_rule_pdf, divisor] | (12 x 2250)/2288 = 11.8007, x1.5 = 17.7010; rounded display only, source rounding not pinned (intermediates corrected per independent review F1)
    ? source rounding rule is not pinned; 11.80/17.70 show the PDF's arithmetic, not a payable amount
derive mom_1360_gap: (12 x 2600)/2288 = 13.636 while MOM states 13.60, a gap of 0.036 unexplained on the page from [mom_formula_monthly, mom_cap_sentence, divisor] | 31200/2288 = 13.6364; nearest-S$0.10 rounding fits but no rounding rule is stated
    ? the rounding rule mapping 13.636 to 13.60 is unstated; Fourth Schedule text or calculator logic must supply it
derive rate_from_formula: R1 = (12 x 2600)/2288 = 13.6364 from [mom_formula_monthly, mom_cap_sentence, divisor] | monthly cap value through the stated formula; no rounding applied
derive rate_stated: R2 = 13.60 from [mom_cap_sentence] | the hourly figure as stated by MOM; derivation from R1 unstated
    ? what legal rounding rule produces R2, at what stage it applies, whether R2 is a statutory rounded rate or a display convention, and how the Fourth Schedule expresses the calculation
derive actual_preserved: recorded overtime hours H are preserved in full regardless of the 72-hour limit from [ot_hours, limit_72_figure] | a working-time limit constrains hours worked, never hours recorded; truncation would discard evidence
derive limit_72_check: the 72-hour figure is evaluated as a working-time compliance state over counted hours, never as a payable-hours formula from [limit_72_figure, ot_hours, cap_72_exemption] | counted-hours definition and breach consequences remain open here
    ? breach consequences, pay treatment when exceeded, exemption effect on recorded hours, and the counted-hours definition need legislation text
if covered:
    then
        derive pot_covered: minimum ot_hourly_min = 1.5 x the hourly basic rate from [ot_mult, hourly_formula, mvp_rate] | the 1.5 statutory minimum applies to the hourly basic rate; contractual rates above it are outside this package
    else
        claim no_statutory_ot: no statutory overtime calculation applies
? the cap figure is narrowed but unresolved: MOM states S$13.60 as the hourly rate at the S$2,600 cap (P1, provisional), yet (12x2600)/2288 = 13.636; the rounding from 13.636 to 13.60 is unstated, so S$20.45 (unrounded formula) versus S$20.40 (MOM figure) stays open; Fourth Schedule text must decide
? provisional MOM captures P1-P5 exist but no byte-grade source is pinned; every MOM value above is provisional_transcript and the SSO legislation text (Fourth Schedule, amendment history) is still uncaptured
? rest-day overtime interaction (PDF p.2: 1 or 2 days basic salary plus 1.5 x excess; P1 rest-day/PH 72-hour interaction) is out of this rule's scope and needs its own package before any rest-day hour is paid through this formula
```
