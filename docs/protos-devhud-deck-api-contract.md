# protos-devhud-deck-api-contract

## Scope

- Project/component: `devhud` / `deck-api`
- Canonical source path: `protos/devhud-deck/v1`
- Contract identity: `devhud.deck.v1`
- Status: planned for issue #755; no source, generated package, deployed API, or published client is claimed.

## Runtime and Language

- Versioned Protobuf is authoritative.
- Generate reproducible Connect-compatible Go and protobuf-es v2 TypeScript artifacts under `protos/devhud-deck/gen/go` and `protos/devhud-deck/gen/ts`; generated files are derived and never a second contract.
- The future workspace TypeScript package is `@delinoio/devhud-deck-connect`.
- Preserve released v1 fields additively. Breaking changes require `devhud.deck.v2` and synchronized consumer migration docs.

## Services and RPCs

- `DeckViewService`: `ListViews`, `GetView`, `CreateView`, `UpdateView`, `DeleteView`, `ListPullRequests`, `RefreshView`, `MutatePullRequest`, `DeleteFeatureData`.
- `DeckIntegrationService`: `GetGitHubConnection`, `StartGitHubConnection`, `ListGitHubInstallations`, `DisconnectGitHubConnection`.
- `DeckDeviceService`: `RegisterDevice`, `UpdateDevice`, `UnregisterDevice`, `UpdateViewNotificationPreference`.

No additional v1 service or RPC is implied. GitHub callback/webhook handlers and push delivery are HTTP/server boundaries, not public Connect services.

## Wire Contract

- IDs are UUID v7 wrappers. Owner scope, view kind, sort, grouping, mutation kind, connection state, refresh outcome, notification transition, freshness state, and stable error reason are closed enums.
- The only v1 view kind is `GITHUB_PULL_REQUESTS`.
- Views carry owner/billing scope, name, canonical raw query, typed builder clauses, sort/grouping, notification preference, revision/ETag, and timestamps.
- Lists use opaque cursor pagination. Limits are 50 personal views, 250 organization views, and 500 PR results per view with explicit truncation.
- PR results/details carry only repository, number, title, author, review decision, checks, mergeability, draft state, and updated time by default.
- Mutations are a closed union for assign/unassign, reviewer request/removal, label add/remove, draft/ready, close/reopen, merge, and native auto-merge enable/cancel. Merge requests carry explicit user confirmation.
- Refresh requests distinguish automatic/widget/manual origin, client request identity, and cache behavior. Responses expose freshness, outcome, cache/coalescing state, and billing disposition without exposing provider credentials.
- Device registration returns a bounded server lease and an opaque registration ID. `UnregisterDevice` is idempotent and revokes delivery for the authenticated account/device; a new or renewed registration is rejected while a credential-free cleanup tombstone for that device remains pending. Opaque push events are usable only by the matching active local account/device registration.
- `DeleteFeatureData` carries a personal or organization owner scope plus an idempotency key scoped to the authenticated subject, operation, and owner scope. A personal caller may delete only their own Deck data; organization deletion requires an Owner. The first accepted request immediately blocks new scope access and mutations and starts asynchronous hard deletion of all data owned by that scope, including view definitions and attached connection/provider, cache, notification, widget, and shortcut data. Exact replays return the same deletion-job result while only required pseudonymized financial/security records survive.
- Every mutation that changes synchronized data carries the expected revision and returns a new revision. Stable conflict details support reload/compare/reapply.
- Authenticated messages identify personal/organization/team resources by IDs only; client-provided roles, repository permission, billing authority, provider access, and price are never authoritative.

## Authentication, Privacy, and Errors

- Protected RPCs require the Deck-audience user token plus the dedicated memory-only delibase-audience forwarded bearer metadata defined by the server contract. Generated clients must treat both as sensitive and keep them out of messages, logs, errors, caches, persistence, and diagnostics.
- The server verifies matching subjects, DeliDev role/scope, and the viewer's GitHub permission. Error enums distinguish authentication, authorization, provider permission, stale revision, limits, truncation, rate/concurrency limits, billing reservation, provider failure/rate limit/timeout, offline/stale, disconnected, and unsupported host/action.
- Messages must not carry GitHub tokens, webhook secrets, raw authorization headers, production secrets, or sensitive push payloads.
- The API transports data, not UI definitions. It must not expose HTML, JavaScript, component trees, plugin manifests, arbitrary URLs, or runtime code.

## Build and Test

Once implementation exists, canonical checks are:

- `pnpm generate:proto`;
- `pnpm check:proto`;
- `go test ./protos/devhud-deck/...`;
- `go vet ./protos/devhud-deck/...`;
- `pnpm --filter @delinoio/devhud-deck-connect typecheck`.

Generation must lint, check the immutable descriptor baseline, generate Go/TypeScript artifacts, build the TypeScript package, and reject a generated diff. CI fails on compatibility, service/RPC/enum drift, stale artifacts, sensitive metadata leakage, or missing cross-consumer generation.

Checks do not publish the TypeScript package, deploy `https://deck.deli.dev`, register the GitHub App, activate a catalog entry, or publish a server image.

## Dependencies and Change Triggers

- Owned by `devhud`; consumed by `servers/devhud-deck`, the authenticated Deck client under `apps/devhud`, and the authenticated DeliDev settings client under `apps/delidev-app` only for `DeckIntegrationService`.
- Update this document, [project-devhud](project-devhud.md), [servers-devhud-deck-foundation](servers-devhud-deck-foundation.md), [apps-devhud-foundation](apps-devhud-foundation.md), [apps-delidev-app-foundation](apps-delidev-app-foundation.md), and affected `AGENTS.md` files for any service, RPC, message, enum, auth metadata, error, pagination, generated package, or compatibility change.

## References

- [Project devhud](project-devhud.md)
- [Deck server](servers-devhud-deck-foundation.md)
- [DevHud app](apps-devhud-foundation.md)
- [DeliDev app](apps-delidev-app-foundation.md)
- [Issue #755](https://github.com/delinoio/oss/issues/755)

## Out of Scope

- Public API/plugin SDK status, third-party clients, remote UI, GHES/custom hosts, server schedulers, production deployment, generated-client publication, GitHub App registration, and catalog activation.
