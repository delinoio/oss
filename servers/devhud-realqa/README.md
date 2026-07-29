# DevHud RealQA server

`servers/devhud-realqa` is the inactive server foundation for the private
`devhud.realqa.v1` Connect contract. It implements authenticated personal and
organization preset synchronization, GitHub.com destination/repository schema
selection, typed revisions/conflicts, private image transfer, sanitized
PNG/WebP verification, durable public-image tombstones, and scoped feature
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
- Repository enumeration and issue-schema reads expose only repositories the
  caller's GitHub identity can access. Submission creation revalidates the same
  access and the 25 MiB/image, 250 MiB/submission, and 100 MP/image limits
  before creating private staging state.
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
  deletion. Same-issue body cleanup is best effort and cannot gate deletion.
- `artifacts/cloudflare-public-images.fixture.json` is a non-deploying WAF and
  300 GETs/minute/IP fixture scoped only to `/i/`.
- Application-envelope columns store GitHub credentials only as ciphertext,
  wrapped data keys, and a versioned key ID. Bearers and OAuth state plaintext
  have no persistence fields; OAuth state is stored only as a digest.
- Append-only typed audits and redacted structured operational logging.
- Owner/account/organization feature deletion tombstones access first and
  removes scoped presets, submissions/assets, destinations, installation
  bindings, connection state, encrypted credentials, and callback state.

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
- `REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID`
- `REALQA_GITHUB_OAUTH_CLIENT_ID`
- `REALQA_ASSET_ORIGIN=https://assets.realqa.deli.dev`
- `REALQA_R2_ENDPOINT` (the account's HTTPS
  `r2.cloudflarestorage.com` S3-compatible endpoint)
- `REALQA_R2_BUCKET`
- `REALQA_R2_ACCESS_KEY_ID`

Required secrets:

- `REALQA_DATABASE_URL`
- `REALQA_IDENTITY_HASH_KEY` (at least 32 bytes)
- `REALQA_LOG_PSEUDONYM_KEY` (at least 32 bytes)
- `REALQA_UPLOAD_SIGNING_KEY` (at least 32 bytes)
- `REALQA_R2_SECRET_ACCESS_KEY`
- `REALQA_GITHUB_WEBHOOK_SECRET` (at least 32 bytes)

Optional process settings are `REALQA_HTTP_ADDRESS` (default `:8080`) and
`REALQA_SHUTDOWN_TIMEOUT` (default `10s`). Configuration values are never
logged.

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
