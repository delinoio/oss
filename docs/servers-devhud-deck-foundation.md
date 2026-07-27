# servers-devhud-deck-foundation

## Scope

- Project/component: `devhud` / `deck-server`
- Canonical implementation path: `servers/devhud-deck`
- Status: planned contract for issue #755; no service directory, deployment, DNS, production secret, registered GitHub App, published image, enabled catalog record, or production operation is claimed.
- Future canonical API origin and Logto audience: `https://deck.deli.dev`; documenting it does not create or activate the origin.
- Runtime: Go service with PostgreSQL, migrations, sqlc, Connect RPC, narrowly scoped HTTP handlers, and shared `servers/internal` infrastructure where its generic contracts apply.

## Users and Authorization

- The signed-out DevHud base shell never depends on Deck. A user must complete DeliDev Logto Authorization Code with PKCE before entering Deck.
- Human RPCs accept a Deck-audience bearer and a memory-only delibase-audience forwarded bearer. The service validates issuer, audience, expiry, scopes, and matching subjects and strips credentials before business handlers. `DeleteFeatureData` alone also accepts the exact Deck lifecycle M2M identity used by delibase for a typed account/organization-deletion trigger. No other procedure accepts that identity, and no credential may be stored or logged.
- One DeliDev account is active per OS user/device. Personal views are managed by their owner. Organization Owners/Admins create, edit, and delete organization views; members may use them only when DeliDev membership and that member's own GitHub authorization both permit every underlying repository.
- Personal resources may select an accessible organization/team for billing. Organization resources bill their owning organization/team.
- Every synchronized mutation uses a revision/ETag. Stale writes fail with a typed conflict suitable for reload, compare, and reapply.

## GitHub.com Provider Boundary

- Deck uses its own minimal-permission GitHub App, separate from RealQA, and uses GitHub App user authorization tokens so reads and mutations are attributed to the current GitHub user.
- Only GitHub.com is accepted. GHES, GitHub Enterprise Server, custom hosts, and on-premises connectors fail closed.
- Permissions are limited to repository metadata, pull requests, checks, labels, assignees, requested reviewers, draft state, merge state, supported mutations, and member/team read only when resolving team reviewers.
- One installation binds to exactly one DeliDev personal or organization owner scope.
- Connections may be managed from DevHud Deck settings and the existing DeliDev `/account` or `/o/:orgSlug/settings` sections. The DeliDev settings client may call only the four `DeckIntegrationService` RPCs; it must not consume views, pull-request data, mutations, devices, notifications, or widgets. No new top-level DeliDev route is authorized.
- HTTP is limited to GitHub OAuth/App callbacks and installation-lifecycle webhooks. Provider webhooks must not refresh pull-request status.
- Disconnect immediately deletes provider tokens, cached PR results, notification state, and widget snapshots while retaining view definitions as disconnected records.
- Authorization filtering occurs before identity-bearing results. Repository names, PR titles, counts, and query results must not be revealed to a DeliDev member whose GitHub identity cannot access the repository.

## Data and Query Contract

- Implement exactly the `DeckViewService`, `DeckIntegrationService`, and `DeckDeviceService` RPC sets in [protos-devhud-deck-api-contract](protos-devhud-deck-api-contract.md). Business mutations remain Connect RPC; only the provider callback/webhook handlers described above use HTTP.
- Persisted identifiers are UUID v7. Owner scope, view kind, sort, grouping, mutation kind, connection state, refresh outcome, notification transition, and freshness state are closed enums.
- The closed v1 view registry contains only `GITHUB_PULL_REQUESTS`. It is internal and source-controlled, not a public plugin SDK or remote-UI registry.
- A view contains owner scope and optional organization ID, billing organization/team, name, canonical raw GitHub search query, typed visual-builder representation, sort/grouping, notification preference, revision, and timestamps.
- The raw query is authoritative. The builder supports owner/repository, author, assignee, individual/team reviewer, label, open/closed/draft, base/head, review decision, checks, and updated range. Editing recognized fields rewrites their clauses while preserving unknown raw clauses. Relative clauses such as `@me` are evaluated for the current viewer, including organization views.
- Limits are 50 personal views, 250 views per organization, and 500 PR results per view with cursor pagination and an explicit truncated state. Copying or moving views between personal and organization scopes is excluded in v1.
- Sorting supports updated time, created time, attention, checks, or review state; grouping supports repository, author, reviewer, or status; default sorting is recently updated.
- Default PR results/details contain repository, number, title, author, review decision, checks, mergeability, draft state, and updated time. Additional provider metadata is fetched only for the action that needs it.
- Mutations are limited to assign/unassign users; request/remove individual or team reviewers; add/remove labels; mark draft/ready; close/reopen; merge; and enable/cancel GitHub native auto-merge. Merge requires explicit confirmation and respects current-user permission, repository rules, branch protection, and available merge methods. Commenting, approving, and requesting changes open GitHub and are not Deck mutations.

## Client-Initiated Refresh and Billing

- The service must not contain a scheduler, durable refresh job, or worker that initiates or continues GitHub PR polling.
- Every refresh is traceable to a running desktop tray process, an open mobile app, an OS-permitted client background task, or an explicit app/shortcut/widget action.
- Automatic client refresh is limited to views attached to a widget, notification, or shortcut, or opened within the previous 30 days. Other views refresh on open or manually.
- Clients may target five-minute polling while permitted to remain active. Automatic/widget requests for the same viewer/view coalesce into one provider request during a five-minute server cache window. Manual refresh bypasses the cache and must carry a billed-provider-call warning.
- When all clients stop, PR state, widgets, and notifications become stale and the service performs no later provider refresh. Widget execution is OS-controlled and no five-minute freshness guarantee is allowed.
- Cache hits are free. An actual GitHub provider refresh costs 50 USD micros: reserve delibase usage before dispatch; release when no provider request was dispatched; commit once dispatched, including provider errors, rate limits, and timeouts. Failed reservation prevents dispatch.
- Limits are 12 manual refresh requests/minute/user, 30 PR mutations/minute/user, and four concurrent GitHub provider requests/installation.
- Deck catalog records remain stable and disabled in production-facing artifacts until a separate activation change.

## Devices, Notifications, and Widgets

- The server synchronizes Deck views for desktop, iOS, Android, tray, shortcuts, notifications, WidgetKit, and Android widgets. Client/native code remains under `apps/devhud`.
- Up to 20 per-view shortcut definitions synchronize per account/device; the client registers them through one unified DevHud conflict registry and marks unavailable bindings inactive without replacing another binding.
- Notification preferences are opt-in per view for viewer assignment/review request, check failure, becoming mergeable, conflict, and merged/closed. Respect OS Do Not Disturb; v1 adds no quiet-hours system.
- Default text is exactly `Deck view updated`. Detailed repository/PR titles require per-device opt-in. Push payloads contain opaque event identifiers only.
- Device push registrations have bounded server-issued leases renewed only by an authenticated matching account/device. Logout and `Reset DevHud` call the idempotent `UnregisterDevice` before local credential deletion. If that call is offline or ambiguous, the client disables the local registration, deletes its platform push token, retains a credential-free cleanup tombstone, and retries unregister before any later registration; an opaque event is never displayed without the matching active local account/device binding.
- Each widget selects one view. Supported families are WidgetKit small/medium/large for iPhone and macOS and responsive Android compact/wide/list. New widgets show counts only; repository and PR titles require per-widget privacy opt-in.
- Widget actions open a view/PR or request refresh and never mutate a PR. The only offline PR-data exception is a minimal encrypted widget snapshot with freshness/offline state. The regular Deck app shows a connection/offline state rather than a cached PR list.

## Storage, Retention, and Deletion

- Retain only current matching PR snapshots, notification event/delivery history for 30 days, and view definitions until deletion.
- Logout revokes the device push registration as described above, then deletes Deck tokens, PR data, and widget snapshots from that device.
- `Reset DevHud` revokes the device push registration as described above, then remains device-local: it deletes tokens, Deck snapshots, and shortcut effective state and does not delete server views or GitHub connections.
- Disconnect follows the immediate provider-data deletion boundary above and preserves disconnected view definitions.
- `DeckViewService.DeleteFeatureData` is the only feature-deletion mutation. In owner-request mode it is idempotent by authenticated subject, operation, and owner scope: a personal caller may delete only their personal Deck data, while an organization Owner may delete organization Deck data. In lifecycle mode it requires the exact Deck-scoped delibase M2M identity, accepts only a typed account or organization target plus the immutable delibase deletion-job UUID, and treats that UUID as its replay identity; DeliDev and ordinary feature users cannot select this mode.
- The first accepted request in either mode immediately tombstones the target scope, blocks new scope access/mutations, and starts asynchronous hard deletion of all data owned by that scope, including view definitions and attached connection/provider, cache, notification, widget, and shortcut data. Exact replays return the same deletion-job result, absent feature data succeeds idempotently, and only required pseudonymized financial/security records survive. Delibase transactionally enqueues the lifecycle call when it accepts account/organization deletion and retries ambiguous or failed delivery through its immutable retained outbox/dead-letter contract.

## Security and Observability

- Remote client telemetry remains prohibited. The service may use redacted structured logs, metrics, traces, and audit events for operations and authorization.
- Never persist or log bearer/provider tokens, authorization headers, URLs, push content, or user content outside the explicit data contract. Canonical raw queries may persist only in view definitions, and repository names/PR titles may persist only in current matching PR snapshots; encrypt those fields at rest with managed environment-scoped keys, authorize every read before decryption, and delete them at the retention boundaries above. Never place those fields in logs, telemetry, traces, audits, notification history, or uncontracted caches. Audit only typed safe decisions and pseudonymized actors.
- Use least-privilege database/provider identities, fail-closed authorization, CSRF/state validation for callbacks, webhook signature validation, rate/concurrency limits, and explicit safe error enums.
- DevHud reaches Connect through its private native transport, not browser fetch. Do not allow `http://tauri.localhost` in CORS. The exact `https://deli.dev` browser origin may call only `DeckIntegrationService`; all other procedures reject browser-origin requests.
- Measure refresh latency, query latency, mutation latency, and widget snapshot size in CI/fixtures only. This contract defines no production SLO, alert threshold, dashboard, or telemetry pipeline.

## Build and Test

Once implementation exists, canonical server checks are:

- `gofmt`/format verification for `servers/devhud-deck`;
- `go vet ./servers/devhud-deck/...`;
- `go test ./servers/devhud-deck/...`;
- sqlc reproducibility and migration checks;
- PostgreSQL integration/concurrency tests;
- mock GitHub App callback, webhook, permission, rate-limit, and provider tests;
- non-root `linux/amd64` and `linux/arm64` image validation with SBOM and signature/attestation verification.

Coverage must include unknown-clause preservation, per-viewer `@me`, repository non-disclosure, limits/pagination/truncation, stale revisions, multi-device coalescing and billing, provider timeout charging, reservation failure, zero refresh after all clients stop, every supported mutation and merge confirmation, widget privacy/staleness, DND-safe notifications, shortcut conflicts, redaction, disconnect/logout/reset, owner and delibase-lifecycle deletion authorization, deletion-job replay/absent data, browser-origin rejection outside exact DeliDev `DeckIntegrationService`, GitHub.com-only rejection, and a fixture GitHub App.

These checks validate artifacts only. They must not push GHCR images, deploy the API, configure DNS, create production secrets, register a GitHub App, enable catalog records, publish widgets, release an app, or begin operations.

## Dependencies and Integrations

- Wire contract: [protos-devhud-deck-api-contract](protos-devhud-deck-api-contract.md).
- Client/native contract: [apps-devhud-foundation](apps-devhud-foundation.md).
- The DeliDev settings client contract is [apps-delidev-app-foundation](apps-delidev-app-foundation.md) and is limited to `DeckIntegrationService`.
- Shared service utilities may come from `servers/internal`; Deck business policy remains under `servers/devhud-deck`.
- External boundaries are Logto/DeliDev authentication, delibase reservation/commit/release plus its durable account/organization-deletion lifecycle calls, PostgreSQL, the separate Deck GitHub App on GitHub.com, and opaque push delivery. The canonical API origin is future and inactive.

## Change Triggers

- Update this document, [project-devhud](project-devhud.md), the Deck proto and app contracts, docs catalogs, and root/apps/servers/protos `AGENTS.md` files for service, provider, auth, refresh, billing, data, deletion, origin, validation, or activation changes.
- Server-initiated polling, another provider, another view kind, public plugins, remote UI, new HTTP handlers, catalog enablement, or operational rollout requires a separate explicit contract change.

## References

- [Project devhud](project-devhud.md)
- [Deck API](protos-devhud-deck-api-contract.md)
- [DevHud app](apps-devhud-foundation.md)
- [Issue #755](https://github.com/delinoio/oss/issues/755)

## Out of Scope

- GHES/custom GitHub hosts/on-premises connectors.
- Server-scheduled or server-initiated PR polling.
- Public plugin SDKs, third-party views, remotely supplied UI, and arbitrary remote assets.
- GitHub comments, review approvals/change requests, or personal/organization view transfer/copy.
- Guaranteed five-minute widget refresh and offline PR lists beyond the minimal encrypted widget snapshot.
- Production deployment, DNS, GitHub App registration, catalog activation, GHCR/image publication, widget/store publication, production SLOs/alerts, or rollout.
