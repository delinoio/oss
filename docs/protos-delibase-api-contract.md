# protos-delibase-api-contract

## Scope
- Project/component: `delibase` / `api`
- Canonical source path: `protos/delibase/v1`
- Contract identity: `delibase.v1`

## Runtime and Language
- Source format: versioned Protobuf.
- Generated consumers: Connect-compatible Go server/client artifacts and TypeScript browser client artifacts.
- Protobuf source is authoritative; generated output is derived and must not be edited as a second contract.
- Checked-in Go output is `protos/delibase/gen/go`; protobuf-es v2 output is `protos/delibase/gen/ts` and is exposed to workspace consumers as `@delinoio/delibase-connect`. Connect 2 consumes the `GenService` descriptors emitted in the generated `*_pb.ts` modules.

## Users and Operators
- DeliDev browser client, delibase service, authenticated human users, and authenticated mini-app backend services.
- Maintainers validating compatibility and generation output.

## Interfaces and Contracts
- Exactly six Connect services: `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`.
- Stable route/API origin pairing: browser `https://deli.dev`; future API `https://delibase.deli.dev`.
- Account: authenticated account state, interactive first-organization onboarding that atomically establishes the unique Logto-`sub` local user and default organization memberships when the user has not joined through an invitation, and account deletion/blockers.
- Organization: CRUD, globally unique changeable slugs/aliases, member roles, invitations, and idempotent acceptance and revocation.
- Team: nullable-parent hierarchy, depth/cycle-safe moves, confirmed non-`General` subtree deletion, memberships, and effective downward access.
- Catalog: anonymous public app/meter listing and details, including public effective-dated USD micro-unit prices, their version metadata, and stable service authorization target UUIDs/safe names without Logto client IDs or credentials; no runtime mutation API.
- Billing: summary, hosted Polar checkout/portal session, overage limit, ledger/usage reads, and human create/get/opaque-list/revoke of bounded background-usage authorizations.
- Usage: live-user and authorized reserve, commit, and release plus authorized feature-resource deletion notification, with signed int64 units, pinned prices, reservation TTL, and service-scoped idempotency. Authorized methods are additions to `UsageService`, not a seventh service.
- Human requests use Logto user access tokens except for anonymous CatalogService reads, with the canonical audience `https://delibase.deli.dev`. Live-user usage mutations carry Logto M2M authorization and the dedicated `x-delibase-forwarded-user-token` Connect metadata key for the forwarded end-user token; authorized-usage mutations are M2M-only and must not require, accept as handler state, or persist that bearer. Servers redact any credential metadata anywhere headers, metadata, or diagnostics are logged. Raw Logto client secrets are provider-side credentials and never cross this API boundary. Authenticated invitation preview and acceptance authorize with the invitation bearer token and do not require a pre-existing local account, organization membership, or team access; acceptance establishes the active local account with a privacy-safe non-empty placeholder display name when needed, and an account can consume each invitation only once even when a later request uses a different idempotency key. Team RPCs establish organization membership before resolving team identity so non-members cannot probe team existence. The server owns authorization decisions.
- The released `delibase.v1` live-usage wire contract has no single-reservation
  finalization grant: `ReserveUsage`, `CommitUsage`, and `ReleaseUsage` all
  require the current forwarded user bearer. Deck uses these existing RPCs only
  inside an active authenticated client request and never receives or stores a
  background authorization. Its exact later client retry may idempotently
  finish the same reservation with a fresh forwarded bearer; no protobuf field
  authorizes a scheduler, worker, detached continuation, or another provider
  request.
- Lists use opaque cursor pagination. Preserve released `delibase.v1` additively; breaking changes require `delibase.v2` or later.
- Persisted entity IDs are UUID v7. Money values are signed int64 USD micro-units; usage values are signed int64 units. Error details use stable enum identifiers.
- The wire wrappers are `UuidV7`, `UsdMicros`, and `UsageUnits`; protobuf-es represents both signed-int64 values as TypeScript `bigint`, while generated Go uses `int64`. Idempotent mutations carry `IdempotencyKey` and return `IdempotencyResult` alongside the original typed result so exact-payload replays are observable without changing the result. Keys are scoped to the authenticated user subject and operation for human RPCs, or the authenticated service identity and operation for M2M RPCs. Live-user usage digests also bind the forwarded user subject. Authorized reserve/commit/release digests bind authorization, authenticated service, purpose, resource, UTC period, and units, with commit/release also binding the reservation ID; resource-deletion digests bind authorization, service, purpose, resource, and expected revision. Invitation acceptance/revocation, background-authorization creation/revocation, and authorized reserve/commit/release/resource-deletion notification have distinct `IdempotentOperation` values; invitation creation does not carry idempotency fields.
- `BackgroundUsageAuthorization` uses a UUID v7 ID and binds authorizer, a one-of personal-account or organization feature owner, payer organization/team where applicable, exact service identity/meter, closed `REALQA_STORAGE` purpose, feature-resource UUID, closed `UTC_DAY` period, maximum units, status, revision, and creation/update/revocation timestamps. Current-period views expose authoritative held/committed/remaining unit summaries. Status is closed to `ACTIVE`, `REVOKED`, `ACCESS_LOST`, `RESOURCE_DELETED`, and `OWNER_DELETED`; Deck is intentionally absent from the purpose enum.
- `ReserveAuthorizedUsage` accepts only a canonical period start for the current or immediately preceding UTC day according to server time, preventing arbitrary past/future quota buckets while permitting completed-day settlement. `CommitAuthorizedUsage` and `ReleaseAuthorizedUsage` use the period stored with their reservation. `MarkBackgroundUsageResourceDeleted` lets only the authenticated bound service idempotently transition an `ACTIVE` matching grant to `RESOURCE_DELETED` when the expected revision is current. The same exact bound-service request returns the current closed status and revision without transition when the grant is already `REVOKED`, `ACCESS_LOST`, `RESOURCE_DELETED`, or `OWNER_DELETED`, including when the request's expected revision predates that closure; a future/unknown expected revision or any authorization/service/purpose/resource substitution fails closed.
- The complete stable `ErrorReason` taxonomy covers missing/invalid/expired/issuer/audience/scope authentication, forwarded-user token failures, permission/organization/team/service-meter authorization, resource/slug/member conflicts including member removal blocked by held reservations, invitation state/roles, hierarchy depth/cycles/cross-organization/protected-team/active-reservation state, subscription/overage/price/overflow/precision state, reservation expiry/finalization/unit limits, deletion blockers, idempotency conflicts, and background-authorization substitution/access/status/period-limit/replay failures. It is carried in `ErrorDetail` on non-OK Connect responses.

## Storage
- Protobuf defines transport messages only; PostgreSQL schema, append-only ledger, reservations, Polar inbox/outbox, and seven-year pseudonymized retention are owned by the server contract.
- Do not put tokens, card data, webhook secrets, or raw billing PII into messages, logs, or default error payloads.

## Security
- Document authentication requirements on every protected RPC. Catalog reads may be anonymous; organization, billing, usage, invitation, onboarding, and account operations are protected.
- Usage authorization must represent both service identity and forwarded end-user context without exposing the forwarded token. The `x-delibase-forwarded-user-token` metadata value is sensitive credential material and must never appear in logs, traces, errors, or persisted data.
- Do not make client-provided roles, prices, balances, team access, or overage decisions authoritative.

## Logging
- Generated clients and messages must support request/trace correlation through the shared server interceptor boundary without serializing credentials.
- Connect status and stable error details must permit safe user-facing classification for auth, authorization, slug, invitation, team, subscription, overage, reservation, idempotency, deletion, and resource-state failures.

## Build and Test
- Canonical validation: root aggregates `pnpm generate:proto` and `pnpm check:proto`; component-local `pnpm --dir protos/delibase generate:proto` and `pnpm --dir protos/delibase check:proto`; `go test ./protos/delibase/...`; `go vet ./protos/delibase/...`; and `pnpm --filter @delinoio/delibase-connect typecheck`. Delibase generation is scoped to `protos/delibase/v1`, refreshes only `protos/delibase/gen/go`, `protos/delibase/gen/ts`, and the checked-in `delibase.v1` descriptor, and builds the package `dist` output referenced by the existing workspace exports. The component check runs scoped Buf lint/breaking validation against only the delibase baseline, regenerates twice to prove determinism, and rejects a component-local generated diff. Local checks use the checked-in descriptor; the change-scoped `proto-contracts` CI job extracts the immutable delibase descriptor from the pull request base or pre-push commit, independently of future Deck and RealQA baselines, then runs aggregate checks and every implemented component's Go and TypeScript validation.
- CI must fail on stale generated output, incompatible released-field changes, service-name drift, or missing cross-consumer generation.
- No runtime activation, API deployment, or generated-client publication is part of issue #722.

## Dependencies and Integrations
- Owned by `delibase`; consumed by `servers/delibase` and `apps/delidev-app`.
- The app uses `@connectrpc/connect-query`; the server uses Connect RPC and shared `servers/internal` interceptors/types.
- Configuration ownership: this contract owns package/version/service/message identifiers and generation settings; the app owns browser endpoint/client settings; the server owns server endpoint/auth/provider settings.

## Change Triggers
- Any service, RPC, field, enum, auth metadata, error, pagination, or compatibility change updates this document, [project-delibase](project-delibase.md), the server contract, the app contract, generated-client validation, and `protos/AGENTS.md`.
- Breaking changes require a new API version and synchronized consumer migration documentation.
- Changes to shared interceptors or UUID/error conventions update [servers-internal-foundation](servers-internal-foundation.md) and `servers/AGENTS.md`.

## References
- [Project delibase](project-delibase.md)
- [Project delidev](project-delidev.md)
- [Server contract](servers-delibase-server-foundation.md)
- [Shared server infrastructure](servers-internal-foundation.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
- [Issue #756](https://github.com/delinoio/oss/issues/756)
