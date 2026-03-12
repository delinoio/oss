# Feature: operations

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


## Build and Test
Current commands:
- Proto generation (server-local): `go generate ./servers/thenv`
- Proto generation (workspace): `./scripts/generate-go-proto.sh`
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
14. Web console audit table renders per-event outcome and honors optional `fromTime`/`toTime` filters via Devkit audit proxy route.
15. Applying/clearing audit time-range filters refreshes only audit data and does not discard unsaved policy draft bindings in the web console.
16. Web console version and audit tables support cursor pagination and continue loading additional pages until `nextCursor` is empty.
17. Failed scope refreshes or failed audit filter reloads clear stale cursors so load-more actions cannot append mixed-scope or mixed-filter rows.
18. Slow in-flight load-more responses are ignored when a newer scope refresh or audit filter reload has already replaced table state.

