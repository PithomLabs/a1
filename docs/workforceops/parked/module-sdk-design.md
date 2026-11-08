# Module SDK Design (`sdk/module`, Phase 7)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

The SDK is a thin starter, not a framework: a `starter/` template (manifest,
`module.go`, one route, one tool, one scenario, one migration, one test),
doc comments linking each piece to the contract docs, and `task module:new`
scaffolding plus `task module:validate` checks.

Explicit non-goals: code generation beyond scaffolding, a test double library
(copies of the reference module's patterns suffice), version negotiation
helpers, or any runtime beyond `core/module` itself. The `order-fulfillment`
example is built from this starter to prove it works.
