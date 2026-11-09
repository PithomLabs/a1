# How industry solves these problems: the architecture and product landscape

Yes. There are established industries, products, frameworks and libraries specifically designed to address many of the problems we encountered.

In fact, looking at them retrospectively reveals something important:

We were trying to solve several problems simultaneously that mature enterprise systems typically handle through separate, specialized capabilities.

Accounting period closure is one capability. Transaction integrity is another. An immutable financial ledger is another. Workflow orchestration, audit, historical reconstruction and data provenance are others.

These capabilities complement one another, but they are not interchangeable.

There is no single library that can turn an ordinary CRUD application into a trustworthy enterprise system. Mature systems assemble a collection of capabilities around their business requirements.

I would organize the landscape into eight categories.

## The enterprise systems toolbox

Business closure

SAP, Oracle, Odoo

Database integrity

PostgreSQL, constraints, transactions

Financial ledgers

Modern Treasury, ledger engines

Historical data

Event sourcing, temporal tables

Workflow execution

Temporal, River

Reliable integration

Transactional outbox, Debezium

Security and audit

RLS, grants, pgAudit

Developer tooling

pgx, sqlc

Let's examine what each actually does, where it helps, and what it cannot solve.

# 1. Business-level period closing: how established ERP systems avoid moving targets

Highest relevance to our current business decision

This is probably the most important industry practice to understand before we write more synchronization code.

Enterprise accounting systems have long recognized that allowing unlimited changes to historical periods creates reconciliation problems.

Instead, they introduce business controls such as:

- Open accounting periods.
- Close-pending periods.
- Closed periods.
- Restricted adjustment periods.
- Authorized exceptions and reopening.
- Reversing or adjusting entries.

These are actual product capabilities, not just architectural recommendations.

## Examples from established products

[How works the Close Periods app in SAP S/4HANA Clo... - SAP Community](https://images.openai.com/static-rsc-4/yjAqIB2-YXMBEtqDSJjDe7RGbtwxetQ-i7JLxH9FHB3W2fpcE-M50NWTEvIeCRFuNsHyCq346UwjxdHUJabc2Ze6oVTx24NgyZAIMLKF6TbW-CGtnFFw5pBfRBa89kQ2FDN_pAVTqh2nSsvcvgD2b9PYAq5198yXkdG-_8qBdXU?purpose=inline)

SAP S/4HANA

SAP uses posting-period variants to control which periods permit financial postings. It can differentiate restrictions by account type and provide special authorized intervals for closing adjustments.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://learning.sap.com\&sz=32)

learning.sap.com



[SAP posting-period documentation](https://learning.sap.com/courses/customizing-core-settings-in-financial-accounting-in-sap-s4hana/managing-posting-periods?utm_source=chatgpt.com)

[Oracle Project Portfolio Management Cloud R13 (updates 18A - 18C)](https://images.openai.com/static-rsc-4/I9IqHcqK_LLyILDZ88iQzW9yvD_aB8dAPvQ-uc1ZNpBD3DdmkwOpjRzGudItEdz1cfQhHlx4iab7dp35587Kwgiii6v44RsbQWLi0RXYeCRmK8s2D6LQ1Gaa5-zTP1eWEBNcUmm_huxXmjeUqJkFvhHNgwljcaT6awGkFBFBDCs?purpose=inline)

Oracle Fusion Cloud Financials

Oracle distinguishes `Close Pending` from `Closed`. Its documentation describes clearing unposted items before closing; a closed period prevents new journal entries and postings unless reopened. Close-pending supports a review process while still allowing certain entries.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.oracle.com\&sz=32)

docs.oracle.com+1



[Oracle close-pending and closed periods](https://docs.oracle.com/en/cloud/saas/financials/25c/faofc/what-s-the-difference-between-a-closed-and-close-pending.html?utm_source=chatgpt.com)

[fermeture de la comptabilite le nouvel assistant de verrouillage](https://images.openai.com/static-rsc-4/10RspwUxS_fB7owgxFS6cUC6LoFdr1vAXZygUFZKtVfUp68O6KhMR6tNI1aeoRjIELAkyE9zID_b_gW7wnld9oSHqJTUbnC0NjqLkeJmWEUEzd_JNEhUEUAKIp3QGvTuZgFBDhftY-Rr7f71vMSvbAGtLWRa4fQsiYhv0YgpHSc?purpose=inline)

Odoo

Odoo provides accounting lock dates that prevent modifications to posted entries or new postings before a specified date. Its documentation also describes temporary exceptions for administrators.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.odoo.com\&sz=32)

Odoo 19.0 documentation



[Odoo year-end closing and lock dates](https://www.odoo.com/documentation/19.0/applications/finance/accounting/reporting/year_end.html?utm_source=chatgpt.com)

## What should we learn from them?

The architectural lesson is that period closing is a first-class business operation.

It should not be implemented as simply setting:

```
pay_period.closed = true
```

A mature close process typically needs a sequence resembling:

\#chatgpt-mermaid-\_r_8m4\_{font-family:-apple-system-body,ui-sans-serif,-apple-system,system-ui,"Segoe UI","Helvetica","Apple Color Emoji","Arial",sans-serif,"Segoe UI Emoji","Segoe UI Symbol";font-size:16px;fill:rgb(237, 237, 237);}@keyframes edge-animation-frame{from{stroke-dashoffset:0;}}@keyframes dash{to{stroke-dashoffset:0;}}#chatgpt-mermaid-\_r_8m4\_ .edge-animation-slow{stroke-dasharray:9,5!important;stroke-dashoffset:900;animation:dash 50s linear infinite;stroke-linecap:round;}#chatgpt-mermaid-\_r_8m4\_ .edge-animation-fast{stroke-dasharray:9,5!important;stroke-dashoffset:900;animation:dash 20s linear infinite;stroke-linecap:round;}#chatgpt-mermaid-\_r_8m4\_ .error-icon{fill:rgb(48, 48, 48);}#chatgpt-mermaid-\_r_8m4\_ .error-text{fill:rgb(237, 237, 237);stroke:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ .edge-thickness-normal{stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .edge-thickness-thick{stroke-width:3.5px;}#chatgpt-mermaid-\_r_8m4\_ .edge-pattern-solid{stroke-dasharray:0;}#chatgpt-mermaid-\_r_8m4\_ .edge-thickness-invisible{stroke-width:0;fill:none;}#chatgpt-mermaid-\_r_8m4\_ .edge-pattern-dashed{stroke-dasharray:3;}#chatgpt-mermaid-\_r_8m4\_ .edge-pattern-dotted{stroke-dasharray:2;}#chatgpt-mermaid-\_r_8m4\_ .marker{fill:rgb(175, 175, 175);stroke:rgb(175, 175, 175);}#chatgpt-mermaid-\_r_8m4\_ .marker.cross{stroke:rgb(175, 175, 175);}#chatgpt-mermaid-\_r_8m4\_ svg{font-family:-apple-system-body,ui-sans-serif,-apple-system,system-ui,"Segoe UI","Helvetica","Apple Color Emoji","Arial",sans-serif,"Segoe UI Emoji","Segoe UI Symbol";font-size:16px;}#chatgpt-mermaid-\_r_8m4\_ p{margin:0;}#chatgpt-mermaid-\_r_8m4\_ .label{font-family:-apple-system-body,ui-sans-serif,-apple-system,system-ui,"Segoe UI","Helvetica","Apple Color Emoji","Arial",sans-serif,"Segoe UI Emoji","Segoe UI Symbol";color:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ .cluster-label text{fill:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ .cluster-label span{color:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ .cluster-label span p{background-color:transparent;}#chatgpt-mermaid-\_r_8m4\_ .label text,#chatgpt-mermaid-\_r_8m4\_ span{fill:rgb(237, 237, 237);color:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ .node rect,#chatgpt-mermaid-\_r_8m4\_ .node circle,#chatgpt-mermaid-\_r_8m4\_ .node ellipse,#chatgpt-mermaid-\_r_8m4\_ .node polygon,#chatgpt-mermaid-\_r_8m4\_ .node path{fill:rgb(36, 22, 63);stroke:rgb(120, 73, 209);stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .rough-node .label text,#chatgpt-mermaid-\_r_8m4\_ .node .label text,#chatgpt-mermaid-\_r_8m4\_ .image-shape .label,#chatgpt-mermaid-\_r_8m4\_ .icon-shape .label{text-anchor:middle;}#chatgpt-mermaid-\_r_8m4\_ .node .katex path{fill:#000;stroke:#000;stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .rough-node .label,#chatgpt-mermaid-\_r_8m4\_ .node .label,#chatgpt-mermaid-\_r_8m4\_ .image-shape .label,#chatgpt-mermaid-\_r_8m4\_ .icon-shape .label{text-align:center;}#chatgpt-mermaid-\_r_8m4\_ .node.clickable{cursor:pointer;}#chatgpt-mermaid-\_r_8m4\_ .root .anchor path{fill:rgb(175, 175, 175)!important;stroke-width:0;stroke:rgb(175, 175, 175);}#chatgpt-mermaid-\_r_8m4\_ .arrowheadPath{fill:rgb(175, 175, 175);}#chatgpt-mermaid-\_r_8m4\_ .edgePath .path{stroke:rgb(175, 175, 175);stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .flowchart-link{stroke:rgb(175, 175, 175);fill:none;}#chatgpt-mermaid-\_r_8m4\_ .edgeLabel{background-color:rgb(0, 0, 0);text-align:center;}#chatgpt-mermaid-\_r_8m4\_ .edgeLabel p{background-color:rgb(0, 0, 0);}#chatgpt-mermaid-\_r_8m4\_ .edgeLabel rect{opacity:0.5;background-color:rgb(0, 0, 0);fill:rgb(0, 0, 0);}#chatgpt-mermaid-\_r_8m4\_ .labelBkg{background-color:rgba(0, 0, 0, 0.5);}#chatgpt-mermaid-\_r_8m4\_ .cluster rect{fill:rgb(48, 48, 48);stroke:rgba(255, 255, 255, 0.15);stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .cluster text{fill:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ .cluster span{color:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ div.mermaidTooltip{position:absolute;text-align:center;max-width:200px;padding:2px;font-family:-apple-system-body,ui-sans-serif,-apple-system,system-ui,"Segoe UI","Helvetica","Apple Color Emoji","Arial",sans-serif,"Segoe UI Emoji","Segoe UI Symbol";font-size:12px;background:rgb(48, 48, 48);border:1px solid rgba(255, 255, 255, 0.15);border-radius:2px;pointer-events:none;z-index:100;}#chatgpt-mermaid-\_r_8m4\_ .flowchartTitleText{text-anchor:middle;font-size:18px;fill:rgb(237, 237, 237);}#chatgpt-mermaid-\_r_8m4\_ rect.text{fill:none;stroke-width:0;}#chatgpt-mermaid-\_r_8m4\_ .icon-shape,#chatgpt-mermaid-\_r_8m4\_ .image-shape{background-color:rgb(0, 0, 0);text-align:center;}#chatgpt-mermaid-\_r_8m4\_ .icon-shape p,#chatgpt-mermaid-\_r_8m4\_ .image-shape p{background-color:rgb(0, 0, 0);padding:2px;}#chatgpt-mermaid-\_r_8m4\_ .icon-shape .label rect,#chatgpt-mermaid-\_r_8m4\_ .image-shape .label rect{opacity:0.5;background-color:rgb(0, 0, 0);fill:rgb(0, 0, 0);}#chatgpt-mermaid-\_r_8m4\_ .label-icon{display:inline-block;height:1em;overflow:visible;vertical-align:-0.125em;}#chatgpt-mermaid-\_r_8m4\_ .node .label-icon path{fill:currentColor;stroke:revert;stroke-width:revert;}#chatgpt-mermaid-\_r_8m4\_ .node .neo-node{stroke:rgb(120, 73, 209);}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node rect,#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].cluster rect,#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node polygon{stroke:url(#chatgpt-mermaid-\_r_8m4\_-gradient);filter:drop-shadow( 1px 2px 2px rgba(185,185,185,1));}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].swimlane.cluster rect{filter:none;}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node path{stroke:url(#chatgpt-mermaid-\_r_8m4\_-gradient);stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node .outer-path{filter:drop-shadow( 1px 2px 2px rgba(185,185,185,1));}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node .neo-line path{stroke:rgb(120, 73, 209);filter:none;}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node circle{stroke:url(#chatgpt-mermaid-\_r_8m4\_-gradient);filter:drop-shadow( 1px 2px 2px rgba(185,185,185,1));}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].node circle .state-start{fill:#000000;}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].icon-shape .icon{fill:url(#chatgpt-mermaid-\_r_8m4\_-gradient);filter:drop-shadow( 1px 2px 2px rgba(185,185,185,1));}#chatgpt-mermaid-\_r_8m4\_ [data-look="neo"].icon-shape .icon-neo path{stroke:url(#chatgpt-mermaid-\_r_8m4\_-gradient);filter:drop-shadow( 1px 2px 2px rgba(185,185,185,1));}#chatgpt-mermaid-\_r_8m4\_ .node text{font-size:14px;font-weight:600;letter-spacing:normal;fill:rgb(206, 176, 251);}#chatgpt-mermaid-\_r_8m4\_ .edgeLabels text{font-size:13px;font-weight:600;letter-spacing:-0.08px;fill:rgb(206, 176, 251);}#chatgpt-mermaid-\_r_8m4\_ .node tspan[font-weight="normal"],#chatgpt-mermaid-\_r_8m4\_ .edgeLabels tspan[font-weight="normal"]{font-weight:600;}#chatgpt-mermaid-\_r_8m4\_ .edgeLabel .label rect{opacity:1;rx:13px;ry:13px;fill:rgb(23, 12, 38);stroke:rgb(65, 47, 92);stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .node rect,#chatgpt-mermaid-\_r_8m4\_ .node circle,#chatgpt-mermaid-\_r_8m4\_ .node ellipse,#chatgpt-mermaid-\_r_8m4\_ .node polygon,#chatgpt-mermaid-\_r_8m4\_ .node path{fill:rgb(44, 24, 74);stroke:rgba(255, 255, 255, 0.1);stroke-width:1px;}#chatgpt-mermaid-\_r_8m4\_ .node rect{rx:16px;ry:16px;}#chatgpt-mermaid-\_r_8m4\_ .mermaid-decision .label-container{fill:rgb(23, 12, 38);stroke:rgb(65, 47, 92);stroke-dasharray:2px,2px;}#chatgpt-mermaid-\_r_8m4\_ .edgePaths .flowchart-link{stroke:rgb(175, 175, 175);stroke-width:1px;stroke-linecap:round;stroke-linejoin:round;}#chatgpt-mermaid-\_r_8m4\_ .marker{fill:rgb(175, 175, 175);stroke:rgb(175, 175, 175);}#chatgpt-mermaid-\_r_8m4\_ :root{--mermaid-font-family:-apple-system-body,ui-sans-serif,-apple-system,system-ui,"Segoe UI","Helvetica","Apple Color Emoji","Arial",sans-serif,"Segoe UI Emoji","Segoe UI Symbol";}Open periodClose pendingCheck outstanding inputsAll required items reconciled?Resolve exceptionsClosedPost-close adjustmentAuthorized reopeningNoYes

This separates three concepts:

1. The business has announced a cutoff.
2. The system has verified that the closing conditions are satisfied.
3. The system prevents ordinary postings into the closed period.

What this does not solve: the database must still protect the transition from open to closing. A transaction that began posting before closure must not slip through and commit after the system has declared the period closed.

So period locking is a business-level solution backed by database concurrency control, not a replacement for it.

For WorkforceOps, this is the first established pattern I would borrow.

# 2. Financial ledger engines: a specialized product for money integrity

Very relevant to financial and accounting domains

There is an entire category of software dedicated to preserving the integrity of financial transactions.

A ledger engine is not simply an accounting table with debit and credit columns.

It provides a constrained environment for recording value movements and maintaining financial invariants.

## Example: Modern Treasury Ledgers

[July/August Changelog](https://images.openai.com/static-rsc-4/KT5RFR2m_fbLpwBwTWmPJ0axN0JgjVvHUE-t8kuXk6MNm4yKGmsWS5DSMcBWjwLgFYZoJziC5i3sPNKleScPb6JRdH-NZDxqL2v_Xjh1nR_5OkxauQK_SMnaJi0Gab8od7fMc-ZQPrwc9zdG9am1zbng_vKzNNV-oKqFhfkhaak?purpose=inline)

[moderntreasury.com](https://www.moderntreasury.com/journal/july-august-changelog-2024?utm_source=chatgpt.com)

Modern Treasury documents several explicit guarantees:

- Debits equal credits.
- Entries are immutable after posting.
- Ledger transactions cannot simply be deleted.
- Historical account versions can be queried.
- Idempotency keys help prevent duplicate operations.
- A transaction's entries succeed or fail atomically.
- Conditional balance operations can use locking.&#x20;

  [image](https://www.google.com/s2/favicons?domain=https://docs.moderntreasury.com\&sz=32)

  docs.moderntreasury.com+2



This is particularly interesting because it demonstrates that financial integrity is often built into a dedicated domain product rather than reimplemented independently in every application.

### How it compares with our problem

| Our challenge                | Financial ledger equivalent |
| ---------------------------- | --------------------------- |
| Snapshot provenance          | Auditable ledger versions   |
| Financial immutability       | Immutable posted entries    |
| Corrections                  | Reversing transactions      |
| Duplicate submissions        | Idempotency keys            |
| Atomic calculation artifacts | Atomic ledger transaction   |
| Concurrent balance changes   | Conditional balance locking |
| Historical reconstruction    | Versioned account state     |

A useful design pattern is:

```
Pending transaction
       |
       v
Validated transaction
       |
       v
Posted transaction
       |
       v
Immutable financial record
```

When a posted transaction must be reversed, the system creates another transaction rather than deleting the historical posting.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.moderntreasury.com\&sz=32)

docs.moderntreasury.com



### Should we adopt one?

Not necessarily.

For WorkforceOps, I would distinguish:

- Payroll calculation and employee entitlement.
- Payroll approval and finalization.
- Accounting journal generation.
- Actual payment settlement.

They are related, but they are not the same domain.

We should model and preserve the payroll calculation result ourselves. A specialized ledger becomes attractive when we need a true double-entry accounting system, more sophisticated account-balance concurrency, payment integration and financial reconciliation.

Modern Treasury is an example of a product worth evaluating, not an automatic recommendation for this MVP.

# 3. Event sourcing: a different way to preserve business history

Useful selectively, not necessarily for the entire application

This is probably the most misunderstood architectural alternative.

Traditional CRUD stores the current state:

```
Employee salary = S$4,000
```

When the salary changes, the existing value is overwritten:

```
Employee salary = S$4,500
```

Unless you separately preserve history, the earlier value and the reason for the change can become difficult to reconstruct.

Event sourcing takes a fundamentally different approach:

```
EmploymentCreated
CompensationEstablished(S$4,000)
CompensationChanged(S$4,500)
CompensationCorrectionRecorded(...)
```

The current state is derived from the sequence of recorded events.

[Cloud-Native Architecture Patterns Design & Use Cases](https://images.openai.com/static-rsc-4/wEoCiiEdBlo3tGvRBs3xgcOJ2rqtlEtirj0cBqW4EU4V8nTbOjx5RnPQC-VYs0mDIAU0NqMX2aokBAfoqjnOx7oX1RoFOYPGYDr7arJt5H-89SkkHO7dt0-g_Bt3V4MadO1VOwVN157J-_6seWgkFc0ECtr1m19qlpBuVj2dt_A?purpose=inline)

[code-b.dev](https://code-b.dev/blog/best-cloud-native-architecture-patterns?utm_source=chatgpt.com)

This approach has several important benefits:

- Historical changes are preserved.
- Business activity can be reconstructed.
- Corrections are recorded as new events.
- The state can be projected into different read models.
- Optimistic concurrency can protect against competing changes.

The official EventSourcingDB documentation describes event sourcing as storing changes in an append-only event log and reconstructing current state by replaying events.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.eventsourcingdb.io\&sz=32)

EventSourcingDB



## Products and frameworks

[EventSourcingDB 1.2.0 Elevates Event-Driven Architecture with Built-in Security and Operational Smarts | LavX News | LavX News](https://images.openai.com/static-rsc-4/BT55_Lg0SNLi_w9c2EkQWgAaaYuUMOH0nQu_pQS01CgrHavmxjaYW8rG8ZPUWPFQ6OJaS_7BbeODE83SkegoIK-kUb9PQFt1BsQegmfKfmX2UVutzXVF2wl0_h0KYZJrfY8Ome11wCSydqT1WJTTAtGO2buSOoz1k7F-7i9TWyQ?purpose=inline)

EventSourcingDB

A purpose-built event store with append-oriented event handling, consistency controls, preconditions, event schemas, snapshots and integrity-related capabilities.

Its preconditions can enforce expected stream state and reject concurrent writes when the expected version changes. Its documented multi-event writes are atomic.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.eventsourcingdb.io\&sz=32)

EventSourcingDB+1



[Event sourcing introduction](https://docs.eventsourcingdb.io/about-eventsourcingdb/introduction-to-event-sourcing/?utm_source=chatgpt.com)

· [Preconditions](https://docs.eventsourcingdb.io/fundamentals/preconditions/?utm_source=chatgpt.com)

[Axon Framework で簡単にEventSourcing+CQRSなアプリケーションを作る - エムスリーテックブログ](https://images.openai.com/static-rsc-4/r_RiGoFiXcFwa3cFxmI-L98dlUt1Tmu9aB-WidAaydVV2zzmxlxuc7vHHvTDADVZgmQ2kmvcte6ipY_ri7kWlOy8CaRrQoW-YqhIV9sXsEdwb6tMsLw_hk9nYCGcwG_CRlMLSvgqrUlbVaPLr3R8O3yzte9m9vF8vzd8-VjFY6I?purpose=inline)

Axon Framework

Provides domain-driven design, CQRS, aggregate-oriented event sourcing and event processing capabilities. Its repository and event processor models help structure systems around business entities and commands.

[Axon Framework documentation](https://docs.axoniq.io/axon-framework-reference/?utm_source=chatgpt.com)

## What event sourcing does not solve automatically

This is crucial.

Event sourcing does not automatically guarantee:

- That the original event was legitimate.
- That the actor was authorized.
- That an event was not a duplicate.
- That external systems received the event exactly once.
- That a business rule was legally correct.
- That multiple independent databases committed atomically.

EventSourcingDB explicitly documents that preconditions are needed to prevent duplicate writes, and external transactions require additional patterns such as an outbox.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.eventsourcingdb.io\&sz=32)

EventSourcingDB+1



Also, event sourcing introduces its own complexity:

- Event schema evolution.
- Replay performance.
- Projection rebuilding.
- Historical event interpretation.
- Handling corrected or obsolete events.
- Migration of old event formats.

My recommendation: use event-sourcing principles where they provide real business value, but do not turn the entire WorkforceOps application into an event-sourced system.

For example, compensation changes, SHG corrections, employee status changes and payroll adjustment decisions deserve strong historical lineage. An employee's temporary display preference probably does not.

A useful compromise is a relational operational model with append-only business-event or correction-history tables for the important facts.

# 4. Temporal databases: automatic historical versioning

Very useful for effective-dated and audit-sensitive domains

There is another category specifically designed to answer:

> What did this record look like at a particular point in the past?

This is called temporal data management.

For example, an employee's compensation may have changed several times:

| Salary  | Effective from | Effective until |
| ------- | -------------- | --------------- |
| S$3,500 | January 1      | March 31        |
| S$3,800 | April 1        | June 30         |
| S$4,000 | July 1         | Present         |

But there is a second dimension:

When did the organization actually learn about each salary?

Suppose the S$3,800 compensation was entered on April 10 but was effective from April 1.

The business needs to preserve both:

- Valid time: when the fact applies in the real business world.
- Transaction/system time: when the system recorded that fact.

This is often called bitemporal modelling.

[Time travel: two-dimensional time with bitemporal data](https://images.openai.com/static-rsc-4/EQjmaoRZXhrkeTb1J8BDzDDw8ELkY-B8SmBGG6np0W9bAJo9JWZvBGG0oO6WsP3u_BYv1oKYoW8ae3ezStZ4V6kbFWmu1NEcxPShXIK_Qmu6yZEOJ1GYt3E1YS01TRIXOLWeJY3rhT5_McRIpRQqonGVzIlJ1FKCQiFTkNr7Fk4?purpose=inline)

[aiven.io](https://aiven.io/blog/two-dimensional-time-with-bitemporal-data?utm_source=chatgpt.com)

## Available technology

[Getting Started with Temporal Table in SQL Server 2016 – Part 1 | Database Journal](https://images.openai.com/static-rsc-4/56ZqM05TSlQ0SCt53iHQ94AUJy0s8wjEJwnSQSZ42ZNfZGcyvq6KeD4p4Jazj3dLeb1uvwpEKbjRB_4lSCZF69JB_eutri-DXQnVgeRqADU462szEz3kmY3uVfOEZMsIc7Om-ApnKwpysnRFFadu5JyKh-NvnhmPDWjLjQ9d1dk?purpose=inline)

Microsoft SQL Server Temporal Tables

SQL Server has system-versioned temporal tables that automatically retain previous row versions in associated history tables and allow historical queries.

[Microsoft temporal tables documentation](https://learn.microsoft.com/en-us/sql/relational-databases/tables/temporal/overview?view=sql-server-ver17\&utm_source=chatgpt.com)

[image](https://www.google.com/s2/favicons?domain=https://learn.microsoft.com\&sz=32)

Microsoft Learn+1

A distinction worth understanding:

System-versioned temporal tables automatically track system history. They do not automatically define your business effective dates, legal applicability, approval process or correction semantics.

Those still require domain modelling.

PostgreSQL has range types and exclusion constraints that help enforce non-overlapping effective periods. For example, an exclusion constraint can prevent two reservation periods from overlapping.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

postgresql.org



For our current stack, I would keep explicit effective-dated records and correction lineage in PostgreSQL rather than introduce a separate temporal database.

But I would study bitemporal patterns carefully before adding more HR, compensation or regulatory history.

# 5. Workflow engines: handling long-running business processes

Strong fit for enterprise process orchestration

A workflow engine answers a different question:

> How does a business operation safely progress when it takes minutes, days or weeks, involves multiple people and systems, and may fail along the way?

Think about payroll:

1. Generate a draft run.
2. Verify employee eligibility.
3. Check attendance exceptions.
4. Request manager approvals.
5. Resolve missing compensation information.
6. Run payroll.
7. Obtain financial approval.
8. Submit payment instructions.
9. Reconcile settlement.

This is not one database transaction. It is a long-running business process.

Holding one PostgreSQL transaction open while waiting for all these steps would be a bad design.

A workflow engine persists progress and resumes the process after failures or restarts.

## Example: Temporal

[Workflow visualization with Temporal’s Timeline View | Temporal](https://images.openai.com/static-rsc-4/YkNv6y9g2lMsU4sJzlq_Z071ScnzegRKFvkQOhIeaW207MQ8lvXHMOcqGHNpZ5ytsP1O0uKT7SYK_XU_KcPjKMvagUmk-4CtLblqS6tv4n_x9irqhbE-lrjJ7kn0vlIc_DrWwrmc99PCVSZQroolVFOONjlli0MSE_LpveNGEu4?purpose=inline)

[temporal.io](https://temporal.io/blog/lets-visualize-a-workflow?utm_source=chatgpt.com)

Temporal provides:

- Durable workflow execution.
- Activities and workers.
- Timers and scheduling.
- Workflow cancellation.
- Message passing.
- Workflow versioning.
- Retry policies.
- Execution history.

Its Go SDK is available through `go.temporal.io/sdk`.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.temporal.io\&sz=32)

Temporal Platform Documentation+1



The distinction between workflows and activities matters. Temporal automatically retries activities according to retry policies, but the activity's external side effect must be designed to tolerate re-execution. A workflow retry is not the same thing as an exactly-once business transaction.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://docs.temporal.io\&sz=32)

Temporal Documentation



## A lighter alternative: River

[River - Fast, reliable background jobs in Go](https://images.openai.com/static-rsc-4/OYpAQ7NZ1krFCgXMdFl1G4b7rvW04F3m0JFkwMS2cEfXfB6bJXs_eWbpVovbq8q7F9wUes4dBeS7FGQSxaVPqOM7pxbnRv3k8IYtVaLf59ixyUqBQ4UFMWp0-JzGVL19iGv2F65s0Oo7v3jOG5-0wMizvtEXbOEPjPE4QyTNuEk?purpose=inline)

River is a Go job queue backed by PostgreSQL or SQLite. It provides scheduled jobs, uniqueness and automatic retries. Jobs can be enqueued transactionally with application data, so the job becomes available only if the database transaction commits.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://github.com\&sz=32)

GitHub



[River documentation](https://github.com/riverqueue/river/blob/master/docs/README.md?utm_source=chatgpt.com)

### Temporal versus River

| Requirement                                          | Better fit |
| ---------------------------------------------------- | ---------- |
| Send an email or generate a report in the background | River      |
| Retry a failed synchronization job                   | River      |
| Schedule recurring operational work                  | River      |
| Multi-day approval workflow                          | Temporal   |
| Wait for several external organizations              | Temporal   |
| Human approval, timers and compensating actions      | Temporal   |
| Durable execution across process restarts            | Temporal   |

My recommendation for WorkforceOps: do not add Temporal merely to manage the payroll transaction. A payroll capture and validation operation must remain an ordinary, carefully controlled database transaction.

Temporal becomes interesting when we build a full payroll processing and approval lifecycle involving external services and human decisions.

River is a more natural initial addition for straightforward background tasks.

# 6. Reliable integration: the transactional outbox pattern

Important when connecting independent systems

This is one of the most widely useful patterns in distributed business applications.

Imagine WorkforceOps approves a timesheet and needs to notify another payroll application.

A naïve implementation does this:

1. Commit the timesheet approval in PostgreSQL.
2. Publish a message to Kafka or call another API.

What happens when step one succeeds but step two fails?

The timesheet is approved, but the receiving system never hears about it.

Reverse the order and you get the opposite problem: an external system receives a notification for an approval that later rolls back.

The transactional outbox addresses this.

[Outbox Pattern for Reliable Event Publishing | Conduktor](https://images.openai.com/static-rsc-4/W5LVi_TQcPZQZ4LBWH_Pkz8ECnpW8pAsw3nbZjWkvK9bHc0D-XmfooOZovJzq4Z3uUOEpeWQlrT2vVdeBOCkRWTUAkKFu38pBHvCDAnfWfP8aI4NN8XSHYvfLh1dJ61nhL_S-DEEKr9kCNdKknmDbZ2xF1IeBcIMWrB2tMOuqC8?purpose=inline)

[conduktor.io](https://www.conduktor.io/glossary/outbox-pattern-for-reliable-event-publishing?utm_source=chatgpt.com)

The business change and an event record are committed in the same PostgreSQL transaction:

```
BEGIN

Approve timesheet
Insert outbox event

COMMIT
```

A separate component reads the outbox and delivers the event.

## Example: Debezium

Debezium provides an Outbox Event Router that reads committed outbox records and routes them to message topics. The pattern prevents the application database update and event publication from becoming two unrelated writes.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://debezium.io\&sz=32)

debezium.io+1



[Debezium Outbox Event Router](https://debezium.io/documentation/reference/transformations/outbox-event-router.html?utm_source=chatgpt.com)

What it solves:

- Reliable transfer of committed business events.
- Reduced risk of lost notifications.
- Decoupling of the main application from message delivery.
- A foundation for retries and downstream reconciliation.

What it does not solve:

- Whether the original business event was legitimate.
- Whether the source transaction obeyed its invariants.
- Whether a consumer processes a duplicated event safely.
- Whether several external systems commit atomically.

You still need idempotent consumers and reconciliation.

My recommendation: for the current single-database modular monolith, use the transactional outbox pattern when reliable cross-system delivery becomes necessary. You do not need to introduce Kafka and Debezium immediately. A PostgreSQL outbox table and a simple Go worker may be sufficient initially.

# 7. Database security and audit: what PostgreSQL already provides

Directly applicable to our existing architecture

Before buying another security product, it is important to understand that PostgreSQL itself already has powerful mechanisms.

## A. Grants, constraints and triggers

These are your foundational enforcement mechanisms.

- `NOT NULL`, `CHECK`, `UNIQUE` and foreign-key constraints.
- Exclusion constraints for non-overlapping ranges.
- Trigger-enforced rules.
- Transaction boundaries.
- Restricted function execution.
- Database role separation.

These are often more valuable than a large application-level validation framework because they protect the stored state even when several different applications use the same database.

## B. Row-Level Security (RLS)

PostgreSQL RLS allows policies to restrict which rows a database role can read or modify.

For example:

```
app can read employer A
app cannot read employer B
```

The database evaluates the policy during ordinary queries and DML.

However, RLS has important boundaries:

- Table owners normally bypass RLS unless forced to obey it.
- Superusers and roles with `BYPASSRLS` always bypass it.
- `TRUNCATE` is not governed by RLS.&#x20;

  [image](https://www.google.com/s2/favicons?domain=https://www.postgresql.org\&sz=32)

  postgresql.org+1



Documentation: [PostgreSQL Row Security Policies](https://www.postgresql.org/docs/17/ddl-rowsecurity.html?utm_source=chatgpt.com).

For WorkforceOps, RLS may eventually help if multiple independent employers use the same database. It is not a replacement for our existing `app`/`migration_admin` privilege model or the payroll gate.

## C. pgAudit

[How to Install and Configure PGAudit on PostgreSQL 17: Complete Guide to Database Auditing and Security | by Matheus dos Santos | Medium](https://images.openai.com/static-rsc-4/LtBWJA90L_dBu7Hj8NUTEA65fipf_k5YYYq4m7OFFbLGYg1czRCyHxddgIYZ5HhMzL1yrJLL5_GmmEPqAnZMV-ZTHGbhgmqmY14ER9147zpCo3OylvwUSpAbA45NLU8ntftxSK3rYruviVM7wh6Xn7-hPXeVSIh8h74GZ447qlo?purpose=inline)

pgAudit

Provides detailed session and object audit logging through PostgreSQL's standard logging facility. It can help capture database activity for security investigations and compliance evidence.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://www.pgaudit.org\&sz=32)

PGAudit+1



[pgAudit project](https://www.pgaudit.org/?utm_source=chatgpt.com)

This distinction is critical:

| Audit type       | What it records                                                                    |
| ---------------- | ---------------------------------------------------------------------------------- |
| Application logs | Operational messages and errors                                                    |
| Database audit   | SQL operations and database activity                                               |
| Business audit   | Who performed which business action, why, against which records, with what outcome |
| Evidence record  | The original source facts, versions and proof supporting a decision                |

pgAudit helps with database activity. It does not independently establish that a payroll approval was legitimate or that the correct statutory rule was applied.

I would retain explicit business audit records and consider pgAudit as complementary database-level monitoring.

# 8. Go database tooling: what libraries can and cannot save us from

Relevant to our existing Go architecture

This is where many development teams expect the ORM or framework to solve more than it actually can.

Let's look at the principal choices.

[GitHub - jackc/pgx: PostgreSQL driver and toolkit for Go · GitHub](https://images.openai.com/static-rsc-4/tv6bVABs1c5D8pDFb_32KrjwgKSAGOojbp-w-4LjLgIdmnLRjccD9ayEVPFfC7pjiVN1VPUZcoEb6CmZsbsVUufh680tNlv6mv6wDGMmhfjkc9Hpl3_nJh2HVs1nWsfKA63pN9r0qdYrRtPxUq4r8ts-ed0RIWlX_rUwxq4ejf0?purpose=inline)

pgx

The native PostgreSQL driver and toolkit. It supports PostgreSQL-specific functionality, connection management, transactions and type handling.

[pgx documentation](https://github.com/jackc/pgx?utm_source=chatgpt.com)

[Beyond the ORM: Generating Type-Safe Go Code Directly from SQL with sqlc. ✨ | by Puneet | Jan, 2026 | Medium](https://images.openai.com/static-rsc-4/2jTjqAwvB89NK_Ltdahh1W0ukQb0cSlWUVZT1Nx1FJP70id4vN4r_Qso8OCuVsgA4aPfkymd6gy27JTWzbi6CHrQQuioxoLOAJL_bRoY7RlZwwkmJciOCf7VfJQJ47RBKuDjUniJYRhCEZAe7FNTv4SE7DpgDvHiUhJUlK6dKjI?purpose=inline)

sqlc

Generates type-safe Go code from explicit SQL queries. It reduces repetitive scanning and parameter mapping while keeping SQL visible and reviewable.

[sqlc documentation](https://docs.sqlc.dev/?utm_source=chatgpt.com)

[Comparing the best Go ORMs (2026) – Encore](https://images.openai.com/static-rsc-4/KCILyXD2FnTdXb7-MwhVeh-WDuI9fmBauynLo3xFCs99TeVtAjzl0ZXLkkwa_m4eQi-fIJoQJqivSUc4iW9wl20U0eLHarSKx9HFWQmkO6iuyWLtHXE4QGfmPzAL0KbFKRmyUnlSkzHU9Lei8Gp9HduYYNEX6KV-Mh905xTkHYI?purpose=inline)

Ent

A schema-oriented Go framework that generates typed entity APIs and supports declarative schema management.

[Ent documentation](https://entgo.io/?utm_source=chatgpt.com)

These products solve developer-experience problems:

- Less repetitive SQL scanning.
- Better type checking.
- More structured schema definitions.
- Easier query maintenance.
- Consistent database access.

They do not decide:

- What a valid business state is.
- Whether a period is closed.
- Which input version a payroll calculation must use.
- Whether a caller is allowed to validate payroll.
- Whether two concurrent operations can create an invalid financial state.

An ORM can generate code that violates a business invariant just as easily as manually written SQL.

For your current architecture, I would keep `pgx` and explicit SQL, and selectively introduce `sqlc` where it reduces repetitive data-access code without hiding important transaction boundaries.

Do not change the ORM or database driver in an attempt to solve a business-consistency problem.

# 9. Putting everything together: how mature enterprise systems are actually structured

The most important realization is that these products generally operate at different architectural layers.

A typical enterprise system might look like this:

Business process and policy

Period closing, approvals, adjustment policies, authorization, exception handling.

Examples: SAP, Oracle, Odoo

Workflow orchestration

Long-running processes, timers, human approval, retries, external coordination.

Examples: Temporal, River

Domain application and rules

HR, WFM, payroll, accounting, eligibility, compensation and calculations.

Go application, domain services and versioned rule packages

Data integrity and authoritative records

Transactions, constraints, locking, versioning, restricted writers, snapshots and financial ledgers.

Examples: PostgreSQL, specialized ledger engines, event stores

External integration and evidence

Outbox, change data capture, audit, tracing, reconciliation and monitoring.

Examples: Debezium, pgAudit, OpenTelemetry

The architecture is not necessarily composed of separate vendors. A well-designed modular monolith can provide several of these capabilities itself.

The crucial requirement is that each capability has a clearly defined responsibility.

## 10. What I would actually adopt for WorkforceOps

Knowing the system we are building, I would resist the temptation to adopt every product mentioned above.

I would use the following stack as the target architecture.

| Capability              | Recommendation                                                 | Decision                                 |
| ----------------------- | -------------------------------------------------------------- | ---------------------------------------- |
| Business-period closure | Explicit period lifecycle and posting controls                 | Design now                               |
| Database integrity      | PostgreSQL constraints, triggers, functions and transactions   | Retain                                   |
| Synchronization         | Gate 2 Option B                                                | Retain, pending Gate 2.1 acceptance      |
| Go database access      | pgx + explicit SQL                                             | Retain                                   |
| Typed SQL access        | sqlc                                                           | Selectively adopt                        |
| Historical corrections  | Versioned and append-only correction lineage                   | Adopt where consequential                |
| Event sourcing          | Selective application only                                     | Defer                                    |
| Temporal history        | Explicit effective and recorded dates                          | Retain and strengthen                    |
| Financial ledger        | Separate accounting ledger model                               | Design when accounting scope requires it |
| Workflow orchestration  | River initially; Temporal for genuinely long-running workflows | Defer adoption                           |
| Integration             | PostgreSQL transactional outbox                                | Adopt when required                      |
| Audit                   | Business audit records plus pgAudit where justified            | Design now                               |
| Row-level security      | Evaluate for multi-employer data isolation                     | Defer                                    |
| Distributed messaging   | Kafka/Debezium                                                 | Defer until justified                    |
| Observability           | OpenTelemetry                                                  | Gradual adoption                         |

This is intentionally conservative.

Our goal is not to reproduce the entire SAP or Oracle architecture. It is to choose the smallest combination of established patterns that gives us the required guarantees.

## 11. The particularly valuable lesson from ERP products

I want to emphasize one distinction.

ERP systems don't make every record immutable and don't freeze the entire database at month-end.

They distinguish between:

- Open operational activity.
- Pending financial or period closure.
- Approved and posted transactions.
- Restricted late adjustments.
- Historical records that cannot be silently rewritten.

That allows ordinary business operations to continue while protecting the financial consequences of earlier decisions.

SAP, for example, permits special posting intervals for authorized closing adjustments. Oracle distinguishes close-pending and closed periods, and explicitly protects reconciliation against further postings.&#x20;

[image](https://www.google.com/s2/favicons?domain=https://learning.sap.com\&sz=32)

learning.sap.com+1



This is a better conceptual model for WorkforceOps than treating all payroll input tables as one permanently frozen dataset.

The system should have a clear distinction between operational truth, approved business truth and finalized financial truth.

However, don't overgeneralize accounting workflows into payroll legislation. Which late adjustments are permitted, where they are attributed and how they affect contributions or statutory reporting must be researched and validated under the applicable jurisdiction.

# 12. The product adoption decision: what is worth studying first?

Here is my prioritized reading and experimentation list.

First: Study real ERP closing processes

Highest priority

Compare the period-close lifecycle in SAP, Oracle and Odoo.

Learn how they distinguish close-pending, closed, adjustments, restricted reopening and authorization.

This addresses our unresolved business cutoff decision.

Second: Study ledger architecture

Study Modern Treasury's guarantees, transaction statuses, immutable postings, reversals and concurrency model.

This addresses financial integrity and historical correction design.

Third: Study event sourcing and temporal modelling

Learn where immutable events, versioned corrections, effective-time records and system-time histories belong.

This addresses history, provenance and reconstruction.

Fourth: Study Temporal and transactional outbox

Learn how long-running processes, external integration, retries, cancellation and human approvals are handled.

This addresses distributed process reliability.

Fifth: Strengthen PostgreSQL capabilities

Study constraints, row-level security, privileges, transaction semantics, temporal range handling and audit.

This addresses enforcement at the authoritative data boundary.

# 13. The big conclusion

There is a reason mature enterprise systems have accumulated so many specialized capabilities.

They separate business closure, transaction integrity, financial immutability, historical reconstruction, workflow progression and external evidence rather than forcing one mechanism to solve every problem.

That is the architectural lesson I would carry forward.

For WorkforceOps, I would make three strategic decisions:

1. Borrow the ERP period-closing model for payroll business semantics.
2. Borrow the ledger model for finalized financial records and corrections.
3. Borrow database-enforced invariants for the actual trust boundary.

Then add workflow orchestration and event-driven integration only when the business process requires them.

One important warning: adopting a product does not transfer all responsibility for correctness to that product. Modern Treasury cannot decide whether an employee was eligible for overtime. Temporal cannot make a payroll calculation legally correct. EventSourcingDB cannot decide which compensation rule applied. pgAudit cannot prove an approval was authorized.

Those remain our domain responsibilities.

The best architecture is not the one with the most enterprise products. It is the one in which each difficult responsibility has a well-understood owner, and no two components silently disagree about who owns the truth.