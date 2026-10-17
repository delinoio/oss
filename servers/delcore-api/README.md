# Delcore API

Delcore owns shared personal and organization tenants, service authorization, usage accounting and tenant-wide billing. It runs a Connect API and durable background jobs in one Go process with PostgreSQL. It has no UI or separate SDK package.

The [project index](../../docs/project-delcore.md), [server contract](../../docs/servers-delcore-api-contract.md) and [protocol contract](../../docs/protos-delcore-v1-contract.md) define the full behavior contract. Repository-owned code and distributions use Apache-2.0.

## Operator prerequisites

- A PostgreSQL database dedicated to this service, with a separate backup destination. Use a migration role that can create schema objects. Restrict application access to the owning database.
- An operator-owned Logto issuer, API audience, dedicated operator role, and distinct M2M applications for each service. User access tokens must include a verified email claim. Never use an ID token as an API access token. Register each M2M client and its allowed scopes through the operator API.
- A Paddle Billing account with monthly recurring prices, an appropriate product for purchased usage credits, an API token, and a signed notification destination at `/webhooks/paddle`. Subscribe to transaction completion/failure, subscription lifecycle and adjustment creation/update events. Use sandbox credentials for validation.
- Cloudflare Email Sending enabled for the sender domain, an account-scoped sending token, and valid sender DNS configuration. Email Sending remains beta. A Workers Paid plan is required for arbitrary recipients; consult the [current setup guide](https://developers.cloudflare.com/email-service/get-started/). The Go server uses REST, not a Workers binding.
- Consumer application pages for invitation acceptance and payment return. Register their exact URLs. Delcore does not supply these screens.
- A TLS reverse proxy for public API access. Expose health and metrics endpoints only to trusted monitoring infrastructure. Do not publish PostgreSQL.

## Configuration and startup

Copy `config.example.json` outside the checkout, replace every placeholder, restrict file permissions and choose policy limits for your deployment. The example retention periods are examples, not a statement of legal requirements. Keep the identity key stable across updates: it derives the stored identity reference. Loss or unplanned rotation prevents identity matching.

```sh
go run ./servers/delcore-api/cmd/delcore-api -config "/secure/delcore.json" -migrate
go run ./servers/delcore-api/cmd/delcore-api -config "/secure/delcore.json"
```

Run commands from the repository root. Migrations are explicit and transactional. Normal startup checks schema compatibility; it does not apply migrations. A database session lock rejects a second running instance. The service stops if it loses that lock connection.

Bootstrap the service catalog and default free plan with a user token carrying the dedicated operator role. Create registered meters before creating usage prices. Register all prices before marking the free plan as the default. Price versions are immutable. Existing entitlements refer to their chosen price version. Keep fixed recurring prices separate from usage prices.

| Settings                                                      | Purpose                                                |
| ------------------------------------------------------------- | ------------------------------------------------------ |
| `listen`, `database_url`                                      | HTTP listener and authoritative PostgreSQL connection  |
| `issuer`, `audience`, `operator_role`, `identity_key`         | Authentication and stable identity matching            |
| `currency`, `minor_units`                                     | One deployment currency and its minor-unit exponent    |
| `paddle_*`                                                    | Billing API, credit product and webhook authentication |
| `email_*`                                                     | Cloudflare REST account, sender and authentication     |
| `return_urls`                                                 | Exact consumer URL allowlist                           |
| `timeout_seconds`, `max_payload_bytes`, `requests_per_minute` | Bounded HTTP and provider work                         |
| `retry_limit`, `retry_seconds`                                | Durable retry limits and delay                         |
| `invitation_seconds`, `usage_lateness_seconds`                | Invitation expiry and accepted reporting lateness      |
| `reporting_grace_seconds`, `deletion_grace_seconds`           | Final reporting and deletion delays                    |
| `*_retention_seconds`                                         | Explicit retention policy inputs                       |
| `backlog_alert`, `settlement_alert_seconds`                   | Operational alert thresholds                           |
| `development`                                                 | Permit loopback HTTP provider URLs for local fixtures  |

The JSON loader rejects unknown fields and contradictory required policies. Secrets are read from the operator-owned file. Optional standard OTLP environment variables configure tracing.

## Consumer API

Generate clients from the committed `delcore.v1` schemas or use the generated Go Connect client. Every mutation supplies a UUID v7 `request_key`. Repeating a key with different input returns a conflict. Responses and typed errors carry a correlation ID.

The sample client reads a token from the environment and prints only tenant count and correlation metadata:

```sh
go run ./servers/delcore-api/cmd/sample-client -endpoint "http://127.0.0.1:8080"
```

User clients discover their personal tenant, create organizations, manage invitations and groups, and inspect billing through their current roles. Owner transfer requires acceptance by the receiving member. Admin has no implicit billing authority and cannot grant privileged roles.

M2M clients call `Authorize`, `ReportUsage`, `CorrectUsage` and service-scoped `ListUsage` under their registered service identity. Treat an unavailable authorization decision as a denial of new paid execution. Retain pending usage reports locally until accepted. Use a stable event key for every measured execution. A correction supplies the original tenant, meter and occurrence time, references the previous event, and replaces its quantity. Corrections and ledger effects append new records.

Post-execution reporting cannot guarantee zero overspend under concurrency. A non-postpaid tenant may incur negative credits; the next top-up offsets that debt. Monetary values are decimal strings. Prorated included quantities are floored to 0.000001; monetary products keep full precision until cumulative service/meter period totals are rounded half-up to currency minor units. Occurrence timestamps are normalized to PostgreSQL microsecond precision. Credits pay usage only. Fixed subscription and add-on fees remain Paddle charges.

Payment requests return a durable job ID. Poll `ListBillingHistory` for payment state and checkout URLs. A checkout return is not proof of payment. One-time charges use Paddle price metadata to correlate the resulting transaction; the subscription response itself never grants credits. Recover payment failures through the payment provider and inspect independently enforced blocking reasons.

## Operations and recovery

Use `/healthz`, `/readyz`, `/metrics`, structured JSON logs and optional OTLP traces. Correlate failures by response IDs. Never include access tokens, invitation tokens, raw webhook bodies or operator configuration in support reports. Metric labels must not contain user or tenant identifiers.

Operators inspect failed jobs and audit history through authenticated APIs. Reprocessing requires a reason. An ambiguous payment returns to reconciliation, not a blind charge retry. If no provider transaction can be identified, inspect Paddle and preserve the failed job for review. Never change a balance directly in PostgreSQL; the ledger is append-only.

Refunds are initiated in Paddle. Allow signed adjustment events to synchronize the result. Deletion cannot finish while debt, a dispute, purchased balance or unresolved payment work remains. Do not delete the shared Logto identity.

Before an update:

1. Stop new traffic and terminate the server gracefully.
2. Create and verify a PostgreSQL backup using `pg_dump` and a disposable restore database. Protect the backup as sensitive data.
3. Preserve the current image reference and matching configuration separately.
4. Run the new image with `-migrate` and inspect the result.
5. Start one new instance and verify readiness and operator access.

Use an older image only if it accepts the installed schema version. Otherwise stop the service, restore the verified backup into a separate database, restore matching configuration, and restart the compatible image. There is no arbitrary schema downgrade command. Reconcile provider events that arrived after the backup before reopening paid execution.

## Threat model

Trust boundaries are clients, Logto, consumer services, PostgreSQL, payment notifications, email delivery and operators. Check tenant scope and current roles on every protected operation. Reject user/M2M substitution, cross-service usage, privilege escalation, expired invitations, unauthorized redirect URLs and forged notifications. Signed events still require local ownership, currency, amount and item checks. Durable keys and transactional transitions protect against duplicate requests and restarts. Limit payloads, work duration and retries. Keep billing recovery available while new paid use is blocked.

A malicious authorized service can submit false usage for its own registered meters. Deployers must trust and monitor their reporting services. No reservation protocol prevents concurrent overspend. PostgreSQL and operator credentials are privileged; database-level administration can bypass application controls.

## Validation and packaging

```sh
go test -count=1 ./servers/delcore-api/...
go vet ./servers/delcore-api/...
go test -count=1 -race -tags=integration ./servers/delcore-api/...
pnpm proto:check
pnpm --filter @delinoio/delcore-api ci:oci
docker build -f "servers/delcore-api/Dockerfile" -t delcore-api:local .
```

Integration tests require `DELCORE_TEST_DATABASE_URL` and fail if it is absent. They create isolated schemas in a disposable test database. Never point them at production. Provider fixtures and OCI builds do not establish live Logto, Paddle or Cloudflare account acceptance. No deployment infrastructure or image publication is included.
