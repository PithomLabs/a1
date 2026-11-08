# Authentication Provider Boundary (Amendment 3)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

- v0.1 ships a **local development provider** only: seeded operator/
  investigator/evaluator/learner accounts (documented dev credentials) plus
  CLI token flow for scripts and tests. No external IdP, no network calls.
- Provider interface (finalized Phase 4): `Authenticate(ctx, credential)
  (Principal, error)` with credential types per provider; business packages
  depend on `Principal`, never on provider implementations.
- JWT hygiene ported from bchat: server refuses to boot auth paths with an
  empty signing secret; secrets via env/Fly secrets only.
- Session/token lifetimes are short and documented; service identities use
  scoped API tokens, never user sessions.
- When SSO arrives: established, maintained, standards-compliant Go libraries
  only — never a homemade OIDC/SAML stack. No stub protocol endpoints exist
  until a real implementation ships.

Acceptance (MVP): local auth works offline; CLI/test flows unaffected;
operator/investigator/evaluator boundaries enforced end to end.
