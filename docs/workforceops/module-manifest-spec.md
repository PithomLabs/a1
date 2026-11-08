# Module Manifest Spec (module.yaml v0)

**Status:** Phase 1 design gate candidate · **Date:** 2026-11-08

The manifest is descriptive metadata, validated at boot and by CI. It never
executes code and never grants permissions.

```yaml
module: {id, name, version, description, status}  # status: experimental|stable
platform: {minimum_version}                       # major-version match in v0
capabilities: [domain-model, ...]                 # from the Capability set
dependencies: [other-module-ids]                  # empty in v0.1
database: {schemas: [...]}                        # schemas the module migrates
rules: {jurisdiction, effective-dated}            # optional
entrypoint: {registration: Register}              # Go entry symbol name
```

Validation (`module.LoadManifest`, tested): required fields present, core
version compatible, capability names known, declared schemas match the
module's migration set (checked in Phase 2+). Unknown top-level keys are
rejected to keep manifests honest.
