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
- Human RPCs accept a RealQA-audience bearer and a memory-only delibase-audience forwarded bearer and validate matching subjects/scopes. `DeleteFeatureData` alone also accepts the exact RealQA lifecycle M2M identity used by delibase for a typed account/organization-deletion trigger. No other procedure accepts that identity, and no credential is stored or logged.
- Personal users manage personal presets/destinations. Organization Owners/Admins manage organization destinations, presets, and mappings; members may submit only when their GitHub identity can access the selected repository.
- Personal resources may select an accessible billing organization/team; organization resources use their owning organization/team.
- Synchronized mutations use revisions/ETags with typed reload/compare/reapply conflicts.

## Presets and Synchronization

- Synchronize logical presets, tracker destinations, repository-template/form choices, ordered process/URL mappings, and shortcut definitions.
- Keep OS capture permission, Chrome optional-host permission, shortcut registration result, and extension pairing device-local.
- Limits are 50 personal presets, 250 organization presets, and 20 active RealQA shortcut definitions/device.
- A preset contains capture mode, pointer default, normal/DOM selector default, tracker destination/repository, template/form selection, supported default labels/assignees/milestone/project, ordered process/window-title URL rules, shortcut, owner/billing scope, and revision.
- Process/title rules use a Rust-compatible non-backtracking regex syntax with compilation and length limits. Resolve in order by exact process name plus an optional safe title match and URL template.

## GitHub.com Tracker Boundary

- RealQA uses a dedicated minimal-permission GitHub App separate from Deck and acts with GitHub App user authorization tokens.
- Only GitHub.com is supported. GHES, custom GitHub hosts, on-premises trackers, and non-GitHub adapters fail closed.
- Minimum permissions are Issues write, Metadata read, Contents read for templates/forms, and the relevant project permission only for a configured GitHub Projects submission.
- One installation binds to exactly one DeliDev personal or organization owner scope. Connections may be managed only from authenticated RealQA settings in DevHud; DeliDev has no RealQA client or management route.
- Disconnect immediately deletes provider tokens while preserving presets/mappings as disconnected records.
- RealQA creates new issues only. It does not comment on or update an existing issue.
- Support repository Markdown issue templates and Issue Forms with provider-required validation. Final body order is template/form response, `RealQA capture` with user-approved environment/URL/DOM metadata, then inline Markdown images.
- Include a hidden `realqa:submission:<UUID>` marker. Serialize before submission and enforce 60,000 UTF-8 bytes. There is no image-count cap inside the session limits; overflow stays a draft and requires manual removal/splitting, never automatic multi-issue creation.
- On an ambiguous create result, reconcile recent user-created issues by hidden marker before retrying; never silently duplicate an issue.

## Tracker, Submission, and HTTP Boundaries

- Implement exactly the `RealQAPresetService`, `RealQATrackerService`, and `RealQASubmissionService` RPC sets in [protos-devhud-realqa-api-contract](protos-devhud-realqa-api-contract.md).
- The internal tracker interface normalizes title, body, attachments, labels, and assignees plus typed provider extensions. Only GitHub is registered in v1; adapter contract tests are required. It is not a public tracker/plugin SDK.
- HTTP handlers are limited to GitHub OAuth/App callbacks, installation/issue-lifecycle webhooks, a short-lived signed image PUT at the exact asset origin, and public image GET. The PUT accepts only the object, content type, checksum, and size bound by `CreateImageUpload`; all business mutations use Connect RPC.
- Persisted IDs and local draft/submission idempotency keys are UUID v7. Preserve an idempotency key across retries. Owner scope, capture mode, selector mode, tracker kind, submission state, upload state, failure class, and asset state are closed enums.
- `ListSubmissions` is an authenticated, owner-authorized, opaque-cursor discovery path for retained submissions and assets the caller may delete. It returns only submission and asset UUIDs, asset state, bounded timestamps, and the minimum retained provider issue URL/ID needed to identify the record; it never reconstructs or returns submitted title, body, URL, DOM metadata, screenshot content, object keys, or a public bucket index.
- Do not retain submitted title, body, URL, or DOM metadata after provider reconciliation. Retain only minimum provider IDs/URLs, asset state, and an idempotency digest.

## Capture Payload and URL Boundary

- Uploads are flattened PNG/WebP only. Raw originals remain solely in encrypted local drafts and are never uploaded.
- A session has no explicit image-count limit, at most 25 MiB/encoded image, 250 MiB total encoded images, and 100 megapixels/decoded image. Reject malformed/unsupported images, decompression bombs, and limit overflow before upload.
- Chrome capture starts with active-tab HTTP/HTTPS URL. Desktop capture applies ordered process/title mapping. No match leaves an editable blank URL.
- URLs accept HTTP/HTTPS only, reject credentials, warn for localhost/private destinations, and strip query/fragment by default; review may explicitly restore/edit them.
- Suggested OS/architecture, DevHud/Chrome version, screen/viewport size, capture time, sanitized URL, and DOM fields are user-removable.

## Image Upload, Public Delivery, and Deletion

- Stage privately through short-lived RealQA-signed PUT URLs at exactly `https://assets.realqa.deli.dev`; the same-origin handler writes through a least-privilege R2 binding. `CreateImageUpload` never returns or allowlists an account-specific `r2.cloudflarestorage.com` S3 endpoint. After upload, re-decode/re-encode, remove metadata, and verify type, dimensions, SHA-256, and size before marking verified.
- Promote only submitted images under opaque unguessable identifiers with at least 128 bits of entropy at the future exact asset origin. Never expose bucket indexes, sequential IDs, object keys, or signed GET URLs.
- Inline public Markdown images must be readable by ordinary issue readers without RealQA authentication. Before every submission, explain that anyone with the issue/image URL may view the screenshot and require explicit confirmation.
- Public delivery applies WAF controls and 300 GETs/minute/IP.
- Delete unlinked private staging uploads after 24 hours.
- Retain submitted images until explicit image/range deletion, GitHub issue-deletion webhook, account/organization deletion, or storage-billing grace expiry.
- Deleted URLs remain stable and return only a generic non-sensitive `Image removed` placeholder. Best-effort update the issue body when valid user authorization remains; deletion itself never depends on GitHub access.

## Submission Consistency and Billing

The exact sequence is:

1. create the RealQA submission;
2. use delibase `BillingService.CreateBackgroundUsageAuthorization` to create the submission-bound `REALQA_STORAGE` authorization defined by issue #756;
3. reserve live transfer usage;
4. upload and verify images;
5. commit transfer usage for verified bytes;
6. validate the exact issue body and public-image confirmation;
7. create/reconcile the GitHub issue;
8. promote linked assets to retained public state.

- Transfer is 500 USD micros/MiB of verified uploaded bytes. Aggregate bytes once per submission and compute `ceil(verified_bytes * 500 / 1_048_576)` USD micros using a checked wide-integer intermediate; zero bytes cost zero, and overflow beyond signed int64 fails before reservation/commit. Commit when R2 verification succeeds even if issue creation later fails; release only bytes without a verified upload.
- Storage is 2 USD micros/MiB-day through the submission-bound durable authorization, aggregating image byte-seconds per authorization/UTC day and rounding up once/day. Recurring settlement uses the issue #756 `ReserveAuthorizedUsage`, `CommitAuthorizedUsage`, and `ReleaseAuthorizedUsage` M2M RPCs; the current live forwarded-token usage RPCs cannot be used after the client exits.
- RealQA implementation and activation are blocked until issue #756 lands its synchronized delibase proto/server contract. This planned contract does not authorize storing a forwarded user token or inventing an unbounded background charge path.
- Failed recurring storage billing immediately blocks new submissions, notifies the owner and exposes rebind/payment recovery, keeps existing images public for 30 days, never back-bills grace-period days after recovery, and deletes screenshots after 30 days if not recovered/rebound.
- Failed/ambiguous submissions remain encrypted local drafts. Verified unlinked server uploads remain for 24 hours. Retries reuse the idempotency key and reconcile the marker before another create.
- Limits are three concurrent upload sessions/user and 30 issue submissions/hour/user.
- RealQA catalog records remain stable and disabled in production-facing artifacts until a separate activation change.

## Offline, Local Lifecycle, and Feature Deletion

- After a prior successful login/device binding, offline capture/edit and encrypted account-bound local drafts are allowed. A first-time offline user cannot enter RealQA; upload/submission requires online reauthentication.
- Successful submission deletes the local raw draft. Explicit draft deletion removes it immediately.
- Logout hides and locks drafts without deleting them; signing into the same account restores access. Another account cannot see or decrypt them.
- `Reset DevHud` deletes local drafts, tokens, RealQA shortcut state, and extension pairing. It cannot revoke Chrome-owned tab/origin permission and must direct the user to Chrome extension settings.
- Provider disconnect follows the token-deletion/preset-preservation rule above.
- `RealQAPresetService.DeleteFeatureData` is the only feature-deletion mutation. In owner-request mode it is idempotent by authenticated subject, operation, and owner scope: a personal caller removes their personal presets, submissions, and assets, while an organization Owner removes organization RealQA data. In lifecycle mode it requires the exact RealQA-scoped delibase M2M identity, accepts only a typed account or organization target plus the immutable delibase deletion-job UUID, and treats that UUID as its replay identity; DeliDev and ordinary feature users cannot select this mode.
- Acceptance in either mode immediately tombstones the target scope, blocks new access/mutations, and starts asynchronous hard deletion. Exact replays return the same deletion-job result, absent feature data succeeds idempotently, and only required pseudonymized financial/security records survive. Delibase transactionally enqueues the lifecycle call when it accepts account/organization deletion and retries ambiguous or failed delivery through its immutable retained outbox/dead-letter contract.

## Security and Observability

- Remote DevHud/extension telemetry, analytics, crash reporting, advertising, and user tracking remain prohibited. The service may use redacted structured operational logs, metrics, traces, and audits.
- Never log/persist authorization/provider tokens, signed upload URLs, object keys, screenshot content, title/body/URL/DOM data, repository names, raw process/window values, or user content outside the explicitly retained encrypted draft/asset/provider state.
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

Coverage must include all size/body boundaries, verification failures and bombs, templates/forms, marker reconciliation without duplicates, private staging/promotion/24-hour cleanup, authorized paginated submission/asset discovery after local draft deletion, explicit deletion/placeholder/best-effort issue update, transfer/storage rounding and commit semantics, payer rebind/30-day grace/no back-billing/deletion, rate limits, redaction, owner and delibase-lifecycle deletion authorization, deletion-job replay/absent data, browser-origin rejection, and GitHub.com-only rejection.

These checks use fixture production substitutes and a fixture extension ID. They must not publish images, deploy services, configure DNS/R2, register a GitHub App, publish a Chrome extension, inject a production extension ID, ship stores, activate catalog entries, or begin operations.

## Dependencies and Integrations

- Wire contract: [protos-devhud-realqa-api-contract](protos-devhud-realqa-api-contract.md).
- Client/native/Chrome contract: [apps-devhud-foundation](apps-devhud-foundation.md).
- Shared service utilities may come from `servers/internal`; RealQA business policy remains under `servers/devhud-realqa`.
- External boundaries are Logto/DeliDev authentication, delibase live usage plus the issue #756 background-usage prerequisite and durable account/organization-deletion lifecycle calls, PostgreSQL, the separate RealQA GitHub App on GitHub.com, and same-origin signed upload/public delivery backed by R2 at the exact future asset origin.

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
- Public tracker/plugin SDKs; comments/screenshots on existing issues; automatic issue splitting.
- Incognito or full-page/scroll-stitched Chrome capture; mobile RealQA.
- Private/authenticated image URLs that prevent ordinary issue readers from viewing inline images.
- Production deployment, DNS/R2 provisioning, GitHub App registration, extension/store/image publication, catalog activation, production SLOs/alerts, or rollout.
