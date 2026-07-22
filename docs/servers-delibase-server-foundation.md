# servers-delibase-server-foundation

## Scope
- Project/component: `delibase` / `server`
- Canonical path: `servers/delibase`
- Canonical future API origin: `https://delibase.deli.dev`

## Runtime and Language
- Runtime: Go service
- Persistence: PostgreSQL with ordered migrations and sqlc for all queries
- API: `delibase.v1` Connect RPC, with signed Polar webhook REST as the explicit non-Connect exception.
- Stateful structure must provide explicit service, contracts, logging, database, generation, and configuration boundaries before runtime rollout.

## Users and Operators
- DeliDev users and organization Owners, Admins, Members, Team Admins, and Team Members.
- Authenticated mini-app backend services reporting integer metered usage.
- Maintainers operating PostgreSQL, Logto, Polar, artifact builds, and future GHCR releases.

## Interfaces and Contracts
- Six versioned Connect services: `AccountService` (account state/onboarding/deletion), `OrganizationService` (organizations, slugs, roles, invitations), `TeamService` (hierarchy, moves, subtree deletion, memberships/access), `CatalogService` (anonymous public enabled app/meter metadata and effective-dated USD micro-unit prices with version metadata), `BillingService` (summary, checkout, portal, limits, ledger/usage reads), and `UsageService` (reserve/commit/release).
- Human calls require a Logto user access token except for anonymous `CatalogService` reads. Usage mutations require a Logto M2M token plus a dedicated redacted forwarded end-user Logto token. Validate issuer, the canonical `https://delibase.deli.dev` audience, expiry, scopes, M2M service meter allowlists, membership, and effective team access server-side.
- List APIs use opaque cursors. Errors use Connect status codes and stable enum-based details for auth, authorization, slug, invitation, team depth/cycle, subscription, overage, reservation, idempotency, deletion, and resource-state failures.
- Persisted IDs are UUID v7. Slugs are globally unique/changeable and retain aliases. Teams have one organization, nullable parent, maximum five levels below the organization, downward inherited access, and an undeletable/non-renamable protected `General` team. Deleting another team requires explicit confirmation, deletes its subtree only when no active usage reservation exists there, and historical usage/audit records retain immutable team ID/name snapshots.
- Invitations are hashed bearer links, seven-day valid, revocable, and reusable by distinct users. An invitation may grant only Organization Admin or Member; Member invitations must target a team and assign Team Admin or Member. Owner may be granted only by an existing Owner after the user has joined, and existing membership roles are not silently changed.
- Organization roles: Owner (full access and deletion), Admin (management, billing, limits, full usage, but no Owner management or organization deletion), Member (app use, shared credits, and accessible team usage, but no invoices/full ledger/subscription/overage controls). Owners and Admins are implicit Team Admins for every team. Preserve at least one Owner.
- Account deletion blocks on last-Owner status, disables local access, removes operational data, queues Logto deletion, signs out, retries external deletion, and retains only pseudonymized financial/audit references for seven years.

## Storage
- PostgreSQL tables/migrations cover users, organizations, memberships, slug aliases, teams, invitations, catalog/meter enabled state and effective-dated USD micro-unit prices, Polar mappings/subscriptions/periods, append-only ledger entries, reservations, webhook inbox, integration outbox, deletion jobs, and audit events. Each organization has exactly one Polar team customer, keyed by the organization UUID as Polar `external_id`.
- Money is signed 64-bit USD micro-units (`10,000,000` = $10.00); usage is signed 64-bit integer units. Reject overflow, negative reservations, and invalid catalog precision.
- Polar supplies one monthly $10 product: each successfully paid cycle grants exactly $10.00 rollover credit; unused credits do not expire; metered overage is invoiced by Polar. Taxes/fees do not alter the grant.
- Delibase ledger is authoritative for operational balance, holds, authorization, and DeliDev display; Polar is authoritative for payment settlement and invoices. Refund/chargeback reverses its grant and makes consumed shortfall overage subject to the limit.
- Owner/Admin must set a non-negative monthly overage limit; zero is default. Committed and held overage count against the current Polar period. Lowering the limit below current committed or held overage blocks only new reservations; it never reverses existing usage or mutates append-only ledger entries. Cancellation/revocation/past-due preserves existing credits but grants no credits and permits no new overage.
- `ReserveUsage` requires organization/team/meter IDs, max units, client reference, and idempotency key; atomically holds credit/overage portions and pins price/version/expiry. Commit applies actual units (never above reserved), consumes credits before overage, releases unused holds, attributes actor/team/service, and queues Polar usage. Release/expiry returns holds; late commit fails.
- Idempotency is operation/service scoped: same key and payload returns the original result; different payload conflicts. PostgreSQL transactions/row locks prevent over-allocation. Polar webhooks are signature-verified and persisted idempotently before success; inbox/outbox workers retry 12 times with capped exponential backoff/jitter, retain dead letters, and retry them every 24 hours.

## Security
- Logto is the identity trust boundary; delibase owns local authorization and data. Polar owns hosted payment consent, cards, receipts, invoices, cancellation, and payment recovery; delibase never handles card data.
- Store only invitation token hashes. Never persist or log Logto/Polar tokens, webhook secrets, authorization headers, forwarded user tokens, card data, or raw billing PII.
- PostgreSQL failure fails mutations closed. During Polar outage, authorize within local credits/overage and queue events; checkout creation makes no local mutation if Polar is unavailable.
- Organization deletion hides access immediately, blocks usage, forfeits remaining credits without refund, queues Polar cancellation, deletes operational org/team/member data, and retains seven-year pseudonymized financial/audit records.

## Logging
- Use `log/slog` structured events with request/trace ID, pseudonymous actor, organization/team/service/meter/reservation IDs, decision, result, and safe error classification.
- Audit authorization, organization/role/invitation/team, billing-limit, checkout/subscription/refund, reservation/settlement, deletion, and webhook decisions immutably without secrets or raw PII.

## Build and Test
- Required checks once implementation exists: `gofmt -w`/format check, `go vet ./...`, `go test ./servers/delibase/...`, sqlc generation/checks, PostgreSQL integration/concurrency tests, migration checks, Protobuf generation/compatibility checks, and Docker build validation. The Docker check must validate a minimal multi-stage image that runs as non-root and exposes working health/readiness behavior.
- CI must test duplicate/reordered webhooks, idempotency, concurrent reservations, account/organization/team rules, five-level/cycle constraints, invitation role boundaries, protected-team deletion, catalog validation with fixtures covering at least two apps and meters, billing state, outages, and deletion retention behavior.
- Release CI must add `release-delibase.yml`, trigger only on `delibase@v*` tag pushes, and publish only signed `ghcr.io/delinoio/delibase:vX.Y.Z` and `:latest` multi-architecture (`linux/amd64`, `linux/arm64`) images with SPDX SBOM, provenance, and health/readiness plus non-root validation; no `edge`, SHA, or main-branch images.
- This documentation change does not activate a service, deploy an API, or publish an image; the release workflow and artifacts are issue #722 implementation deliverables.

## Dependencies and Integrations
- Consumes root sources at `protos/delibase/v1` and shared packages under `servers/internal`.
- Integrates with DeliDev at `https://deli.dev`, Logto, Polar, PostgreSQL, and future GHCR artifact publication.
- Catalog is checked-in validated configuration with explicit app/meter enabled state, synchronized idempotently at migration/startup; disabled entries are excluded from anonymous reads and usage authorization, and effective-dated price changes pin reservation versions. No runtime catalog mutation API or operator UI.
- Configuration ownership: this service owns server non-secret configuration, database/catalog settings, CORS, API origin, and secret environment variable names; Logto/Polar own provider-side configuration; DeliDev owns browser-safe client configuration; shared defaults belong to `servers/internal`. The canonical server variables are `DELIBASE_API_ORIGIN`, `DELIBASE_CORS_ALLOWED_ORIGINS`, `DELIBASE_CATALOG_PATH`, `DELIBASE_LOGTO_ISSUER`, `DELIBASE_LOGTO_AUDIENCE` (which must equal `https://delibase.deli.dev`), and `DELIBASE_LOGTO_JWKS_URL` (non-secret), plus `DELIBASE_DATABASE_URL`, `DELIBASE_LOGTO_M2M_CLIENT_ID`, `DELIBASE_LOGTO_M2M_CLIENT_SECRET`, `DELIBASE_POLAR_ACCESS_TOKEN`, and `DELIBASE_POLAR_WEBHOOK_SECRET` (secret). The browser-safe client variables are `PUBLIC_DELIBASE_API_ORIGIN`, `PUBLIC_LOGTO_ENDPOINT`, `PUBLIC_LOGTO_APP_ID`, and `PUBLIC_LOGTO_AUDIENCE`; they must not contain secrets and the audience must use the same canonical value. No variable may be logged, and required variables fail closed when absent. The implementation must add the matching CI/deployment templates when the runtime is introduced.

## Change Triggers
- Update this document, [project-delibase](project-delibase.md), the proto contract, app contract, and `servers/AGENTS.md` for service, data, auth, billing, usage, origin, or configuration changes.
- Update [servers-internal-foundation](servers-internal-foundation.md) for shared package changes.
- Update CI/release documentation and workflows for validation, image, tag, SBOM, provenance, or deployment-scope changes.
- Issue #722 out of scope: public/API activation or deployment, production SLO/RPM limits, dashboards/alerts, kill switches, feature flags, operator RPC, manual replay tooling, and a non-empty production catalog requirement.

## References
- [Project delibase](project-delibase.md)
- [Project delidev](project-delidev.md)
- [Protobuf API contract](protos-delibase-api-contract.md)
- [Shared server infrastructure](servers-internal-foundation.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
