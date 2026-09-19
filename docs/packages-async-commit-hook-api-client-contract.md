# async-commit-hook API client contract

## Scope
`packages/async-commit-hook-api-client`, package @delinoio/async-commit-hook-api-client.

## Runtime and Language
TypeScript, protobuf-es and Connect Query generated from async_commit_hook.v1.

## Users and Operators
The ach static application and protocol maintainers.

## Interfaces and Contracts
Export versioned messages, enums, services and namespaced query bindings. No independent gate implementation or implicit persistence. Generate committed src/gen; compile ignored dist before consumers build.

## Storage
No owned persistence. Dist is generated, ignored and removed from the final worktree.

## Security
Transport authorization belongs to the application. Never embed credentials or introduce unrestricted file/command endpoints.

## Logging
No implicit logs or telemetry.

## Build and Test
Package-local typecheck, unit tests and build; root protocol generation/freshness and compatibility checks.

## Dependencies and Integrations
@bufbuild/protobuf, @connectrpc/connect, @connectrpc/connect-query and React Query.

## Change Triggers
Update project/protocol/app contracts with generated API changes.

## References
- [Project](project-async-commit-hook.md)
- [Repository defaults](repository-defaults.md)
