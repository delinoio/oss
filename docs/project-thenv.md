# Project: thenv

## Goal
`thenv` provides secure sharing of `.env` and `.dev.vars` files across teams with explicit trust boundaries.
Phase 1 is implemented with a Go CLI, Go Connect RPC server, and a Devkit web console for metadata-only operations.

## Path
- CLI: `cmds/thenv`
- Server: `servers/thenv`
- Web console mini app: `apps/devkit/src/apps/thenv`
- Connect schema: `protos/thenv/v1/thenv.proto`
- Shared Go API types: `pkg/thenv/api`

## Runtime and Language
- CLI: Go
- Server: Go
- Web console: Next.js 16 mini app (TypeScript)
- RPC schema: Protobuf (`proto3`)

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
- Devkit web console management UX for metadata, policy, activation, and audit visibility.

## Out of Scope
- Replacing all enterprise secret manager use cases.
- Executing arbitrary remote scripts through environment distribution.
- Storing plaintext secret material in frontend code or browser storage.
- End-to-end client-only encryption in Phase 1.
- Per-key ACL policy in Phase 1.
- Merge-on-pull behavior as a default sync strategy.

## Architecture
- CLI (`cmds/thenv`) handles local workflows:
: Local file parse (`.env`, `.dev.vars`), push orchestration, pull file materialization, and conflict enforcement.
- Server (`servers/thenv`) handles business flows over Connect RPC:
: Bundle version storage, active pointer state, policy enforcement, authorized decrypt for pull, and immutable audit persistence.
- Web console (`apps/devkit/src/apps/thenv`) handles management and visibility:
: Version inventory, active version switching, role policy management, and audit browsing without secret value rendering.

Trust boundary and plaintext handling:
- Plaintext is allowed in CLI process memory when reading local source files and writing pulled output files.
- Plaintext is allowed in server process memory only during authorized encrypt/decrypt paths.
- Plaintext is not allowed in persistent server storage, logs, metrics labels, frontend state, or browser storage.

Communication boundary:
- Business flows use Connect RPC between clients (CLI/web server adapter) and `servers/thenv`.
- Web console calls Connect RPC through server-side adapters (`apps/devkit/src/server/thenv-api.ts`) rather than browser-direct business calls.

## Interfaces
Canonical thenv component identifiers:

```ts
enum ThenvComponent {
  Cli = "cli",
  Server = "server",
  WebConsole = "web-console",
}
```

Component mapping contract:
- `Cli` -> `cmds/thenv`
- `Server` -> `servers/thenv`
- `WebConsole` -> `apps/devkit/src/apps/thenv`

Devkit route contract for web console:
- `/apps/thenv`

Connect schema contract:
- Source of truth: `protos/thenv/v1/thenv.proto`
- Services:
: `BundleService` (`PushBundleVersion`, `PullActiveBundle`, `ListBundleVersions`, `ActivateBundleVersion`, `RotateBundleVersion`)
: `PolicyService` (`GetPolicy`, `SetPolicy`)
: `AuditService` (`ListAuditEvents`)

Namespace contract:
- Every bundle operation includes `workspaceId`, `projectId`, and `environmentId`.
- CLI scope flags are mandatory for all core commands:
: `--workspace <id> --project <id> --env <id>`

Role authorization contract:
- `Reader`
: Allowed: `PullActiveBundle`, `ListBundleVersions`.
: Denied: `PushBundleVersion`, `RotateBundleVersion`, `ActivateBundleVersion`, `SetPolicy`.
- `Writer`
: Allowed: all `Reader` operations plus `PushBundleVersion`, `RotateBundleVersion`.
: Denied: `SetPolicy`, `ActivateBundleVersion` unless promoted.
- `Admin`
: Allowed: all `Writer` operations plus `ActivateBundleVersion`, `GetPolicy`, `SetPolicy`, `ListAuditEvents`.

Implemented command contract:
- `thenv push --workspace <id> --project <id> --env <id> [--env-file <path>] [--dev-vars-file <path>]`
: Requires at least one input file.
: Creates a new immutable version.
: Does not move active pointer.
- `thenv pull --workspace <id> --project <id> --env <id> [--output-env-file <path>] [--output-dev-vars-file <path>] [--force]`
: Default conflict policy is fail-closed.
: If target output exists and content differs, operation fails unless `--force` is provided.
: Output files are written and chmod-ed to `0600`.
- `thenv list --workspace <id> --project <id> --env <id> [--limit <n>] [--cursor <token>]`
: Returns version metadata only.
- `thenv rotate --workspace <id> --project <id> --env <id> [--from-version <id>]`
: Creates a new version from the source version and moves active pointer.
: If `--from-version` is omitted, the current active version is used.

## Storage
Server-owned logical entities (SQLite):
- `bundle_versions`
: `bundle_version_id`, scope IDs, `status`, `created_by`, `created_at`, `source_version_id`, `metadata`.
- `bundle_files`
: `bundle_version_id`, `file_type`, ciphertext payload, wrapped DEK, nonces, checksum, byte length.
- `active_bundle_pointers`
: scope IDs, active `bundle_version_id`, `updated_by`, `updated_at`.
- `policy_bindings`
: scope IDs, `subject`, `role`, `policy_revision`, `updated_at`.
- `audit_events`
: `event_id`, `event_type`, actor, scope, target version, result/failure, request IDs, metadata, timestamp.

Local and frontend storage:
- CLI does not persist decrypted secrets outside explicit pull destination files.
- Web console stores view state only and does not persist secret payloads.

## Security
- Transport security:
: RPC traffic is designed for TLS deployment; local development may use HTTP.
- At-rest security:
: Server-side envelope encryption is implemented.
: Each file payload uses a random DEK.
: DEK is wrapped by workspace master key.
: Workspace keys are derived from `THENV_WORKSPACE_MASTER_KEYS` / `THENV_DEFAULT_MASTER_KEY`.
- Authentication:
: JWT (HS256) via `THENV_JWT_SECRET` is implemented for Phase 1.
- Authorization:
: RBAC checks are enforced for every RPC operation at `workspace/project/environment` scope.
: Deny by default on missing role bindings.
: `THENV_SUPER_ADMINS` supports bootstrap admin subjects.
- Secret exposure rules:
: Never show full secret values in default CLI or web output.
: Policy/list/audit APIs never return plaintext secret payloads.
: Web console remains metadata-only for secret data.
- File output safety:
: Pull writes destination files with restrictive permissions (`0600`).
: Existing-file conflicts require explicit `--force` override.

## Logging
Required baseline logs:
- `operation`
- `actor`
- `workspace_id`, `project_id`, `environment_id`
- `result` / `failure_code`
- `bundle_version_id` / `target_bundle_version_id` where relevant
- `file_types` (type names only, no contents)
- `request_id` and `trace_id` where provided

Prohibited log content:
- Plaintext secret values
- Full `.env` or `.dev.vars` payloads
- Decrypted key material
- Raw authentication tokens

## Build and Test
Current local commands:
- Proto: `buf lint` and `buf generate`
- CLI: `go test ./cmds/thenv/...`
- Server: `go test ./servers/thenv/...`
- Web console: `pnpm --dir apps/devkit test`

CI contract:
- Workflow: `.github/workflows/thenv-ci.yml`
- Steps: install proto tooling, run `buf lint` + `buf generate`, run Go tests, run Devkit tests.

Acceptance scenarios:
- Push `.env` only, `.dev.vars` only, and both in one version.
- Pull creates missing outputs, fails on conflict by default, succeeds with `--force`.
- `reader` pull/list only; `writer` push/rotate; `admin` activate/policy/audit.
- `push` does not move active pointer.
- `rotate` creates a new version and updates active pointer.
- Database stores ciphertext only; no secret leakage in logs or audit streams.

## Roadmap
- Phase 1: implemented baseline Connect RPC, versioned bundles, RBAC, CLI flows, and metadata web console.
- Phase 2: policy UX expansion, richer audit filters/export, and operational hardening.
- Phase 3: KMS integration, key rotation automation, and retention policy controls.
- Phase 4: enterprise governance features (compliance controls, delegated administration, policy automation).

## Open Questions
- OIDC issuer/audience integration timeline after local JWT bootstrap.
- KMS backend selection for production key lifecycle management.
- Payload size and rate-limit defaults for production deployment.
- Fine-grained audit read permissions for non-admin roles.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
- `protos/thenv/v1/thenv.proto`
