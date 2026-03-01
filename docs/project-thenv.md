# Project: thenv

## Goal
`thenv` provides secure sharing of `.env` and `.dev.vars` files across teams with explicit trust boundaries.
It is a multi-component system composed of a Go CLI, backend server, and Devkit web console.
Phase 1 MVP is implemented as a metadata-safe vertical slice at `workspace/project/environment` scope.

## Path
- CLI: `cmds/thenv`
- Server: `servers/thenv`
- Connect proto contract: `servers/thenv/proto/thenv/v1/thenv.proto`
- Generated Go RPC code (gitignored; regenerate via `./scripts/generate-go-proto.sh`): `servers/thenv/gen/proto/thenv/v1`
- Web console mini app: `apps/devkit/src/apps/thenv`
- Web console route: `apps/devkit/src/app/apps/thenv/page.tsx`
- Devkit API proxy routes: `apps/devkit/src/app/api/thenv/*`

## Runtime and Language
- CLI: Go
- Server: Go
- Web console: Next.js 16 mini app (TypeScript)

## Users
- Developers who need secure distribution of environment variables
- Team operators managing shared environment sets

## In Scope
- Secure publish and retrieval workflows for `.env` and `.dev.vars` payloads.
- Versioned bundle model with immutable versions and active pointer management.
- Multi-file bundle support (`ENV`, `DEV_VARS`) in a single version.
- Namespace and policy model: `workspace/project/environment` plus RBAC.
- Server-side envelope encryption for at-rest protection.
- Connect RPC service contracts for business operations.
- CLI contracts for `push`, `pull`, `list`, and `rotate`.
- Audit and operational logging contracts without secret value exposure.
- Devkit web console management UX for metadata, policy, and audit visibility.

## Out of Scope
- Replacing all enterprise secret manager use cases.
- Executing arbitrary remote scripts through environment distribution.
- Storing plaintext secret material in frontend code or browser storage.
- End-to-end client-only encryption in Phase 1.
- Per-key ACL policy in Phase 1.
- Merge-on-pull behavior as a default sync strategy.
- Full OIDC signature validation and external KMS integration in MVP.

## Architecture
- CLI (`cmds/thenv`) handles local workflows:
: Local file parse (`.env`, `.dev.vars`), push orchestration, pull file materialization, and conflict enforcement.
- Server (`servers/thenv`) handles business flows over Connect RPC:
: Bundle version storage, active pointer state, policy enforcement, envelope encryption/decryption, and audit event persistence.
- Web console (`apps/devkit/src/apps/thenv`) handles management and visibility:
: Version inventory, active version switching, role policy management, and audit browsing without secret value rendering.
- Devkit API routes (`apps/devkit/src/app/api/thenv/*`) proxy web requests to Connect RPC procedures.

Trust boundary and plaintext handling:
- Plaintext is allowed in CLI process memory when reading local source files and writing pulled output files.
- Plaintext is allowed in server process memory only during authorized encrypt/decrypt paths.
- Plaintext is not allowed in persistent server storage, logs, metrics labels, frontend state, or browser storage.

Communication boundary:
- Business flows use Connect RPC between clients (CLI/web backend adapters) and `servers/thenv`.
- Tauri-specific bindings are not part of the thenv business contract.

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

## Storage
Server-owned logical entities (SQLite):
- `bundle_versions`
: `bundle_version_id`, scope IDs, `status`, `created_by`, `created_at_unix_ns`, `source_version_id`.
- `bundle_file_payloads`
: `bundle_version_id`, `file_type`, ciphertext payload, encrypted DEK, nonces, checksum, byte length.
- `active_bundle_pointers`
: scope IDs, active `bundle_version_id`, `updated_by`, `updated_at_unix_ns`.
- `policy_bindings`
: scope IDs, subject identifier, `role`.
- `policy_revisions`
: scope IDs, monotonically increasing `revision`.
- `audit_events`
: `event_id`, `event_type`, actor metadata, scope IDs, target version metadata, outcome, timestamp, request correlation IDs.

Local and frontend storage:
- CLI does not persist decrypted secrets outside destination files explicitly written by pull.
- Web console stores metadata view state only and never stores secret payloads.

## Security
- Transport security:
: Deployments should expose RPC traffic over TLS.
: Local MVP development defaults to `http://127.0.0.1:8087`.
- At-rest security:
: Server-side envelope encryption is required for bundle payloads.
: Each payload uses a random DEK (AES-256-GCM).
: DEK is encrypted with a master key from `THENV_MASTER_KEY_B64` (AES-256-GCM).
- Authentication (MVP):
: Subject is resolved from `X-Thenv-Subject` only.
: `X-Thenv-Subject` must exactly match the `Authorization: Bearer <token>` value for authorization to proceed.
: Bearer token payload/claims are not parsed for identity derivation in the server.
: Requests without explicit `X-Thenv-Subject` are rejected as unauthenticated.
: Raw bearer token bytes are never stored in actor-facing metadata fields.
: If subject equals bearer token value (legacy compatibility), actor is stored as deterministic redaction `token_sha256:<prefix>`.
- Authorization:
: RBAC checks are applied for every RPC operation at `workspace/project/environment` scope.
: Deny by default on missing bindings.
: Bootstrap admin subject is configurable (`THENV_BOOTSTRAP_ADMIN_SUBJECT`, default `admin`).
- Secret exposure rules:
: Never show full secret values in CLI default output or web console output.
: Never return secret payloads from policy/audit/list operations.
: Web console is metadata-only for secret data.
- File output safety:
: Pull writes files with restrictive default permissions (`0600`).
: Existing file conflicts require explicit `--force` override.
- Audit requirements:
: Sensitive operations emit immutable audit events with actor/scope/outcome metadata.

## Logging
Required baseline logs:
- `operation`
- `event_type`
- `actor`
- `auth_identity_source`
- `scope`
- `role_decision`
- `bundle_version_id` and `target_bundle_version_id` when applicable
- `file_types` for bundle operations
- `conflict_policy` for CLI pull operations
- `result`
- `request_id` and `trace_id`

Result semantics:
- Authorization and authentication rejections must emit `role_decision=deny` and `result=denied`.

Prohibited log content:
- Plaintext secret values
- Full `.env` or `.dev.vars` payloads
- Decrypted key material
- Raw authentication tokens

## Runtime Defaults
Server environment variables:
- `THENV_ADDR` (default: `127.0.0.1:8087`)
- `THENV_DB_PATH` (default: `${XDG_CONFIG_HOME or OS config dir}/thenv/thenv.sqlite3`)
- `THENV_MASTER_KEY_B64` (required, base64-encoded 32-byte key)
- `THENV_BOOTSTRAP_ADMIN_SUBJECT` (default: `admin`)

CLI environment variables:
- `THENV_SERVER_URL` (default: `http://127.0.0.1:8087`)
- `THENV_TOKEN` (default: `admin`)
- `THENV_SUBJECT` (optional; defaults to `THENV_TOKEN` value, and must match token for server authorization)

Devkit environment variables (optional):
- `THENV_SERVER_URL` or `NEXT_PUBLIC_THENV_SERVER_URL`
- `THENV_WEB_TOKEN` or `THENV_TOKEN` or `NEXT_PUBLIC_THENV_TOKEN`
- `THENV_WEB_SUBJECT` or `THENV_SUBJECT` or `NEXT_PUBLIC_THENV_SUBJECT` (defaults to resolved token value and must match token for server authorization)

## Build and Test
Current commands:
- Proto generation prerequisite: `./scripts/generate-go-proto.sh`
- CLI build/test: `go build ./cmds/thenv/...` and `go test ./cmds/thenv/...`
- Server build/test: `go build ./servers/thenv/...` and `go test ./servers/thenv/...`
- Web console tests: `cd apps/devkit && pnpm test`

Acceptance-focused scenarios:
1. Push `.env` only.
2. Push `.dev.vars` only.
3. Push both file types in one version.
4. Pull creates missing output files with `0600` permissions.
5. Pull fails on content conflict by default.
6. Pull succeeds on conflict with explicit `--force`.
7. `ROLE_READER` can pull/list only.
8. `ROLE_WRITER` can push/rotate plus reader operations.
9. `ROLE_ADMIN` can activate/set policy/list audit.
10. `rotate` creates a new version and updates active pointer.
11. Sensitive operations emit audit metadata without plaintext values.
12. Web console renders metadata only and never plaintext secrets.
13. CLI pull conflict failures and pull successes both emit structured baseline logs including `conflict_policy`, `request_id`, and `trace_id`.

## Roadmap
- Phase 1: Connect RPC foundation, versioned multi-file bundles, RBAC, and secure push/pull/list/rotate flows.
- Phase 2: OIDC/JWT verification, richer audit filtering/export, and operational hardening.
- Phase 3: External KMS integration, key rotation automation, and retention controls.
- Phase 4: Enterprise governance features (compliance controls, delegated administration, policy automation).

## Open Questions
- OIDC provider and tenant-mapping strategy for production identity.
- KMS backend selection and key lifecycle SLOs for production deployments.
- Maximum payload size and rate-limiting defaults for push/pull APIs.
- Fine-grained audit read permissions for non-admin roles.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
