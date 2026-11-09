There is no single library or product that covers all of it. The industry splits the problem into layers and handles each with a different mechanism: business process, database features, specialist stores, and bought domain engines. Most teams get by with the first two layers.

## How the industry handles each challenge

| Challenge | Typical approach | Tools and products |
|---|---|---|
| Cut-off and recency | Close the period: reject postings into closed periods and make late changes as adjustments in the next period. This is a business rule first and a technical one second. | Mostly custom. ERP and ledger products implement period close natively. |
| Concurrency, stale views | Read committed plus explicit row or advisory locks, optimistic version columns, or serializable with retries. | PostgreSQL or CockroachDB features, ORM optimistic locking, and Spanner for external consistency. |
| Effective-dating, gaps and overlaps | Valid-time ranges with constraints. | PostgreSQL range types with exclusion constraints, SQL Server and MariaDB system-versioned tables, XTDB and Datomic. |
| Immutable history, tamper evidence | Append-only tables with restricted privileges, audit triggers, optional hash chains. | `pgaudit`, SQL Server and Azure SQL ledger tables, Oracle blockchain tables, immudb. |
| Provenance and reproducibility | Store inputs, rule versions and a content hash with every output. | Largely custom; data-versioning tools like Dolt for some cases. |
| Rule management | Rules as versioned data, or a decision-model engine. | Decision engines (DMN, Drools, Camunda), policy engines (OPA). Statutory content is often bought as a maintained feed. |
| Rule supply-chain trust | Signed, digest-pinned artifacts in a reviewed repo. | Sigstore, in-toto, SLSA, plain git plus CI. |
| Many writers, one gate | Everything goes through a service or database functions, with privileges enforcing it. | Mostly design discipline; the database role model is the only enforcement. |
| Long-running workflows, retries | Durable workflow engines. | Temporal, event-sourcing frameworks (Axon, EventStoreDB, Marten). |

Many teams buy the domain instead: payroll engines and embedded payroll APIs, where a vendor maintains statutory content and period-close behavior. That moves much of the table above to someone else, at the price of control over provenance and customization.

## What I verified as current

- **PostgreSQL 18 added temporal keys.** It added `WITHOUT OVERLAPS` for primary and unique keys, and `PERIOD` for foreign keys. The foreign-key rule is that the referenced row must exist and be valid for the entire period in question. A test in the PostgreSQL 18 source shows an insert that spans two parent records being accepted. In plain terms, adjacent referenced rows can jointly cover a referencing period. That is close to your SHG union-coverage and rule-pack full-period coverage questions.
- **PostgreSQL 19 is expected to extend this.** It expands the temporal capabilities with `UPDATE/DELETE ... FOR PORTION OF`.
- **XTDB offers bitemporality as a built-in database feature.** It tracks both the system time when data is written and the valid time when a record is effective. The 2.x line was described as beta in the README I found, with 1.x as the stable release. Check its current status before relying on it.
- **Ledger databases carry vendor risk.** AWS discontinued QLDB, and said support would end on July 31, 2025. The suggested Aurora PostgreSQL alternative offers detailed audit logging but no cryptographic verifiability. That is a good reason to prefer open, portable mechanisms for tamper evidence.

## What no one gives you

- Your cut-off definition, threat model, write-path inventory and coverage policy. Tools implement these once you've decided them.
- Authority over derived data, meaning who may create what and how it's verified.
- The proof that your invariants hold. That is still adversarial testing.

## What this suggests for your project

- Your current database is PostgreSQL 16. The exclusion-constraint approach I suggested earlier works there with `btree_gist`. PostgreSQL 18 gives a cleaner form, and `PERIOD` foreign keys could enforce "this pack or instruction covers the period" in the database. Whether to move to 18 is an upgrade decision worth taking before 00013 is written, not after.
- The period-close pattern is probably your cheapest answer to the cut-off question. It is a business rule you could adopt even if you keep the gate design.

Do you want me to map each checklist item in my last answer to the layer and tool that would address it, as a table you could keep?