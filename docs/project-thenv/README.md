# Project: thenv

## Documentation Layout
- Canonical entrypoint for this project: docs/project-thenv/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`thenv` provides secure sharing of `.env` and `.dev.vars` files across teams with explicit trust boundaries.
It is a multi-component system composed of a Go CLI, backend server, and Devkit web console.
Phase 1 MVP is implemented as a metadata-safe vertical slice at `workspace/project/environment` scope.


## Path
- CLI: `cmds/thenv`
- Server: `servers/thenv`
- Connect proto contract: `servers/thenv/proto/thenv/v1/thenv.proto`
- Server-local proto generation script: `servers/thenv/scripts/generate-go-proto.sh`
- Generated Go RPC code (gitignored; regenerate via `go generate ./servers/thenv` or `./scripts/generate-go-proto.sh`): `servers/thenv/gen/proto/thenv/v1`
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


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)
- [feature-runtime-defaults.md](./feature-runtime-defaults.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
