# protos-devhud-deck-api-contract

## Scope

- Project/component: `devhud` / `deck-api`
- Canonical source path: `protos/devhud-deck/v1`
- Contract identity: `devhud.deck.v1`
- Status: implemented source contract and private generated workspace package for
  issue #755; no Deck server, deployed API, published client, or activated
  feature is claimed.

## Runtime and Language

- Versioned Protobuf is authoritative.
- Generate reproducible Connect-compatible Go and protobuf-es v2 TypeScript artifacts under `protos/devhud-deck/gen/go` and `protos/devhud-deck/gen/ts`; generated files are derived and never a second contract.
- The private workspace TypeScript package is
  `@delinoio/devhud-deck-connect`. It exports all v1 messages and service
  descriptors at its root and exposes
  `@delinoio/devhud-deck-connect/devhud-deck/v1/*_pb` generated subpaths; it is
  not published and is not a public SDK.
- Preserve released v1 fields additively. Breaking changes require `devhud.deck.v2` and synchronized consumer migration docs.

## Services and RPCs

- `DeckViewService`: `ListViews`, `GetView`, `CreateView`, `UpdateView`, `DeleteView`, `ListPullRequests`, `GetManualRefreshQuote`, `RefreshView`, `MutatePullRequest`, `DeleteFeatureData`.
- `DeckIntegrationService`: `GetGitHubConnection`, `StartGitHubConnection`, `ListGitHubInstallations`, `DisconnectGitHubConnection`.
- `DeckDeviceService`: `RegisterDevice`, `UpdateDevice`, `UnregisterDevice`, `UpdateViewNotificationPreference`, `ResolveNotificationEvent`.

No additional v1 service or RPC is implied. GitHub callback/webhook handlers and push delivery are HTTP/server boundaries, not public Connect services.

## Wire Contract

- IDs are UUID v7 wrappers. Owner scope, view kind, sort, grouping, mutation kind, connection state, refresh outcome, notification transition, freshness state, and stable error reason are closed enums.
- The only v1 view kind is `GITHUB_PULL_REQUESTS`.
- Views carry owner/billing scope, name, canonical raw query, typed builder clauses, sort/grouping, notification preference, revision/ETag, and timestamps. `UpdateView` may change the notification preference but never owner or kind.
- `CreateView` carries a stable client-generated UUID v7 idempotency key scoped to the authenticated subject and operation. An exact replay returns the originally created view and revision; reuse with changed creation input returns the typed idempotency-conflict reason.
- Lists use opaque cursor pagination. Limits are 50 personal views, 250 organization views, and 500 PR results per view with explicit truncation.
- PR result rows carry repository, number, title, author, individual/team
  reviewer identities needed for reviewer grouping, current assignees and
  labels needed to populate removal actions, review decision, checks,
  mergeability, explicit open/closed/merged lifecycle state, independent draft
  state, updated time, and the synchronized PR revision required by
  `MutatePullRequest`. They also expose the currently supported mutations and
  available merge methods so clients never guess or probe action availability
  or current removal operands.
- Device registration/update requests carry request-only shortcut configurations using closed modifier/key enums. Effective shortcut conflict state and synchronized shortcut revisions are server-authored response state; clients cannot submit either field or unchecked binding strings.
- Device registration/update requests carry widget identity, selected view, family, and privacy configuration only. Widget snapshots, freshness/offline state, and synchronized widget revisions are server-authored response state.
- Mutations are a closed union for assign/unassign, reviewer request/removal, label add/remove, draft/ready, close/reopen, merge, and native auto-merge enable/cancel. Merge requests carry explicit user confirmation.
- `GetManualRefreshQuote` is the only manual-refresh billing preflight. Its request identifies the view and the client-generated UUID v7 identity for the prospective logical refresh. The server validates the authoritative `(devhud, deck_github_pull_request_refresh)` meter identity, Deck service mapping, precision-zero `provider_refresh` unit, and effective 50-USD-micro unit price before returning that server-derived display price, an opaque short-lived preflight token bound to the authenticated subject, view, billing scope, refresh identity, and validated catalog version, plus its expiry. It performs no usage reservation, provider dispatch, cache refresh, or charge; missing, disabled, or divergent catalog metadata returns a typed unavailable result with no quote.
- `RefreshView` carries a stable client-generated UUID v7 request identity scoped to the authenticated subject, operation, and view and distinguishes automatic/widget/manual origin and cache behavior. For a manual request, the server first looks up the authenticated identity and request digest. An existing exact attempt returns or resumes its original freshness, outcome, cache/coalescing, and billing disposition even if its quote later expires or the catalog mapping changes; changed-input reuse returns the typed idempotency-conflict reason. Only creation of a new manual attempt additionally requires the unexpired preflight token from `GetManualRefreshQuote` for that same identity, revalidates the token and current authoritative mapping before reservation or provider dispatch, and rejects missing, expired, substituted, or stale preflight state. The client preserves the refresh identity and token across ambiguous retries. One durable attempt covers reservation, provider dispatch, and commit/release without another provider request or charge.
- `StartGitHubConnection` returns an ephemeral GitHub.com authorization target. DevHud may pass it only to the scoped native system-browser action; the DeliDev settings client may pass a Deck target only to its separately contracted validated top-level browser handoff. It is never a general navigation field: each client applies the exact authorization host/path/App validation in its app contract and must not log, cache, persist, or reuse the query data.
- Each logical `RegisterDevice` creation or lease renewal carries a stable client-generated UUID v7 idempotency key scoped to the authenticated subject, operation, account/device, and request input. Initial creation omits `expected_revision`; renewal carries the current device revision and a stale renewal fails without replacing newer display-name, push, shortcut, widget, or notification-detail configuration. While the original lease remains valid, an exact replay returns the original opaque registration ID, identical lease, and identical revocation grant in the same dedicated sensitive response metadata; changed-input reuse returns the typed idempotency-conflict reason. The grant remains valid through the lease and authorizes only idempotent `UnregisterDevice` when supplied as dedicated authorization metadata; ordinary matching-account authentication may also unregister. A new or renewed registration is rejected while cleanup remains pending, but a cleanup tombstone retains the registration ID, lease expiry, and grant in the OS secure vault so revocation can finish after logout or an account switch. Terminal unregister/absent-registration success or observed lease expiry permits the tombstone and grant to be deleted.
- Push payloads carry only an opaque event ID. `ResolveNotificationEvent` requires human authentication and the matching active account/device registration, never accepts a cleanup revocation grant, and returns the affected view/PR detail only after current owner/repository authorization and the device's detailed-text opt-in are revalidated. Otherwise it returns a typed unavailable/generic result without identity-bearing fields and never initiates provider refresh.
- `DeleteFeatureData` carries a closed trigger union. Owner-request mode carries a personal or organization owner scope plus an idempotency key scoped to the authenticated subject, operation, and owner scope; a personal caller may delete only their own Deck data, and organization deletion requires an Owner. Delibase-lifecycle mode carries only an account or organization target UUID plus the immutable delibase deletion-job UUID and requires the exact Deck-scoped delibase M2M identity. The deletion-job UUID is the replay identity, DeliDev and ordinary feature callers cannot select lifecycle mode, and absent feature data succeeds idempotently.
- The first accepted deletion request immediately blocks new scope access and mutations and starts asynchronous hard deletion of all data owned by that scope, including view definitions and attached connection/provider, cache, notification, widget, and shortcut data. Exact replays return the same deletion-job result while only required pseudonymized financial/security records survive.
- Every mutation that changes existing synchronized data, including a device
  lease renewal that resubmits mutable configuration, carries the expected
  revision and returns a new revision. Stable conflict details support
  reload/compare/reapply; initial creation replay safety is provided by the
  operation-specific idempotency keys above.
- Authenticated messages identify personal/organization/team resources by IDs only; client-provided roles, repository permission, billing authority, provider access, and price are never authoritative.

## Authentication, Privacy, and Errors

- Human RPCs use `authorization: Bearer <deck-token>` plus the dedicated
  memory-only `x-devhud-deck-forwarded-delibase-token` metadata key.
  `RegisterDevice` returns its single-registration grant only in sensitive
  `x-devhud-deck-device-revocation-grant` response metadata;
  `UnregisterDevice` alone may accept that key as request authorization.
  `DeleteFeatureData` in delibase-lifecycle mode instead requires the exact
  Deck-scoped delibase M2M bearer. Each alternate credential is rejected by
  every other procedure. Generated clients must treat all credentials as
  sensitive and keep them out of messages, logs, errors, caches, persistence,
  and diagnostics except for the revocation grant's narrowly scoped OS-vault
  cleanup tombstone. `protos/devhud-deck/AUTHENTICATION.md` is the package-local
  metadata and scope reference.
- The future delibase single-reservation billing-finalization grant is an internal delibase-to-Deck server credential, not a `devhud.deck.v1` field or metadata value. Deck clients never receive, retain, or submit it; server-side recovery uses it only under the Deck server contract.
- The DevHud generated client runs through the private native Connect transport's closed procedure/origin mapping rather than browser fetch. This changes no Connect wire shape and grants no arbitrary URL, header, method, redirect, or `http://tauri.localhost` CORS access.
- The server verifies matching subjects, DeliDev role/scope, and the viewer's GitHub permission. Error enums distinguish authentication, authorization, provider permission, stale revision, limits, truncation, rate/concurrency limits, billing catalog/preflight, billing reservation, provider failure/rate limit/timeout, offline/stale, disconnected, and unsupported host/action.
- Messages must not carry GitHub tokens, webhook secrets, raw authorization headers, production secrets, or sensitive push payloads.
- The API transports data, not UI definitions. It must not expose HTML, JavaScript, component trees, plugin manifests, arbitrary URLs, or runtime code.

## Build and Test

Canonical checks are:

- `pnpm generate:proto`;
- `pnpm check:proto`;
- `pnpm --dir protos/devhud-deck generate:proto`;
- `pnpm --dir protos/devhud-deck check:proto`;
- `go test ./protos/devhud-deck/...`;
- `go vet ./protos/devhud-deck/...`;
- `pnpm --filter @delinoio/devhud-deck-connect typecheck`.

The workspace package owns `buf.gen.yaml`, component-local generation/check
scripts, `gen/go`, `gen/ts`, and `devhud.deck.v1.binpb`. Generation scopes every
Buf operation to `protos/devhud-deck/v1`, cleans and writes only those owned
outputs, lints, checks only the immutable Deck descriptor baseline, generates
Go/TypeScript artifacts, builds the TypeScript package, and rejects
nondeterminism or a component-local generated diff. The fixed-order root
aggregate and `proto-contracts` CI job include Deck. CI fails on compatibility,
service/RPC/enum drift, stale artifacts, sensitive metadata leakage,
cross-component writes, or missing cross-consumer generation.

Checks do not publish the TypeScript package, deploy `https://deck.deli.dev`, register the GitHub App, activate a catalog entry, or publish a server image.

## Dependencies and Change Triggers

- Owned by `devhud`; consumed by `servers/devhud-deck`, the authenticated Deck client under `apps/devhud`, the authenticated DeliDev settings client under `apps/delidev-app` only for `DeckIntegrationService`, and `servers/delibase` only for service-authenticated `DeleteFeatureData` lifecycle delivery.
- Update this document, [project-devhud](project-devhud.md), [servers-devhud-deck-foundation](servers-devhud-deck-foundation.md), [apps-devhud-foundation](apps-devhud-foundation.md), [apps-delidev-app-foundation](apps-delidev-app-foundation.md), and affected `AGENTS.md` files for any service, RPC, message, enum, auth metadata, error, pagination, generated package, or compatibility change.

## References

- [Project devhud](project-devhud.md)
- [Deck server](servers-devhud-deck-foundation.md)
- [DevHud app](apps-devhud-foundation.md)
- [DeliDev app](apps-delidev-app-foundation.md)
- [Issue #755](https://github.com/delinoio/oss/issues/755)

## Out of Scope

- Public API/plugin SDK status, third-party clients, remote UI, GHES/custom hosts, server schedulers, production deployment, generated-client publication, GitHub App registration, and catalog activation.
