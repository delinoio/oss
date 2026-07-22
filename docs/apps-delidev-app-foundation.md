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
- `/apps` and `/apps/:appSlug` are public. Organization context, billing, usage, invitation acceptance, onboarding, and account management require authentication.
- Consume the six `delibase.v1` Connect services: `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`.
- Logto browser authentication supplies user identity; delibase decides local profile, organization, team, billing, and authorization state. The browser never handles card data; Polar-hosted Checkout and Customer Portal own payment UI.
- On first authenticated entry, require organization name and globally unique user-selected slug before allowing entry; the server transaction creates the user, default organization, Owner membership, protected `General` team, and creator Team Admin membership exactly once.
- Support multiple organizations, changeable globally unique slugs with retained aliases/old-slug redirects, nested teams up to five levels, invitations, and role-aware pages according to the delibase contract.

## Storage
- Cloudflare Pages serves a static artifact with SPA fallback; no server-side app runtime is activated by this issue.
- Service worker/cache may contain only the versioned static shell and anonymous public catalog data. Never cache authenticated organization/team data, balances, ledger, usage, tokens, invitation tokens, or other sensitive data.
- Show an offline state and disable server-backed actions while offline. No browser persistence of access/refresh tokens is part of this contract.

## Security
- Treat Logto as the identity trust boundary and delibase as the application authorization boundary.
- Protect authenticated routes and do not infer authorization from cached UI state. Do not log tokens, invitation tokens, billing PII, or authorization headers.
- Dialogs close with `Esc`, restore focus, and expose complete keyboard/screen-reader states; critical single-input forms autofocus their first input.
- Pages configuration is artifact-only. No public DNS, Pages project activation, or deployment is authorized here.

## Logging
- Client diagnostics must use safe classifications and request/trace correlation without token, payment, invitation, or sensitive organization data.
- Keep dependency and offline errors distinguishable from authorization errors in UI states; do not claim an unavailable backend is operational.

## Build and Test
- Required local checks once the app exists: `pnpm --filter delidev-app typecheck`, `pnpm --filter delidev-app lint`, `pnpm --filter delidev-app test`, `pnpm --filter delidev-app build`, plus PWA, accessibility, and browser smoke validation.
- CI must validate the production static artifact, SPA fallback, manifest/service worker, sensitive-cache exclusions, responsive accessibility, and Connect client generation compatibility.
- These are validation prerequisites; this documentation change does not create or run an app runtime.

## Dependencies and Integrations
- Depends on `protos/delibase/v1` and the generated TypeScript Connect client.
- Calls the future `https://delibase.deli.dev` API origin; configuration owns the origin and Logto client/audience values, while secrets remain outside the app artifact.
- Pages owns static hosting; GHCR is unrelated to this app and must not be used as its deployment path.

## Change Triggers
- Update this document and [project-delidev](project-delidev.md) for route, PWA, cache, UI, build, origin, or configuration changes.
- Update [project-delibase](project-delibase.md), [servers-delibase-server-foundation](servers-delibase-server-foundation.md), and [protos-delibase-api-contract](protos-delibase-api-contract.md) for API or domain semantic changes.
- Update `apps/AGENTS.md`, CI docs/workflows, and release docs when validation, artifact, or deployment policy changes.
- Issue #722 out of scope: runtime implementation, public activation/deployment, a complete brand system, and server-side background or production operations.

## References
- [Project delidev](project-delidev.md)
- [Project delibase](project-delibase.md)
- [Protobuf API contract](protos-delibase-api-contract.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
