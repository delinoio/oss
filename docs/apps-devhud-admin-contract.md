# apps-devhud-admin-contract

## Scope

`apps/devhud-admin` is the implemented Logto-protected administrator SPA embedded at `/admin` by `servers/devhud-api`. It must never display synchronized settings contents.

## Runtime and Language

React/TypeScript SPA using the shared frontend conventions, English/Korean UI, Toss Design Guidelines, and WCAG 2.2 AA. Startup uses a valid stored locale when available and otherwise derives the locale from the browser. Locale switching always updates in-memory state even when Web Storage reads or writes fail. Fixed development origin: `http://localhost:46306`.

The package-owned development wrapper accepts only `DEVHUD_LOGTO_ISSUER`, requires an explicit HTTP(S) authority, validates it before Rsbuild starts, and creates a sanitized child environment. In team mode it obtains that one name from the owning team service scope without exposing values through Turbo, arguments, files, logs, or diagnostics; preflight requires its exact issuer to match the API issuer, and Rsbuild starts only when the freshly injected issuer matches that private comparison pin. In OSS mode it uses the local Logto loopback issuer; a contributor-owned override must exactly match the API wrapper's package-local issuer override so Bootstrap and CSP agree. Authenticated administrator flows remain unavailable until the contributor creates and owns the required Logto administrator/application setup. Root entry points and environment ownership are defined in the repository environment contract.

## Users and Operators

Only individual accounts with the stable Logto role `devhud-admin`; API and security operators use it for user, quota, upload, and audit administration.

## Interfaces and Contracts

If browser policy prevents storing the OIDC nonce, the SPA does not start the Logto redirect and renders a localized retryable sign-in state.

Use the bootstrap `admin` public client ID with Logto Authorization Code plus PKCE. Validate state and nonce, complete each callback through one shared initialization operation even when React development effects replay, and discard a callback whose tab-local nonce is unavailable by clearing its location and tokens before returning to the fresh sign-in path. Use the exact development redirect `http://localhost:46306/auth/callback` or the embedded API-origin `/admin/auth/callback` redirect supplied by bootstrap, and render a permission state when the exact `devhud-admin` role is denied. The API independently enforces that role on every AdminService RPC. The development server requires `DEVHUD_LOGTO_ISSUER` to match the API configuration and adds only that validated issuer origin to its CSP for Logto discovery and token requests. Support bounded, newest-first cursor pagination for user search through `AdminService.ListUsers`, with an optional server-normalized Unicode-NFC, trimmed, full-case-fold prefix query over display name, email, and Logto subject; reject submissions above 512 UTF-8 bytes before the RPC, and retain the submitted query with its result set so continuation tokens never follow later draft edits. Support the same pagination contract for upload listing through `AdminService.ListUploads` and security/admin audit-event inspection through `AdminService.ListAuditEvents`; continuation controls remain disabled while their request is pending, and every audit row renders its available actor-user, target-user, and target-upload identifiers. Block/unblock with mandatory reason through `AdminService.SetUserBlocked`, quota inspection through `AdminService.GetUserUsage` only when bootstrap advertises `OFFICIAL_UPLOADS`, and quarantine/permanent deletion through `AdminService.QuarantineUpload` and `AdminService.DeleteUpload`. Quota responses remain bound to the active user request and are ignored after the dialog closes or another user is selected; failures retain their mapped type and correlation metadata and expose the localized retry path when retryable. Mutations carry the expected current state and use compare-and-set behavior, and their dialogs cannot close while a request is pending. An `Aborted` mutation closes the stale dialog and reloads current records before another attempt; other failures retain their mapped type and correlation metadata and render the matching localized recovery path. Every accepted or application-rejected mutation creates a UUID-v7 correlation-bound audit outcome. Every mutation reason is Unicode-trimmed and NFC-normalized before validation and submission, must contain non-whitespace, NUL-free, well-formed Unicode text, fit within 4 KiB of UTF-8, and pass the shared credential, configured public asset locator, and local-path rejection rules. Denied roles receive typed permission errors. The UI must not expose settings snapshots, PATs, R2 secrets, local paths, issue bodies, browser DOM, screenshots, or browser data.

Search byte validation applies the same trim, NFC, and full case folding as the server before enforcing the 512-byte limit. User cards label administrative blocking and account deletion as independent states and show the recovery deadline when supplied; block/unblock confirmation dialogs identify the target UUID, display name, email, and Logto subject. Submission-scoped quota rows identify their submission UUID, upload listings and destructive confirmation dialogs identify the owner user and submission, and rejected audit rows render the localized typed rejection reason.

Use the generated `AdminQuery` namespace so administrator list/upload methods cannot collide with user-service methods. Administrator upload results use only the direct metadata-only `AdminUpload` projection and never expose the user-facing upload object or a public/signed asset locator. Page size defaults to 50 and is capped at 100, opaque tokens are capped at 2 KiB, and invalid pagination returns the typed `InvalidArgument` mapping. Every response and typed error preserves the UUID-v7 correlation metadata matching `x-devhud-correlation-id`. User search sends the normalized query scope used by the continuation token; the UI must reset pagination whenever its normalized query or filters change.

The embedded admin SPA calls the API cross-origin only from the exact development origin `http://localhost:46306` and same-origin in production. It must use the API's exact CORS allowlist and Connect preflight policy: frontend/admin loopback origins and pinned Tauri `http://tauri.localhost` only, with no wildcard origin or header behavior. Embedded HTML limits CSP connections to `'self'` and the validated deployment-configured Logto issuer origin. Production HTML is served with no-store caching while fingerprinted assets are immutable.

## Storage

No independent persistent store. Server-side admin actions and audit records are owned by `devhud-api`; browser state contains no secrets beyond the authenticated session boundary.

## Security

Require Logto authentication and `devhud-admin`; require reasoned mutations; preserve immutable audit identity and timestamps; display only the minimum user/usage/upload metadata required for administration. All headings, controls, accessibility labels, states, quota names, audit actions, and outcomes are localized in English and Korean. The administrator protobuf message graph is intentionally disconnected from settings snapshots and may not carry secrets, DOM, screenshots, Deck results, agent output, settings bodies, or local paths.

## Logging

Use redacted structured client and server diagnostics. Never log tokens, settings bodies, upload bytes, correlation-associated sensitive content, or any field forbidden from the administrator wire model.

## Build and Test

Validate role denial, empty/oversized/sensitive mutation-reason rejection, concurrent admin mutations, independent administrative-block/account-deletion presentation, quota and attributed upload-group views, quarantine/delete origin replacement before CDN purge/revalidation, audit integrity, settings non-disclosure, English/Korean layouts, accessibility, and fixed port `46306` conflict failure that reaches the bind attempt.

## Dependencies and Integrations

Uses `packages/devhud-api-client` and `protos/devhud/v1`; production assets are embedded by `servers/devhud-api`. It does not call GitHub or R2 directly.

## Change Triggers

Update the project index, server/protocol/client contracts, `apps/AGENTS.md`, and root/domain rules when administrator roles, routes, mutations, audit fields, or disclosure boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [API contract](servers-devhud-api-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Repository environment contract](repository-environment-contract.md)
- [Repository defaults](repository-defaults.md)
