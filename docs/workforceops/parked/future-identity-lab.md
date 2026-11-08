# Future Identity Lab (Amendment 3 target design)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08
**Not in MVP.** Recorded so the MVP's boundaries don't block it.

```text
Simulated enterprise → Identity provider (OIDC/SAML/SCIM simulators)
  → HR + WFM apps → tokens/sessions/logs → support incident
  → CaseTrace investigation → evidence + evaluation
```

Learners will configure an IdP and relying apps, run login/provisioning
flows, inject authN/authZ/provisioning/session failures, and investigate via
the same evidence fabric. The lab distinguishes authentication vs
authorization vs provisioning vs session failures, exposes protocol exchanges
through safe simulated views, and uses synthetic credentials/test certificates
only — real signing keys and tokens never appear in training logs.

MVP provisions that enable it: `Principal` abstraction, provider boundary,
generic connector framework, environment isolation ladder, capability metadata
in module manifests. Nothing more is built now.
