# Oracle Design — independent expected-results calculator

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## 1. Role

The oracle independently computes what *should* have happened from canonical
source facts. On clean data it reconciles with PayLoop; after fault injection
the oracle-vs-PayLoop diff becomes evaluation ground truth. It is
evaluator-only: no investigator tool, API response, template or prompt may
expose its outputs.

## 2. Independence (review correction 1)

Independence is a verifiable requirement, not an absolute claim:

1. Separate implementation: `modules/sg-wfm-payroll/oracle/` is derived
   independently from the pinned sources — a different derivation, not a copy.
2. No dependency: the oracle package must not import PayLoop calculation
   packages (`payloop/...` calc paths). An import test fails the build on
   violation.
3. Golden fixtures: versioned input/output pairs reviewed against official
   worked examples; both implementations must satisfy them independently.
4. Separate human review of the calculation logic before any answer key is used.

## 3. Contract

```go
// core/oracle (contract types; implementations are module-owned)
type Input struct {
    Facts       CanonicalFacts // approved punches/timesheets, terms, policy
    RulePack    string         // e.g. SG-MOM-OT@2026.1
    Period      string
    CutOff      time.Time
    CalcVersion string
}
type Outcome struct {
    Status  Status // calculated | not_applicable | indeterminate
    Lines   []ExpectedLine
    Hash    string // reproducible input/output hash
    Version string // oracle implementation version
}
```

`indeterminate` is mandatory for unsupported arrangements — the oracle must
never force a result the rule pack or facts do not support.

## 4. Reconciliation

For clean baselines, PayLoop lines must equal oracle lines within explicit
rounding rules (documented per rule pack; half-up to cents unless the source
says otherwise). Reconciliation records diffs in `payloop.reconciliation`;
any non-zero diff on a clean fixture fails the Phase 2 gate.

## 5. Versioning and provenance

Every oracle result records oracle version, rule-pack version, fixture version
and input hash. Rule-pack upgrades never rewrite history: old runs keep their
recorded versions and re-run deterministically.
