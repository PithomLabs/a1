# Module Versioning

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

- Modules version independently (`module.version`, semver-ish `major.minor.patch`).
- `platform.minimum_version` declares the oldest compatible core; v0 compares
  major versions only (`coreSatisfied`, tested).
- Capabilities are additive within a core major; removing or changing a
  Registrar method requires a core major bump and a migration note.
- Rule packs and config records carry their own effective-dated versions,
  orthogonal to module releases.
- The environment manifest pins every version needed to replay a workspace;
  CI replays the pinned matrix on every change to core or the reference module.
- Pre-freeze (before Phase 7): breaking contract changes are allowed but must
  update the reference module, the example module, the SDK starter and this doc
  in the same change.
