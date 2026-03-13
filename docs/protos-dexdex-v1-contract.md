# protos-dexdex-v1-contract

## Scope
- Project/component: DexDex shared v1 proto contract
- Canonical path: `protos/dexdex/v1/dexdex.proto`

## Runtime and Language
- Runtime: Connect RPC schema contract shared across Go and desktop runtimes
- Primary language: Protocol Buffers (`proto3`)

## Users and Operators
- API and client engineers consuming DexDex service schemas
- Operators validating cross-component compatibility during rollout

## Interfaces and Contracts
- `dexdex.v1` package definitions are the source of truth for cross-component business contracts.
- Service, message, and enum identifiers must remain stable or follow explicit versioning policy.
- Schema evolution must preserve backward compatibility guarantees for active clients and servers.

## Storage
- Schema files are versioned in-repo.
- Generated artifacts are derived outputs and must remain reproducible from canonical proto definitions.

## Security
- Proto-level fields carrying secret or sensitive data must be clearly documented for redaction handling.
- Breaking auth semantics require coordinated updates across all consuming components.

## Logging
- Schema-change workflows should log compatibility checks and generation outcomes.
- Generated client/server logging contracts must preserve request correlation fields.

## Build and Test
- Validate generation and compatibility via project scripts and CI workflows.
- Keep generated artifacts synchronized in the same change set when proto contracts change.

## Dependencies and Integrations
- Downstream consumers: `apps/dexdex`, `servers/dexdex-main-server`, `servers/dexdex-worker-server`.
- Integration protocol: Connect RPC-first communication model.

## Change Triggers
- Update `docs/project-dexdex.md` and this file for any schema-level contract changes.
- Synchronize downstream contract docs in apps/servers domains in the same change set.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
