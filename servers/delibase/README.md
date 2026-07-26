# delibase server foundation

This directory contains the runnable, artifact-only delibase server foundation.
It does not deploy or activate `https://delibase.deli.dev`. Authenticated
`AccountService`, `TeamService`, and `OrganizationService` RPCs (including
invitations), plus the billing summary, Checkout, Customer Portal,
overage-limit, ledger, and role-filtered usage-read RPCs are backed by
PostgreSQL/sqlc transactions. `UsageService` reserve, commit, and release RPCs
use the same database-backed authorization, credit/overage, idempotency, and
durable Polar-outbox boundaries. Polar delivery reports only nonzero locally
settled overage as chargeable USD micro-units. A local worker expires
catalog-TTL holds.

## Runtime configuration

Configuration is environment-owned. This document lists variable names and
categories only; it intentionally provides no values or example secrets.

Required non-secret server configuration:

- `DELIBASE_API_ORIGIN`: the externally visible API origin. It must be the
  canonical `https://delibase.deli.dev`.
- `DELIBASE_CORS_ALLOWED_ORIGINS`: a comma-separated, explicit browser-origin
  allowlist. The future DeliDev client origin is `https://deli.dev`; wildcards
  and credential-bearing origins are rejected.
- `DELIBASE_CATALOG_PATH`: a readable strict versioned JSON catalog path. The
  image includes the intentionally empty foundation catalog at
  `/etc/delibase/catalog.json`; operators may mount another validated file.
- `DELIBASE_LOGTO_ISSUER`: the exact HTTPS OIDC issuer used for user, forwarded
  user, and M2M token validation and as the Logto Management API origin.
- `DELIBASE_LOGTO_AUDIENCE`: the exact canonical
  `https://delibase.deli.dev` audience.
- `DELIBASE_LOGTO_JWKS_URL`: the issuer's HTTPS JWKS endpoint without
  credentials, query, or fragment.
- `DELIBASE_LOGTO_M2M_CLIENT_ID`: the non-secret identifier paired with the
  Management API client secret for provider-side account deletion.
- `DELIBASE_POLAR_PRODUCT_ID`: the provider product whose active fixed monthly
  USD price is validated at startup.

Optional non-secret server configuration:

- `DELIBASE_HTTP_ADDRESS`: the process listen address.
- `DELIBASE_SHUTDOWN_TIMEOUT`: the bounded graceful-shutdown duration.
- `DELIBASE_POLAR_API_URL`: a compatible HTTPS endpoint without credentials,
  query, or fragment. The selected official provider endpoint is used when it
  is absent.
- `DELIBASE_POLAR_ENVIRONMENT`: either `production` or the explicit `sandbox`
  opt-in. Production is the default, and provider credentials/catalogs must
  remain environment-specific.

Required secret server configuration:

- `DELIBASE_DATABASE_URL`: the PostgreSQL connection URL, including its
  credential material and required TLS policy.
- `DELIBASE_LOGTO_M2M_CLIENT_SECRET`: the Logto Management API client secret.
- `DELIBASE_POLAR_ACCESS_TOKEN`: the Polar API credential.
- `DELIBASE_POLAR_WEBHOOK_SECRET`: the Polar webhook verification secret.
- `DELIBASE_LOG_PSEUDONYM_KEY`: a distinct key of at least 32 bytes used only
  for stable log/audit pseudonyms.

Pass secrets through the runtime's secret manager. Do not bake them into image
layers, labels, catalog files, workflow build arguments, or checked-in
environment files. Configuration errors identify variable names but never
include configured values.

`DELIBASE_CATALOG_PATH` points to a strict versioned JSON catalog. The checked-in
`catalog.json` is intentionally empty for this foundation. Startup validates the
complete document and transactionally synchronizes apps, meters, price versions,
service allowlists, and Polar meter mappings before readiness is exposed.
Startup also fetches `DELIBASE_POLAR_PRODUCT_ID` from the selected Polar
environment and requires one active fixed monthly USD $10 base price before
pinning that provider catalog to the local grant contract.

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

Browser configuration belongs to the DeliDev build, is non-secret, and is not
consumed by this process:

- `PUBLIC_DELIBASE_API_ORIGIN`: the canonical
  `https://delibase.deli.dev` API origin.
- `PUBLIC_LOGTO_ENDPOINT`: the browser-safe Logto endpoint.
- `PUBLIC_LOGTO_APP_ID`: the browser application's public identifier.
- `PUBLIC_LOGTO_AUDIENCE`: the same canonical
  `https://delibase.deli.dev` audience.

These `PUBLIC_*` variables must never contain PostgreSQL, Logto M2M, Polar, or
other provider credentials. The two canonical origins are documented targets,
not evidence that DNS or either public service is active.

## Container and release artifacts

The multi-stage image cross-compiles a static Go executable, then copies only
that executable and the foundation catalog into a distroless runtime. Its
declared runtime identity is UID/GID `65532:65532`; it contains no shell,
compiler, source tree, repository metadata, or configured secret.

`.github/workflows/release-delibase.yml` runs only for pushed
`delibase@vX.Y.Z` tags with core SemVer numbers and no leading zeroes. It
pushes each `linux/amd64` and `linux/arm64` build by digest, pulls and tests
that exact digest, and assembles the validated digests into a run-scoped
candidate index. Only after the candidate is signed and its SPDX and GitHub
attestations succeed does it publish the public release references:

- `ghcr.io/delinoio/delibase:vX.Y.Z`
- `ghcr.io/delinoio/delibase:latest`

The immutable published digest receives a keyless Sigstore/Cosign signature,
an uploaded SPDX JSON SBOM, and GitHub build-provenance and SBOM attestations.
The `latest` promotion is serialized across releases and occurs only when the
current version remains the newest pushed core SemVer, so an older release
cannot roll it backward. Run-scoped candidate references are not release
channels. The workflow does not publish branch, `edge`, or commit-SHA tags and
does not deploy the image.

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
multiple-owner, slug-alias, five-level/cycle-safe team hierarchy,
downward-only effective access, protected/subtree deletion, hashed reusable
invitations, revocation/expiration, and retention-safe deletion integration
tests. Usage coverage includes exact-boundary concurrent holds, partial
commit/release, refund shortfall, expiration and late commits, idempotency,
role-filtered visibility, immutable snapshots, Polar outage recovery, and
active-reservation subtree-deletion blocking:

```sh
servers/delibase/scripts/test-postgres.sh
```

With PostgreSQL listening on port 5432, validate the minimal non-root image and
its live health/readiness endpoints:

```sh
servers/delibase/scripts/test-image.sh
```

The operational endpoints are `GET /healthz` for process liveness and
`GET /readyz` for PostgreSQL readiness. Both return JSON with `Cache-Control:
no-store`; liveness returns HTTP 200 while the process can serve requests,
readiness returns HTTP 200 only after a bounded PostgreSQL ping and HTTP 503
when that dependency is unavailable.
