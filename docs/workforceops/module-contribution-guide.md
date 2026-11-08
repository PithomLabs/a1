# Module Contribution Guide (public modules)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08
**Full text ships with `CONTRIBUTING.md` in Phase 6; this is the binding outline.**

1. Start from `sdk/module/starter` (Phase 7) or copy `examples/modules/order-fulfillment`.
2. Write `module.yaml` first; keep capabilities minimal and honest.
3. Domain code stays inside your module directory; import `core/*` only.
4. Ship migrations (Up/Down), fixtures, one scenario with sealed ground truth,
   golden tests, and docs (domain notes + rule sources where applicable).
5. Synthetic data only; nothing resembling a real person, company or credential.
6. Run `task module:validate` (manifest, contract tests, boundary suite,
   leak scans) — green is required for review.
7. One focused review + one correction pass per contribution; reviewers check
   boundary compliance, data honesty and test reality (no unexecuted claims).

Maintainers may reject modules that duplicate the reference domain, smuggle
customer specifics, or expand core without a second consumer.
