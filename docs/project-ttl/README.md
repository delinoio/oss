# Project: ttl

## Documentation Layout
- Canonical entrypoint for this project: docs/project-ttl/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`ttl` defines a new task language and toolchain for Go ecosystems inspired by TurboTasks-style execution.
The project focuses on incremental computing, persistent caching, and parallel task scheduling, while allowing v1 delivery by compiling `.ttl` sources to Go code.


## Path
- Canonical compiler CLI path: `cmds/ttlc`
- Canonical language contract path: `docs/project-ttl/feature-language-spec.md`


## Runtime and Language
- TTL language (`.ttl`) + Go compiler CLI
- v1 compile target: Go source (`ttl` -> generated Go)
- v1 cache backend: SQLite


## Users
- Build/tooling engineers maintaining CI and developer workflows
- Monorepo maintainers optimizing repeated build and analysis pipelines
- Platform engineers building deterministic task graphs with cache reuse


## In Scope
- `.ttl` parsing and syntax validation
- Typed task signatures and type checking for v1 core types
- Go source generation from `.ttl` modules
- Dependency graph extraction and cycle diagnostics
- Persistent metadata cache contract backed by SQLite
- Cache-key fingerprint derivation for future execution reuse
- Structured logging contract for compile/runtime observability


## Out of Scope
- Distributed cache and remote execution in v1
- Full IDE language server feature set in v1
- Multi-target backends beyond Go source in v1
- Runtime task execution workers in phase 1
- Non-deterministic or side-effectful task semantics by default


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-language-spec.md](./feature-language-spec.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
