### Instructions for `servers/`

- Follow root `AGENTS.md`, project index docs, and relevant `docs/servers-*.md` contracts before implementation.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form strings for API contracts.

### Scope in This Domain

- `servers/thenv`: Backend for secure environment sharing.
- `servers/delibase`: Go/PostgreSQL/sqlc organization, billing, and usage service owned by project `delibase`.
- `servers/internal`: Repository-shared Go package boundary consumed by delibase; not a project-owned delibase subcomponent or an unrelated project.

### Server Language and Data Rules

- Servers in this domain must be implemented in Go.
- SQL queries and type-safe data access must use `sqlc`.
- Protobuf definitions should live at `proto/<service_name>/v1/*.proto` unless a project contract explicitly uses a shared cross-runtime proto root.
- Each server project must provide a local protobuf generation script and a `go generate` entrypoint.
- Keep API boundaries explicit and versionable.
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Keep authorization and audit behavior documented and testable.
- Never expose secret values in logs or default API responses.

### Fixed Server Project Structure

Stateful server projects under `servers/<service_name>/` should follow this minimum structure:

- `cmd/<service_name>/main.go`
- `internal/service/`
- `internal/contracts/`
- `internal/logging/`
- `db/query/`
- `db/migrations/`
- `db/sqlc.yaml`
- `proto/<service_name>/v1/*.proto`
- `buf.yaml`
- `buf.gen.yaml`
- `scripts/generate-go-proto.sh`
- `generate.go` (with `go:generate` directive)

Scaffold-only service projects may start with a smaller structure (`main.go` + `internal/service`) when documented in the project index and matching server-domain contract docs, but must adopt explicit contract/data/logging subdirectories before persistence and public API rollout.

### Integration Rules

- Changes to server interfaces must be synchronized with related CLI and app contracts.
- Update `docs/project-thenv.md` and `docs/servers-thenv-server-foundation.md` for every thenv interface or trust model update.

### Delibase Rules

- Follow `docs/project-delibase.md`, `docs/servers-delibase-server-foundation.md`, `docs/protos-delibase-api-contract.md`, and `docs/servers-internal-foundation.md` before implementation.
- The canonical future API origin is `https://delibase.deli.dev`; do not activate or deploy a runtime for issue #722.
- Use PostgreSQL and sqlc for persistence, UUID v7 for persisted IDs, signed int64 USD micro-units for money, signed int64 units for usage, non-overlapping effective price/billing-period windows, organization-scoped billing references, and transactional/locked append-only ledger, audit, hierarchy, and reservation invariants. Every committed organization must retain at least one active Owner and exactly one Polar customer. Organization/team-membership identity, Polar subscription identifiers, and Logto service-client identifiers are immutable; organizations may retain historical subscription rows but have at most one active subscription, newer Polar events may reactivate scheduled-canceled subscriptions, and revoked subscriptions remain terminal. Reservation prices, team snapshots, active memberships/accounts and effective team access, storage-derived creation times, expiries, credit availability, and active-subscription current-period overage capacity must match authoritative storage state at reservation and commit; reservation costs must equal their overflow-safe unit-price product; commits must originate from immutable usage records, consume only the held credit/overage split, require and snapshot the containing billing period for usage-linked settlement, and never exceed the reserved maximum. Release and expiry must restore the held credit/overage amounts through matching ledger rows, and explicit release and automatic expiry use distinct typed audit events. Polar usage delivery must omit credit-funded settlement and report only nonzero overage as chargeable USD micro-units; a deferred database invariant requires exactly one matching durable Polar outbox event for every nonzero-overage usage record. Reservation rows, webhook inbox events, integration-outbox events, and deletion jobs cannot be deleted; organization deletion markers and retained slug aliases are one-way/immutable; integration-outbox event bodies and deletion-job targets are immutable; and append-only audit rows accept only pseudonymous actors and allowlisted metadata that passes credential-shape rejection. Deleting organizations cannot accept new reservations. Revoked or expired invitations cannot be accepted, and active reservations retain their service allowlist and Polar meter mapping. Active references block operational deletion while finalized financial and usage snapshots do not.
- Held reservations block organization-member removal and self-leave with `ERROR_REASON_MEMBER_HAS_ACTIVE_RESERVATIONS`.
- Authenticated invitation preview and acceptance must not require a pre-existing local account, organization membership, or team access. Acceptance creates and locks the active local account with a privacy-safe non-empty placeholder display name when absent, locks the organization before the invitation, revalidates invitation state, and records the account's first acceptance before creating memberships. A later request with a different idempotency key must not let the same account consume the invitation again or restore removed access.
- Team reads and mutations must establish organization membership before resolving requested team identities so non-members receive the same membership-required failure for existing and absent team IDs.
- Keep Logto identity validation separate from delibase authorization; validate the canonical `https://delibase.deli.dev` audience, require confirmation for non-`General` subtree deletion, and exclude organization deletion from Admin permissions. Polar owns payment settlement/invoices, while delibase owns local users keyed immutably by their original Logto `sub`, organization, team, membership, ledger, reservation, and audit state.
- The six Connect services are `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`. Human calls use user tokens except for anonymous `CatalogService` reads; usage mutations validate M2M and the `x-delibase-forwarded-user-token` forwarded end-user context, whose value is always redacted.
- Shared reusable auth/JWKS, Connect interceptors, redaction, request/trace IDs, HTTP defaults, structured logging, and UUID v7 code belongs under `servers/internal`; business policy remains in delibase.
- Shared Logto validation must require the exact configured issuer, canonical `https://delibase.deli.dev` audience, expiry, allowed asymmetric signature/header type, exact ES algorithm/curve pairing, `kid`, route scopes, and the expected user or client-credentials token shape. Unknown JWKS key IDs force a serialized refresh without blocking fresh cached-key validation; stale-key use is opt-in and bounded; final JWKS response URLs after redirects must satisfy the configured HTTPS/no-credentials/no-query/no-fragment boundary.
- Authentication route policies must select an explicit public, user, M2M, or M2M-plus-forwarded-user mode. The zero-value mode is invalid so missing policy entries fail closed.
- Authentication middleware must strip authorization and forwarded-user credential headers after validation and attach typed claims to context. Shared code must never infer local organization, team, billing, meter-allowlist, or authorization state from Logto claims/scopes.
- Shared safe-error mapping must attach vetted bearer or forwarded-user authentication reasons, map only explicit signing-key provider unavailability to retryable dependency failure, classify unknown keys and key/algorithm/type/curve mismatches as invalid credentials, and may otherwise preserve an intentional Connect status code and recognized `delibase.v1.ErrorDetail.reason` only. It must discard arbitrary response metadata, unrecognized detail types, and free-form detail fields; validated request and trace IDs are added to unary and streaming failures by the outer request metadata interceptor.
- Server root loggers must use the shared redacting handler, and routine events must use allowlisted safe fields plus keyed actor pseudonyms in the exact `actor:v1:<32-lowercase-hex>` shape. Configure pseudonymization with a distinct secret of at least 32 bytes; never log the key or raw Logto subject.
- Delibase inbox, outbox, and deletion-job enqueueing must use the typed `internal/reliability` contracts with transaction-bound sqlc queries. Worker dispatch identifiers are allowlisted typed values. Claims use skip-locked leases and claim-token compare-and-set transitions; normal attempts are capped at exactly 12, expired twelfth claims recover into retained dead-letter state, and dead letters retry every 24 hours until terminal success. Retry clocks, jitter, and claim-token generation remain injectable for deterministic tests.
- Delibase Polar startup requires the single provider product ID, fetches it from the configured production/sandbox API, verifies one active fixed monthly USD `$10` base price, and only then pins it together with the provider environment to the database-enforced `10,000,000`-micro-unit paid-cycle grant. Production is the default provider environment; sandbox checkout requires the explicit `DELIBASE_POLAR_ENVIRONMENT=sandbox` opt-in, and startup must fail rather than reuse provider state persisted for the other environment. An optional compatible provider API override must be an HTTPS URL without credentials, query, or fragment; official production/sandbox endpoints remain the default.
- Polar subscription checkout creation must durably claim one provider-idempotency attempt per organization before provider creation. The same request digest may recover an ambiguous or post-provider-success failure, distinct keys remain blocked, and successful active-checkout, audit, and idempotency finalization removes the attempt atomically under the organization billing lock. A definitive provider rejection clears the attempt; a successful checkout blocks distinct idempotency keys until provider expiry. Billing summaries prefer an active subscription, otherwise report an unexpired checkout as checkout-pending, and select inactive subscription history by provider-event recency. Retained refunds preserve their organization-scoped provider-order binding and monotonic provider-event/reversal/chargeback state, and refund reversal increases must transactionally match retained paid-cycle totals. Retained paid cycles immutably snapshot their provider-order, subscription, billing-period, and original period-bound identities, increase reversals only to match retained refunds, and block deletion of their referenced subscriptions and billing periods. Any outstanding refund shortfall after a renewed paid grant carries into the renewed current billing period; append-only reconciliation adjustments remove the carried amount when a reordered paid grant clears the organization-level debt.
- Polar refunds and chargebacks must reconcile against retained financial history after organization deletion. Organization deletion must cancel every nonterminal Polar subscription, including subscriptions materialized by delayed webhooks after deletion. Hosted billing-portal sessions and subscription checkouts use distinct typed audit events.
- Required checks once code exists include `gofmt`, `go vet ./...`, `go test ./servers/delibase/...`, sqlc/migration checks, Protobuf generation/compatibility, PostgreSQL concurrency tests, and Docker validation.
- Issue #722 artifact scope excludes public activation/deployment, production SLO/RPM controls, dashboards/alerts, kill switches, feature flags, operator RPCs, and manual replay tooling. Future GHCR release scope is signed `delibase@v*` multi-architecture `vX.Y.Z` and `latest` only.

### Multi-Component Contract Sync

- `servers/thenv` changes must keep CLI contracts synchronized.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Keep operational logging sufficient for incident debugging and audit reconstruction.
