# apps-devhud-admin-contract

## Scope

`apps/devhud-admin` is the planned Logto-protected administrator SPA embedded at `/admin` by `servers/devhud-api`. It is not implemented and must never display synchronized settings contents.

## Runtime and Language

React/TypeScript SPA using the shared frontend conventions, English/Korean UI, Toss Design Guidelines, and WCAG 2.2 AA. Fixed development port: `46306`.

## Users and Operators

Only individual accounts with the stable Logto role `devhud-admin`; API and security operators use it for user, quota, upload, and audit administration.

## Interfaces and Contracts

Use the bootstrap `admin` public client ID with Logto Authorization Code plus PKCE. Validate state and nonce, use the exact development redirect `http://localhost:46306/auth/callback` or the embedded API-origin `/admin/auth/callback` redirect supplied by bootstrap, and require the `devhud-admin` role before rendering or calling admin RPCs. Support bounded, newest-first cursor pagination for user search through `AdminService.ListUsers`, upload listing through `AdminService.ListUploads`, and audit-event inspection through `AdminService.ListAuditEvents`; block/unblock with mandatory reason through `AdminService.SetUserBlocked`, quota inspection through `AdminService.GetUserUsage`, and quarantine/delete through `AdminService.QuarantineUpload` and `AdminService.DeleteUpload`. Every mutation creates an audit record. Denied roles receive typed permission errors. The UI must not expose settings snapshots, PATs, R2 secrets, local paths, issue bodies, or browser data.

The embedded admin SPA calls the API cross-origin during development and from the packaged shell. It must use the API's exact CORS allowlist and Connect preflight policy: frontend/admin loopback origins and pinned Tauri `http://tauri.localhost` only, with no wildcard origin or header behavior.

## Storage

No independent persistent store. Server-side admin actions and audit records are owned by `devhud-api`; browser state contains no secrets beyond the authenticated session boundary.

## Security

Require Logto authentication and `devhud-admin`; require reasoned mutations; preserve immutable audit identity and timestamps; display only the minimum user/usage/upload metadata required for administration.

## Logging

Use redacted structured client and server diagnostics. Never log tokens, settings bodies, upload bytes, or sensitive user content.

## Build and Test

Validate role denial, block/unblock reason enforcement, concurrent admin mutations, quota and upload-group views, quarantine/delete origin replacement before CDN purge/revalidation, audit integrity, settings non-disclosure, English/Korean layouts, accessibility, and fixed port `46306` conflict failure.

## Dependencies and Integrations

Uses `packages/devhud-api-client` and `protos/devhud/v1`; production assets are embedded by `servers/devhud-api`. It does not call GitHub or R2 directly.

## Change Triggers

Update the project index, server/protocol/client contracts, `apps/AGENTS.md`, and root/domain rules when administrator roles, routes, mutations, audit fields, or disclosure boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [API contract](servers-devhud-api-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Repository defaults](repository-defaults.md)
