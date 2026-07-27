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
- The onboarding route admits only accounts whose server-authoritative state requires onboarding and refreshes that account state before entering the created organization. Onboarding and invitation-acceptance retries retain their pending idempotency keys until success or their operation inputs change; organization-slug retries retain the pending key until account refresh and navigation succeed or the slug input changes; account-deletion retries retain the pending key until success or cancellation.
- The authenticated account surface supports creating and switching among organizations. Owner/Admin organization surfaces manage invitation creation, active-invitation listing and revocation, nested team creation, rename, depth-safe move, and confirmed subtree deletion. Billing and usage use authenticated generated Connect Query clients: managers receive the complete paginated ledger, organization-wide attributed usage, subscription/payment and current-period context, held/committed overage, exact monthly-limit controls, and HTTPS Polar-hosted checkout/portal navigation; Members receive only shared available credit and server-filtered personal/effectively accessible-team usage. The app explains the fixed $10/10,000,000-micro-unit rollover model, zero-default/lower-limit blocking, inactive-subscription credit behavior, reversals, queued Polar delivery, and reservation failures without containing card UI or issuing browser usage mutations. Billing and usage queries and session responses are memory-only, have no persistent browser cache, and every mutation is disabled offline. Sensitive creation tokens remain memory-only, absent balance wrappers remain visibly unavailable, replay-sensitive retries retain their pending keys and bound inputs until their success boundary, subscription checkout identity and its normalized return URL survive route unmounts in account-and-organization-scoped memory until hosted navigation succeeds, component-local portal identity and its normalized return URL survive ambiguous failures until the session response succeeds, the latest hosted-billing action owns the visible error, and generated stable details keep billing, reservation, and held-reservation failures distinguishable.
- Issue #756 extends the generated `BillingService` client with human background-authorization create/get/opaque-list/revoke methods, exposes stable service authorization targets and safe names in public meter metadata, and keeps authorized settlement/resource-deletion notification M2M-only in `UsageService`. Future management UI stays inside `/account` and `/o/:orgSlug/settings`, remains memory/network-only, gives Owners/Admins organization-wide visibility and Members only their own grants, and must not expose a Deck purpose, Logto client IDs/credentials, or M2M methods. The generated contract alone does not activate this UI or its catalog entries.
- Protected account state is provider-owned and memory-only. Current organization routes resolve through delibase, preserve the full suffix during alias canonicalization, and expose an accessible current-slug organization switcher.
- Owner/Admin/Member and effective Team Admin/Member controls follow generated server role/access responses: Admins cannot manage Owners or delete organizations; direct and inherited Team Admins can manage authorized team subtrees and direct memberships; General remains structurally protected; and last-Owner, active-reservation, invitation-state, and permission failures use generated stable error details. Organization-deletion retries retain their pending replay key until success or cancellation, and team dialogs retain unresolved membership-mutation state. Modal confirmation and account-deletion progress meet the documented keyboard and assistive-technology boundary.

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
- [Issue #756](https://github.com/delinoio/oss/issues/756)
