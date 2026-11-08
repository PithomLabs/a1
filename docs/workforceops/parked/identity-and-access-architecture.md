# Identity and Access Architecture (Amendment 3)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

## Separation of concerns

Authentication establishes identity; authorization decides what it may do.
Business modules never know which provider authenticated the caller.

## Principal (illustrative v0 — finalized in Phase 4)

```go
type Principal struct {
    ID           string
    TenantID     string
    Subject      string
    DisplayName  string
    Email        string
    Groups       []string
    Roles        []string
    AuthProvider string // "local" in v0.1
}
```

The application operates on `Principal`, built once per request/session by the
auth layer. Modules receive the principal (or its scoped subset), never tokens.

## Two SSO futures (never conflated)

A. **Platform auth:** organizations log into WorkforceOps Lab via their IdP
   (OIDC/SAML/SCIM, group→role mapping, JIT provisioning) — Future B/C.
B. **Simulated IdP:** learners configure and troubleshoot SSO *inside* a
   simulated enterprise (redirect mismatches, token/audience errors, cert
   rotation, mapping failures) — a future business module + lab.

Both are documented now, implemented never in the MVP.
