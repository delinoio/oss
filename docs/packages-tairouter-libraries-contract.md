# TaiRouter shared Go libraries contract

## Scope

Owns `packages/tairouter`, reusable Go libraries shared by the service and CLI. The repository root owns the single Go module `github.com/delinoio/oss`; do not introduce component-specific Go modules.

## Runtime and Language

Go, built and tested through the root module.

## Users and Operators

Service and CLI implementers and generated-Go consumers.

## Interfaces and Contracts

Provide shared mechanisms for protocol/client integration, typed configuration, and security/lifecycle support where both consumers need them. Service policy is owned by the service contract; CLI settings/OS behavior is owned by the CLI contract. Do not import executable entry points into libraries or hide policy changes in shared helpers.

## Storage

Migration replay must preserve expanded provider/outcome usage dimensions and recreate backup-excluded temporary tables without reinstating superseded constraints.

Storage adapters must preserve the owning service or CLI storage boundary; no separate shared secret store or cross-tenant cache.

## Security

Require explicit ownership for tenant-aware operations. Never weaken current authorization, send budgets, deletion fences, encrypted state expiry, or local credential-store requirements through reuse.

## Logging

Use structured logs and request identifiers. Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Distinguish unknown usage, write failures, cancellation, limits, and upstream/gateway errors. See the [observability contract](servers-tairouter-operations-contract.md).

## Build and Test

Follow the release gates and required scenarios in [operations](servers-tairouter-operations-contract.md). Run root `go test ./...` for Go changes. Passing package tests does not establish complete runtime or live acceptance.

## Dependencies and Integrations

- [servers-tairouter-service-contract](servers-tairouter-service-contract.md)
- [cmds-tairouter-client-contract](cmds-tairouter-client-contract.md)
- [protos-tairouter-management-contract](protos-tairouter-management-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-tairouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
