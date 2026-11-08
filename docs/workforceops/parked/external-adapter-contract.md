# External Adapter Contract (extension point, Phase 9+)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

For customers who will not port proprietary logic into the platform, the
integration framework supports external adapters over versioned contracts:

- Transports: REST, webhooks, batch file exchange (CSV/JSON), versioned event
  schemas. No bespoke binary protocols in v0.1.
- Every adapter declares capabilities, auth method, rate limits, retry and
  idempotency semantics, payload schemas (versioned) and reconciliation hooks.
- The platform observes adapter traffic (request/response metadata, never
  secrets) into the same correlation/evidence fabric as native integrations,
  so incidents remain investigable.
- Adapters never receive investigator credentials or ground-truth access;
  service identities with minimal scopes only (see `authorization-model.md`).

The MVP builds no external adapters; it keeps the framework free of HRIS,
payroll or SSO-specific assumptions so adapters slot in later.
