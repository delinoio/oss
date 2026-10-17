# Delcore v1 protocol contract

## Scope

Schemas live in `protos/delcore/v1`. Generated messages and Connect-Go bindings live in `protos/gen/go/delcore/v1`.

## Runtime and Language

Protobuf package `delcore.v1`; generated Go clients and handlers use the root module. There is no separate SDK package or UI.

## Users and Operators

User clients, service-specific M2M clients and platform operators.

## Interfaces and Contracts

Expose typed Connect interfaces for user discovery, tenants, memberships, invitations, ownership transfer, groups, catalog, subscriptions, top-ups, usage, corrections, ledger, audit and failed-job recovery. Every protected request checks current permissions. Mutations accept client-generated idempotency keys; repeated keys with different inputs fail. Enum values describe states, roles and failure reasons. Delcore entity IDs are UUID v7; provider IDs remain explicit external references. Decimal quantities and monetary values use canonical decimal strings, never floats. Public decimal inputs are limited to 64 characters; internal products and sums retain their full precision. Return correlation metadata on responses and errors. Lists have bounded pagination.

Catalog mutations reject invalid combinations as invalid-argument errors. `PutPlan` rejects a free plan that enables postpaid continuation, including the default free plan, because free tenants never have a Paddle subscription to collect postpaid usage. `CreatePrice` rejects an add-on price whose service differs from the service that owns the add-on, and a price that names an unknown add-on, before it writes any price or audit row. These checks apply to new requests only; existing plans and prices are not rewritten.

Occurrence timestamps use PostgreSQL microsecond precision. The service normalizes timestamps before hashing, storing and matching corrections. Prorated included quantities use a 0.000001 quantum; monetary products retain exact decimal precision until currency rounding.

Deletion and withdrawal return a failed precondition while a previously attempted subscription intent remains incomplete. Unattempted subscription intents, and intents that the provider definitively rejected without creating a subscription, are stopped atomically when deletion starts. A stopped rejected intent keeps its rejection category and cannot be retried.

Disabling automatic top-ups clears the settings and stops queued charges that never reached the provider in the same request. Charges that may have reached the provider, including a rejected charge that an operator retried, continue. Accepting a subscription cancellation stops the same unattempted charges.

Negative postpaid corrections carry forward against usage charges. Operators refund any remainder in Paddle on cancellation. Confirmed usage-line refunds clear the corresponding settlement balance; fixed subscription refunds do not clear usage debt.

## Storage

Wire messages are not the database schema. Historical pricing and usage identities survive replay and corrections. See the server contract for retention boundaries.

## Security

User and M2M credentials cannot substitute for each other. Service reporting is scoped to the authenticated registered service. Operator changes require an audit reason. Consumer application URLs are operator-allowlisted. Payment callbacks do not authorize entitlements. Enabling automatic top-ups requires an active Paddle subscription; manual top-ups remain available without a subscription. Subscription cancellation suspends automatic top-ups; settings cannot re-enable them while cancellation is pending.

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
