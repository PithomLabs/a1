# Licensing and IP Boundaries

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

- New project license: **MIT, copyright Pithom Labs** (Q3 approved; `LICENSE`
  applied at scaffold). No license server or hosted control plane required.
- `bchat/` has no LICENSE file and is untouched: nothing is assumed about its
  status, nothing is copied wholesale — only reimplemented patterns (see the
  audit's port list) plus third-party open-source libraries via `go.mod`.
- Pre-release dependency audit (Phase 6 gate): enumerate `go.mod` licenses,
  confirm MIT-compatibility (no copyleft linked in), record versions in the
  release notes. Current pins: Echo 4.13.4, pgx 5.12.0, Cobra 1.10.2,
  Viper 1.21.0, Goose 3.27.3, Testify 1.12.1, sqlc tool 1.30.0.
- Customer IP: ownership of commissioned modules is agreed per engagement
  before work starts; private code/data/credentials never enter the public
  repo without explicit authorization.
- Synthetic-data IP: all fixtures are generated in-repo; the anonymization
  PDF under `plans/` informs design only and is not redistributed.
