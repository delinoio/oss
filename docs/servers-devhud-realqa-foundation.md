# servers-devhud-realqa-foundation

## Scope

- Project/component: `devhud` / `realqa-server`
- Canonical implementation path: `servers/devhud-realqa`
- Status: planned contract for issue #757; no service directory, deployment, DNS, R2 production bucket, production secret, registered GitHub App, published image/extension, enabled catalog record, or production operation is claimed.
- Future canonical API origin and Logto audience: `https://realqa.deli.dev`; future public-image origin: `https://assets.realqa.deli.dev`. Both are inactive contract identifiers.
- Runtime: Go service with PostgreSQL, migrations, sqlc, Connect RPC, narrow HTTP handlers, Cloudflare R2 signed uploads/public delivery, and shared `servers/internal` infrastructure where its generic contracts apply.

## Clients, Users, and Authorization

- RealQA is available only to DevHud desktop on macOS 14+, Windows 11, and Ubuntu 24.04. Ubuntu capture covers X11/XWayland and native Wayland through `xdg-desktop-portal`. iOS and Android are excluded.
- The signed-out base shell remains usable. Entering RealQA requires DeliDev Logto Authorization Code with PKCE through a system browser and a one-shot random `127.0.0.1` callback.
- Human RPCs accept a RealQA-audience bearer and a memory-only delibase-audience forwarded bearer and validate matching subjects/scopes. `DeleteFeatureData` alone also accepts the exact RealQA lifecycle M2M identity used by delibase for a typed account/organization-deletion trigger. Its token must have the client-credentials shape (`sub == client_id`) and both values must equal the configured `REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID` in addition to passing issuer, RealQA audience, expiry, and lifecycle-scope validation; another M2M client with the same audience/scope is rejected. No other procedure accepts that identity, and no credential is logged.
- Personal users manage personal presets/destinations. Organization Owners/Admins manage organization destinations, presets, and mappings; members may submit only when their GitHub identity can access the selected repository.
- Personal resources may select an accessible billing organization/team; organization resources use their owning organization/team.
- Synchronized mutations use revisions/ETags with typed reload/compare/reapply conflicts.

## Presets and Synchronization

- Synchronize logical presets, tracker destinations, repository-template/form choices, ordered process/URL mappings, and shortcut definitions.
- Keep OS capture permission, Chrome optional-host permission, shortcut registration result, and extension pairing device-local.
- Limits are 50 personal presets, 250 organization presets, and 20 active RealQA shortcut definitions/device.
- A preset contains capture mode, pointer default, normal/DOM selector default, tracker destination/repository, template/form selection, supported default labels/assignees/milestone/project, ordered process/window-title URL rules, shortcut, owner/billing scope, and revision.
- Preset creation requires a stable client-generated UUID v7 idempotency key scoped to the authenticated subject and operation. Exact replay returns the original preset and revision without consuming another limit slot; reuse with changed creation input fails with the typed idempotency-conflict reason.
- Process/title rules use a Rust-compatible non-backtracking regex syntax with compilation and length limits. Resolve in order by exact process name plus an optional safe title match and URL template.

## GitHub.com Tracker Boundary

- RealQA uses a dedicated minimal-permission GitHub App separate from Deck and acts with GitHub App user authorization tokens.
- Only GitHub.com is supported. GHES, custom GitHub hosts, on-premises trackers, and non-GitHub adapters fail closed.
- Minimum permissions are Issues write, Metadata read, Contents read for templates/forms, and the relevant project permission only for a configured GitHub Projects submission.
- One installation binds to exactly one DeliDev personal or organization owner scope. Connections may be managed only from authenticated RealQA settings in DevHud; DeliDev has no RealQA client or management route.
- Disconnect immediately deletes provider tokens while preserving presets/mappings as disconnected records.
- GitHub App user authorization credentials retained for an active connection use application-level envelope encryption before PostgreSQL persistence: a fresh data-encryption key protects each credential record, only ciphertext plus wrapped key and versioned managed environment-scoped key ID are stored or backed up, and decrypt authority is limited to the provider adapter for the current authorized operation. Rotation must support decrypting old key versions and transactional rewrapping to the active key without exposing plaintext; database/storage encryption alone is insufficient. Plaintext credentials and unwrapped data keys are memory-only for the bounded provider call and never enter logs, traces, errors, audits, caches, or backups.
- RealQA creates new issues only and never comments on an issue. It does not update a pre-existing or unrelated issue; the sole update exception is a best-effort deletion cleanup on the exact issue created by the same reconciled RealQA submission, limited to removing or replacing that submission's deleted-image references while preserving all other issue content.
- Support repository Markdown issue templates and Issue Forms with provider-required validation. Final body order is template/form response, `RealQA capture` with user-approved environment/URL/DOM metadata, then inline Markdown images.
- Include a hidden `realqa:submission:<UUID>` marker. Serialize before submission and enforce 60,000 UTF-8 bytes. There is no image-count cap inside the session limits; overflow stays a draft and requires manual removal/splitting, never automatic multi-issue creation.
- On an ambiguous create result, reconcile recent user-created issues by hidden marker before retrying; never silently duplicate an issue.

## Tracker, Submission, and HTTP Boundaries

- Implement exactly the `RealQAPresetService`, `RealQATrackerService`, and `RealQASubmissionService` RPC sets in [protos-devhud-realqa-api-contract](protos-devhud-realqa-api-contract.md).
- The internal tracker interface normalizes title, body, attachments, labels, and assignees plus typed provider extensions. Only GitHub is registered in v1; adapter contract tests are required. It is not a public tracker/plugin SDK.
- HTTP handlers are limited to GitHub OAuth/App callbacks, installation/issue-lifecycle webhooks, a short-lived signed image PUT at the exact asset origin, and public image GET. The PUT accepts only the object, content type, checksum, and size bound by `CreateImageUpload`; all business mutations use Connect RPC.
- Persisted IDs and preset-creation and local draft/submission idempotency keys are UUID v7. Preserve each idempotency key across retries. Owner scope, capture mode, selector mode, tracker kind, submission state, upload state, failure class, and asset state are closed enums.
- `ListSubmissions` is an authenticated, owner-authorized, opaque-cursor discovery path for retained submissions and assets the caller may delete. It returns only submission and asset UUIDs, asset state, bounded timestamps, and the minimum retained provider issue URL/ID needed to identify the record; it never reconstructs or returns submitted title, body, URL, DOM metadata, screenshot content, object keys, or a public bucket index.
- Do not retain submitted title, body, URL, or DOM metadata after provider reconciliation. Retain only minimum provider IDs/URLs, asset state, and an idempotency digest.

## Capture Payload and URL Boundary

- Uploads are flattened PNG/WebP only. Raw originals remain solely in encrypted local drafts and are never uploaded.
- A session has no explicit image-count limit, at most 25 MiB/encoded image, 250 MiB total encoded images, and 100 megapixels/decoded image. Reject malformed/unsupported images, decompression bombs, and limit overflow before upload.
- Chrome capture starts with active-tab HTTP/HTTPS URL. Desktop capture applies ordered process/title mapping. No match leaves an editable blank URL.
- URLs accept HTTP/HTTPS only, reject credentials, warn for localhost/private destinations, and strip query/fragment by default; review may explicitly restore/edit them.
- Suggested OS/architecture, DevHud/Chrome version, screen/viewport size, capture time, sanitized URL, and DOM fields are user-removable.

## Image Upload, Public Delivery, and Deletion

- Stage privately through short-lived RealQA-signed PUT URLs at exactly `https://assets.realqa.deli.dev`; the same-origin handler writes through a least-privilege R2 binding. `CreateImageUpload` never returns or allowlists an account-specific `r2.cloudflarestorage.com` S3 endpoint, and no signed PUT expires after the submission's server-derived upload deadline. After upload, re-decode/re-encode, remove metadata, and verify type, dimensions, SHA-256, and size before marking verified.
- Promote only submitted images under opaque unguessable identifiers with at least 128 bits of entropy at the future exact asset origin. Never expose bucket indexes, sequential IDs, object keys, or signed GET URLs.
- Inline public Markdown images must be readable by ordinary issue readers without RealQA authentication. Before every submission, explain that anyone with the issue/image URL may view the screenshot and require explicit confirmation.
- Public delivery applies WAF controls and 300 GETs/minute/IP.
- Stop accepting uploads for a submission no later than 23 hours after its transfer reservation is created, reserve the final hour for bounded verification and transfer commit/release, and delete unlinked private staging uploads by the 24-hour boundary after durable abandoned-submission cleanup has been recorded.
- Retain submitted images until explicit image/range deletion, GitHub issue-deletion webhook, account/organization deletion, or storage-billing grace expiry.
- Deleted URLs remain stable and return only a generic non-sensitive `Image removed` placeholder. When valid user authorization remains, best-effort update only the exact issue reconciled to that RealQA submission and only remove or replace its deleted-image references without changing other content; deletion itself never depends on GitHub access.

## Submission Consistency and Billing

The exact sequence is:

1. create the RealQA submission;
2. reserve live transfer units from the submission's aggregate accepted declared encoded bytes only when the computed units are positive;
3. upload and verify images;
4. commit positive transfer units for verified bytes, or release a positive reservation when no verified units remain;
5. when verified retained bytes produce a positive daily maximum, use delibase `BillingService.CreateBackgroundUsageAuthorization` to create the submission-bound `REALQA_STORAGE` authorization defined by issue #756;
6. validate the exact issue body and public-image confirmation;
7. create/reconcile the GitHub issue;
8. promote linked assets to retained public state.

- Transfer uses the future disabled delibase catalog meter `(app key: devhud, meter key: realqa_image_transfer)`, unit `encoded_mib` with precision zero, an effective price of exactly 500 USD micros/unit, an authoritative `reservation_ttl_seconds` of at least 86,400, and an allowlist containing only the RealQA service identity. Reserve `ceil(accepted_declared_encoded_bytes / 1_048_576)` units before upload and commit `ceil(verified_bytes / 1_048_576)` units after aggregating verified bytes once per submission. Zero accepted bytes skip `ReserveUsage`; a positive reservation followed by zero verified bytes is released without calling `CommitUsage`; no live usage request carries zero units. For every positive reservation, durably record its returned `created_at` and `expires_at`, set the upload deadline to the earlier of `created_at + 23 hours` and `expires_at - 1 hour`, return that deadline to the client, and issue no signed PUT beyond it. At the deadline, reject further upload/finalize calls and finish bounded verification plus positive commit or zero-verified release before `expires_at`; never attempt a late commit or promote unbilled data. Use checked wide-integer arithmetic, reject signed-int64 overflow before reservation/commit, commit when R2 verification succeeds even if issue creation later fails, and release only the uncommitted units for bytes without a verified upload.
- Storage uses the future disabled delibase catalog meter `(app key: devhud, meter key: realqa_image_storage)`, unit `mib_day` with precision zero, an effective price of exactly 2 USD micros/unit, and an allowlist containing only the RealQA service identity. The submission-bound authorization maximum is `ceil(verified_retained_bytes / 1_048_576)` units per `UTC_DAY`; zero verified retained bytes skip `CreateBackgroundUsageAuthorization` and create no submission-to-authorization mapping. For each authorization/day, sum each retained image's byte-seconds within that UTC day and settle exactly `ceil(retained_image_byte_seconds / (1_048_576 * 86_400))` units once. A zero byte-second day is recorded locally as complete without calling any authorized-usage mutation; no background authorization or reservation carries zero maximum/units. Checked wide-integer overflow fails before reservation/commit. Recurring settlement uses the issue #756 `ReserveAuthorizedUsage`, `CommitAuthorizedUsage`, and `ReleaseAuthorizedUsage` M2M RPCs authenticated by RealQA's dedicated outbound delibase client-credentials identity; the current live forwarded-token usage RPCs cannot be used after the client exits.
- RealQA validates both authoritative meter identities, units, precision, service mappings, and effective prices plus the transfer meter's minimum 86,400-second reservation TTL before accepting a submission or settling storage. A missing, disabled, short-TTL, or otherwise divergent mapping fails unavailable before reservation/upload; changing either mapping requires a synchronized delibase/RealQA contract and catalog change. Both records remain disabled in production-facing artifacts until a separate activation change, and their immutable UUID v7/Polar identities are assigned only by that change.
- RealQA implementation and activation are blocked until issue #756 lands its synchronized delibase proto/server contract. This planned contract does not authorize storing a forwarded user token or inventing an unbounded background charge path.
- Failed recurring storage billing immediately blocks new submissions, notifies the owner and exposes rebind/payment recovery, keeps existing images public for 30 days, never back-bills grace-period days after recovery, and deletes screenshots after 30 days if not recovered/rebound.
- `RealQASubmissionService.RebindSubmissionStorageAuthorization` is the owner-authorized recovery path for an existing retained submission. The request carries the expected current authorization UUID and mapping revision, replacement payer organization/team, and a stable UUID v7 idempotency key. A durable per-submission rebind attempt serializes the operation and derives stable downstream revoke/create idempotency keys. Using the request's memory-only forwarded delibase bearer, RealQA loads the current authorization and validates its exact owner, submission UUID, RealQA service/storage meter, `REALQA_STORAGE` purpose, `UTC_DAY` period, and authoritative retained-byte maximum. It idempotently revokes the old authorization only when it remains `ACTIVE`; an already `REVOKED` or `ACCESS_LOST` exact grant is accepted as closed and skips revocation, while `RESOURCE_DELETED`, `OWNER_DELETED`, a future/unknown status, or any substitution fails closed. RealQA then creates the replacement with the authoritative retained-byte maximum, validates that it is `ACTIVE` and exactly binds the same owner, submission UUID, service/meter/purpose/period, and accessible replacement payer, and compare-and-swaps the mapping. Ambiguous downstream results resume through the same attempt and keys; exact replay returns the installed authorization/mapping revision, while changed-input reuse, stale mapping, substitution, or a concurrent distinct attempt fails closed without installing a second grant. No bearer is retained. Successful installation resumes only future settlement and never back-bills grace days.
- Failed/ambiguous submissions remain encrypted local drafts. The server stops accepting their uploads at the transfer deadline and removes verified or unverified unlinked staging by the 24-hour boundary after finalizing transfer billing. If no GitHub issue was reconciled and a submission-bound `REALQA_STORAGE` authorization exists, the expiry transaction tombstones the submission, blocks recurring settlement, durably enqueues an independently idempotent `MarkBackgroundUsageResourceDeleted` call, and may remove blobs, but retains the submission tombstone, authorization mapping, and retry state until the exact bound-service response reports `RESOURCE_DELETED`, `REVOKED`, `ACCESS_LOST`, or `OWNER_DELETED`; ambiguous delivery retries and cannot leave an active orphan grant. Client retries before terminal expiry reuse the submission idempotency key and reconcile the marker before another create.
- Limits are three concurrent upload sessions/user and 30 issue submissions/hour/user.
- RealQA catalog records remain stable and disabled in production-facing artifacts until a separate activation change.

## Offline, Local Lifecycle, and Feature Deletion

- After a prior successful login/device binding, offline capture/edit and encrypted account-bound local drafts are allowed. A first-time offline user cannot enter RealQA; upload/submission requires online reauthentication.
- Successful submission deletes the local raw draft. Explicit draft deletion removes it immediately.
- Logout hides and locks drafts without deleting them; signing into the same account restores access. Another account cannot see or decrypt them.
- `Reset DevHud` deletes local drafts, tokens, RealQA shortcut state, and extension pairing. It cannot revoke Chrome-owned tab/origin permission and must direct the user to Chrome extension settings.
- Provider disconnect follows the token-deletion/preset-preservation rule above.
- `RealQAPresetService.DeleteFeatureData` is the only feature-deletion mutation. In owner-request mode it is idempotent by authenticated subject, operation, and owner scope: a personal caller removes their personal presets, submissions, assets, tracker connection/installation bindings, envelope-encrypted user authorization credentials, and related callback/webhook state, while an organization Owner removes the same organization-scoped RealQA data. In lifecycle mode it requires the exact RealQA-scoped delibase M2M identity, accepts only a typed account or organization target plus the immutable delibase deletion-job UUID, and treats that UUID as its replay identity; DeliDev and ordinary feature users cannot select this mode.
- Acceptance in either mode immediately tombstones the target scope, blocks new access/mutations and provider credential use, and starts asynchronous hard deletion of all scoped feature and provider records. Exact replays return the same deletion-job result, absent feature data succeeds idempotently, and only required pseudonymized financial/security records survive. Delibase transactionally enqueues the lifecycle call when it accepts account/organization deletion and retries ambiguous or failed delivery through its immutable retained outbox/dead-letter contract.
- Before owner-request deletion hard-deletes an affected submission or removes its submission-to-authorization mapping, the deletion job durably enqueues an independently idempotent `MarkBackgroundUsageResourceDeleted` call for each submission-bound `REALQA_STORAGE` authorization, binding the authorization, authenticated service, purpose, feature-resource UUID, and expected revision. It retains the mapping and retry state until the exact bound-service response reports `RESOURCE_DELETED`, `REVOKED`, or `ACCESS_LOST`; the latter two are already closed and require no forbidden transition. In delibase lifecycle mode, `OWNER_DELETED` is also terminal because delibase closes the owner grants before dispatching `DeleteFeatureData`. Ambiguous delivery retries and cannot orphan an active grant.

## Security and Observability

- Remote DevHud/extension telemetry, analytics, crash reporting, advertising, and user tracking remain prohibited. The service may use redacted structured operational logs, metrics, traces, and audits.
- Never log or persist feature/delibase bearer tokens, signed upload URLs, object keys, screenshot content, title/body/URL/DOM data, repository names, raw process/window values, or user content outside the explicitly retained encrypted draft/asset/provider state. The sole provider-token persistence exception is the envelope-encrypted active-connection credential record defined above; it must never appear in plaintext storage or backups.
- Validate callback state and webhook signatures, use least-privilege R2/database/provider identities, and expose typed safe errors.
- DevHud reaches Connect and the signed image PUT through its private native transports, not browser fetch. Do not allow `http://tauri.localhost` in CORS; RealQA has no browser RPC client.
- Public assets are intentionally unauthenticated only after per-submission confirmation and verified promotion; no public asset listing or bucket index exists. Authenticated `ListSubmissions` exposes only the caller-authorized retained references defined above.

## Build and Test

Once implementation exists, canonical server checks are:

- `gofmt`/format verification for `servers/devhud-realqa`;
- `go vet ./servers/devhud-realqa/...`;
- `go test ./servers/devhud-realqa/...`;
- sqlc reproducibility and migration checks;
- PostgreSQL integration/concurrency tests;
- fixture GitHub App, R2, callback/webhook, permission, reconciliation, WAF/rate, billing, retention, and deletion tests;
- non-root `linux/amd64` and `linux/arm64` image validation with SBOM and signature/attestation verification.

Coverage must include all size/body boundaries, verification failures and bombs, replay-safe preset creation, templates/forms, marker reconciliation without duplicates, private staging/promotion/23-hour upload deadline/one-hour finalization/24-hour cleanup, short transfer-TTL rejection and no late commit, abandoned-submission authorization closure without orphan grants, authorized paginated submission/asset discovery after local draft deletion, explicit deletion/placeholder and same-submission image-reference-only best-effort issue update, transfer/storage rounding and zero-unit mutation skipping, live/background outbound delibase client-credentials acquisition/startup failure and service-binding rejection, serialized payer rebind replay/concurrency/stale-mapping/substitution plus `ACTIVE`/`REVOKED`/`ACCESS_LOST` source grants, deletion closure for `RESOURCE_DELETED`/`REVOKED`/`ACCESS_LOST`/`OWNER_DELETED`, 30-day grace/no back-billing/deletion, rate limits, provider-credential envelope encryption/rotation and ciphertext-only backups, redaction, exact lifecycle-client pinning and peer-M2M rejection, owner and delibase-lifecycle deletion authorization including scoped connection/installation/credential removal, deletion-job replay/absent data, browser-origin rejection, and GitHub.com-only rejection.

These checks use fixture production substitutes and a fixture extension ID. They must not publish images, deploy services, configure DNS/R2, register a GitHub App, publish a Chrome extension, inject a production extension ID, ship stores, activate catalog entries, or begin operations.

## Dependencies and Integrations

- Wire contract: [protos-devhud-realqa-api-contract](protos-devhud-realqa-api-contract.md).
- Client/native/Chrome contract: [apps-devhud-foundation](apps-devhud-foundation.md).
- Shared service utilities may come from `servers/internal`; RealQA business policy remains under `servers/devhud-realqa`.
- External boundaries are Logto/DeliDev authentication, delibase live usage plus the issue #756 background-usage prerequisite and durable account/organization-deletion lifecycle calls, PostgreSQL, the separate RealQA GitHub App on GitHub.com, and same-origin signed upload/public delivery backed by R2 at the exact future asset origin.
- Live transfer billing and recurring storage settlement share RealQA's dedicated outbound-only delibase configuration set: required non-secret `REALQA_DELIBASE_API_ORIGIN`, `REALQA_DELIBASE_LOGTO_AUDIENCE`, `REALQA_DELIBASE_SERVICE_IDENTITY_ID`, and `REALQA_DELIBASE_LOGTO_M2M_CLIENT_ID`, plus required secret `REALQA_DELIBASE_LOGTO_M2M_CLIENT_SECRET`. In production, origin and audience are exactly `https://delibase.deli.dev`; the service identity is the stable UUID v7 authorization target published for both RealQA meters; and the Logto client must be the delibase service identity bound to that target. RealQA obtains short-lived OAuth 2 client-credentials access tokens from its already validated exact Logto issuer, requesting only the live-usage scopes for transfer calls and only the authorized-usage/resource-deletion scopes for recurring storage calls, caches tokens in memory only until bounded expiry, and never logs, persists, or exposes the client secret or access token. Live `ReserveUsage`/`CommitUsage`/`ReleaseUsage` calls combine that M2M bearer with the current request's memory-only forwarded user bearer; authorized-usage calls never use the forwarded bearer. Once either billing path exists, startup fails before accepting submissions or claiming settlement work when this set is absent, partial, malformed, targets the wrong origin/audience, names an invalid service UUID, or cannot acquire each required scoped token; delibase still rejects any token-to-service mapping mismatch before usage mutation.
- `REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID` is a separate required non-secret receiver-side pin once lifecycle mode is implemented. It must exactly equal delibase's outbound `DELIBASE_REALQA_LIFECYCLE_LOGTO_M2M_CLIENT_ID`; RealQA stores no corresponding lifecycle client secret. Startup fails before serving if the lifecycle handler exists and this value is absent or malformed. The outbound usage identity and inbound lifecycle identity have opposite directions and must not be conflated or granted each other's scopes.

## Change Triggers

- Update this document, [project-devhud](project-devhud.md), RealQA proto/app contracts, docs catalogs, and root/apps/servers/protos `AGENTS.md` files for service, tracker, auth, billing, capture payload, asset, deletion, origin, validation, or activation changes.
- Another tracker, public SDK, new HTTP surface, private image delivery, changed retention/grace, production registration/publication/deployment, or mobile RealQA requires a separate explicit contract change.

## References

- [Project devhud](project-devhud.md)
- [RealQA API](protos-devhud-realqa-api-contract.md)
- [DevHud app](apps-devhud-foundation.md)
- [Issue #757](https://github.com/delinoio/oss/issues/757)
- [Issue #756](https://github.com/delinoio/oss/issues/756)

## Out of Scope

- GHES/custom GitHub hosts/on-premises or non-GitHub trackers.
- Public tracker/plugin SDKs; comments/screenshots on existing issues; updates to issues not created by the same RealQA submission; general edits beyond best-effort deleted-image-reference cleanup on that submission's issue; automatic issue splitting.
- Incognito or full-page/scroll-stitched Chrome capture; mobile RealQA.
- Private/authenticated image URLs that prevent ordinary issue readers from viewing inline images.
- Production deployment, DNS/R2 provisioning, GitHub App registration, extension/store/image publication, catalog activation, production SLOs/alerts, or rollout.
