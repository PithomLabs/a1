# SPEC.pseudo.md — <RULE-ID> (skeleton)

Applicability givens first, then definitions, assumptions, claims,
derivations, conditionals; document-level `?`s last. Exact upstream
syntax only. No status words in this file.

```text
# source: <authority> <document>, <scope> — <retrieved date>
given scope_jurisdiction: jurisdiction is SG  # <source note>
given scope_category: <who this rule covers>  # <source note>
define target := <the quantity or outcome defined>
assume effective: <effective-date or scenario stipulation>
claim result: <the proposition under examination>
derive main: <explicit result> from [scope_jurisdiction, target] | <reasoning>
if <condition using declared names>:
    then <consequence>
    else <explicit alternative outcome>
? <what is missing and what would resolve it>
```
