# servers-devhud-deck-foundation

## Scope

- Project/component: `devhud` / `deck-server`
- Canonical implementation path: `servers/devhud-deck`
- Status: bounded server foundation implemented for issue #755. The service
  directory implements dual-audience authentication, current DeliDev
  membership authorization, PostgreSQL migrations/sqlc persistence,
  encrypted view/device/current-snapshot storage, typed audits, health
  endpoints, view/device CRUD, query rewriting, limits, revisions,
  owner/lifecycle deletion, and the GitHub.com-only connection/provider slice.
  That slice implements signed App/OAuth callbacks, signed installation
  lifecycle webhooks, per-viewer encrypted user authorization credentials,
  installation ownership, authorization-filtered search and candidates,
  action metadata, and the closed PR mutation set. The search adapter remains
  unreachable from `RefreshView` while provider refresh and billing fail
  closed. Notification delivery, packaging, deployment, DNS, production
  secrets, registered GitHub Apps, catalog activation, and production
  operation remain unimplemented and unclaimed.
- Future canonical API origin and Logto audience: `https://deck.deli.dev`; documenting it does not create or activate the origin.
- Runtime: Go service with PostgreSQL, migrations, sqlc, Connect RPC, narrowly scoped HTTP handlers, and shared `servers/internal` infrastructure where its generic contracts apply.

## Users and Authorization

- The signed-out DevHud base shell never depends on Deck. A user must complete DeliDev Logto Authorization Code with PKCE before entering Deck.
- Human RPCs accept a Deck-audience bearer in `authorization` and a
  memory-only delibase-audience forwarded bearer only in
  `x-devhud-deck-forwarded-delibase-token`. The service validates issuer,
  audience, expiry, Deck procedure scope, forwarded
  `delibase:account:read`/`delibase:organizations:read`/`delibase:teams:read`
  scopes, and matching subjects and strips credentials before business
  handlers. `RegisterDevice` returns its opaque single-registration
  revocation grant only in
  `x-devhud-deck-device-revocation-grant` response metadata;
  `UnregisterDevice` additionally accepts only that same key as alternate
  request authorization. The grant authorizes no other procedure or
  registration. `DeleteFeatureData` alone also accepts the exact Deck lifecycle
  M2M identity used by delibase for a typed account/organization-deletion
  trigger. Its token must have the client-credentials shape
  (`sub == client_id`) and both values must equal the configured
  `DECK_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID` in addition to passing issuer,
  Deck audience, expiry, and lifecycle-scope validation; another M2M client with
  the same audience/scope is rejected. No other procedure accepts that identity.
  No credential may be logged. The registration record retains only a
  non-reversible verifier for the revocation grant; a separate
  application-level encrypted idempotency result may retain replay material only
  through the registration lease so an exact lost-response `RegisterDevice`
  retry can return the same grant, and must delete it on unregister or lease
  expiry. The client retains the grant only in the OS secure vault as described
  below.
- One DeliDev account is active per OS user/device. Personal views are managed
  by their owner. Organization Owners/Admins create, edit, and delete
  organization views; those management-only update/delete paths may open a
  retained definition after repository access is removed only when every
  inaccessible qualifier has exact keyed evidence from a signed repository
  removal event. Repository addition or installation replacement clears that
  evidence. This lets a manager repair or delete the view while every updated
  repository qualifier is reauthorized before persistence. Members may use
  views only when DeliDev membership and that member's own GitHub authorization
  both permit every underlying repository.
- Personal resources may select an accessible organization/team for billing. Organization resources bill their owning organization/team.
- Every synchronized mutation uses a revision/ETag. Stale writes fail with a typed conflict suitable for reload, compare, and reapply.

## GitHub.com Provider Boundary

- Deck uses its own minimal-permission GitHub App, separate from RealQA, and uses GitHub App user authorization tokens so reads and mutations are attributed to the current GitHub user.
- Only GitHub.com is accepted. GHES, GitHub Enterprise Server, custom hosts, and on-premises connectors fail closed.
- Permissions are limited to repository metadata, pull requests, checks, labels,
  assignees, requested reviewers, draft state, merge state, supported
  mutations, contents write only for merge/native auto-merge, organization
  members read for team-reviewer flows, and repository administration read
  only to enumerate teams with access to the repository.
- One installation binds to exactly one DeliDev personal or organization owner scope.
- Organization Owners/Admins may install, replace, or reconfigure that
  owner-scoped installation. An ordinary organization member may start only
  the user OAuth leg for the already-connected installation, which attaches
  that member's credential without changing connection metadata or state.
- Installation list rows expose the stable GitHub account ID, login, and closed
  user/organization kind so settings clients can identify each accessible
  installation without displaying an opaque provider installation ID.
- Connections may be managed from DevHud Deck settings and the existing DeliDev `/account` or `/o/:orgSlug/settings` sections. The DeliDev settings client may call only the four `DeckIntegrationService` RPCs and may navigate a returned authorization target only through its Deck-specific exact GitHub.com top-level browser validator; it must not consume views, pull-request data, mutations, devices, notifications, or widgets. No new top-level DeliDev route or generic navigation field is authorized.
- HTTP is limited to GitHub OAuth/App callbacks and installation-lifecycle webhooks. Provider webhooks must not refresh pull-request status.
- The implemented HTTP paths are exactly `/github/app/callback`,
  `/github/oauth/callback`, and `/github/webhooks`. App and OAuth callbacks use
  separate HMAC-authenticated opaque random handles whose expiring one-use
  account, owner, and current DeliDev GitHub-login bindings exist only in the
  encrypted server-side state record. The OAuth callback rejects a GitHub user
  whose current login does not match that initiating DeliDev identity.
  Webhooks require `X-Hub-Signature-256`, a bounded body, and an explicit
  delivery ID.
  Subscribed `installation`, `installation_repositories`, and
  `installation_target` events update only installation lifecycle state.
  Account-renamed target events update the stable installation binding,
  purge stale provider state, and invalidate repository indexes for repair.
  GitHub's mandatory
  `github_app_authorization` revocation event deletes every credential for the
  sending GitHub user. Keyed lifecycle tombstones serialize an
  `installation.deleted` event with a first connection and serialize a user
  revocation with callback/refresh credential upserts; only an authorization
  callback created after the recorded revocation may supersede that user's
  tombstone. Any delivered PR, check, or status event is accepted without
  state mutation and cannot refresh a view.
- The source-controlled test-only manifest is
  `servers/devhud-deck/testdata/github-app/manifest.json`. It requests only
  metadata read, administration read, contents write, pull-request write,
  checks read, and members read. Administration read is used only to enumerate
  repository teams for team-reviewer candidates; contents write is used only
  for merge/native auto-merge. The manifest explicitly subscribes only to
  installation lifecycle events; GitHub's mandatory user-authorization
  revocation webhook remains handled. The App is private and explicitly not a
  production registration. Member/team APIs are invoked only by an explicit
  team-reviewer candidate/action path.
- Disconnect immediately deletes provider tokens, cached PR results, notification state, and widget snapshots while retaining view definitions as disconnected records.
- GitHub App user authorization credentials retained for an active connection use application-level envelope encryption before PostgreSQL persistence: a fresh data-encryption key protects each credential record, only ciphertext plus wrapped key and versioned managed environment-scoped key ID are stored or backed up, and decrypt authority is limited to the provider adapter for the current authorized operation. Rotation must support decrypting old key versions and transactional rewrapping to the active key without exposing plaintext; database/storage encryption alone is insufficient. Plaintext credentials and unwrapped data keys are memory-only for the bounded provider call and never enter logs, traces, errors, audits, caches, or backups.
- Credentials are keyed by DeliDev account and connection. An organization
  installation never causes one member's user token to be reused for another
  member; the provider operation loads the current viewer's authorization and
  intersects current user authority with the installation permission set.
  Expired access tokens are rotated through their still-valid refresh token
  and the replacement access/refresh pair is envelope-encrypted and persisted
  before the provider operation continues. Missing, expired, revoked, or
  rejected refresh tokens require reauthorization; GitHub's
  `bad_refresh_token` response is classified as reauthentication rather than a
  generic provider failure.
- Authorization filtering occurs before identity-bearing results. Repository
  names, PR titles, counts, and query results must not be revealed to a
  DeliDev member whose GitHub identity cannot access the repository. Connected
  view-definition reads use a versioned keyed repository-qualifier index
  stored outside the encrypted query to decide current GitHub visibility
  before opening any view ciphertext. The sole exception is an authorized
  personal owner or organization Owner/Admin opening the definition through
  `UpdateView` or `DeleteView` to repair repository-removal fallout when every
  inaccessible qualifier is backed by exact keyed evidence from the signed
  repository-removal lifecycle; addition or installation replacement clears
  the evidence. An update still reauthorizes every repository in the resulting
  definition before it is persisted. Exact snapshot membership reads use a keyed
  repository-and-PR index after repository authorization, so they do not scan
  or decrypt unrelated retained repository references.
- The provider search adapter rechecks each result repository against both the
  selected owner-scoped installation and the current viewer's user
  authorization before returning any result. It returns only the filtered page
  count and never GitHub's upstream search `total_count`.

## Data and Query Contract

- Implement exactly the `DeckViewService`, `DeckIntegrationService`, and `DeckDeviceService` RPC sets in [protos-devhud-deck-api-contract](protos-devhud-deck-api-contract.md). Business mutations remain Connect RPC; only the provider callback/webhook handlers described above use HTTP.
- Persisted identifiers are UUID v7. Owner scope, view kind, sort, grouping, mutation kind, connection state, refresh outcome, notification transition, and freshness state are closed enums.
- The closed v1 view registry contains only `GITHUB_PULL_REQUESTS`. It is internal and source-controlled, not a public plugin SDK or remote-UI registry.
- A view contains owner scope and optional organization ID, billing organization/team, name, canonical raw GitHub search query, typed visual-builder representation, sort/grouping, notification preference, revision, and timestamps. `UpdateView` may change the notification preference but never owner or kind.
- `CreateView` requires a stable client-generated UUID v7 idempotency key scoped to the authenticated subject and operation. An exact replay returns the original view and revision without consuming another view-limit slot; reuse with changed creation input fails with the typed idempotency-conflict reason.
- The raw query is authoritative. The builder supports owner/repository, author, assignee, individual/team reviewer, label, open/closed/draft, base/head, review decision, checks, and updated range. Editing recognized fields rewrites their clauses while preserving unknown raw clauses. Relative clauses such as `@me` are evaluated for the current viewer, including organization views.
- Limits are 50 personal views, 250 views per organization, and 500 PR results per view with cursor pagination and an explicit truncated state. Copying or moving views between personal and organization scopes is excluded in v1.
- Sorting supports updated time, created time, attention, checks, or review state; grouping supports repository, author, reviewer, or status; default sorting is recently updated.
- Default PR result rows contain repository, number, title, author,
  individual/team reviewer identities, current assignees and labels, review
  decision, checks, mergeability, explicit open/closed/merged lifecycle state,
  independent draft state, updated time, the synchronized PR revision required
  by mutations, and the currently supported mutations/available merge methods.
  Reviewer identities make reviewer grouping deterministic; current assignees
  and labels populate removal controls; and action metadata is server-authored
  before a client constructs a mutation. For advertised assign-user,
  request-reviewer, and add-label actions,
  `ListPullRequestMutationCandidates` provides a permission-filtered,
  opaque-cursor user/team/label search plus the synchronized PR revision;
  unsupported mutation kinds fail closed, and candidate reads neither mutate
  nor refresh the PR.
- Mutations are limited to assign/unassign users; request/remove individual or team reviewers; add/remove labels; mark draft/ready; close/reopen; merge; and enable/cancel GitHub native auto-merge. Merge requires explicit confirmation and respects current-user permission, repository rules, branch protection, and available merge methods. When GitHub accepts a mutation but the immediate result reload fails, the RPC returns success with `refresh_required` and omits stale pull-request detail so clients refresh instead of retrying the provider side effect. Commenting, approving, and requesting changes open GitHub and are not Deck mutations.
- A single mutation accepts at most 10 assignee operands, 100 combined
  reviewer user/team operands, or 100 label operands, so provider mutations
  cannot fan out into unbounded calls.

## Client-Initiated Refresh and Billing

- The service must not contain a scheduler, durable refresh job, or worker that initiates or continues GitHub PR polling.
- Every refresh is traceable to a running desktop tray process, an open mobile app, an OS-permitted client background task, or an explicit app/shortcut/widget action.
- Automatic client refresh is limited to views attached to a widget, notification, or shortcut, or opened within the previous 30 days. Other views refresh on open or manually.
- Clients may target five-minute polling while permitted to remain active. Automatic/widget requests for the same viewer/view coalesce into one provider request during a five-minute server cache window. Manual refresh bypasses the cache and must display the billed-provider-call warning returned by `GetRefreshPreflight`. Every `RefreshView` logical request uses a stable client-generated UUID v7 identity that the client preserves across ambiguous retries; exact replay returns the first result and billing disposition, while changed view/origin/cache input conflicts.
- When all clients stop, PR state, widgets, and notifications become stale and the service performs no later provider refresh. Widget execution is OS-controlled and no five-minute freshness guarantee is allowed.
- Cache hits are free. The future disabled delibase catalog record is identified by app key `devhud` plus meter key `deck_github_pull_request_refresh`, uses unit name `provider_refresh` with precision zero, is allowlisted only to the Deck service identity, and has an effective unit price of exactly 50 USD micros. `GetRefreshPreflight` validates that authoritative identity, unit, service mapping, and effective price for every prospective origin without reserving usage, dispatching GitHub, refreshing a cache, or charging. It returns the server-derived price plus an opaque short-lived preflight token bound to the authenticated subject, view, billing scope, prospective `RefreshView` identity, origin, validated catalog version, and expiry; a missing, disabled, or divergent mapping fails unavailable before any attempt or manual warning. Every `RefreshView` first looks up the durable attempt by authenticated identity and request digest. An existing exact attempt returns or resumes independently of later token expiry or catalog change, while changed input conflicts. Creation of every new attempt requires and revalidates the unexpired token, bound origin, and current mapping before reservation/provider dispatch, so a caller cannot select a different refresh origin to bypass billing preflight and missing, expired, substituted, or stale state fails closed before new work. One actual GitHub provider refresh reserves and commits exactly one unit: reserve before dispatch, release when no provider request was dispatched, and commit once dispatched, including provider errors, rate limits, and timeouts. A durable attempt keyed by the `RefreshView` identity serializes reservation, provider dispatch, and commit/release, records whether dispatch occurred before exposing a result, and resumes ambiguous downstream outcomes without issuing or billing a second provider request. Failed reservation also prevents dispatch. Changing the unit or price requires a synchronized delibase/Deck contract and catalog change.
- Deck implementation and catalog activation are blocked until a synchronized additive delibase API/server change makes live-reservation finalization independent of the originating forwarded user bearer. A successful `ReserveUsage` must issue an opaque finalization grant bound to the exact reservation ID, authenticated Deck service identity, reserved maximum, and reservation expiry; delibase retains only a non-reversible verifier and accepts the grant only with a fresh same-service M2M bearer for idempotent `CommitUsage` or `ReleaseUsage` of that reservation. The grant cannot reserve usage, change payer/meter/units, authorize provider work, or finalize another service's reservation. Deck application-level envelope-encrypts the grant in the durable refresh attempt, never logs or returns it, and deletes its ciphertext after terminal commit/release or observed expiry. A billing-only recovery worker claims an unfinished attempt after request loss or restart and retries its stable commit/release operation without a client request, a forwarded bearer, or another GitHub call. Until that bounded grant contract and a minimum 86,400-second Deck reservation TTL are implemented and validated, Deck must fail startup before billed refresh handling and the meter must remain disabled.
- Limits are 12 manual refresh requests/minute/user, 30 PR mutations/minute/user, and four concurrent GitHub provider requests/installation.
- Deck catalog records, including the identity and mapping above, remain disabled in production-facing artifacts until a separate activation change; this contract does not add them to the current empty production catalog.

## Devices, Notifications, and Widgets

- The server synchronizes Deck views for desktop, iOS, Android, tray, shortcuts, notifications, WidgetKit, and Android widgets. Client/native code remains under `apps/devhud`.
- Up to 20 per-view shortcut definitions synchronize per account/device. Device writes accept request-only configurations with closed modifier/key enums and no client-authored effective state or revision; the server returns synchronized state after the client uses the unified DevHud conflict registry to mark unavailable bindings inactive without replacing another binding.
- Notification preferences are opt-in per view for viewer assignment/review request, check failure, becoming mergeable, conflict, and merged/closed. Respect OS Do Not Disturb; v1 adds no quiet-hours system.
- Default text is exactly `Deck view updated`. Detailed repository/PR titles require per-device opt-in. Push payloads contain opaque event identifiers only. An online authenticated client resolves an event through `ResolveNotificationEvent`; the service requires the matching active account/device registration, rechecks current view and repository authorization, and returns identity-bearing detail only when that device opted in. Offline, signed-out, expired, disconnected, unauthorized, or unresolvable events use only the generic text and never trigger provider refresh.
- Device push registrations have bounded server-issued leases renewed only by an authenticated matching account/device. Authenticated `GetDevice` reloads the caller's current registration, lease, server-authored shortcut/widget state, and device revision by stable device ID after restart or stale conflict. Each logical `RegisterDevice` creation or renewal uses a stable client-generated UUID v7 idempotency key. Initial creation omits `expected_revision`; renewal requires the current device revision and rejects a stale request before replacing any mutable device configuration. The durable result binds the authenticated account/device and request digest; while the original lease remains valid, exact replay returns the same registration ID, lease, and revocation grant through dedicated sensitive response metadata without creating another registration, while changed-input reuse conflicts. The opaque grant is bound to that account/device/registration, is usable only as `UnregisterDevice` authorization metadata, and remains valid through the registration lease. Logout and `Reset DevHud` call the idempotent `UnregisterDevice` before deleting general credentials. If that call is offline or ambiguous, the client disables the local registration, deletes its platform push token, and keeps a cleanup tombstone containing only the registration ID, lease expiry, and revocation grant in the OS secure vault. It retries with that grant before any later registration, including after another account signs in; terminal unregister/absent-registration success or observed lease expiry removes the tombstone and grant. An opaque event is never displayed without the matching active local account/device binding.
- Each widget selects one view. Supported families are WidgetKit small/medium/large for iPhone and macOS and responsive Android compact/wide/list. New widgets show counts only; repository and PR titles require per-widget privacy opt-in. Device registration/update accepts only widget identity, selected view, family, and privacy configuration.
- Widget actions open a view/PR or request refresh and never mutate a PR. The server alone authors synchronized widget revisions and the minimal encrypted widget snapshot with freshness/offline state; clients cannot submit snapshot contents. This snapshot is the only offline PR-data exception. The regular Deck app shows a connection/offline state rather than a cached PR list.

## Storage, Retention, and Deletion

- Retain only current matching PR snapshots, notification event/delivery history for 30 days, and view definitions until deletion.
- Logout revokes the device push registration as described above, then deletes Deck tokens, PR data, and widget snapshots from that device.
- `Reset DevHud` revokes the device push registration as described above, then remains device-local: it deletes tokens, Deck snapshots, and shortcut effective state and does not delete server views or GitHub connections.
- Disconnect follows the immediate provider-data deletion boundary above and preserves disconnected view definitions.
- `DeckViewService.DeleteFeatureData` is the only feature-deletion mutation. In owner-request mode it is idempotent by authenticated subject, operation, and owner scope: a personal caller may delete only their personal Deck data, while an organization Owner may delete organization Deck data. In lifecycle mode it requires the exact Deck-scoped delibase M2M identity, accepts only a typed account or organization target plus the immutable delibase deletion-job UUID, and treats that UUID as its replay identity; DeliDev and ordinary feature users cannot select this mode.
- The first accepted request in either mode immediately tombstones the target scope, blocks new scope access/mutations, and starts asynchronous hard deletion of all data owned by that scope, including view definitions and attached connection/provider, cache, notification, widget, and shortcut data. Exact replays return the same deletion-job result, absent feature data succeeds idempotently, and only required pseudonymized financial/security records survive. Delibase transactionally enqueues the lifecycle call when it accepts account/organization deletion and retries ambiguous or failed delivery through its immutable retained outbox/dead-letter contract.

## Security and Observability

- Remote client telemetry remains prohibited. The service may use redacted structured logs, metrics, traces, and audit events for operations and authorization.
- Never persist or log feature/delibase bearer tokens, authorization headers, URLs, push content, or user content outside the explicit data contract. The sole provider-token persistence exception is the envelope-encrypted active-connection credential record defined above; the bounded server-side grant exceptions are the encrypted `RegisterDevice` replay result and the encrypted single-reservation billing-finalization grant defined above. Canonical raw queries and identity-bearing typed builder clauses may persist only in view definitions, and repository names, PR titles, PR authors, reviewer and assignee identities, and labels may persist only in current matching PR snapshots; encrypt those fields at rest with managed environment-scoped keys, authorize every read before decryption, and delete them at the retention boundaries above. Never place credentials or those fields in logs, telemetry, traces, audits, notification history, uncontracted caches, or plaintext backups. Audit only typed safe decisions and pseudonymized actors.
- Use least-privilege database/provider identities, fail-closed authorization, CSRF/state validation for callbacks, webhook signature validation, rate/concurrency limits, and explicit safe error enums.
- DevHud reaches Connect through its private native transport, not browser fetch. Do not allow `http://tauri.localhost` in CORS. The exact `https://deli.dev` browser origin may call only `DeckIntegrationService`; all other procedures reject browser-origin requests.
- Measure refresh latency, query latency, mutation latency, and widget snapshot size in CI/fixtures only. This contract defines no production SLO, alert threshold, dashboard, or telemetry pipeline.

## Build and Test

The shared wire contract already runs its package-local checks. Once the server
implementation exists, canonical server checks are:

- `gofmt`/format verification for `servers/devhud-deck`;
- `go vet ./servers/devhud-deck/...`;
- `go test ./servers/devhud-deck/...`;
- sqlc reproducibility and migration checks;
- PostgreSQL integration/concurrency tests;
- mock GitHub App callback, webhook, permission, rate-limit, and provider tests;
- non-root `linux/amd64` and `linux/arm64` image validation with SBOM and signature/attestation verification.

The implemented bounded foundation currently runs the format, vet, unit,
sqlc reproducibility, ordered-migration, redaction, PostgreSQL integration,
signed callback/webhook, permission intersection, GitHub rate/concurrency,
search non-disclosure, candidate, and mutation fixture checks. Billing,
notification-delivery, and image checks remain blocked with their
corresponding RPCs failing closed; those bullets do not claim implementation.

Coverage must include unknown-clause preservation, per-viewer `@me`, repository non-disclosure, limits/pagination/truncation, deterministic reviewer grouping, current assignee/label removal operands, explicit PR lifecycle state, pre-mutation action metadata, stale revisions, multi-device coalescing and catalog-bound billing, non-dispatching manual quote success/cancel/expiry/substitution/catalog-change handling, refresh exact replay before and after quote expiry/catalog change, changed-input conflict, lost-response recovery without duplicate provider dispatch or charge, crash-after-dispatch billing recovery without a client retry or second provider request, finalization-grant reservation/service/unit/operation/expiry substitution rejection and encrypted terminal deletion, minimum Deck reservation-TTL rejection, disabled/mismatched meter rejection before warning or provider dispatch, outbound live-usage client-credentials acquisition/startup failure and service-binding rejection, provider timeout charging, reservation failure, zero refresh after all clients stop, every supported mutation and merge confirmation, widget privacy/staleness, DND-safe generic/detailed notification resolution and authorization loss, `RegisterDevice` exact replay/changed-input conflict/lost-response recovery with the identical lease/grant, stale-renewal rejection, and bounded encrypted replay-material deletion, single-purpose revocation-grant cleanup across account switches and lease expiry, shortcut conflicts, provider-credential and query/builder/snapshot envelope encryption/rotation with ciphertext-only backups, redaction, disconnect/logout/reset, exact lifecycle-client pinning and peer-M2M rejection, owner and delibase-lifecycle deletion authorization, deletion-job replay/absent data, browser-origin rejection outside exact DeliDev `DeckIntegrationService`, GitHub.com-only rejection, and a fixture GitHub App.

These checks validate artifacts only. They must not push GHCR images, deploy the API, configure DNS, create production secrets, register a GitHub App, enable catalog records, publish widgets, release an app, or begin operations.

## Dependencies and Integrations

- Implemented private wire contract:
  [protos-devhud-deck-api-contract](protos-devhud-deck-api-contract.md).
- Client/native contract: [apps-devhud-foundation](apps-devhud-foundation.md).
- The DeliDev settings client contract is [apps-delidev-app-foundation](apps-delidev-app-foundation.md) and is limited to `DeckIntegrationService`.
- Shared service utilities may come from `servers/internal`; Deck business policy remains under `servers/devhud-deck`.
- External boundaries are Logto/DeliDev authentication, delibase reservation/commit/release plus its durable account/organization-deletion lifecycle calls, PostgreSQL, the separate Deck GitHub App on GitHub.com, and opaque push delivery. The canonical API origin is future and inactive.
- Live refresh billing has a dedicated outbound-only delibase configuration set: required non-secret `DECK_DELIBASE_API_ORIGIN`, `DECK_DELIBASE_LOGTO_AUDIENCE`, `DECK_DELIBASE_SERVICE_IDENTITY_ID`, and `DECK_DELIBASE_LOGTO_M2M_CLIENT_ID`, plus required secret `DECK_DELIBASE_LOGTO_M2M_CLIENT_SECRET`. In production, origin and audience are exactly `https://delibase.deli.dev`; the service identity is the stable UUID v7 authorization target published for the Deck meter; and the Logto client must be the delibase service identity bound to that target. Deck obtains short-lived OAuth 2 client-credentials access tokens from its validated exact Logto issuer with only the live-usage scopes and caches them in memory only until bounded expiry. Initial `ReserveUsage` combines that M2M bearer with the current request's memory-only forwarded user bearer; after reservation, `CommitUsage` or `ReleaseUsage` uses either the current forwarded bearer or the exact bounded finalization grant above with a fresh same-service M2M bearer. Once billed refresh exists, startup fails before accepting refresh requests when this set is absent, partial, malformed, targets the wrong origin/audience, names an invalid service UUID, cannot acquire the required scoped token, or delibase lacks the finalization-grant capability; delibase still rejects any token-to-service or grant-to-reservation mapping mismatch before mutation. Neither secret nor token is logged, persisted, baked into an image, or exposed to a client, and the finalization grant has only the explicitly bounded encrypted persistence exception above.
- `DECK_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID` is a required non-secret receiver-side pin once lifecycle mode is implemented. It must exactly equal delibase's outbound `DELIBASE_DECK_LIFECYCLE_LOGTO_M2M_CLIENT_ID`; Deck stores no corresponding client secret. Startup fails before serving if the lifecycle handler exists and this value is absent or malformed, so lifecycle authorization never falls back to audience/scope alone.
- The implemented provider startup set is
  `DECK_GITHUB_APP_CLIENT_ID`, `DECK_GITHUB_APP_CLIENT_SECRET`,
  `DECK_GITHUB_APP_SLUG`, base64-encoded `DECK_GITHUB_WEBHOOK_SECRET`, and
  base64-encoded `DECK_GITHUB_CALLBACK_SIGNING_KEY`. Missing, malformed, or
  host-substituting values fail startup. These values configure only the
  GitHub.com adapter and do not register an App.
- Envelope encryption startup additionally requires
  `DECK_ENCRYPTION_KEY_ID` and base64-encoded `DECK_ENCRYPTION_KEY`.
  `DECK_ENCRYPTION_PREVIOUS_KEYS` may contain a JSON object from retained
  managed key IDs to base64-encoded 32-byte wrapping keys during rotation.
  Startup transactionally rewraps retained GitHub credential data keys under
  the active key ID before serving; an unavailable retained key fails startup.

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
