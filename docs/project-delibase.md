# Project: delibase

## Goal
Provide reusable organization, team, catalog, billing, and metered-usage infrastructure for DeliDev and future mini-app services.

This index establishes the documentation and ownership prerequisites for issue [#722](https://github.com/delinoio/oss/issues/722). It is a planned contract, not evidence that a delibase runtime exists, is activated, or is deployed.

## Project ID
`delibase`

## Domain Ownership Map
- `servers/delibase` (`server`): Go/PostgreSQL/sqlc service and its operational configuration.
- `protos/delibase` (`api`): shared `delibase.v1` Protobuf and generated Connect-compatible client boundary.

`servers/internal` is repository-shared Go infrastructure consumed by this project. It remains a shared package boundary and is not silently assigned to delibase or another unrelated project.

## Domain Contract Documents
- [servers-delibase-server-foundation](servers-delibase-server-foundation.md)
- [protos-delibase-api-contract](protos-delibase-api-contract.md)
- [servers-internal-foundation](servers-internal-foundation.md)

## Cross-Domain Invariants
- Canonical API origin: `https://delibase.deli.dev`; this is a future canonical origin and must not be represented as an active deployment in this issue.
- The six Connect services are `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`, all under `delibase.v1`.
- Human APIs use Logto user access tokens except for anonymous `CatalogService` reads. Usage mutations require a Logto M2M token plus the dedicated, redacted `x-delibase-forwarded-user-token` metadata key for end-user context; delibase validates issuer, the canonical `https://delibase.deli.dev` audience, expiry, scopes, service allowlists, organization membership, and effective team access. Raw Logto client secrets remain provider-side and are never accepted as API credentials. Authenticated invitation preview and acceptance use the invitation bearer token instead of pre-existing organization membership or team access.
- Repository-shared packages under `servers/internal` validate and context-bind Logto user/M2M authentication, rotate cached JWKS keys, strip raw credential headers, propagate request/trace IDs, map safe errors, enforce HTTP/CORS defaults, redact diagnostics, emit allowlisted pseudonymous `slog` events, and generate UUID v7 IDs. They do not own or infer delibase authorization or billing state.
- Delibase owns local user profiles keyed by unique Logto `sub` values, organizations, memberships, roles, teams, invitations, catalog configuration, billing ledger, reservations, and audit records. Logto owns identity authentication; Polar owns payment settlement and invoices.
- Persisted entity IDs use UUID v7. Money is signed 64-bit USD micro-units; meter usage is signed 64-bit integer units. Overflow, negative reservations, and invalid precision are rejected.
- PostgreSQL transactions and row locks enforce append-only ledger behavior and prevent concurrent reservations from exceeding available credits plus the configured overage allowance.
- Organization, team (including confirmed non-`General` subtree deletion and Admin organization-deletion exclusion), invitation (including distinct acceptance/revocation idempotency operations), account-deletion, credit, reservation, settlement, refund, cancellation, and webhook semantics are defined in the server and proto contracts and must remain additive/versioned. Idempotency keys are scoped to the authenticated user subject and operation for human RPCs, or the authenticated service identity and operation for M2M RPCs.
- Root Protobuf sources live at `protos/delibase/v1/`; generated artifacts are derived and must not become a second source of truth.
- Reproducible generated consumers live at `protos/delibase/gen/go` and `protos/delibase/gen/ts`; the latter is the private workspace package `@delinoio/delibase-connect`. Root `pnpm generate:proto` generates both runtimes and builds the package's loadable `dist` exports; `pnpm check:proto` is the canonical compatibility entrypoint.
- The issue scope is artifact-only: validate/build the app, validate/generate the API, test/build the server artifacts, and publish only the specified tagged GHCR images later. Do not activate or deploy either service.

## Change Policy
- Any API change updates this index, the app index/contract, the server contract, the proto contract, and the relevant `AGENTS.md` files.
- Any shared Go package change updates this index, [servers-internal-foundation](servers-internal-foundation.md), the server contract, and `servers/AGENTS.md`; shared packages remain unassigned unless a later ownership decision explicitly changes the boundary.
- Any data, billing, identity, team, or authorization change updates all affected contracts and their validation commands together.
- Changes to artifact scope, tag triggers, GHCR image names, Pages output, or configuration ownership update this index and the affected domain contracts before CI/release changes.

## References
- [Project template](project-template.md)
- [Domain contract](domain-template.md)
- [Project delidev](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
