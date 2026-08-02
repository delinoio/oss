# protos-devhud-deck-api-contract

## Scope

- Project/component: `devhud` / `deck-api`
- Canonical source path: `protos/devhud-deck/v1`
- Contract identity: `devhud.deck.v1`
- Status: implemented source contract, private generated workspace package,
  bounded Deck server persistence/authentication/provider-refresh integration,
  and dependency-injected client polling controller for issue #755. No
  deployed API, published client, catalog activation, push delivery, or
  activated product feature is claimed.

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

- `DeckViewService`: `ListOwners`, `ListViews`, `GetView`, `CreateView`, `UpdateView`, `DeleteView`, `ListPullRequests`, `ListPullRequestMutationCandidates`, `GetRefreshPreflight`, `RefreshView`, `MutatePullRequest`, `DeleteFeatureData`.
- `DeckIntegrationService`: `GetGitHubConnection`, `StartGitHubConnection`, `ListGitHubInstallations`, `DisconnectGitHubConnection`.
- `DeckDeviceService`: `GetDevice`, `RegisterDevice`, `UpdateDevice`, `UnregisterDevice`, `UpdateViewNotificationPreference`, `ResolveNotificationEvent`.

No additional v1 service or RPC is implied. GitHub callback/webhook handlers and push delivery are HTTP/server boundaries, not public Connect services.

## Wire Contract

- IDs are UUID v7 wrappers. Owner scope, view kind, sort, grouping, mutation
  kind, connection state, refresh origin, refresh client kind, refresh outcome,
  billing disposition, notification transition, freshness state, and stable
  error reason are closed enums.
- The only v1 view kind is `GITHUB_PULL_REQUESTS`.
- `ListOwners` derives the authenticated caller's personal and organization
  owner scopes, management authority, and currently authorized billing
  organization/team selections on the server. Clients never invent owner IDs,
  roles, or billing choices.
- Views carry owner scope, an explicit server-authorized billing organization/team, name, a canonical raw query limited to 4,096 UTF-8 bytes, typed builder clauses, sort/grouping, notification preference, revision/ETag, and timestamps. Missing or partial billing selections are rejected by `CreateView` and `UpdateView`; `UpdateView` may change the notification preference but never owner or kind.
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
- `ListPullRequestMutationCandidates` is an authenticated, permission-filtered,
  opaque-cursor search for the user, team, or label operands accepted by an
  advertised assign-user, request-reviewer, or add-label action. Its response
  carries the synchronized PR revision. Other mutation kinds are rejected, and
  candidate discovery neither mutates nor refreshes the PR.
- `ListGitHubInstallations` rows carry the provider installation ID plus the
  stable GitHub account ID, login, and closed user/organization kind needed to
  identify the installation in DevHud and DeliDev settings.
- Device registration/update requests carry request-only shortcut configurations using closed modifier/key enums. Effective shortcut conflict state and synchronized shortcut revisions are server-authored response state; clients cannot submit either field or unchecked binding strings.
- Device registration/update requests carry widget identity, selected view, family, and privacy configuration only. Widget snapshots, freshness/offline state, and synchronized widget revisions are server-authored response state.
- `GetDevice` reloads the authenticated caller's current registration, bounded
  lease, server-authored shortcut/widget state, and device revision by stable
  device ID so a restarted or stale client can compare and reapply before
  renewal.
- Mutations are a closed union for assign/unassign, reviewer request/removal, label add/remove, draft/ready, close/reopen, merge, and native auto-merge enable/cancel. Merge requests carry explicit user confirmation. A successful `MutatePullRequestResponse` normally carries synchronized pull-request detail; when GitHub accepted the mutation but the result reload failed, it instead sets `refresh_required`, omits the stale detail, and requires a client refresh without retrying the mutation.
- `GetRefreshPreflight` is the billing preflight for every prospective logical
  refresh origin. Its request identifies the view, client-generated UUID v7
  refresh identity, origin, and active desktop/mobile/OS-background/widget
  client kind. The origin/client combination is closed and invalid
  combinations fail before billing. The server validates the authoritative
  `(devhud, deck_github_pull_request_refresh)` meter identity, Deck service
  mapping, precision-zero `provider_refresh` unit, minimum 86,400-second
  reservation TTL, and effective 50-USD-micro unit price before returning that
  server-derived price, an opaque short-lived token bound to the authenticated
  subject, view, billing scope, refresh identity, origin, active client kind,
  and validated catalog version, plus its expiry. It authorizes retained
  view/owner/billing metadata and performs no GitHub request, usage reservation,
  provider dispatch, cache refresh, or charge. Missing, disabled, or divergent
  catalog metadata returns a typed unavailable result with no preflight. Only
  manual UI displays the returned price as a warning.
- `RefreshView` carries a stable client-generated UUID v7 request identity
  scoped to the authenticated subject, operation, and view and distinguishes
  automatic/widget/manual/view-open/shortcut origin, active client kind, and
  cache behavior. The server first looks up the authenticated identity and
  request digest. An existing completed exact attempt returns its original
  freshness, outcome, cache/coalescing, and billing disposition after preflight
  expiry; changed-input reuse returns the typed idempotency-conflict reason.
  Creation of every new attempt requires the unexpired token from
  `GetRefreshPreflight` for that same identity, origin, and client kind,
  revalidates the current authoritative mapping before reservation or provider
  dispatch, and rejects missing, expired, substituted, or stale preflight
  state. The client preserves the refresh identity and token across ambiguous
  retries. One durable attempt covers live reservation, immediate pre-dispatch
  accounting, and commit/release without another provider request or charge.
  A nonterminal attempt advances only on an active authenticated client retry
  carrying a fresh forwarded-user bearer; it is not a server job.
- `RefreshOutcome` includes refreshed, free cache hit, free coalesced,
  reservation rejected, provider permission/rate-limit/timeout/failure,
  disconnected, provider-concurrency-limited, and automatic-not-eligible
  states. `BillingDisposition` distinguishes free cache/coalesced/ineligible,
  reserved, committed, released, and rejected states. `FreshnessState` remains
  independent and distinguishes fresh, stale, offline, disconnected, and
  never-refreshed snapshots.
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
- Deck defines no background-usage or billing-finalization-grant field. Live
  reserve, commit, and release use only request metadata and never appear in a
  `devhud.deck.v1` message.
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

- Owned by `devhud`; consumed by `servers/devhud-deck`, the authenticated Deck client under `apps/devhud`, the implemented authenticated DeliDev `/account` and organization-settings client only for `DeckIntegrationService`, and `servers/delibase` only for service-authenticated `DeleteFeatureData` lifecycle delivery.
- Update this document, [project-devhud](project-devhud.md), [servers-devhud-deck-foundation](servers-devhud-deck-foundation.md), [apps-devhud-foundation](apps-devhud-foundation.md), [apps-delidev-app-foundation](apps-delidev-app-foundation.md), and affected `AGENTS.md` files for any service, RPC, message, enum, auth metadata, error, pagination, generated package, or compatibility change.

## References

- [Project devhud](project-devhud.md)
- [Deck server](servers-devhud-deck-foundation.md)
- [DevHud app](apps-devhud-foundation.md)
- [DeliDev app](apps-delidev-app-foundation.md)
- [Issue #755](https://github.com/delinoio/oss/issues/755)

## Out of Scope

- Public API/plugin SDK status, third-party clients, remote UI, GHES/custom hosts, server schedulers, production deployment, generated-client publication, GitHub App registration, and catalog activation.
