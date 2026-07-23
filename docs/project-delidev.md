# Project: delidev

## Goal
Provide an English, responsive developer-tools PWA where anonymous visitors can browse a mini-app catalog and authenticated organization users can manage teams, billing, usage, and account settings.

This index records the repository implementation for issue [#722](https://github.com/delinoio/oss/issues/722). The static app artifact is implemented and validated; it is not publicly activated or deployed by this issue.

## Project ID
`delidev`

## Domain Ownership Map
- `apps/delidev-app` (`app`): React/TypeScript/Rsbuild Cloudflare Pages PWA.

`servers/internal` is repository-shared Go infrastructure used by `delibase`; it is not owned by `delidev`.

## Domain Contract Documents
- [apps-delidev-app-foundation](apps-delidev-app-foundation.md)

## Cross-Domain Invariants
- Canonical origin: `https://deli.dev`; this origin is a documented future canonical origin, not an activation claim.
- Stable route IDs are `/`, `/apps`, `/apps/:appSlug`, `/auth/callback`, `/onboarding`, `/invite/:token`, `/o/:orgSlug/apps`, `/o/:orgSlug/members`, `/o/:orgSlug/teams`, `/o/:orgSlug/billing`, `/o/:orgSlug/usage`, `/o/:orgSlug/settings`, and `/account`.
- Public catalog metadata and pricing are anonymous; organization, billing, usage, invitation acceptance, onboarding, and account operations require authentication.
- The app consumes the versioned `delibase.v1` Connect contract and must update with its owning proto contract for any interface change.
- Logto is the authentication provider. Delibase is authoritative for local profiles keyed by unique Logto `sub` values, organizations, memberships, roles, teams, and billing ownership.
- The PWA may cache only versioned static shell and public catalog data; authenticated organization, team, balance, ledger, usage, and token data are excluded.
- PWA output is an artifact-only Cloudflare Pages deliverable. This project must not activate or deploy the site as part of issue #722.
- The generated `dist` artifact includes an installable manifest, simple `D` lettermark icons, `_redirects`, `404.html`, `_headers`, and a versioned service worker. Its cache policy is an explicit allowlist rather than a sensitive-data denylist; initial service-worker control does not reload the page, while accepted updates reload after controller change. CI rebuilds this deterministic checked-in artifact and rejects any resulting diff.
- Protected Connect requests obtain memory-only Logto tokens on demand for the canonical audience, while anonymous catalog requests use a transport that has no token getter or authorization interceptor. PKCE state and non-sensitive one-shot protected return paths may cross the redirect in same-tab session storage and are consumed on callback; invitation returns use a state-bound sealed handoff so the bearer token is never serialized in plaintext.
- The onboarding route admits only accounts whose server-authoritative state requires onboarding and refreshes that account state before entering the created organization. Onboarding and invitation-acceptance retries retain their pending idempotency keys until success or their operation inputs change; account-deletion retries retain the pending key until success or cancellation.
- The authenticated account surface supports creating and switching among organizations. Owner/Admin organization surfaces manage nested team creation, rename, move, and confirmed subtree deletion, and expose the complete paginated billing ledger; Member visibility remains restricted by the server-authoritative caller role.

## Change Policy
- Route, authentication, cache, UI-state, or Pages artifact changes update this index and [apps-delidev-app-foundation](apps-delidev-app-foundation.md).
- Connect request/response or service changes update this index, the app contract, [project-delibase](project-delibase.md), and [protos-delibase-api-contract](protos-delibase-api-contract.md).
- Organization, team, invitation, billing, or usage semantics update both project indexes and all affected app, server, proto, and shared-infrastructure contracts.
- Do not describe planned behavior as available, and do not activate or deploy either service without a later explicit scope and contract update.

## References
- [Project template](project-template.md)
- [Domain contract](domain-template.md)
- [Project delibase](project-delibase.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
