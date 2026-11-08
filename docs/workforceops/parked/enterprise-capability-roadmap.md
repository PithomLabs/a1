# Enterprise Capability Roadmap (Amendment 3)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

The MVP implements the foundation row only. Everything else is an explicitly
marked future phase — documented boundaries, no stubs, no fake endpoints.

**MVP (this plan):** local Postgres, Go backend, console, CLI, REST, reference
module, CSV/JSON import slice, versioned config slice, incident simulation,
CaseTrace, practice/assessment/interview, local auth, documented roadmap.

**Future A — Environment authoring:** multi-env isolation, PG import/export,
anonymization pipeline, custom rules, workflow definitions, snapshots/cloning,
customer Go modules.

**Future B — Enterprise identity laboratory:** OIDC, SAML 2.0, SCIM 2.0, IdP
simulators, provisioning, authN/authZ scenarios, SSO troubleshooting labs.

**Future C — Enterprise platform:** advanced RBAC/ABAC, LDAP/AD, service
identities, API-gateway/security testing, secrets lifecycle, audit/SIEM,
environment promotion, backup/recovery/HA-DR exercises.

**Future D — Customer staging:** private modules, real connectors, customer
data/configs, private deploys, custom operational scenarios.

No delivery dates are assigned. Each future phase needs its own design gate
before implementation.
