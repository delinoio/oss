# apps-delidev-app-foundation

## Scope
- Project/component: `delidev` / `app`
- Canonical path: `apps/delidev-app`
- Canonical future origin: `https://deli.dev`

## Runtime and Language
- Runtime: browser PWA
- Primary language: TypeScript with React
- Build: Rsbuild; output: `apps/delidev-app/dist`
- Use React Query and `@connectrpc/connect-query` for server state and Connect RPC integration.
- English-only, responsive desktop/mobile UI for current Chrome, Edge, Firefox, and Safari.
- Follow Toss Design Guidelines and WCAG 2.2 AA. The wordmark is text `DeliDev`; the PWA uses a simple `D` lettermark. A complete brand system is out of scope.

## Users and Operators
- Anonymous developers browsing catalog metadata and pricing.
- Authenticated organization Owners, Admins, and Members; Team Admins and Members.
- Platform maintainers operating the future Pages, Logto, Polar, and delibase integrations.

## Interfaces and Contracts
- Stable routes: `/`, `/apps`, `/apps/:appSlug`, `/auth/callback`, `/onboarding`, `/invite/:token`, `/o/:orgSlug/apps`, `/o/:orgSlug/members`, `/o/:orgSlug/teams`, `/o/:orgSlug/billing`, `/o/:orgSlug/usage`, `/o/:orgSlug/settings`, `/account`.
- `/apps` and `/apps/:appSlug` are public. Organization context, billing, usage, invitation preview/acceptance, onboarding, and account management require authentication. Invitation preview and acceptance authorize with the invitation bearer token and do not require the user to be an existing organization or team member.
- Consume the five human-facing `delibase.v1` Connect services: `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, and `BillingService`. `UsageService` is server-to-server; the browser must not issue its reserve, commit, or release mutations or hold M2M credentials.
- Consume protobuf-es v2 messages and `GenService` descriptors through the workspace package `@delinoio/delibase-connect`; Connect Query remains responsible for browser transport/query integration.
- Logto browser authentication supplies user identity; delibase decides local profile, organization, team, billing, and authorization state. The browser never handles card data; Polar-hosted Checkout and Customer Portal own payment UI.
- On first authenticated entry, require organization name and globally unique user-selected slug before allowing entry. In the same transaction, create the local user keyed by the unique Logto `sub`, then create the default organization, Owner membership, protected `General` team, and creator Team Admin membership exactly once. Use the same organization transaction for every additional organization.
- Support multiple organizations, changeable globally unique slugs with retained aliases/old-slug redirects, nested teams up to five levels, invitations with replay-safe acceptance/revocation results, and role-aware pages according to the delibase contract. Human idempotency keys are scoped to the authenticated user subject and operation. Create-organization and create-team retries reuse the pending operation key until the request succeeds or its inputs change.

## Storage
- Cloudflare Pages serves a static artifact with SPA fallback; no server-side app runtime is activated by this issue.
- Service worker/cache may contain only the versioned static shell and anonymous public catalog data. Never cache authenticated organization/team data, balances, ledger, usage, tokens, invitation tokens, or other sensitive data.
- Show an offline state and disable server-backed actions while offline. Access, refresh, and ID tokens remain memory-only. PKCE state and non-sensitive one-shot protected return paths may use same-tab session storage solely across the Logto redirect and must be consumed on callback; invitation returns use a state-bound sealed handoff that never serializes the bearer token in plaintext. None may enter local storage, React Query, or the service worker.

## Security
- Treat Logto as the identity trust boundary and delibase as the application authorization boundary.
- Protect authenticated routes and do not infer authorization from cached UI state. Do not log tokens, invitation tokens, billing PII, or authorization headers.
- Dialogs close with `Esc`, restore focus, and expose complete keyboard/screen-reader states; critical single-input forms autofocus their first input.
- Pages configuration is artifact-only. No public DNS, Pages project activation, or deployment is authorized here.

## Logging
- Client diagnostics must use safe classifications and request/trace correlation without token, payment, invitation, or sensitive organization data.
- Keep dependency and offline errors distinguishable from authorization errors in UI states; do not claim an unavailable backend is operational.

## Build and Test
- Shared client checks are `pnpm check:proto` and `pnpm --filter @delinoio/delibase-connect typecheck`. Required local checks once the app exists are `pnpm --filter delidev-app typecheck`, `pnpm --filter delidev-app lint`, `pnpm --filter delidev-app test`, `pnpm --filter delidev-app build`, plus PWA, accessibility, and browser smoke validation. The app test and build commands build the generated Connect client first so they work from a clean checkout.
- CI must validate the production static artifact, SPA fallback, manifest/service worker, sensitive-cache exclusions, responsive accessibility, and Connect client generation compatibility. The deterministic build must leave the checked-in `apps/delidev-app/dist` artifact unchanged.
- `pnpm --filter delidev-app test:pwa` validates the installable manifest, generated SPA fallback, versioned shell, canonical metadata, and allow/deny cache rules. `pnpm --filter delidev-app test:browser` covers Chromium, Edge, Firefox, WebKit, and representative mobile Chromium/WebKit viewports.
- Rsbuild writes the static app to `apps/delidev-app/dist`; `scripts/postbuild.mjs` copies `index.html` to `404.html` and produces `sw.js` from the exact generated shell file set. Cloudflare Pages `_redirects` provides the primary SPA fallback.

## Dependencies and Integrations
- Depends on `protos/delibase/v1` and the generated TypeScript Connect client.
- Calls the future `https://delibase.deli.dev` API origin; the canonical browser-safe build variables are `PUBLIC_DELIBASE_API_ORIGIN`, `PUBLIC_LOGTO_ENDPOINT`, `PUBLIC_LOGTO_APP_ID`, and `PUBLIC_LOGTO_AUDIENCE` (the latter is `https://delibase.deli.dev`). These values are non-secret and must never contain tokens or provider secrets; required values fail closed when absent.
- Pages owns static hosting; GHCR is unrelated to this app and must not be used as its deployment path.

## Change Triggers
- Update this document and [project-delidev](project-delidev.md) for route, PWA, cache, UI, build, origin, or configuration changes.
- Update [project-delibase](project-delibase.md), [servers-delibase-server-foundation](servers-delibase-server-foundation.md), and [protos-delibase-api-contract](protos-delibase-api-contract.md) for API or domain semantic changes.
- Update `apps/AGENTS.md`, CI docs/workflows, and release docs when validation, artifact, or deployment policy changes.
- The issue #722 app artifact is implemented in this repository. Public activation/deployment, a complete brand system, and server-side background or production operations remain out of scope.

## Implemented Client Boundaries
- Public pages use a dedicated anonymous Connect transport for `CatalogService`. Protected routes use a separate transport interceptor that requests a Logto access token for exactly `https://delibase.deli.dev`, attaches it only to the current request, and sends `Cache-Control: no-store`.
- React Query state is memory-only. Public catalog requests may be served by the service worker; protected account, organization, invitation, team, billing, ledger, and usage queries have no persistent browser cache.
- The service worker recognizes only the four checked-in `CatalogService` read method names, requires an absent `Authorization` header, and keys cached POST responses through a synthetic body digest. Every other RPC is network-only.
- Missing or invalid public configuration fails closed: public catalog requests show a dependency error and Logto controls remain disabled. The environment contract remains browser-safe and contains no provider secret.
- Initial service-worker control does not reload the page. Later updates wait for user confirmation, activate through `SKIP_WAITING`, reload on controller change, remove prior version caches, and retain only the new versioned shell and public catalog cache.
- Logto uses an injected browser client whose access, refresh, and ID token state is memory-only and which removes legacy state for the configured app from local storage. Its PKCE sign-in session and non-sensitive protected return path use same-tab session storage. Invitation return paths are sealed with a key derived from the high-entropy OIDC state, restored only by the matching callback, and removed from storage immediately; abandoned sealed handoffs are discarded on the next same-origin load.
- Public catalog, organization member, team hierarchy, and usage-record lists use opaque cursor pagination with explicit load-more actions.
- The account surface creates additional organizations through the same atomic organization transaction used by onboarding and then enters the returned canonical slug.
- Organization settings expose the name and changeable-slug RPCs, refresh server-authoritative shell data after successful name updates (including a partial save when the following slug update fails), and follow the returned canonical slug after slug changes.
- The organization shell loads the server-authoritative caller role. Team hierarchy creation, rename, move, and confirmed subtree deletion controls render only for Owners and Admins. Subscription, billing-portal, overage-limit, and complete ledger controls follow the same role boundary; overage limits retain exact USD micro-unit handling and ledger reads use opaque cursor pagination.

## References
- [Project delidev](project-delidev.md)
- [Project delibase](project-delibase.md)
- [Protobuf API contract](protos-delibase-api-contract.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
