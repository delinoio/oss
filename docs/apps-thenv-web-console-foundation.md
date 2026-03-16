# apps-thenv-web-console-foundation

## Scope
- Project/component: thenv web console contract
- Canonical path: `apps/devkit/src/apps/thenv`

## Runtime and Language
- Runtime: Next.js mini app module
- Primary language: TypeScript

## Users and Operators
- Developers validating canonical Devkit mini app routing
- Maintainers preserving scaffold contracts before web-console reactivation

## Interfaces and Contracts
- Stable component identifier: `web-console`.
- Route contract: `/apps/thenv`.
- Registration status contract: `active`.
- UI contract renders ThenvApp with scope selector, bundle management (list/detail/activate/rotate), policy editor, and audit viewer.
- Devkit API proxy: `/api/thenv/*` routes to `http://127.0.0.1:8087/*` via Next.js rewrites.

## Storage
- Uses no component-specific persistence while scaffold-only.

## Security
- Placeholder route must not process or render secret values.
- Error and diagnostic output must remain free of sensitive payloads.

## Logging
- Route-level diagnostics should include mini app id and route context.
- Placeholder logs must avoid backend or secret-specific fields.

## Build and Test
- Local validation: `pnpm --filter devkit... test`
- Build validation: `pnpm --filter devkit... build`

## Dependencies and Integrations
- Integrates with `servers/thenv` via Connect RPC (BundleService, PolicyService, AuditService).
- Uses `@connectrpc/connect-query` hooks with React Query for server-state management.

## Change Triggers
- Update `docs/project-thenv.md` and this file when web-console behavior changes.
- If `/api/thenv/*` routes are reintroduced, synchronize this contract with server and CLI docs in the same change.

## References
- `docs/project-thenv.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/cmds-thenv-cli-foundation.md`
- `docs/apps-devkit-foundation.md`
- `docs/domain-template.md`
