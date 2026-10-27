# AIPRouter generated TypeScript client contract

## Scope

Owns `packages/aiprouter-client`, generated TypeScript management bindings consumed by the console.

## Runtime and Language

TypeScript generated from `protos/aiprouter/v1`, integrated with Connect RPC and `@connectrpc/connect-query`. Node.js follows `.nvmrc`.

## Users and Operators

Console developers and internal management-flow consumers.

## Interfaces and Contracts

The Protobuf schemas own messages and services. Generate reproducibly; do not hand-edit generated behavior or establish a separate stable external automation API. Keep React component state and server business policy outside the generated package.

The private workspace package is `@aiprouter/client`. Its root exports generated message schemas and service descriptors from `src/gen`; consumers use Protobuf-ES v2 message creation and Connect v2 clients. React Query consumers use service method descriptors with `@connectrpc/connect-query`. Do not hand-edit generated bindings.

## Storage

No durable state, embedded credentials, or tenant cache. Consumers own cache isolation.

## Security

Propagate authenticated request context without broadening permissions. No inference-key-to-management-authority conversion. Preserve unknown/absent fields and lifecycle errors.

## Logging

Use structured logs and request identifiers. Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Distinguish unknown usage, write failures, cancellation, limits, and upstream/gateway errors. See the [observability contract](servers-aiprouter-operations-contract.md).

## Build and Test

Verify reproducible generation, type checking, and console integration tests. Follow [protocol validation](protos-aiprouter-management-contract.md) and [operations gates](servers-aiprouter-operations-contract.md).

## Dependencies and Integrations

- [protos-aiprouter-management-contract](protos-aiprouter-management-contract.md)
- [apps-aiprouter-console-contract](apps-aiprouter-console-contract.md)
- [servers-aiprouter-service-contract](servers-aiprouter-service-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-aiprouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
