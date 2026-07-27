# delibase shared API

`v1/*.proto` is the canonical `delibase.v1` contract owned by delibase. It
defines exactly six Connect services and is the only source edited by hand.
Generated Go and TypeScript files under `gen/` are checked in so both consumers
compile against the same revision.

The generated Go packages are:

- `github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1`
- `github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect`

The workspace TypeScript package is `@delinoio/delibase-connect`. With Connect
2, protobuf-es generates `GenService` descriptors in each `*_pb.ts` file; pass
those descriptors to `createClient()` or `@connectrpc/connect-query`. The
package exports all v1 messages and descriptors from its root and also exposes
`@delinoio/delibase-connect/delibase/v1/*_pb` subpaths.

## Representation contract

- Every persisted entity ID uses `UuidV7`, whose value is a canonical lowercase
  UUID v7 string.
- USD amounts use `UsdMicros`, a signed protobuf `int64`. One USD is 1,000,000
  micro-units. Meter quantities use the separate signed-int64 `UsageUnits`.
  TypeScript represents both values as `bigint`; Go represents them as `int64`.
- Lists use `PageRequest.cursor` and `PageResponse.next_cursor`. Cursor contents
  are opaque to callers.
- Mutation idempotency is scoped by authenticated identity and
  `IdempotentOperation`. Invitation acceptance and revocation have distinct
  operation values; invitation creation is not idempotent. Background
  authorization creation/revocation and authorized reserve/commit/release/
  resource-deletion notification also have distinct human- or service-scoped
  operations. A replay with the same
  payload returns the stored original response and marks
  `IdempotencyResult.replayed`; a different payload returns
  `ERROR_REASON_IDEMPOTENCY_CONFLICT`.
- Non-OK Connect responses carry `delibase.v1.ErrorDetail`. Consumers switch on
  `ErrorReason`, never the human-readable message.

## Bounded background usage

`BillingService` exposes human-authenticated create, get, opaque-cursor list,
and revoke operations for `BackgroundUsageAuthorization`. Each UUID v7 grant
binds its authorizer; personal or organization feature-resource owner; payer
organization/team where applicable; exact service identity and meter;
`REALQA_STORAGE` purpose; feature-resource UUID; `UTC_DAY` period; maximum
units; lifecycle status; revision; and timestamps. Status is closed to
`ACTIVE`, `REVOKED`, `ACCESS_LOST`, `RESOURCE_DELETED`, and `OWNER_DELETED`.
There is intentionally no Deck purpose.

`UsageService.ReserveAuthorizedUsage`, `CommitAuthorizedUsage`, and
`ReleaseAuthorizedUsage`, plus `MarkBackgroundUsageResourceDeleted`, are
M2M-only. They do not accept, require, forward, or store a user bearer.
Reserve/commit/release replay digests bind the authorization, authenticated
service identity, purpose, feature resource, UTC period, and units;
commit/release also bind the reservation ID. Resource-deletion digests bind
authorization, service, purpose, resource, and expected revision. Reserve accepts
only the current or immediately preceding canonical UTC day according to server
time, while commit/release use the reservation's stored period. Public catalog
meters expose stable service authorization target UUIDs and safe names without
Logto client IDs or credentials. The resource-deletion method lets the bound
service idempotently transition a matching `ACTIVE` grant to
`RESOURCE_DELETED`. For an exact matching grant already closed as `REVOKED`,
`ACCESS_LOST`, `RESOURCE_DELETED`, or `OWNER_DELETED`, it returns the current
status and revision without transition even when the expected revision predates
closure; future revisions and substitutions fail closed. The server rejects
stale active revisions, period-limit overflow, and altered replays with stable
background-usage `ErrorReason` values. These additions do not create another
service: the package still contains exactly the existing six services.

See [AUTHENTICATION.md](AUTHENTICATION.md) for token metadata and scopes.

## Generation and checks

Run from the repository root after `pnpm install`:

```sh
pnpm generate:proto
pnpm check:proto
pnpm --filter @delinoio/delibase-connect typecheck
go test ./protos/delibase/...
```

`pnpm check:proto` lints the module, checks the source against the checked-in v1
descriptor baseline, regenerates artifacts and the descriptor, and rejects a
generation diff. CI supplies the immutable descriptor from the pull request
base or pre-push commit for the compatibility check. Generator versions come
from `go.mod`, `scripts/lib/go-proto-tools.sh`,
`protos/delibase/package.json`, and `pnpm-lock.yaml`. `pnpm generate:proto`
refreshes the descriptor and also builds the package `dist` output referenced
by its workspace exports.

## Compatibility

`delibase.v1` evolves additively. Do not remove or rename services, RPCs, fields,
enum values, or change their semantics. Removed field numbers and names must be
reserved. Any breaking wire or behavioral change requires a new API package,
such as `delibase.v2`, plus a documented consumer migration.
