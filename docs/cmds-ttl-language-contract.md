# cmds-ttl-language-contract

## Scope
- Project/component: TTL language syntax/type/invalidation contract
- Canonical path: `cmds/ttlc`

## Runtime and Language
- Runtime: Go language frontend and code generation pipeline
- Primary language: TTL specification + Go implementation

## Users and Operators
- Engineers authoring TTL definitions
- Tooling maintainers implementing parser/type-checker/codegen behavior

## Interfaces and Contracts
- TTL grammar and parser behavior must remain deterministic and versioned.
- Type-check rules and invalidation semantics must remain backward compatible unless explicitly versioned.
- Go code-generation contracts must remain aligned with runtime execution contracts.

## Storage
- Defines schema for generated artifacts and cache invalidation metadata consumed by TTL runtime.
- Language-level metadata must map consistently to command-level cache analysis.

## Security
- Parser and type-checker must reject malformed or unsafe definitions deterministically.
- Generated output must avoid introducing unsafe defaults in runtime integration.

## Logging
- Use structured logs for parser phases, type-check diagnostics, and code-generation stages.
- Include source location metadata and deterministic diagnostic identifiers.

## Build and Test
- Local validation: `go test ./cmds/ttlc/...`
- Contract validation: parser/type-check fixtures and code-generation golden tests

## Dependencies and Integrations
- Downstream dependency: `docs/cmds-ttl-foundation.md` command execution contract.
- Integrates with generated Go runtime foundations used by TTL execution.

## Change Triggers
- Update this file and `docs/cmds-ttl-foundation.md` together when language changes affect runtime behavior.
- Update `docs/project-ttl.md` for ownership, compatibility, or stability policy changes.

## References
- `docs/project-ttl.md`
- `docs/cmds-ttl-foundation.md`
- `docs/domain-template.md`
