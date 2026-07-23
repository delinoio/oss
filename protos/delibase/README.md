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
  operation values; invitation creation is not idempotent. A replay with the
  same payload returns the stored original response and marks
  `IdempotencyResult.replayed`; a different payload returns
  `ERROR_REASON_IDEMPOTENCY_CONFLICT`.
- Non-OK Connect responses carry `delibase.v1.ErrorDetail`. Consumers switch on
  `ErrorReason`, never the human-readable message.

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
