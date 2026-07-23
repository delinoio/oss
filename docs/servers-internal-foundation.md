# servers-internal-foundation

## Scope
- Repository-shared Go package boundary: `servers/internal`
- Consumer in issue #722: `servers/delibase`
- Ownership: shared repository infrastructure; no independent project ID and no ownership transfer to an unrelated project.

## Runtime and Language
- Runtime: Go packages imported by server projects.
- Primary language: Go.
- Packages are reusable, narrowly scoped, and must not contain delibase-specific business rules, persistence models, billing policy, or product UI concerns.

## Users and Operators
- Server projects, initially delibase, that need consistent auth, transport, identifiers, logging, and HTTP behavior.
- Repository maintainers reviewing shared compatibility and security changes.

## Interfaces and Contracts
- Provide reusable boundaries for Logto JWT/JWKS validation, typed claims, Connect interceptors, request/trace IDs, authorization-header/forwarded-token redaction, HTTP defaults, structured logging hooks, and UUID v7 generation.
- Shared interfaces must remain provider-agnostic where possible; delibase maps them to its organization, team, meter, and billing policy.
- No package in this boundary may decide organization roles, team inheritance, Polar billing, ledger mutations, or catalog authorization.

## Storage
- Shared packages are stateless by default and own no database tables, migrations, caches, or secret persistence.
- UUID v7 generation provides identifiers for consumers; persistence and transaction semantics remain owned by each service.

## Security
- Validate and type identity claims without logging tokens or raw sensitive claims. Redact authorization headers and dedicated forwarded-user headers before logs or diagnostics.
- Shared HTTP/Connect defaults must fail closed for malformed credentials and preserve context for authorization/audit decisions.
- Logto remains the identity trust boundary; shared code does not turn authentication into application authorization.

## Logging
- Expose structured `log/slog` hooks and request/trace correlation fields without requiring a product-specific event schema.
- Shared diagnostics must support safe error classification and never include secret values, token contents, or raw billing PII.

## Build and Test
- Validate with `gofmt`, `go vet ./servers/...`, and `go test ./servers/...` when Go implementation exists.
- Add focused tests for JWT/JWKS claims, header redaction, interceptor behavior, UUID v7 shape/order, HTTP defaults, and structured logging safety.
- Any consumer must run its own tests; shared changes must not be validated only through delibase.

## Dependencies and Integrations
- Consumed by `servers/delibase`; future server consumers require an explicit ownership and compatibility review.
- Coordinates with `protos/delibase/v1` for transport metadata but does not own Protobuf sources.
- Configuration ownership: shared packages own safe defaults and typed configuration contracts; each consuming service owns provider endpoints, credentials, and product-specific policy.

## Change Triggers
- Update this document, `servers/AGENTS.md`, [project-delibase](project-delibase.md), and the delibase server contract for shared API, security, logging, identifier, or HTTP behavior changes.
- Update every consuming project/domain contract and run its validation when a shared exported interface changes.
- A later decision to make `servers/internal` a separately owned project requires an explicit new project ID and synchronized ownership-map change; do not infer that from a package import.

## References
- [Project delibase](project-delibase.md)
- [Server contract](servers-delibase-server-foundation.md)
- [Protobuf API contract](protos-delibase-api-contract.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
