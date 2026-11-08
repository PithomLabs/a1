# Independent review — SG-MOM-OT-001

- reviewer: independent-reviewer-subagent
- date: 2026-11-08
- protocol: MRC (vendored `MRC_REVIEW_PROTOCOL.md`, adaptation-spec §7)
- spec version reviewed: 0.2.0

Target (protocol step 1): ordinary overtime eligibility and hourly
computation for the restricted monthly-rated non-workman profile under
Employment Act Part IV — threshold, 1.5x multiple, (12xB)/(52x44)
formula, cap mechanism, rounding, 72h monthly cap. Stated result: none
endorsed; package self-declares BLOCKED with load-bearing `?`s open.

## Findings

1. Where: `derive pdf_cap_arithmetic:` (SPEC line 36; repeated in
   derivation.md same-author replay).
   What: invalid arithmetic — wrong intermediate values.
   Why: recomputed independently by hand long division and with Python
   Decimal: (12x2250)/2288 = 27000/2288. 2288x11 = 25168, remainder
   1832; 18320/2288 = 8 (8x2288 = 18304), remainder 16, so 11.80;
   160/2288 = 0; 1600/2288 = 0; 16000/2288 = 6, remainder 2272;
   22720/2288 = 9, remainder 2128 — i.e. 11.800699..., 11.8007 to 4
   decimals, x1.5 = 17.7010. The SPEC's 11.797 / 17.696 are wrong by
   ~0.004/~0.005. Display values 11.80/17.70 survive by rounding luck.
   Minimum fix: correct the intermediates to 27000/2288 = 11.8007,
   x1.5 = 17.7010 (and the same two numbers in derivation.md).

2. Where: `derive mech_monthly:` (SPEC line 48).
   What: omitted premise.
   Why: the value 2600 enters only through the branch-condition prose;
   `mom_cap_sentence` (the MOM statement that the rate is capped at the
   S$2,600 salary level, P1 line 121) carries the step but is not in
   `from [hourly_formula, mom_formula_monthly, divisor]`.
   Minimum fix: add `mom_cap_sentence` to the premise list (branch
   conditions are not referenceable per PSEUDO §5.5, so the cap source
   must appear as a listed premise, not prose).

3. Where: `derive mech_hourly:` (SPEC line 50).
   What: result rests on an unestablished `claim` (PSEUDO §4 — allowed,
   but the reviewer must say so).
   Why: `from [pot, mom_cap_sentence]` cites `claim pot`, which no
   step derives; the S$20.40 result therefore inherits fully
   unestablished status. The document-level `?`s already cover this,
   but the step itself does not say it.
   Minimum fix: append "rests on unestablished `pot`" to the reasoning,
   or attach a statement-level `?` to that effect.

4. Where: claims.md C10 ("72h/month cap excl. emergency", marked
   corroborated provisional).
   What: overstated corroboration (ledger level; the SPEC `given
   monthly_cap` itself is correctly attributed to the PDF).
   Why: P1 corroborates only the 72-hour figure (line 167: "up to 72
   overtime hours in a month") and the exemption path (line 169).
   Neither "calendar month" nor the emergency-work exclusion appears in
   P1-P4; the only emergency/exception text (P1 lines 154-160) concerns
   the 12-hour daily limit, not the 72-hour cap.
   Minimum fix: qualify C10 as corroborated for the 72h figure only, or
   split the emergency-exclusion qualifier into its own unresolved row.

5. Where: `assume profile:` (SPEC line 31).
   What: premise wording asserts an untrue state (minor; load-free —
   no `derive` cites `profile`).
   Why: "effective date pinned by retrieval" contradicts the package's
   own record: every capture is provisional_transcript and P6 (SSO)
   failed, so nothing is pinned.
   Minimum fix: reword to "monthly-rated non-workman; effective date to
   be established by a pinned retrieval".

6. Where: second `if` condition (SPEC line 46, "statutory_basic computed
   with monthly cap 2600").
   What: condition is mechanism-prose rather than an explicit
   logical expression over declared names (PSEUDO §5.3; cosmetic —
   both branches state explicit outcomes, satisfying adaptation-spec
   §2).
   Minimum fix: restate as named `assume`s per mechanism, or leave as is.

7. Where: statement-level `?` on `cov_nonworkman_register` (SPEC
   line 13, "is corroborated by").
   What: status-adjacent wording (cosmetic; content verified accurate —
   S$2,600 confirmed on P1 line 116, P2 line 100, P3 line 47, P4
   line 49).
   Why: PSEUDO §7 bans status labels ("words like PROVEN, VERIFIED,
   ..."); "corroborated" sits uncomfortably close even with the
   "(provisional)" qualifier.
   Minimum fix: reword to plain attribution ("MOM-current pages P1-P4
   state S$2,600 (provisional transcripts)").

Checks passed (no finding): sha256 of all six artifacts recomputed by
running sha256sum — every digest matches CAPTURES.md and manifest.json
(P1 2048e745..., P2 bddfd246..., P3 1802efcb..., P4 adb2bb9a...,
P5 bac2d0ad..., P6 967190fb...); every SPEC `given`/`claim` traced to
its ledger row and capture lines with quotes supported (C15: P1 L121;
C16: P1 L127-128; C17: P1 L146; C18: P1 L112 + P4 L54; C19: P1 L169;
C20: P1 L173; C10/C11: P1 L167/L125 subject to finding 4); 7-construct
conformance holds — 31 names unique, all premise lists non-empty exact
earlier names, all `derive`s carry explicit results, all six `?`s
attach mechanically (3 statement-level at +1 indent immediately below
their targets, 3 document-level at column 0, each stating what is
missing and what would resolve it), no status words (only the prose
denial "unverified", conforming per PSEUDO §7); arithmetic confirmed —
52x44 = 2288 (52x40 + 52x4); 31200/2288 = 13 + 1456/2288 = 13 + 7/11 =
13.636363... = 13.6364 to 4 decimals, gap to 13.60 is 0.036;
13.636363x1.5 = 20.454545 -> 20.45; 13.60x1.5x2 = 40.80 exact;
unrounded 2-hour pay 13.636363x3 = 40.909090 -> S$40.91, S$0.11 above
MOM's S$40.80; mechanisms differ by ~S$0.05/hour. Both discrepancy
dispositions are correct and BLOCKED stands: threshold (four MOM pages
agree on S$2,600 but all are provisional transcripts with no
effective date and no SSO amendment text) and cap mechanism/rounding
(one data point cannot distinguish nearest- vs floor-to-S$0.10, the
rounding stage is unstated, and the calculator JS is uncaptured per
CAPTURES.md P4 limitations). C21 (rounding rule unstated, by omission)
confirmed by full read of P1. Fixtures-empty stance endorsed: with
threshold date, cap mechanism, and rounding all open, no golden,
anti-golden, or boundary fixture can honestly be written yet — even E6
(H=0 valid zero) needs a pinned coverage gate. contract.md is correct:
no owner/version, indeterminate entire rule, no Go.

## Evidential boundary

Checked (how): sha256 of P1-P6 recomputed by running sha256sum myself
(all match); P1-P4 transcripts and the P6 failure record read in full
by me, line by line, against every SPEC given/claim and ledger row;
all arithmetic recomputed independently by hand long division and
cross-checked with Python Decimal; SPEC scanned by hand and grep for
status words, empty premise lists, name reuse, forward references, and
`?` attachment. Could not check: SG_research.pdf bytes and
rule-source-register.md (not inspected — S1/S2-dependent givens taken
on the package's attribution, all honestly marked SPEC_INFERENCE/
unverified); the fetched HTML payloads behind the transcripts
(advisory: formulas need rendered pages, and only transcripts were
available); P5 poster content beyond file type/size (PDF 1.5, 1 page,
1041950 bytes); any effective-date history or Fourth Schedule text
(P6 403, uncaptured). Remains unresolved: every SPEC `?`, contested
rows C9/C13/C14, C21, the C10 emergency qualifier (finding 4), the
F1 arithmetic correction, and — as derivation.md itself states — a
genuine independent derivation, which still gates INDEPENDENTLY_CHECKED.

Recommended manifest.json review fields: {"reviewer": "independent-reviewer-subagent", "date": "2026-11-08", "protocol": "MRC", "outcome": "reviewed-findings-open"}

## Researcher dispositions (prompt7 step 2)

F1–F7 all addressed in SPEC v0.3.0: intermediates corrected, premise
added, claim-dependence noted, C10/C22 split (C22/C23), profile reworded,
condition restated over declared names, `?` reworded to attribution. The
prompt5_imp_review semantic pass additionally removed `min(H,72)` payable
logic, replaced cap branches with R1/R2 candidate derives, added the
eligibility derive and minimum-rate framing, and split 72h qualifiers.
Re-review of v0.3.0 is open.
