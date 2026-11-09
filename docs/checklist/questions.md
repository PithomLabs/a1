## claude

How the industry usually handles it

The common approach is business-level, not technical. Accounting systems close a period: once a period is closed, postings into it are rejected, and corrections are made as new adjustment entries in a later period. Payroll systems typically use a cut-off date, and late changes flow into the next run as retro adjustments. I’m describing the general practice, not any specific product.

That is the process-level version of your gate and your append-only option. It works because the business accepts “late changes go in the next run” instead of requiring that no late change can ever be missed.

## A ladder of rigor
1. Ordinary CRUD. Default isolation and nothing special.
2. Consistent report. A repeatable-read snapshot where “as of when I started” is acceptable.
3. Cut-off completeness. A close-the-window state, or a gate lock plus read-committed reads.
4. Evidence grade. Add append-only storage, hashes and restricted writers.
5. Adversarial writers. The database enforces who can write and what, even against direct SQL.

Most products stop at rung 2 or 3. Your prompt17 declares rung 5 for payroll. The real design question is which rung your first release needs, which is the same question I raised at the end of the last two answers.

When you meet a “snapshot” feature in any project, ask three things: 

1. what is the cut-off
2. who can write
3. who relies on the output


## Z

