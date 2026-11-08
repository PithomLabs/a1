# Independent derivation — SG-MOM-OT-001

**Not independent yet.** The arithmetic below was drafted by the same
author as the SPEC as a same-author check, not an independent derivation:
it replays the same sources through the same steps and therefore cannot
confirm them. A genuine independent derivation (different author, no
SPEC reuse) is still open and gates `INDEPENDENTLY_CHECKED`.

Same-author replay (PDF-internal arithmetic only):

- 52 x 44 = 2288.
- At B = 2250: (12 x 2250)/2288 = 27000/2288 = 11.8007 -> R_hourly 11.80;
  x 1.5 = 17.7010 -> ot_hourly 17.70. Display rounding only.
  (Intermediates corrected per independent review F1; the original
  11.7972/17.6958 were miscomputed.)
- At B = 2600 under the monthly-cap mechanism: (12 x 2600)/2288 =
  31200/2288 = 13.6364; x 1.5 = 20.4545 -> 20.45. Mechanism unpinned.
- Under the hourly-figure mechanism: 1.5 x 13.60 = 20.40. Figure unpinned.
- Mechanisms differ by S$0.05/hour before rounding; the choice is load-bearing.

No conclusion about which mechanism, threshold, or rounding the law
requires. That needs S3 (MOM retrieval).

## Provisional-capture arithmetic (same author, 2026-11-08)

Still not independent. Worked from P1 values through separately chosen
steps (fraction-first, then decimal expansion):

- 12 x 2600 = 31200; 52 x 44 = 2288.
- 31200 / 2288: 2288 x 13 = 29744; remainder 1456; 14560/2288 = 6
  (6 x 2288 = 13728), remainder 832; 8320/2288 = 3 (3 x 2288 = 6864),
  remainder 1456 — cycle. So 13.636363..., i.e. 13.64 to the cent,
  13.60 to the ten-cent.
- MOM states 13.60 and computes 13.60 x 1.5 x 2 = 40.80 (P1). Internally
  consistent: 13.60 x 3 = 40.80 exactly.
- Unrounded formula for 2 hours: 13.636363... x 3 = 40.909090...,
  i.e. S$40.91 to the cent — S$0.11 above MOM's worked S$40.80.
- Both nearest-S$0.10 and floor-to-S$0.10 map 13.6363... to 13.60; one
  data point cannot distinguish them, and MOM states no rule. The
  rounding stage (hourly rate vs final pay) is likewise unstated.

The load-bearing question is no longer "which of two mechanisms" but
"which rounding rule, applied at which stage" — still open (C21). The SPEC
represents this as two candidate derives (R1 from formula, R2 stated),
not as alternative if/else mechanisms.
