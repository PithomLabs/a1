# ADR-006: Go 1.25 module directive (deliberate)

**Status:** accepted · **Date:** 2026-11-08

## Context

Installed toolchain is go1.25.7; bchat declares `go 1.26`; latest Echo/Goose/
sqlc already require 1.26. The review demanded a deliberate choice.

## Decision

The a1 module declares `go 1.25` to match the installed toolchain, with the
newest compatible pins verified against each candidate's `go` directive:
Echo 4.13.4, pgx 5.12.0, Cobra 1.10.2, Viper 1.21.0, Goose 3.27.3,
Testify 1.12.1, sqlc tool 1.30.0.

## Consequences

Reproducible builds on the present toolchain; upgrades are explicit future
decisions with re-verified pins. Contributors on newer toolchains are unaffected.
