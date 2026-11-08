# Business-rule package template

Copy this directory to `rules/<RULE-ID>/` and fill it in. Delete nothing:
an empty section with a `TODO` and a `?` in the spec is honest; a deleted
section looks complete.

```text
rules/<RULE-ID>/
  manifest.json        # identity, status, applicability, refs (schema below)
  sources/             # immutable evidence: snapshots, digests, retrieval notes
    SOURCES.md         # one entry per source (format below)
  claims.md            # extracted claims ledger (format below)
  SPEC.pseudo.md       # the Phys-Pseudo specification (exact upstream syntax)
  derivation.md        # independent derivation (separate author, separate steps)
  edge-cases.md        # selected categories + six-outcome classification
  fixtures/
    golden/            # hand-derived pass cases (from the spec, never the impl)
    anti-golden/       # plausible wrong readings that must fail
    boundary/          # thresholds, caps, transitions, rounding edges
  REVIEW.md            # independent review record (MRC protocol)
  contract.md          # implementation contract (owner, version, gating set)
```

## manifest.json schema

```json
{
  "rule_id": "SG-MOM-OT-001",
  "name": "string",
  "description": "string",
  "jurisdiction": "SG",
  "authority": "MOM",
  "rigor": "L1",
  "version": "0.1.0",
  "status": "DISCOVERED|SOURCES_PINNED|CLAIMS_EXTRACTED|FORMALIZED|INDEPENDENTLY_CHECKED|GOLDENS_VERIFIED|IMPLEMENTABLE|BLOCKED",
  "blocked_reason": "required unless IMPLEMENTABLE",
  "effective_from": "YYYY-MM-DD|null",
  "effective_until": "YYYY-MM-DD|null",
  "applicability": {
    "employee_categories": [],
    "employment_arrangements": [],
    "wage_basis": [],
    "exclusions": [],
    "unsupported": []
  },
  "sources": [{"id": "S1", "digest": "sha256:…", "retrieved_at": "RFC3339"}],
  "dependencies": ["other rule IDs"],
  "spec_files": ["SPEC.pseudo.md"],
  "review": {"reviewer": "", "date": "", "protocol": "MRC", "outcome": ""},
  "implementation": {"owner": "", "version": "", "fixtures_gate": []}
}
```

`status` is manifest prose, never spec syntax. `BLOCKED` is a workflow
marker for a package stopped by a load-bearing `?`; it is not a language
status.

## SOURCES.md entry format

```markdown
## S<n>: <short title>
- authority: <issuing body>
- url: <stable URL>
- snapshot: <local path under sources/> (rendered pages for formulas)
- sha256: <digest of the snapshot>
- retrieved_at: <RFC3339>
- published: <date or "unstated"> | effective: <date or "unstated">
- scope: <section/paragraph/table/formula/page used>
- method: <rendered-page read | text extraction | transcription>
- restrictions: <redistribution/licensing notes>
- evidence_class: EXPLICIT_SOURCE | SOURCE_CITES_EXTERNAL_RESULT | SOURCE_ASSERTS_OR_SKETCHES | SPEC_INFERENCE | UNRESOLVED
```

A search-result snippet is never evidence. Text extraction that drops
formulas (see OT-001) must say so and point at rendered pages.

## claims.md ledger format

```markdown
| # | claim | source scope | evidence_class | spec ref | state |
|---|-------|--------------|----------------|----------|-------|
| C1 | … | S1 §… | EXPLICIT_SOURCE | `claim …` line | established |
```

`state`: `established | contested | unresolved`. Contested claims quote
both values. Every `claim`/`given` in the spec traces to exactly one row.

## SPEC.pseudo.md conventions

- Exact upstream syntax (`PSEUDO.md`); fenced ```text block; `# source:`
  header for source-backed specs.
- Applicability givens first, then definitions, assumptions, claims,
  derivations, conditionals; document-level `?`s last.
- No status words (`IMPLEMENTABLE`, `VERIFIED`, …) anywhere in the file.
- A `?` for every unresolved load-bearing point, quoting what would
  resolve it.
