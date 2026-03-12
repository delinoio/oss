# Feature: interfaces

## Interfaces
Canonical thenv component identifiers:

```ts
enum ThenvComponent {
  Cli = "cli",
  Server = "server",
  WebConsole = "web-console",
}
```

Devkit route contract:
- `/apps/thenv`
- Current route state: metadata management console (no plaintext payload rendering).
- Audit filter contract:
: Devkit audit proxy route `GET /api/thenv/audit` supports optional `fromTime` and `toTime` query parameters and forwards them to `AuditService.ListAuditEvents`.
- Devkit pagination contract:
: Devkit versions and audit views consume `nextCursor` and allow incremental page loading through explicit load-more actions.
: If a full reload or filtered audit reload fails, stale pagination cursors are cleared before load-more actions can run.
: In-flight load-more responses are discarded when scope or audit filter context changes before the response resolves.
- Devkit proxy input validation contract:
: Scope defaults to `DEFAULT_THENV_SCOPE` when omitted, but explicit blank values are rejected with `400`.
: Pagination fields enforce `limit` as integer `1..100` (default `20`) and `cursor` as empty or non-negative integer string.
: `GET /api/thenv/audit` enforces `eventType` against `ThenvAuditEventType` enum values.
: `PUT /api/thenv/policy` enforces binding `role` against `ROLE_READER | ROLE_WRITER | ROLE_ADMIN` and rejects malformed bindings.
: Invalid request shapes, including malformed JSON in body routes, return deterministic `400` responses.
: Upstream RPC/backend payload parse failures are surfaced as `502` proxy errors (not `400` input errors).

Connect RPC services (implemented):
- `BundleService`
: `PushBundleVersion`
: `PullActiveBundle`
: `ListBundleVersions`
: `ActivateBundleVersion`
: `RotateBundleVersion`
- `PolicyService`
: `GetPolicy`
: `SetPolicy`
- `AuditService`
: `ListAuditEvents`

Proto file type identifiers:

```txt
FILE_TYPE_ENV
FILE_TYPE_DEV_VARS
```

Proto role identifiers:

```txt
ROLE_READER
ROLE_WRITER
ROLE_ADMIN
```

Role authorization contract:
- `ROLE_READER`
: Allowed: `PullActiveBundle`, `ListBundleVersions`.
: Denied: `PushBundleVersion`, `RotateBundleVersion`, `ActivateBundleVersion`, `SetPolicy`, `ListAuditEvents`.
- `ROLE_WRITER`
: Allowed: reader operations plus `PushBundleVersion`, `RotateBundleVersion`.
: Denied: `SetPolicy`, `ActivateBundleVersion`, `ListAuditEvents`.
- `ROLE_ADMIN`
: Allowed: all writer operations plus `ActivateBundleVersion`, `GetPolicy`, `SetPolicy`, `ListAuditEvents`.

CLI command contract:
- `thenv push --workspace <id> --project <id> --env <id> [--env-file <path>] [--dev-vars-file <path>] [--server <url>] [--token <token>] [--subject <subject>]`
: Requires at least one input file.
: Creates a new version in target scope.
- `thenv pull --workspace <id> --project <id> --env <id> [--output-env-file <path>] [--output-dev-vars-file <path>] [--version <id>] [--force] [--server <url>] [--token <token>] [--subject <subject>]`
: Default conflict policy is `fail-closed`.
: If target output exists and content differs, operation fails unless `--force` is supplied.
: Output files are written with restrictive default permissions (`0600`).
- `thenv list --workspace <id> --project <id> --env <id> [--limit <n>] [--cursor <token>] [--server <url>] [--token <token>] [--subject <subject>]`
: Returns version metadata only.
- `thenv rotate --workspace <id> --project <id> --env <id> [--from-version <id>] [--server <url>] [--token <token>] [--subject <subject>]`
: Creates a new version and moves active pointer to that version.

