# Delcore v1 protocol contract

## Scope

Schemas live in `protos/delcore/v1`. Generated messages and Connect-Go bindings live in `protos/gen/go/delcore/v1`.

## Runtime and Language

Protobuf package `delcore.v1`; generated Go clients and handlers use the root module. There is no separate SDK package or UI.

## Users and Operators

User clients, service-specific M2M clients and platform operators.

## Interfaces and Contracts

Expose typed Connect interfaces for user discovery, tenants, memberships, invitations, ownership transfer, groups, catalog, subscriptions, top-ups, usage, corrections, ledger, audit and failed-job recovery. Every protected request checks current permissions. Mutations accept client-generated idempotency keys; repeated keys with different inputs fail. Enum values describe states, roles and failure reasons. Delcore entity IDs are UUID v7; provider IDs remain explicit external references. Decimal quantities and monetary values use canonical decimal strings, never floats. Public decimal inputs are limited to 64 characters; internal products and sums retain their full precision. Return correlation metadata on responses and errors. Lists have bounded pagination.

Occurrence timestamps use PostgreSQL microsecond precision. The service normalizes timestamps before hashing, storing and matching corrections. Prorated included quantities use a 0.000001 quantum; monetary products retain exact decimal precision until currency rounding.

## Storage

Wire messages are not the database schema. Historical pricing and usage identities survive replay and corrections. See the server contract for retention boundaries.

## Security

User and M2M credentials cannot substitute for each other. Service reporting is scoped to the authenticated registered service. Operator changes require an audit reason. Consumer application URLs are operator-allowlisted. Payment callbacks do not authorize entitlements.

## Logging

Correlate responses and logs without exposing credentials or raw provider payloads.

## Build and Test

Use the root Buf generation, lint, formatting, breaking and freshness checks. Add Connect contract tests and a Go sample client against real PostgreSQL and mocked external services. Do not generate a TypeScript SDK.

## Dependencies and Integrations

Connect-Go and Protobuf use the root pinned versions. Add delcore to the existing Buf input selection.

## Change Triggers

Update the [server contract](servers-delcore-api-contract.md) and [project index](project-delcore.md) when public interfaces change. Preserve field numbers and enum values.

## References

- [Repository defaults](repository-defaults.md)
- [Server contract](servers-delcore-api-contract.md)
