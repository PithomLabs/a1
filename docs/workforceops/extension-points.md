# Extension Points — what the kernel offers modules

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

| Extension point | Mechanism | Lands |
|---|---|---|
| HTTP routes (host-surfaced) | `Registrar.RegisterRoutes` | Phase 1 (skeleton) |
| Investigation tools (read-only) | `Registrar.RegisterTools` | Phase 4 |
| Scenarios + fault params | `Registrar.RegisterScenarios` | Phase 3 |
| Evaluators + rubrics | `Registrar.RegisterEvaluators` | Phase 5 |
| Migrations (module schemas) | embedded goose set | Phase 2 |
| Oracle implementation | `core/oracle` contract types | Phase 2 |
| Fault mechanisms | `core/fault` registry | Phase 3 |
| Console nav + views | template contribution (Phase 6) | Phase 6 |
| Import mappings (8A) | `core/import` mapping contracts | Phase 8A |
| Config kinds (D) | `config_record` kinds | Phase 2/8A |
| Enterprise capabilities | manifest `capabilities` (metadata now) | Future |

Rules: every extension point needs a second real consumer before it generalizes
further; metadata-only declarations (no behaviour) are allowed for future
capabilities; nothing here implies a marketplace, billing, or dynamic loading.
