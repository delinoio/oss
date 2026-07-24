# delibase server foundation

This directory contains the runnable, artifact-only delibase server foundation.
It does not deploy or activate `https://delibase.deli.dev`. Authenticated
`AccountService` and core non-invitation `OrganizationService` RPCs are backed
by PostgreSQL/sqlc transactions. Invitation and hierarchy/billing/usage RPCs
outside this implementation slice return Connect `Unimplemented`; none return
placeholder success.

## Configuration categories

Configuration is environment-owned. This document lists variable names and
categories only; it intentionally provides no values or example secrets.

Non-secret server configuration:

- `DELIBASE_HTTP_ADDRESS`
- `DELIBASE_SHUTDOWN_TIMEOUT`
- `DELIBASE_API_ORIGIN`
- `DELIBASE_CORS_ALLOWED_ORIGINS`
- `DELIBASE_CATALOG_PATH`
- `DELIBASE_LOGTO_ISSUER`
- `DELIBASE_LOGTO_AUDIENCE`
- `DELIBASE_LOGTO_JWKS_URL`

Secret configuration:

- `DELIBASE_DATABASE_URL`
- `DELIBASE_LOGTO_M2M_CLIENT_ID`
- `DELIBASE_LOGTO_M2M_CLIENT_SECRET`
- `DELIBASE_POLAR_ACCESS_TOKEN`
- `DELIBASE_POLAR_WEBHOOK_SECRET`
- `DELIBASE_LOG_PSEUDONYM_KEY`

The canonical API origin and Logto audience are both
`https://delibase.deli.dev`. Configuration errors identify variable names but
never include configured values.

`DELIBASE_CATALOG_PATH` points to a strict versioned JSON catalog. The checked-in
`catalog.json` is intentionally empty for this foundation. Startup validates the
complete document and transactionally synchronizes apps, meters, price versions,
service allowlists, and Polar meter mappings before readiness is exposed.

## Persistence reliability

`internal/reliability` supplies typed transaction-bound enqueue functions for
the Polar webhook inbox, Polar/Logto integration outbox, account/organization
deletion jobs, and immutable audit events. Workers register typed handlers and
use leased skip-locked PostgreSQL claims. Normal failures receive exactly 12
capped exponential-backoff attempts; retained dead letters are automatically
eligible every 24 hours until success. Clocks, jitter, and claim tokens are
injectable in tests. The layer has no manual replay API, operator RPC,
dashboard, kill switch, or feature-flag surface.

Queue payloads accept bounded JSON objects only and reject credential, token,
authorization-header, webhook-secret, card, and raw billing-PII shapes.
Operational worker logs contain only stable handler/queue identifiers, safe
UUID entity identifiers, pseudonymous actors, attempt/result state, and safe
error classifications.

Account deletion immediately disables local access, erases the operational
profile and memberships, and queues a Logto Management API deletion. The
deletion worker retries through the shared durable queue. After provider
success, it removes the raw Logto subject while a digest-only tombstone prevents
re-onboarding with an unexpired token. Account/organization tombstones and
financial/audit snapshots carry an explicit minimum seven-year retention
boundary.

Browser configuration belongs to DeliDev, is non-secret, and is not consumed
by this process:

- `PUBLIC_DELIBASE_API_ORIGIN`
- `PUBLIC_LOGTO_ENDPOINT`
- `PUBLIC_LOGTO_APP_ID`
- `PUBLIC_LOGTO_AUDIENCE`

## Local validation

Run generated sqlc consistency and Go checks from the repository root:

```sh
servers/delibase/scripts/generate-sqlc.sh
git diff --exit-code -- servers/delibase/internal/database/dbgen
go test ./servers/delibase/...
go vet ./servers/delibase/...
```

When Docker is available, the PostgreSQL harness creates an ephemeral database,
applies the ordered migrations twice, and runs transaction, duplicate enqueue,
concurrent claim, crash/restart, retry/dead-letter, exact transition, immutable
audit, credential-rejection, authenticated onboarding/organization race,
multiple-owner, slug-alias, and retention-safe deletion integration tests:

```sh
servers/delibase/scripts/test-postgres.sh
```

With PostgreSQL listening on port 5432, validate the minimal non-root image and
its live health/readiness endpoints:

```sh
servers/delibase/scripts/test-image.sh
```

The operational endpoints are `GET /healthz` for process liveness and
`GET /readyz` for PostgreSQL readiness.
