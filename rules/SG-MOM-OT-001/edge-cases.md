# Edge cases — SG-MOM-OT-001

| # | trigger | expected outcome | support | in spec? | fixture? | state |
|---|---------|------------------|---------|----------|----------|-------|
| E1 | B exactly at threshold (2500 or 2600?) | covered (<= form) | S1 `<=` wording | partial | boundary | unresolved interpretation |
| E2 | B = 2250 exactly | uncapped case | S1 `B <= 2250` | yes (`cap_rule_pdf`) | boundary | unresolved interpretation |
| E3 | B between 2250 and threshold | capped at 2250-level | S1 | yes (`cap_rule_pdf`) | boundary | unresolved interpretation |
| E4 | B above threshold | inapplicable rule, no statutory OT | S1 | yes (`else`) | anti-golden | unresolved interpretation |
| E5 | Manager earning below threshold | inapplicable (category exclusion) | S1 | yes (`excluded`) | anti-golden | unresolved interpretation |
| E6 | H = 0 | valid zero (no OT worked) | arithmetic | implied | golden | unresolved interpretation |
| E7 | H > 72 | H preserved in full; compliance state flagged, never truncated | S1 s38(2) + P1 | yes (`actual_preserved`, `limit_72_check`) | boundary | unresolved interpretation (breach consequences open; see E13) |
| E8 | Emergency work beyond 72 | excluded from cap; rate? | S1 (exclusion only) | no | — | missing information |
| E9 | Rounding of R_hourly/ot_hourly | unknown | none pinned | no (`?`) | — | missing information |
| E10 | Effective-date transition (threshold change?) | unknown | none pinned | no (`?`) | — | missing information |
| E11 | Rest-day hours through this formula | unsupported scenario | out of scope | `?` pointer | — | unsupported scenario |
| E12 | Daily-rated/hourly-rated employee | unsupported scenario | out of scope | applicability | — | unsupported scenario |
| E13 | 72h exemption granted | cap lifted administratively | P1 | `given cap_72_exemption` + `?` | — | missing information (grant criteria, effect on runs) |
| E14 | Hourly rate 13.636 at cap | 13.60 per MOM (rounding?) | P1 | `derive mom_1360_gap` + `?` | boundary (when rule known) | unresolved interpretation |
| E15 | Contractual OT rate above 1.5x | outside package | scope (`mvp_rate`) | yes (`assume mvp_rate`) | — | unsupported scenario |

Every row stays non-established until S3 pins the source. E6 shows the
valid-zero shape; E4/E5 the inapplicable-rule shape; E11/E12 the
unsupported-scenario shape — distinct outcomes, never collapsed.
