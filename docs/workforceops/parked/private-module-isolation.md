# Private Module Isolation

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

Customers commissioning proprietary modules must never see their code, data,
rules, credentials or scenarios enter the public repository.

- Private modules live in separate repositories/Go modules and compile into
  customer-specific builds via the same static `Register` contract.
- Public code must not import, vendor or embed private modules; CI on the
  public repo cannot depend on their existence.
- Customer data/credentials live in customer-owned databases and secret stores;
  the public seed/generator paths never reference them. Contractual IP
  ownership is agreed per engagement and recorded before work starts —
  never assumed in either direction.
- Reference-module fixtures stay synthetic and generic; anything resembling a
  real customer (names, codes, mappings) is rejected in review.
- The `order-fulfillment` example doubles as the shape private modules copy:
  small, self-contained, manifest-pinned.
