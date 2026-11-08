# Sources — SG-MOM-OT-001

## S1: SG research PDF (secondary)

- authority: project research artifact (not MOM)
- url: n/a (local file)
- snapshot: `plans/SG_research.pdf` pp.1-2 (overtime section)
- sha256: 9ffbbc59e0a19577ab99cec611756c70b23719e32c08f887fcbec3bf58d55417
- retrieved_at: 2026-11-08T14:32:08+08:00
- published: unstated | effective: unstated
- scope: Part IV coverage, standard hours, OT definition, 1.5x, Fourth
  Schedule formula, 2250/2500 caps, 72h monthly cap, rest-day rules
- method: rendered-page read (text extraction drops every formula value;
  extraction alone would be evidence of nothing)
- restrictions: none known (project-internal artifact)
- evidence_class: SPEC_INFERENCE (values transcribed from a secondary
  artifact; every figure needs MOM confirmation)

## S2: rule-source-register.md coverage notes (unverified)

- authority: project register (not MOM)
- snapshot: `docs/workforceops/rule-source-register.md` §2
- scope: S$2,600 threshold hint, 14-day payment hint
- evidence_class: UNRESOLVED (explicitly unverified until the retrieval pass)

## Source, capture, and grade identities

Three identities stay separate:

| Identity | Meaning | Example |
|---|---|---|
| Source ID | The underlying authoritative document | MOM-HOURS-OT |
| Capture ID | A particular retrieved artifact | P1 |
| Evidence grade | How faithfully the capture represents the source | provisional_transcript |

| Source ID | Document | Captures |
|---|---|---|
| S1 (research PDF) | `plans/SG_research.pdf` | S1 (artifact itself) |
| S2 (register) | `docs/workforceops/rule-source-register.md` | S2 (artifact itself) |
| MOM-HOURS-OT | MOM hours/overtime page | P1 |
| MOM-WHO-COVERED | MOM who-is-covered page | P2 |
| MOM-FAQ-LIMIT | MOM salary-limit FAQ | P3 |
| MOM-CALC | MOM calculator page | P4 |
| MOM-OT-POSTER | MOM OT poster PDF | P5 |
| SSO-EMA1968 | Employment Act 1968 (SSO) | P6 (failed retrieval, not a source) |

## S3: MOM hours/overtime page (identified, accessed provisionally)

- authority: MOM
- url: https://www.mom.gov.sg/employment-practices/hours-of-work-overtime-and-rest-days
- status: official source identified; P1 transcript captured, hashed, retained
- outstanding: original HTML bytes not captured; legislative effective-date
  history not established; byte-grade verification outstanding
- evidence_class: UNRESOLVED (byte-grade pinning is what unblocks the package)

## S4: provisional MOM captures (Stage 1, 2026-11-08)

- authority: MOM (P1–P5); AGC/SSO attempt (P6, failed)
- snapshot: `sources/provisional/` (P1–P5 artifacts + P6 failure record)
- provenance: see `sources/provisional/CAPTURES.md` for per-artifact
  digests, timestamps, and limitations
- evidence_class: SPEC_INFERENCE (captured representations, not source
  bytes; nothing here pins a source — Stage 2 byte-grade verification
  still required for every value)
