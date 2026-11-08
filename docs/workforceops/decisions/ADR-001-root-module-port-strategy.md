# ADR-001: New root module with selective port (bchat read-only)

**Status:** accepted · **Date:** 2026-11-08

## Context

prompt.md asked to transform bchat in place while keeping its module path;
the director authorized the new module `github.com/PithomLabs/a1`.

## Decision

Build at the workspace root as `github.com/PithomLabs/a1`. `bchat/` stays a
read-only reference; only verified patterns are reimplemented (audit port
list P1–P9). No code is moved, no history rewritten, no shared database.

## Consequences

Removal of RAG/chat/frontend happens by never porting it — safer than
in-place deletion. Cost: bootstrap patterns are rewritten rather than moved
(small, and keeps boundaries clean).
