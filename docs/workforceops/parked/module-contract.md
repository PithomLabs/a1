# Module Contract (v0, implemented in `core/module`)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

```go
type Module interface {
    Descriptor() Descriptor   // id, version, minimum-core, deps, capabilities
    Register(Registrar) error // contribute routes/tools/scenarios/evaluators
}
type Registrar interface {
    RegisterRoutes(RouteSet) error
    RegisterTools(ToolSet) error
    RegisterScenarios(ScenarioSet) error
    RegisterEvaluators(EvaluatorSet) error
}
```

## Rules

- Registration is static and explicit in host `main` packages — no reflection
  scanning, no `plugin` package (ADR-008).
- Every `Register*` method is optional to call; capability interfaces stay
  minimal and grow only with a second real consumer.
- Descriptors must be unique by ID; incompatible `minimum_core` fails boot.
- RouteSets are host-surfaced (`ShiftHub`/`PayLoop`/`Console`); a module can
  target any surface but must respect its authorization.
- Migrations are module-embedded goose sets applied after core sets; module
  `Down` is dev-only like core.
- Contract evolves until the Phase 7 freeze; thereafter additive-only within a
  major version (`module-versioning.md`).

The worked reference is `modules/sg-wfm-payroll/module.go` + `module.yaml`.
