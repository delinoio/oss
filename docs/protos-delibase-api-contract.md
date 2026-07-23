# protos-delibase-api-contract

## Scope
- Project/component: `delibase` / `api`
- Canonical source path: `protos/delibase/v1`
- Contract identity: `delibase.v1`

## Runtime and Language
- Source format: versioned Protobuf.
- Generated consumers: Connect-compatible Go server/client artifacts and TypeScript browser client artifacts.
- Protobuf source is authoritative; generated output is derived and must not be edited as a second contract.

## Users and Operators
- DeliDev browser client, delibase service, authenticated human users, and authenticated mini-app backend services.
- Maintainers validating compatibility and generation output.

## Interfaces and Contracts
- Exactly six Connect services: `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`.
- Stable route/API origin pairing: browser `https://deli.dev`; future API `https://delibase.deli.dev`.
- Account: authenticated account state, mandatory first-organization onboarding that atomically establishes the unique Logto-`sub` local user and default organization memberships, and account deletion/blockers.
- Organization: CRUD, globally unique changeable slugs/aliases, member roles, invitations, acceptance, and revocation.
- Team: nullable-parent hierarchy, depth/cycle-safe moves, confirmed non-`General` subtree deletion, memberships, and effective downward access.
- Catalog: anonymous public app/meter listing and details, including public effective-dated USD micro-unit prices and their version metadata; no runtime mutation API.
- Billing: summary, hosted Polar checkout/portal session, overage limit, ledger and usage reads.
- Usage: reserve, commit, and release with organization/team/meter IDs, signed int64 units, pinned prices, reservation TTL, and service-scoped idempotency.
- Human requests use Logto user access tokens except for anonymous CatalogService reads, with the canonical audience `https://delibase.deli.dev`. Usage mutations carry Logto M2M authorization and the dedicated `x-delibase-forwarded-user-token` Connect metadata key for the forwarded end-user token; servers must redact that metadata value anywhere headers, metadata, or diagnostics are logged. The server owns authorization decisions.
- Lists use opaque cursor pagination. Preserve released `delibase.v1` additively; breaking changes require `delibase.v2` or later.
- Persisted entity IDs are UUID v7. Money values are signed int64 USD micro-units; usage values are signed int64 units. Error details use stable enum identifiers.

## Storage
- Protobuf defines transport messages only; PostgreSQL schema, append-only ledger, reservations, Polar inbox/outbox, and seven-year pseudonymized retention are owned by the server contract.
- Do not put tokens, card data, webhook secrets, or raw billing PII into messages, logs, or default error payloads.

## Security
- Document authentication requirements on every protected RPC. Catalog reads may be anonymous; organization, billing, usage, invitation, onboarding, and account operations are protected.
- Usage authorization must represent both service identity and forwarded end-user context without exposing the forwarded token. The `x-delibase-forwarded-user-token` metadata value is sensitive credential material and must never appear in logs, traces, errors, or persisted data.
- Do not make client-provided roles, prices, balances, team access, or overage decisions authoritative.

## Logging
- Generated clients and messages must support request/trace correlation through the shared server interceptor boundary without serializing credentials.
- Connect status and stable error details must permit safe user-facing classification for auth, authorization, slug, invitation, team, subscription, overage, reservation, idempotency, deletion, and resource-state failures.

## Build and Test
- Canonical validation: Protobuf lint and breaking checks, generation from `protos/delibase/v1`, generated Go formatting/vet/tests, and TypeScript type checks used by `apps/delidev-app`.
- CI must fail on stale generated output, incompatible released-field changes, service-name drift, or missing cross-consumer generation.
- No runtime activation, API deployment, or generated-client publication is part of issue #722.

## Dependencies and Integrations
- Owned by `delibase`; consumed by `servers/delibase` and `apps/delidev-app`.
- The app uses `@connectrpc/connect-query`; the server uses Connect RPC and shared `servers/internal` interceptors/types.
- Configuration ownership: this contract owns package/version/service/message identifiers and generation settings; the app owns browser endpoint/client settings; the server owns server endpoint/auth/provider settings.

## Change Triggers
- Any service, RPC, field, enum, auth metadata, error, pagination, or compatibility change updates this document, [project-delibase](project-delibase.md), the server contract, the app contract, generated-client validation, and `protos/AGENTS.md`.
- Breaking changes require a new API version and synchronized consumer migration documentation.
- Changes to shared interceptors or UUID/error conventions update [servers-internal-foundation](servers-internal-foundation.md) and `servers/AGENTS.md`.

## References
- [Project delibase](project-delibase.md)
- [Project delidev](project-delidev.md)
- [Server contract](servers-delibase-server-foundation.md)
- [Shared server infrastructure](servers-internal-foundation.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
