# apps-thenv-web-console-foundation

## Scope
- Project/component: thenv web console contract
- Canonical path: `apps/devkit/src/apps/thenv`

## Runtime and Language
- Runtime: Next.js mini app module
- Primary language: TypeScript

## Users and Operators
- Developers validating canonical Devkit mini app routing
- Operators managing scope-level bundle, policy, and audit workflows through the web console

## Interfaces and Contracts
- Stable component identifier: `web-console`.
- Route contract: `/apps/thenv`.
- Registration status contract: `active`.
- UI contract renders ThenvApp with scope selector, bundle management (list/detail/activate/rotate), policy editor, and audit viewer.
- Bundle detail fetches by selected version id and must remain aligned with bundle-list selection state.
- Secret file content is masked by default and only revealed through explicit user action.
- Devkit API proxy: `/api/thenv/*` routes to `http://127.0.0.1:8087/*` via Next.js rewrites.

## Storage
- Uses no component-specific persistence and relies on Connect RPC server-state reads/writes.

## Security
- Secret values must not be rendered by default; reveal/hide actions must be explicit and user-initiated.
- Error and diagnostic output must remain free of sensitive payloads.

## Logging
- Route-level diagnostics should include mini app id and route context.
- Diagnostic logs must avoid backend secret payloads.

## Build and Test
- Local validation: `pnpm --filter devkit... test`
- Build validation: `pnpm --filter devkit... build`

## Dependencies and Integrations
- Integrates with `servers/thenv` via Connect RPC (BundleService, PolicyService, AuditService).
- Uses `@connectrpc/connect-query` hooks with React Query for server-state management.

## Change Triggers
- Update `docs/project-thenv.md` and this file when web-console behavior changes.
- Synchronize this contract with server and CLI docs when bundle, policy, audit, or security behavior changes.

## References
- `docs/project-thenv.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/cmds-thenv-cli-foundation.md`
- `docs/apps-devkit-foundation.md`
- `docs/domain-template.md`
