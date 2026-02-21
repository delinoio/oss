# Project: thenv

## Goal
`thenv` provides secure sharing of `.env` and `.dev.vars` files across teams with explicit trust boundaries.
It is a multi-component system composed of a Go CLI, backend server, and Devkit web console.
The Phase 1 target is a decision-complete contract for versioned bundle distribution at `workspace/project/environment` scope.

## Path
- CLI: `cmds/thenv`
- Server: `servers/thenv`
- Web console mini app: `apps/devkit/src/apps/thenv`
- Web console route placeholder: `apps/devkit/src/app/apps/thenv/page.tsx`

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

## Architecture
- CLI (`cmds/thenv`) handles local workflows:
: Local file parse (`.env`, `.dev.vars`), push orchestration, pull file materialization, and conflict enforcement.
- Server (`servers/thenv`) handles business flows over Connect RPC:
: Bundle version storage, active pointer state, policy enforcement, decryption for authorized pull, and audit event persistence.
- Web console (`apps/devkit/src/apps/thenv`) handles management and visibility:
: Version inventory, active version switching, role policy management, and audit browsing without secret value rendering.
- Current Devkit shell bootstrap exposes `/apps/thenv` as a placeholder route while business features are deferred.

Trust boundary and plaintext handling:
- Plaintext is allowed in CLI process memory when reading local source files and writing pulled output files.
- Plaintext is allowed in server process memory only during authorized encrypt/decrypt paths.
- Plaintext is not allowed in persistent server storage, logs, metrics labels, frontend state, or browser storage.

Communication boundary:
- Business flows must use Connect RPC between clients (CLI/web backend adapters) and `servers/thenv`.
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

Component mapping contract:
- `Cli` -> `cmds/thenv`
- `Server` -> `servers/thenv`
- `WebConsole` -> `apps/devkit/src/apps/thenv`

Devkit route contract for web console:
- `/apps/thenv`
- Current route state: placeholder page rendered by Devkit shell bootstrap.

High-level operation identifiers:

```ts
enum ThenvOperation {
  Push = "push",
  Pull = "pull",
  List = "list",
  Rotate = "rotate",
}
```

Canonical file type identifiers:

```ts
enum ThenvFileType {
  Env = "env",
  DevVars = "dev-vars",
}
```

Canonical role identifiers:

```ts
enum ThenvRole {
  Reader = "reader",
  Writer = "writer",
  Admin = "admin",
}
```

Canonical bundle lifecycle identifiers:

```ts
enum ThenvBundleStatus {
  Active = "active",
  Archived = "archived",
}
```

Canonical pull conflict policy identifiers:

```ts
enum ThenvConflictPolicy {
  FailClosed = "fail-closed",
  ForceOverwrite = "force-overwrite",
}
```

Canonical audit event identifiers:

```ts
enum ThenvAuditEventType {
  Push = "push",
  Pull = "pull",
  List = "list",
  Rotate = "rotate",
  Activate = "activate",
  PolicyUpdate = "policy-update",
}
```

Namespace contract:
- Every bundle operation must include `workspaceId`, `projectId`, and `environmentId`.
- CLI scope flags are mandatory for all core commands:
: `--workspace <id> --project <id> --env <id>`

Connect RPC service contract:
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

Connect RPC operation contract (high-level):
- `PushBundleVersion`
: Input: scope IDs, one or more file payloads keyed by `ThenvFileType`, optional metadata.
: Behavior: creates a new immutable version and updates audit log.
: Output: `bundleVersionId`, `createdAt`, `status`.
- `PullActiveBundle`
: Input: scope IDs and optional explicit `bundleVersionId` override.
: Behavior: resolves active version when override is absent; returns files authorized by RBAC.
: Output: version metadata and plaintext file contents for authorized CLI pull clients only.
- `ListBundleVersions`
: Input: scope IDs, pagination options.
: Output: version summaries without secret contents.
- `ActivateBundleVersion`
: Input: scope IDs and target `bundleVersionId`.
: Behavior: atomically moves active pointer to target version.
: Output: previous/next active version metadata.
- `RotateBundleVersion`
: Input: scope IDs and optional source version metadata.
: Behavior: creates a new version and then activates it (new version + pointer move).
: Output: new `bundleVersionId` and activation metadata.
- `GetPolicy`
: Input: scope IDs.
: Output: role bindings for subjects in target scope.
- `SetPolicy`
: Input: scope IDs and full replacement or patch policy payload.
: Output: resulting policy revision metadata.
- `ListAuditEvents`
: Input: scope IDs, filters (`eventType`, time range, actor), pagination.
: Output: audit event stream without secret values.

Role authorization contract:
- `Reader`
: Allowed: `PullActiveBundle`, `ListBundleVersions`.
: Denied: `PushBundleVersion`, `RotateBundleVersion`, `ActivateBundleVersion`, `SetPolicy`.
- `Writer`
: Allowed: all `Reader` operations plus `PushBundleVersion`, `RotateBundleVersion`.
: Denied: `SetPolicy`, `ActivateBundleVersion` unless explicitly promoted.
- `Admin`
: Allowed: all `Writer` operations plus `ActivateBundleVersion`, `GetPolicy`, `SetPolicy`, `ListAuditEvents`.

CLI command contract:
- `thenv push --workspace <id> --project <id> --env <id> [--env-file <path>] [--dev-vars-file <path>]`
: Requires at least one input file.
: Creates a new version in target scope.
- `thenv pull --workspace <id> --project <id> --env <id> [--output-env-file <path>] [--output-dev-vars-file <path>] [--force]`
: Default conflict policy is `fail-closed`.
: If target output exists and content differs, operation fails unless `--force` is supplied.
: Output files must be written with restrictive default permissions (`0600`).
- `thenv list --workspace <id> --project <id> --env <id> [--limit <n>] [--cursor <token>]`
: Returns version metadata only.
- `thenv rotate --workspace <id> --project <id> --env <id> [--from-version <id>]`
: Creates new version and moves active pointer to that version.

Web console contract:
- Must never reveal or download plaintext secret values in Phase 1.
- Must support version list, active version switch, policy management, and audit browsing.
- Must display masked/metadata-only representations for sensitive fields.

## Storage
Server-owned logical entities:
- `BundleVersion`
: Fields: `bundleVersionId`, scope IDs, `status`, `createdBy`, `createdAt`, `sourceVersionId` (optional).
- `BundleFilePayload`
: Fields: `bundleVersionId`, `fileType`, ciphertext payload, encrypted data key, checksum, byte length.
- `ActiveBundlePointer`
: Fields: scope IDs, active `bundleVersionId`, `updatedBy`, `updatedAt`.
- `PolicyBinding`
: Fields: scope IDs, subject identifier, `role`, `policyRevision`.
- `AuditEvent`
: Fields: `eventId`, `eventType`, actor metadata, scope IDs, target version metadata, outcome, timestamp, request correlation IDs.

Retention defaults:
- Bundle versions: unlimited by default.
- Audit events: unlimited by default.
- Future retention pruning policies may be added as explicit administrative configuration.

Local and frontend storage:
- CLI may cache non-secret metadata (for example, last successful version reference).
- CLI must not persist decrypted secrets outside destination files explicitly written by pull.
- Web console stores view state only and must not persist secret payloads.

## Security
- Transport security:
: All RPC traffic must use TLS.
- At-rest security:
: Use server-side envelope encryption.
: Each file payload uses a data encryption key (DEK).
: DEKs are encrypted by a workspace-level key encryption key managed by KMS/HSM-compatible backend.
- Authentication:
: Use OIDC/JWT identity tokens.
: Reject expired, invalid-signature, or wrong-audience tokens.
- Authorization:
: Apply RBAC checks for every RPC operation at `workspace/project/environment` scope.
: Deny by default on missing bindings.
- Secret exposure rules:
: Never show full secret values in default CLI or web output.
: Never return secret payloads from policy/audit/list operations.
: Web console Phase 1 is metadata-only for secret data.
- File output safety:
: Pull writes files with restrictive default permissions (`0600`).
: Existing file conflicts require explicit `--force` override.
- Audit requirements:
: All sensitive operations must emit immutable audit events.

## Logging
Required baseline logs:
- `operation`: one of `ThenvOperation`
- `event_type`: one of `ThenvAuditEventType` where applicable
- `actor`: subject/user/service identity metadata (no raw token logging)
- `scope`: `workspaceId`, `projectId`, `environmentId`
- `role_decision`: role evaluated and allow/deny result
- `bundle_version_id` and `target_bundle_version_id` when applicable
- `file_types`: set of `ThenvFileType` only, never file contents
- `conflict_policy`: one of `ThenvConflictPolicy` for pull operations
- `result`: success/failure and classified failure code
- `request_id` and `trace_id` for incident reconstruction

Prohibited log content:
- Plaintext secret values
- Full `.env` or `.dev.vars` payloads
- Decrypted key material
- Raw authentication tokens
- Stack traces containing secret payload fragments

## Build and Test
Current commands:
- CLI build/test: `go build ./cmds/thenv/...` and `go test ./cmds/thenv/...`
- Server build/test: `go build ./servers/thenv/...` and `go test ./servers/thenv/...`
- Web console tests: `pnpm --filter devkit... test`

Documentation acceptance scenarios:
- Push scenarios:
: Push `.env` only.
: Push `.dev.vars` only.
: Push both file types in one version.
- Pull scenarios:
: Pull creates missing output files.
: Pull fails on content conflict by default.
: Pull succeeds on conflict with explicit `--force`.
- Authorization scenarios:
: `reader` can pull/list only.
: `writer` can push/rotate plus reader operations.
: `admin` can policy and activation operations.
- Versioning scenarios:
: `rotate` creates a new version and updates active pointer.
: Previous versions remain addressable for explicit pull.
- Audit and logging scenarios:
: Sensitive operations emit actor/scope/result/event IDs.
: Secret/plaintext values never appear in audit or logs.

## Roadmap
- Phase 1: Connect RPC foundation, versioned multi-file bundles, RBAC, and secure push/pull/list/rotate flows.
- Phase 2: Policy UX expansion, richer audit filtering/export, and operational hardening.
- Phase 3: Key rotation automation, retention policy controls, and ecosystem integrations.
- Phase 4: Enterprise governance features (compliance controls, delegated administration, policy automation).

## Open Questions
- OIDC provider and tenant-mapping strategy for workspace identity.
- KMS backend selection and key lifecycle SLOs for production deployments.
- Maximum payload size limits and rate-limiting defaults for push/pull APIs.
- Fine-grained audit read permissions for non-admin roles.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
