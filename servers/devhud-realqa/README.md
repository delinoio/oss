# DevHud RealQA server

`servers/devhud-realqa` is the inactive server foundation for the private
`devhud.realqa.v1` Connect contract. It implements authenticated personal and
organization preset synchronization, GitHub.com destination/repository schema
selection, typed revisions/conflicts, and scoped feature deletion. It does not
deploy either RealQA origin, register a GitHub App, provision R2 or DNS, enable
billing catalog records, or publish a tracker/plugin interface.

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
- Repository enumeration and issue-schema reads use the caller's bound GitHub
  App user authorization token, or only that caller's short-lived cached access
  when another organization member owns the connection credential, and expose
  only repositories in the user/App intersection. The signed callback binds
  the encrypted credential to the initiating RealQA account, expiring
  credentials are refreshed and transactionally re-sealed on demand, and live
  repository/schema results refresh the caller-scoped preset-validation cache.
  Preset creation also revalidates the selected repository and definition
  through the live adapter when the caller owns the credential. Markdown
  templates and Issue Forms are fetched through GitHub Contents, normalized,
  and provider-required fields/options/defaults are enforced.
- `github-app-manifest.json` is the separate RealQA base manifest with Issues
  write, Metadata read, Contents read, and issue lifecycle delivery. Typed
  manifest generation adds only the explicitly configured repository- or
  organization-project permission.
- `GET /github/oauth/callback`, `GET /github/app/callback`, and
  `POST /github/webhooks` enforce signed, replay-protected state or
  `X-Hub-Signature-256`. Installation/repository, issue deletion, and user
  authorization-revocation events record the delivery and apply all side
  effects in one transaction, so they are durable and idempotent under
  duplicate or racing deliveries. One provider installation can bind to only
  one personal or organization owner.
- The internal adapter normalizes typed labels, assignees, milestone, and
  optional project extensions; composes repository response, `RealQA capture`,
  inline images, and the hidden UUID marker; enforces 60,000 UTF-8 bytes; and
  performs new-issue-only creation. It reconciles recent issues by marker before
  dispatch and after ambiguous results, and never retries when reconciliation
  is unavailable.
- Submission creation revalidates all image declaration limits, then stops
  unavailable before persistence because transfer/storage catalog activation
  and the end-to-end submission orchestrator remain excluded.
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
- `REALQA_GITHUB_APP_SLUG`
- `REALQA_GITHUB_CREDENTIAL_KEY_ID`

Required secrets:

- `REALQA_DATABASE_URL`
- `REALQA_IDENTITY_HASH_KEY` (at least 32 bytes)
- `REALQA_LOG_PSEUDONYM_KEY` (at least 32 bytes)
- `REALQA_GITHUB_OAUTH_CLIENT_SECRET`
- `REALQA_GITHUB_WEBHOOK_SECRET` (at least 32 bytes)
- `REALQA_GITHUB_CALLBACK_SIGNING_KEY` (at least 32 bytes)
- `REALQA_GITHUB_CREDENTIAL_WRAPPING_KEY_BASE64` (exactly 32 decoded bytes)

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
