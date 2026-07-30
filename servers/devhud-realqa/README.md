# DevHud RealQA server

`servers/devhud-realqa` is the inactive server foundation for the private
`devhud.realqa.v1` Connect contract. It implements authenticated personal and
organization preset synchronization, GitHub.com destination/repository schema
selection, typed revisions/conflicts, private image transfer, sanitized
PNG/WebP verification, replay-safe live transfer billing, submission-bound
initial storage authorization, exact-body duplicate-safe GitHub issue
submission, durable public-image promotion/tombstones, and scoped feature
deletion. It does not deploy either RealQA origin, register a GitHub App,
provision R2 or DNS, enable billing catalog records, or publish a
tracker/plugin interface.

## Implemented boundary

- PostgreSQL with strictly ordered, checksummed migrations and sqlc v1.30.0
  generated access.
- UUID v7 resource, rule, shortcut, idempotency, audit, and deletion identities.
- `GET /healthz` and PostgreSQL-backed `GET /readyz`.
- RealQA-audience user bearer plus delibase-audience forwarded user bearer
  validation, matching subjects, route scopes, and credential stripping before
  handlers. Lifecycle deletion alone accepts the configured exact delibase M2M
  client (`sub == client_id == configured client ID`) and rejects a forwarded
  bearer.
- Personal and organization preset list/get/create/update/delete with nested
  destinations, issue template/form selection, labels, assignees, milestone,
  projects, process/title rules, shortcut definitions, payer organization/team,
  50/250 limits, UUID-v7 creation replay, and typed stale-revision ETags.
- Organization Owner/Admin management; Owner-only organization feature
  deletion; and live account/organization/repository access bindings.
- Repository enumeration and issue-schema reads use only the caller's bound
  GitHub App user authorization token and expose repositories in that user/App
  intersection. Owner/Admin setup binds installations after verifying them
  through the OAuth user's live installation list. Organization members use the
  same connection-start RPC for an OAuth-only flow that stores a separate
  caller-scoped encrypted credential only after confirming that their GitHub
  identity can access an active installation already bound to the organization.
  Expiring credentials are refreshed only after the applicable connection or
  caller authorization is durably cleared; the rotated credential is
  compare-and-swap re-sealed, while any provider or persistence failure requires
  only that caller to reconnect unless it was the owner connection credential.
  Live repository/schema results refresh the caller-scoped preset-validation cache.
  Preset creation also revalidates the selected repository and definition
  through the live adapter when the caller owns the credential. Markdown
  templates and Issue Forms are fetched through GitHub Contents, normalized,
  and provider-required fields/options/defaults are enforced. Archived
  repositories remain visible but are never submit-capable. Current
  top-level issue type metadata, textarea render languages, and ID-less inputs
  are accepted; optional provider upload controls are omitted in favor of
  RealQA's bounded image workflow.
- `github-app-manifest.json` is the separate RealQA base manifest with Issues
  write, Metadata read, Contents read, issue lifecycle delivery, and
  installation-target plus repository rename delivery. Typed manifest generation
  adds only the explicitly configured repository- or organization-project
  permission.
- `GET /github/oauth/callback`, `GET /github/app/callback`, and
  `POST /github/webhooks` enforce signed, replay-protected state or
  `X-Hub-Signature-256`. Installation/repository access, repository rename,
  issue deletion, and user authorization-revocation events record the delivery
  and apply all side effects in one transaction, so they are durable and
  idempotent under duplicate or racing deliveries. One provider installation
  can bind to only one personal or organization owner.
- The internal adapter normalizes typed labels, assignees, milestone, and
  optional project extensions; composes repository response, `RealQA capture`,
  inline images, and the hidden UUID marker; enforces 60,000 UTF-8 bytes; and
  performs new-issue-only creation. It reconciles recent visible repository
  issues by marker across credential changes before dispatch and after
  ambiguous results, and never retries when reconciliation is unavailable.
  Post-create project assignment reports per-project applied/failed
  dispositions without masking a successfully created or reconciled issue.
- Submission creation revalidates repository access and the 25 MiB/image,
  250 MiB/submission, and 100 MP/image limits before creating private staging
  state.
- One draft UUID v7 is retained as the submission idempotency root. Creation
  validates the disabled catalog mapping, reserves aggregate declared
  `encoded_mib` once at 500 USD micros/MiB, persists exact stable downstream
  keys, and enforces three concurrent live upload sessions per user.
- `SubmitIssue` accepts at most 30 new attempts/hour/user, requires a fresh
  explicit public-image confirmation and a forwarded bearer with
  `delibase:usage:execute` plus `delibase:billing:write`, commits the aggregate
  verified declared bytes once (or releases a zero-verified reservation), then
  durably creates and validates the exact submission-bound `REALQA_STORAGE`
  authorization before composing/confirming the final body and reconciling the
  hidden GitHub marker. Verified transfer remains committed when later provider
  work fails. Only provider identifiers/URLs, asset state, and request/body
  digests persist.
- Five-minute-or-upload-deadline same-origin signed PUT URLs bind one asset's
  content type, SHA-256, encoded length, dimensions, and private token digest.
  The handler never exposes the R2 S3 endpoint.
- Finalization independently decodes and pixel-only re-encodes PNG/WebP,
  strips metadata, checks the sanitized output again, and marks an asset
  verified only while the submission total remains within 250 MiB.
- Submitted-image promotion uses 16 random bytes per public identifier. Public
  GET exposes neither a list, object key, sequential identifier, nor signed GET;
  deletion leaves an immutable tombstone returning `Image removed`.
- A background cleanup pass expires unlinked private objects at 24 hours.
  Explicit image/all-assets, signed GitHub issue-deletion webhook,
  account/organization/feature, and billing-expiry paths share tombstone-first
  deletion with durable, retryable R2 cleanup work. Same-issue body cleanup is
  best effort and cannot gate deletion.
- `artifacts/cloudflare-public-images.fixture.json` is a non-deploying WAF and
  300 GETs/minute/IP fixture scoped only to `/i/`.
- Application-envelope columns store GitHub credentials only as ciphertext,
  wrapped data keys, and a versioned key ID. The active wrapping key seals new
  records while configured previous key versions remain decryptable and are
  transactionally rewrapped on the next authorized use. Bearers and OAuth state
  plaintext have no persistence fields; OAuth state is stored only as a digest.
- Append-only typed audits and redacted structured operational logging.
- Owner/account/organization feature deletion tombstones access first and
  removes scoped presets, submissions/assets, destinations, installation
  bindings, connection state, encrypted credentials, and callback state.
  Account-lifecycle replays also clear credentials that the deleted account
  connected for an organization even when its personal scope is already absent.

OS capture permission, Chrome optional-host permission, shortcut registration
results, and extension pairing are device state. There are deliberately no
server columns or RPCs for them.

## Rust-compatible safe title regex subset

Title patterns are compiled by Go's RE2-derived non-backtracking engine, with a
contract intentionally limited to syntax shared with Rust's `regex` crate:

- literals, `.`, `^`, `$`, `\A`, and `\z`;
- character classes/ranges and Unicode classes;
- capturing and non-capturing groups, alternation, `?`, `*`, `+`, and bounded
  `{m,n}` repetition;
- scoped `i`, `m`, `s`, and `U` flags.

The server rejects look-around, backreferences/numeric escapes, named captures,
octal escapes, `\C`, `\Q...\E`, character-class set algebra, and free-spacing
mode. Pattern source is limited to 512 bytes, explicit repetition bounds to
100, and the compiled program to 2,048 instructions. A preset has at most 64
rules. Evaluation is deterministic: declaration order first, exact
case-sensitive process name next, then the optional title pattern. URL
templates support common `$1`/`${1}` capture expansion and must produce an
HTTP/HTTPS URL without credentials.

## Configuration

Required non-secret values:

- `REALQA_API_ORIGIN=https://realqa.deli.dev`
- `REALQA_LOGTO_ISSUER`
- `REALQA_LOGTO_JWKS_URL`
- `REALQA_LOGTO_AUDIENCE=https://realqa.deli.dev`
- `REALQA_DELIBASE_LOGTO_AUDIENCE=https://delibase.deli.dev`
- `REALQA_DELIBASE_API_ORIGIN=https://delibase.deli.dev`
- `REALQA_DELIBASE_SERVICE_IDENTITY_ID` (the exact UUID v7 authorization target
  for both RealQA meters)
- `REALQA_DELIBASE_LOGTO_M2M_CLIENT_ID`
- `REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID`
- `REALQA_GITHUB_OAUTH_CLIENT_ID`
- `REALQA_GITHUB_APP_SLUG`
- `REALQA_GITHUB_CREDENTIAL_KEY_ID`
- `REALQA_ASSET_ORIGIN=https://assets.realqa.deli.dev`
- `REALQA_R2_ENDPOINT` (the account's HTTPS
  `r2.cloudflarestorage.com` S3-compatible endpoint)
- `REALQA_R2_BUCKET`
- `REALQA_R2_ACCESS_KEY_ID`

Required secrets:

- `REALQA_DATABASE_URL`
- `REALQA_DELIBASE_LOGTO_M2M_CLIENT_SECRET`
- `REALQA_IDENTITY_HASH_KEY` (at least 32 bytes)
- `REALQA_LOG_PSEUDONYM_KEY` (at least 32 bytes)
- `REALQA_GITHUB_OAUTH_CLIENT_SECRET`
- `REALQA_GITHUB_WEBHOOK_SECRET` (at least 32 bytes)
- `REALQA_GITHUB_CALLBACK_SIGNING_KEY` (at least 32 bytes)
- `REALQA_GITHUB_CREDENTIAL_WRAPPING_KEY_BASE64` (exactly 32 decoded bytes)
- `REALQA_UPLOAD_SIGNING_KEY` (at least 32 bytes)
- `REALQA_R2_SECRET_ACCESS_KEY`

Optional rotation secret:

- `REALQA_GITHUB_CREDENTIAL_PREVIOUS_KEYS_BASE64_JSON` is an optional
  JSON object mapping up to 32 previous key IDs to their base64-encoded
  32-byte wrapping keys. Retain an old entry until all rows using that key ID
  have been rewrapped.

Optional process settings are `REALQA_HTTP_ADDRESS` (default `:8080`) and
`REALQA_SHUTDOWN_TIMEOUT` (default `10s`). Optional
`REALQA_GITHUB_PROJECT_PERMISSION` accepts only `none` (default), `repository`,
or `organization`. Optional GitHub origin assertions accept only exact
`https://github.com` and `https://api.github.com`; GHES and custom hosts fail
startup. Configuration values are never logged.

## Validation

From the repository root:

```sh
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0
servers/devhud-realqa/scripts/check-sqlc.sh
go test ./servers/devhud-realqa/...
go vet ./servers/devhud-realqa/...
```

Set `REALQA_TEST_DATABASE_URL` to run the concurrent/idempotent PostgreSQL
migration test. These checks create only local fixtures; they do not deploy,
publish, register, provision, or activate anything.
