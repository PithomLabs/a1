Excellent question — and "in retrospect" is exactly the right lens, because once you've hand-built the mechanism, the industry map explains *why* you had to. Short version: **the industry has a productized answer for each of the four clauses individually — but no product sells the intersection.** Every offering solves one clause by refusing one of your constraints. Let me walk the map.

---

## 1. The consistent-cut problem (D1): "compute over a fresh, stable cut of mutable data"

Four distinct industry answers, each buying the same clause differently:

**a) Let the database own the protocol.** The mainstream answer is *never hand-roll a locking protocol*: use `SELECT ... FOR UPDATE` for row-scoped freezing, or SERIALIZABLE (PostgreSQL's SSI, Cahill's algorithm, since 9.1) when the freeze is predicate-shaped and spans tables. There's also a built-in primitive most teams never learn exists: `pg_export_snapshot()` / `SET TRANSACTION SNAPSHOT` — a consistent cut *shared across connections*. This is exactly how `pg_dump -j` gives every parallel worker the same view. It doesn't solve gate-freshness semantics, but it proves the platform has first-class multi-reader cuts if you ask for them. Industry posture: correctness-critical synchronization belongs to the engine or to a boundary — not to an advisory-key convention.

**b) Freeze by append, not by lock.** The architecture-level answer: make data immutable and the problem dissolves. **Datomic** (queries *as-of* a time — D1 cannot exist there by construction), **XTDB** (bitemporal), **EventStoreDB** and event sourcing generally, **Apache Iceberg/Delta Lake** (snapshot isolation + time travel on lakes). The writeup's "choice (b)" is an entire product category.

**c) Freeze by state, not by lock.** The ERP/payroll answer — and your domain twin. **SAP payroll** doesn't lock periods: the payroll *control record* carries a status machine (Released → Exited), payroll results land in **immutable, versioned** storage, and corrections happen via retro-runs that create *new* result versions — they never touch old ones. The "gate" is a status flag plus constraints, not an advisory protocol. Most banking cores work the same way.

**d) Freeze by boundary.** The settlement answer: batch/COB windows where the world is quiesced. Crude, operationally real, and the default in payments for decades.

And standing over all of it, the **audit industry**: Jepsen (commercial), **Maelstrom** (Jepsen's free harness for arbitrary languages), and **Antithesis** (deterministic simulation testing as a commercial product, built by FoundationDB alumni). Note the generalization D1 taught you isn't Postgres-specific: a Redis lock plus a PG read has the same lock-visibility desync — the lock and the read live in different consistency domains. That's *why* these testing products exist.

## 2. The authority/tamper-evidence problem (EF-E3/F1): "only the authorized writer writes, and anyone can tell"

This one is the most productized of the four:

- **Ledger tables in mainstream RDBMSs.** SQL Server 2022 / Azure SQL have native **Ledger tables**: append-only, every transaction's hash chained into the block, digest generation and verification as built-in procedures. **Oracle 21c+** has **Blockchain Tables**: append-only, hash-chained rows with retention enforcement. These are *literally* EF-F1 as a checkbox feature.
- **Dedicated ledger databases.** Amazon **QLDB** (journal with Merkle-tree digests, a verification API) lived 2019–2025 and was deprecated/EOL'd — a market lesson: the pattern won, the standalone product lost, and AWS now guides ledger use-cases onto general PostgreSQL/Aurora with published hash-chain patterns. **immudb** (open source, Merkle-tree-backed, SQL-ish) is the surviving standalone. **Azure Confidential Ledger** does it inside TEEs.
- **WORM storage for the exports**: S3 Object Lock (compliance mode), NetApp SnapLock — the legally defensible retention answer auditors actually accept.
- **Choke-point authority** — the pre-database classic: banking cores that expose *only* stored-procedure APIs (no table grants to the app at all), and CQRS command handlers as the single write verb. Your REVOKE + SECURITY DEFINER capture is the same pattern rebuilt inside the frozen two-role model.
- **Provenance frameworks** for the supply-chain flavor of the same problem: **SLSA** (provenance levels), **in-toto** (attestation format), **sigstore/Rekor** (append-only transparency log for signatures), **TUF**. Notice they institutionalize your "evidence grade" concept — levels of claim, each with required artifacts.

## 3. The contract-breadth problem (P1a–g): "every writer joins the protocol"

The industry attacks breadth **structurally and declaratively**, because hoping writers cooperate never works:

- **Make the protocol the only verb.** Event sourcing: every writer appends to one log — participation is enforced by grammar (there is no UPDATE). Procedure-only DB APIs: `app` never gets DML, so breadth collapses to one package.
- **Declarative constraints that survive adversarial writers.** This matters directly for your steps 7–9: the comp/SHG effective-dating overlap problem ("no two compensation terms may overlap for the same employee") has a *native, adversarial-writer-proof* answer in PostgreSQL — **exclusion constraints** (`EXCLUDE USING gist (employee_id WITH =, daterange WITH &&)`). Similarly, "one open period per employer" is a **partial unique index**, not a check in Go. These are invariant-as-mechanism at its cheapest.
- **System-versioned temporal tables** (SQL Server 2016+, DB2's SQL:2011 support) productize effective-dating so history can't be rewritten in place.
- **Enforcement the app can't disable** — triggers owned by a non-app role plus table-owner separation — is precisely migration 00013's shape; it's the only in-database answer for out-of-band writers (psql, migrations, other services).
- What frameworks give you here is the earlier conversation's conclusion, confirmed: Rails callbacks and Hibernate interceptors are *convention*, defeatable by any raw SQL. No ORM enforces participation of writers it can't see.

## 4. The evidence problem (the reliability rung): "prove it, don't assert it"

Every rung of the ladder is now a product shelf:

- **Rung 3**: QuickCheck (Haskell/Erlang), Hypothesis (Python), proptest (Rust), jqwik (Java) — and in Go, thinner but real (rapid, gopter, or hand-rolled randomized invariant tests).
- **Rung 4**: Jepsen, Maelstrom, Antithesis; systematic concurrency testers (JetBrains Lincheck, Microsoft Coyote); chaos platforms (Gremlin, AWS FIS, Chaos Mesh); the race detectors built into Go and TSan.
- **Rung 4.5**: **TLA+** with TLC/Apalache — the AWS/Azure design-stage standard, and the affordable sweet spot for a protocol like your gate.
- **Rung 5**: SPARK, Dafny, F*, Coq, Verus (verified Rust, Azure Research), with seL4/CompCert as the existence proofs of the category.
- One gap worth naming: there is **no mainstream runtime attestation product** for "this transaction was protocol-compliant." That's why the industry encodes compliance as *mechanism* (tickets, hash chains, append-only) rather than *monitoring* — the same choice Gate 1 made.

---

## Why no one sells the whole thing

| Escape route | Clauses it buys | Price you pay | Examples |
|---|---|---|---|
| Engine-owned protocol (SSI, FOR UPDATE) | Complete + governed | Abort storms, throughput, retry discipline | PG SERIALIZABLE |
| Append/versioned architecture | **All four, structurally** | Re-architecture, versioning machinery, new query model | Datomic, EventStoreDB, SAP retro-runs |
| Ledger tables | Authentic + governed | No UPDATE, nothing for freshness, storage cost | SQL Server Ledger, Oracle Blockchain, immudb |
| Batch boundary | Complete, by quiescence | No intraday capture, ops discipline | settlement systems, COB |
| Verification tooling | Evidence for whatever you built | Doesn't change the mechanism; you still build the kernel | Jepsen, TLA+, Antithesis |

The intersection nobody bundles is: *freshness + authority + breadth + proof, over mutable current-state tables, in a general RDBMS, with an untrusted app role.* Every product above escapes the intersection by dropping one constraint. M2.1 sat inside it because the mutable-state architecture was frozen — which is why the work was hand-rolled, and why the writeup prices half of it as inherent. The industry's honest advice would have been: **"don't sit at that intersection; pick the escape route whose price you can pay."**

## What this means concretely for WorkforceOps

**Adoptable now, no re-architecture:**
- Exclusion constraints for comp/SHG term overlap; partial unique indexes for period state machines — declarative, bypass-proof, cheap.
- Property tests for proration/supersession arithmetic (rung 3 on your ladder).
- A 1–2-page TLA+/PlusCal model of the gate protocol — rung 4.5 for the price of a design doc; catches ordering mistakes no attack battery will enumerate.

**Adoptable at v0.2:**
- Append-only, versioned payroll results with SAP-style retro-runs — removes freezing from the results path entirely and turns capture into a plain read (this is the durable version of the "Option 2 long-term" recommendation).
- In-DB hash chaining (a trigger-maintained chain table) if auditors demand cryptographic tamper-evidence — no database switch required.

**Not worth buying here:** dedicated ledger databases (deprecated market direction), blockchain anything, and rung 5 for application code — for a payroll domain that's the over-rung trap; rung 4 on the gate kernel is the right altitude.

---

The retrospective in one sentence: **the industry doesn't sell a solution to this dilemma — it sells four ways to stop having it, and the discipline to know which one you've implicitly chosen.** M2.1's cost was discovering the choice after the fact; the companion checklist's job is to make it before the first migration.

Happy to fold this into the writeup as a second companion section ("C-market: What the Industry Sells"), or draft the exclusion-constraint/TLA+ adoption items into the Gate-2 plan as concrete line items.