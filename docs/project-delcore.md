# Project: delcore

## Goal

Provide shared tenants, authorization, metering and tenant-wide billing for independently hosted Delino services.

## Project ID

`delcore`

## Domain Ownership Map

- `servers/delcore-api`: one Go API and background worker process, migrations, sample client and OCI packaging.
- `protos/delcore/v1`: Connect schemas.
- `protos/gen/go/delcore/v1`: generated Go messages and Connect bindings.

## Domain Contract Documents

- [Server contract](servers-delcore-api-contract.md)
- [Protocol contract](protos-delcore-v1-contract.md)

## Cross-Domain Invariants

PostgreSQL is authoritative. Logto owns identities; delcore owns tenant authorization. Paddle owns payment execution; delcore owns usage rating and credits. Purchased credits pay usage only. Paid periods follow Paddle renewal periods; free periods use UTC calendar months. Consumers fail closed for new paid execution when authorization cannot be checked and retain pending usage reports. Repository-owned code uses Apache-2.0.

## Change Policy

Update the owning server and protocol contracts together when their boundary changes. Update instruction files only for ownership or development-rule changes. Record validation and remaining limits in PRs, issues or CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Environment contract](repository-environment-contract.md)
- [Workflow contract](repository-workflow-contract.md)
