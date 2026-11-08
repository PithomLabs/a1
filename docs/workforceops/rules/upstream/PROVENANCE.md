# Vendor provenance — Phys-Pseudo

Verbatim copies from the upstream repository. Do not edit these files;
WorkforceOps guidance lives alongside in `../`, never inside `upstream/`.

- upstream: https://github.com/PithomLabs/phys-pseudo.git
- pinned commit: `b9bb9a4` (branch `main`)
- license: MIT, copyright 2026 Pithom Labs (see `LICENSE`, required by MIT
  redistribution terms)
- retrieved: 2026-11-08 (read-only copy; upstream tree left untouched)

## Files

| Path here | Upstream path | sha256 |
|---|---|---|
| `PSEUDO.md` | `PSEUDO.md` | 6da7294ae6aa277c7a52975cdfb4a8f54816ee7439b12e0ff8e47a64582448be |
| `MRC_REVIEW_PROTOCOL.md` | `MRC_REVIEW_PROTOCOL.md` | f705979b7cc28c22142686014e04dbdfecc98dbc7bc6c97ca30bf4f9c4514640 |
| `LICENSE` | `LICENSE` | 7386f1ad5fa7ea180e63de11f6917bfd7e5ba0d2cf1fc675f2edeb9df8ee349b |
| `conformance/README.md` | `conformance/README.md` | e7f87bb2f1b9183d3d29c01dc6e8b23f115f284a63f8400cb920cef216ee4196 |
| `conformance/01_operator_composition/fixture.md` | same | 878ec73c7e4b671726adc0202685abc2056c56204072cd0cad1e3b65baa2d388 |
| `conformance/04_conditional/fixture.md` | same | ae20a38534540bbab88975775c6bc6a5c3d265f08d61a7fc452e8dcbe44d2555 |

Why these: `PSEUDO.md` is the language authority; `MRC_REVIEW_PROTOCOL.md`
is the review method our reviews follow; `conformance/README.md` defines
the fixture vocabulary needed to read any fixture honestly; fixtures 01
(refutation-instead-of-`?`) and 04 (conditionals) are the two patterns our
specs reuse most. Deliberately excluded: `DERIVATION_SPEC_GUIDELINES.md`
(PhysTrace-implementation-specific; the adaptation spec re-expresses its
transferable shape), `plan/` history, and the remaining fixtures.
