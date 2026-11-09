Here is the checklist, grouped by area. Questions marked ⚑ are the ones that are most expensive to retrofit. Each is one I'd want answered, in writing, before any schema design.

## 1. Purpose and reliance
- ⚑ What decisions or payments depend on the system's output, and what happens if it is wrong? (Money, legal liability, safety, reputation.)
- Who relies on the output outside the team: customers, regulators, auditors, other systems?
- Is an output ever final, or can it be recalled and reissued? Who can do that, and how is it recorded?
- Does anyone need to reproduce an output years later exactly as it was produced?

## 2. Trust and threat model
- ⚑ Who can touch the data, and which of them do we distrust: the app code, operators, DBAs, integrations, malicious insiders?
- ⚑ Is direct database access by the application role in scope as a threat, or is the app the only trusted writer?
- What must be tamper-evident, and what only needs to be correct?
- Where is the root of trust (a git repo, a registry, a human approval), and who can change it?
- Which duties must be separated (preparer, approver, releaser)?

## 3. Time and cut-off semantics
- ⚑ What is "the cut"? Say whether it means everything committed before a moment, everything effective in a period, or everything known at a point.
- ⚑ Which time dimensions exist: when something was true, when it was recorded, when it took effect? Which ones must we query?
- How are late changes handled: reject, queue for the next cycle, or recompute and adjust?
- Is there a closed-period concept, and who can reopen it?
- What time zones, calendars and cut-off conventions apply?

## 4. Concurrency and consistency
- ⚑ What are all the writers, including admin scripts, imports, jobs and triggers?
- ⚑ Per operation, which guarantee is required: eventual, consistent as-of start, or complete up to a defined cut?
- What happens when two operations conflict: block, fail, retry or merge? Who is told?
- Which operations must be idempotent, and by what key?
- What is the acceptable contention? Can an important operation block writers briefly?
- Are retries safe, or can they hide a consistency failure?

## 5. Mutability and history
- ⚑ Which records are edited in place, and which are append-only with successors? Why?
- ⚑ How are corrections represented and linked to what they correct?
- What may never be deleted, and what must be deletable (privacy, erasure requests)? Where do those two conflict?
- How long is history retained, and in what form?
- Can a corrected fact change a past result? If so, is the old result kept?

## 6. Authority and provenance
- ⚑ For each fact, which single place is the source of truth?
- ⚑ Which values are entered, which are derived, and who is allowed to create each kind?
- For every output, what evidence must be preserved to explain it: inputs, rules, versions, who and when?
- Is there one canonical representation and one place where it is computed? (Two implementations of the same logic will drift.)
- What would "fabricated" mean here, and what prevents it?

## 7. Rules and configuration
- ⚑ Which rules are code, and which are data? Who edits each, and how are changes approved?
- Are rules versioned with effective dates? What if a period spans two versions?
- What is the standard of evidence for a rule: source documents, independent review, test vectors?
- How is a rule's verification status derived rather than asserted?
- Can you roll back, and what happens to results computed under the withdrawn rule?

## 8. Completeness, coverage and ambiguity
- ⚑ What does "complete" mean for each input: every member of the population, every day of the period?
- ⚑ How are gaps, overlaps and partial coverage treated: block, default, or infer?
- Is missing data "unknown", "none", or "not applicable"? Are these distinct states?
- When rules or data are ambiguous, which way do we fail by default? (I'd fail safe: block and say why.)
- Who owns resolving a blocker, and how is the resolution recorded?

## 9. Interfaces and write paths
- ⚑ Which of these can change data: UI, API, CLI, bulk import, migration, backfill, integration, admin SQL?
- ⚑ Is there one gate through which every business write passes, and is it enforced at the database or only in code?
- What do bulk loads and backfills do about validation and audit?
- Do external systems write to us, and with what authority?

## 10. Failure and operations
- What must be atomic: the business change, the audit record, the derived data?
- What happens on crash, cancellation, timeout or partial failure mid-operation?
- How do backup, restore and replication interact with integrity claims and audit history?
- How are schema changes and migrations made safe when history is immutable?
- What are the recovery targets, and does recovery preserve provenance?

## 11. Compliance, audit and privacy
- Which regulations apply, and what will an auditor ask to see?
- What must be logged, for how long, and who can read the logs?
- What personal data is held, and what are the residency, access and erasure obligations?
- Must results be explainable to the person affected?

## 12. Scale, tenancy and change
- Single tenant, or multi-tenant? What isolation is required between tenants?
- What are the realistic peak volumes and concurrency (not the ambitious ones)?
- ⚑ Which entities need stable identity across merges, renames and external systems?
- What is likely to change in two years: new jurisdictions, products, rule types, integrations?
- How are old results, snapshots and schemas read after the format changes?

## 13. Verification strategy
- What counts as proof that an invariant holds? Is the test oracle independent of the code under test?
- Are adversarial tests part of "done": direct writes, both orderings of concurrent operations, partial failures?
- What persisted state is inspected after success and after failure?
- Who reviews, and who can veto a release?

## 14. Staging and decision hygiene
- ⚑ How much rigor does the first release need? Name the level and what is explicitly deferred.
- What would trigger raising that level (new customer, audit, scale, regulation)?
- For every ⚑ answer, who decided, on what evidence, and when will it be revisited?

## How to use it

1. Answer in writing as a short decision record per item, with an owner and a revisit trigger.
2. Treat "we'll decide later" on a ⚑ item as a risk to schedule, not as an answer.
3. Re-run the ⚑ items whenever scope grows. Most of the painful rework in this project came from a handful of them: the threat model, the cut-off definition, the write-path inventory, the authority over derived data, and the coverage-and-ambiguity policy.

Do you want this as a document you can keep and fill in, ideally with a column for decision, owner and revisit trigger?