# apps-devhud-admin-contract

## Scope

`apps/devhud-admin` is the planned Logto-protected administrator SPA embedded at `/admin` by `servers/devhud-api`. It is not implemented and must never display synchronized settings contents.

## Runtime and Language

React/TypeScript SPA using the shared frontend conventions, English/Korean UI, Toss Design Guidelines, and WCAG 2.2 AA. Fixed development port: `46306`.

## Users and Operators

Only individual accounts with the stable Logto role `devhud-admin`; API and security operators use it for user, quota, upload, and audit administration.

## Interfaces and Contracts

Support user search, block/unblock with mandatory reason, quota inspection, upload listing, quarantine/delete, and audit-event inspection through `AdminService`. Every mutation creates an audit record. Denied roles receive typed permission errors. The UI must not expose settings snapshots, PATs, R2 secrets, local paths, issue bodies, or browser data.

## Storage

No independent persistent store. Server-side admin actions and audit records are owned by `devhud-api`; browser state contains no secrets beyond the authenticated session boundary.

## Security

Require Logto authentication and `devhud-admin`; require reasoned mutations; preserve immutable audit identity and timestamps; display only the minimum user/usage/upload metadata required for administration.

## Logging

Use redacted structured client and server diagnostics. Never log tokens, settings bodies, upload bytes, or sensitive user content.

## Build and Test

Validate role denial, block/unblock reason enforcement, concurrent admin mutations, quota and upload views, quarantine/delete placeholder behavior, audit integrity, settings non-disclosure, English/Korean layouts, accessibility, and fixed port `46306` conflict failure.

## Dependencies and Integrations

Uses `packages/devhud-api-client` and `protos/devhud/v1`; production assets are embedded by `servers/devhud-api`. It does not call GitHub or R2 directly.

## Change Triggers

Update the project index, server/protocol/client contracts, `apps/AGENTS.md`, and root/domain rules when administrator roles, routes, mutations, audit fields, or disclosure boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [API contract](servers-devhud-api-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Repository defaults](repository-defaults.md)
