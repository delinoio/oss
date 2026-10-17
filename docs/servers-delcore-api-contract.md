# Delcore API contract

## Scope

`servers/delcore-api` owns the independent service, migrations, background jobs and sample client.

## Runtime and Language

Use the root Go module and pinned toolchain. Run one process against PostgreSQL. Do not change root development commands.

## Users and Operators

Authenticated users, registered M2M consumer services, tenant administrators and dedicated platform operators.

## Interfaces and Contracts

**Project and runtime**

- Use the root Go toolchain and module. Run the API and background settlement, retry, notification, and retention jobs in one server process.
- Use PostgreSQL for authoritative data, accounting records, and durable jobs.
- Support one running instance in v1. Provide an OCI image, configuration documentation, explicit database migrations, startup compatibility checks, and backup/update/recovery procedures.
- Do not provision deployment infrastructure or publish an image as part of this requirement.
- Follow repository environment and secret ownership rules. Commit placeholders and safe examples only; require operator-owned credentials for real integrations.

**Identity, tenancy, and authorization**

- Logto owns authentication and user identity. Delcore owns tenants, memberships, groups, roles, and service permissions.
- Validate user access tokens and service-specific Logto M2M tokens. Bind each service identity to its permitted reporting and query operations.
- Create one personal tenant on first authenticated use. Personal tenants belong exclusively to their user.
- Allow users to create organization tenants and join multiple organizations through email invitations.
- Tenants, memberships, and groups are shared across Delino services.
- Support multiple flat groups per organization. Effective group permissions are additive; nested groups and explicit deny policies are excluded.
- Provide fixed roles:

| Role    | Authority                                                            |
| ------- | -------------------------------------------------------------------- |
| Owner   | All tenant administration, billing, ownership transfer, and deletion |
| Admin   | Members, invitations, and groups; no implicit billing authority      |
| Billing | Subscription, payment, and usage administration                      |
| Member  | Granted service use                                                  |

- Allow multiple roles per member. Admin cannot grant Owner, Admin, or Billing roles.
- Each organization has exactly one Owner. Ownership transfer requires recipient acceptance. Prevent the last Owner from leaving before transfer or organization deletion.
- Platform operators use a dedicated Logto role. Require reasons and audit records for operator mutations.
- Enforce tenant boundaries and current permissions on every protected operation. Group permissions grant registered service capabilities.
- Consumer services must reject new paid execution when delcore authorization or billing status cannot be checked. Consumers retain pending usage reports for later delivery.

**Catalog and subscription lifecycle**

- Provide platform operator APIs for services, meters, plans, add-ons, and versioned prices.
- Each tenant has one base plan and optional service add-ons, consolidated into tenant-wide monthly billing.
- Support free plans without time-limited trials. New tenants start on the operator-designated default free plan.
- Use one operator-configured currency per deployment. Do not perform currency conversion.
- Support fixed unit price multiplied by measured usage, with included free usage. Exclude tiered rates and custom formulas.
- Operators explicitly rank base plans for upgrade/downgrade classification.
- Apply upgrades after successful immediate prorated payment. Add-ons follow the same immediate prorated addition rule.
- Apply downgrades, add-on removal, and cancellation at the next renewal.
- Preserve existing products and permissions if a requested upgrade payment fails.
- Existing subscriptions retain their price version. New subscriptions and explicit plan changes select the applicable new version.
- Included usage expires each month. An upgrade grants the prorated difference for the remaining period.
- After paid cancellation takes effect, move the tenant to the free plan and end paid add-ons. Preserve prior debt and disputes separately.
- Treat a confirmed cancellation as complete for every cancel intent of the tenant, including a terminally failed one, so a stale failed cancel job cannot hold final deletion. Duplicate and stale provider notifications keep their ordering protection and do not complete jobs.
- Retain the latest subscription status and occurrence time by provider subscription ID even when no tenant is associated yet. When the initial subscription payment establishes the association, apply retained state that occurred after that payment in the same transaction: a canceled state moves the tenant to the free plan and ends paid products, and a past-due state blocks paid use as renewal failure. The payment is ordered by its provider billing time, capped at the processing time, so a payment that reconciliation or the immediate provider response confirms late is compared with retained state by when it happened and its entitlements start at that time. The confirmed initial payment becomes the subscription's ordering watermark, so a delayed notification that occurred before it stays stale in either delivery order. Retain an unresolved past-due notification separately from the latest status, so a later active status or a past-due notification delivered after a newer active status cannot erase it; a retained cancellation outranks it. Older retained state never overrides a newer payment, the newest occurrence wins regardless of delivery order, and an active status alone never grants paid products or clears a payment failure. Retention applies to notifications processed after this behavior ships; delcore does not backfill or reconstruct notifications that were already acknowledged without retained state.
- Never activate paid entitlements or credit balances solely from a checkout request or browser return.

**Metering, credits, and settlement**

- Services report measured usage after execution. Delcore calculates charges using its price contracts.
- Preserve usage event identity, service and tenant attribution, occurrence time, and applicable price history.
- Deduplicate accepted usage durably. A service may correct its own prior event through a referenced correction API.
- Keep the ledger append-only. Represent corrections, refunds, and reversals as additional entries.
- Use exact decimal arithmetic without binary floating point. Preserve unrounded credit ledger precision; round monthly service/meter totals to currency minor units using half-up rounding.
- Late usage uses the price applicable when it occurred and becomes an adjustment on the next bill, retaining its original period. Bound accepted lateness through operator configuration. Negative corrections remain durable in the ledger and offset subsequent usage charges, including across restarts. They do not reduce fixed subscription fees. On cancellation, operators refund any remaining usage overpayment in Paddle; synchronize the approved usage-line refund before completing deletion. Invoice-level payment offsets retain their source job and transaction item without inventing service attribution.
- Purchased credits represent monetary balances and do not expire.
- Support manual top-ups for all tenants. Automatic top-ups require an active Paddle subscription; tenants without one use manual top-ups. Automatic top-up settings require a threshold, amount, and monthly cap.
- Permit only one in-flight automatic top-up per tenant. Suspend automatic top-ups after payment failure or an accepted subscription cancellation. Stop unattempted automatic top-up jobs when cancellation is accepted; reconcile any payment already in flight. Disabling automatic top-ups clears the settings and stops queued, unattempted automatic top-up jobs in the same transaction; running, reconciling, previously attempted and externally identified jobs continue, including a job that the provider rejected and an operator explicitly retried. Cancellation and disabling apply the same unattempted rule. A job that the worker deferred inside Paddle's billing window without contacting the provider is unattempted, and a deferred operator retry keeps its rejection category so it stays attempted. Jobs stopped by disabling or cancellation do not count toward the monthly cap. Choosing a tenant for an automatic top-up and queueing its job happen in one maintenance transaction, and every state transition, including settings changes, cancellation and worker claims, runs under the single-instance transaction lock. A settings change therefore either commits before the maintenance pass reads the tenant, so no job is queued, or after the pass commits, so the same change stops the queued job. Integration tests hold the pass between selection and insert to prove this ordering; replacing the lock with finer-grained locking requires keeping those tests.
- A plan determines whether depleted credits cause blocking or postpaid continuation.
- For plans without postpaid continuation, record already-incurred excess usage as negative credit balance, block subsequent paid use, and offset debt with the next top-up.
- Explicitly document that post-execution reporting cannot prevent all concurrent overspend.
- Delcore owns metering, rating, credits, and settlement. Paddle handles subscription and payment operations through its regular Billing APIs.
- A confirmed renewal payment failure blocks new paid use immediately. Tenant management and payment recovery remain available.
- Refunds are initiated in Paddle, not through a delcore refund-request workflow.
- Synchronize refund and dispute outcomes. Reverse affected top-up value; block new paid use while a dispute is active or the balance is negative. Restore access only after all applicable blocking conditions clear.

**Deletion and retention**

- Tenant deletion stops new paid use and starts final settlement after the configured usage-reporting grace period. Deletion and withdrawal stop subscription intents that have never been attempted. They also stop subscription intents that the provider definitively rejected without creating a subscription, retiring them as done while retaining the rejection category, including for a rejected intent that an operator already moved back to pending, so an operator retry cannot revive them. Previously attempted, incomplete subscription intents must be reconciled before deletion or withdrawal starts.
- Outstanding debt, disputes, or refundable purchased balances block deletion completion.
- Operators refund remaining purchased credit through Paddle. Synchronize and verify the result before completing deletion.
- After settlement and the configured deletion grace period, delete personal information and retain required accounting/audit records in de-identified form.
- User withdrawal removes delcore access, the personal tenant, and organization memberships subject to ownership and settlement constraints.
- Do not delete the shared Logto identity. Prevent re-registration through the same identity while deletion is pending.
- Require operator-configured retention periods for raw usage, accounting records, audit records, and webhook records. Implement retention jobs without breaking unsettled accounting or required replay protection.

**Interfaces and notifications**

- Provide Connect v1 interfaces for:
  - Current user and tenant discovery; tenant, membership, invitation, ownership, and group management.
  - Catalog reads; subscription changes; manual top-ups and automatic top-up settings.
  - Usage, ledger, and billing history.
  - Service authorization checks, usage reporting, and usage corrections.
  - Operator catalog management, tenant blocking, audit inspection, and failed-job inspection/reprocessing.
- Use typed errors, correlation identifiers, bounded pagination, and UUID v7 for delcore entities. Keep external provider identifiers as explicit external references.
- Authenticate and verify inbound provider events before processing. Apply duplicate and stale-event protection.
- Use Cloudflare Email REST for invitations, insufficient-balance notices, automatic top-up failures, and paid-use blocking notices.
- Send English email only. Organization billing notifications go to Owner and Billing users; personal tenant notifications go to the owner.
- Use operator-allowlisted consumer application URLs for invitation acceptance and payment return flows. Delcore provides the API references and tokens; it does not implement those screens.
- Protect invitation acceptance with recipient identity, expiration, revocation, and single-use semantics.

**Reliability and operational controls**

- Persist work before acknowledging accepted operations. Recover pending work after restart.
- Use durable deduplication and transactional state transitions to prevent repeated ledger effects.
- Reconcile ambiguous payment outcomes with the provider before retrying a charge. Do not blindly repeat a payment whose outcome is unknown.
- Handle duplicate and out-of-order provider notifications without regressing current state.
- Retry transient failures within configured bounds. Route exhausted retries, permanent failures, and payment mismatches to operator review.
- Require validated operator settings for retry limits, timeouts, payload bounds, request rates, invitation expiry, usage lateness, retention, deletion grace, and alert thresholds. Reject missing or contradictory settings at startup.
- Provide structured `log/slog` logs, Prometheus metrics, OpenTelemetry traces, health/readiness checks, audit records, and operational recovery APIs.
- Include safe correlation IDs and bounded error categories. Exclude secrets, tokens, sensitive payloads, and unbounded identity labels from telemetry.
- Expose signals for payment mismatches, settlement delay, delivery failures, retry exhaustion, and durable job backlog.
- Support explicit migrations and backup-before-update procedures. Roll back through a compatible image or documented backup recovery; do not claim arbitrary schema downgrade support.
- Feature flags are not applicable because this is a new independent service. Authorization, billing checks, tenant blocking, and recovery controls remain independently enforced.
- Include a threat model and automated security tests. External security certification or audit is not a completion requirement.

**Documentation and repository integration**

- Create `docs/project-delcore.md`, `docs/servers-delcore-api-contract.md`, and `docs/protos-delcore-v1-contract.md` before runtime implementation.
- Update the documentation catalog, stable project identifiers, and relevant ownership/navigation instructions.
- Keep behavior requirements in the owning contracts rather than copying them into AGENTS files.
- Provide a server README, API usage guidance, self-hosting prerequisites, configuration reference, and backup/recovery/support procedures.
- Document provider setup, external consumer URL requirements, API failure behavior, and the distinction between automated fixtures and live account acceptance.
- Preserve existing service behavior, root development commands, CI affected selection, platform gates, licenses, and validation records.
- Follow repository dependency-security requirements and report unresolved advisories accurately.

### Approved accounting choices

Prorated included quantities are floored to six decimal places (one millionth of a unit). This quantity rule does not round intermediate monetary ledger amounts.

Purchased monetary credits apply only to metered usage. Paddle collects fixed subscription and add-on fees separately. Paid usage periods follow Paddle subscription periods; free usage periods follow UTC calendar months. Automatic top-up monthly caps use UTC calendar months. Schema generation follows the repository layout: schemas in `protos/delcore/v1`, Go bindings in `protos/gen/go/delcore/v1`.

## Storage

PostgreSQL stores authoritative entities, immutable price history, append-only ledger entries, replay protection and durable jobs. Persist intent before external requests. Use transactional constraints and locks for concurrent mutations. Explicit migrations run separately from server startup. A database session lock prevents a second instance.

## Security

Validate issuer, audience, signature, expiry and token purpose. Recheck current authorization on each operation. Bind M2M identities to services. Treat provider payloads as untrusted. Verify webhook signatures before persistence. Hash invitation tokens; never log tokens or email bodies. Protect accounting records from direct updates and deletions except controlled retention after settlement.

## Logging

Use log/slog, safe correlation identifiers, bounded error categories, Prometheus and OpenTelemetry. Never place tenant, user, event or provider identifiers in metric labels.

## Build and Test

- Go unit tests for pricing, prorated allowance changes, decimal rounding, plan ranking, role rules, and configuration validation.
- Connect contract tests for typed errors, pagination, authentication, tenancy, and service-scoped reporting.
- PostgreSQL integration tests for migrations, concurrent mutations, append-only corrections, deduplication, job recovery, and retention.
- Sample-client flows covering personal/organization tenants, invitation lifecycle, ownership transfer, group permissions, and free/paid transitions.
- Monthly billing with multiple service meters and add-ons; frozen old prices; late usage and historical corrections.
- Manual and automatic top-ups, concurrent triggers, monthly caps, failure suspension, negative balances, and debt offsets.
- Mocked Paddle payment, refund, dispute, duplicate, reordered, delayed, and ambiguous-response scenarios.
- Mocked Logto token validation, issuer/audience mismatch, expired credentials, unauthorized M2M access, and operator-role enforcement.
- Mocked Cloudflare delivery failures, retries, permanent failures, and correct notification recipients.
- Process termination between provider requests and local acknowledgment, followed by restart and reconciliation.
- Final settlement, outstanding debt, balance refunds, deletion holds, identity re-registration prevention, and de-identification.
- Telemetry redaction, bounded metric labels, audit attribution, operator recovery authorization, and configured alert signals.
- OCI startup, schema incompatibility, graceful shutdown, migration failure, and documented recovery scenarios.
- Repository formatting, Go vet/tests, protocol generation/lint/breaking checks, and affected CI validation. Preserve existing service and workflow behavior.

## Dependencies and Integrations

Use Connect-Go, pgx, OIDC and existing repository telemetry dependencies. Use Paddle regular Billing APIs and Cloudflare Email REST. External provider account acceptance is separate from mocked automated acceptance.

## Change Triggers

Update the [project index](project-delcore.md) and [protocol contract](protos-delcore-v1-contract.md) for boundary changes. Preserve existing behavior and validation records. Instruction updates cover ownership and procedures only.

## References

- [Repository defaults](repository-defaults.md)
- [Dependency security](repository-dependency-security-contract.md)
- [Paddle one-time billing](https://developer.paddle.com/build/subscriptions/bill-add-one-time-charge/)
- [Cloudflare Email](https://developers.cloudflare.com/email-service/)

### Exclusions

- Product UI, admin UI, public documentation app pages, and separate SDK packages.
- Integration changes to existing Delino services.
- Production infrastructure provisioning, Compose deployment bundles, multi-instance operation, rolling deployment, and multi-region support.
- Live provider account acceptance, external security certification, guaranteed performance/SLO targets, and a fixed release date.
- Paddle beta usage accounting.
- Annual subscriptions, trials, tiered pricing, custom rating formulas, multiple currencies, and currency conversion.
- Nested groups, explicit deny permissions, and tenant-defined roles.
- Pre-execution cost reservation or a guarantee of zero overspend.
- User refund requests and delcore-initiated refund execution.
- Global Logto account deletion, general user-data export, and outbound service webhook delivery.
- Feature-flag rollout and migration of existing service data.
