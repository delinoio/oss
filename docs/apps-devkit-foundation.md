# apps-devkit-foundation

## Scope
- Project/component: Devkit host app foundation contract
- Canonical path: `apps/devkit`

## Runtime and Language
- Runtime: Next.js 16 web app
- Primary language: TypeScript

## Users and Operators
- Developers using mini apps inside the Devkit host shell
- Maintainers operating app routing and platform modules

## Interfaces and Contracts
- Stable mini app IDs:
  - `commit-tracker`
  - `remote-file-picker`
  - `thenv`
- Route pattern contract: `/apps/<id>`.
- Registration status contract:
  - `commit-tracker`: `active`
  - `remote-file-picker`: `active`
  - `thenv`: `active`
- Current shell contract uses a simple header and inline mini app navigation links.
- Shared shell modules must remain separate from mini app business logic.
- Connect RPC transport is proxied via Next.js rewrites (`/api/{service}` -> backend).
- React Query + connect-query provide server-state management for all mini apps.

## Storage
- Defines browser/session storage usage for host shell preferences.
- Mini app-specific persistence should remain encapsulated by component contracts.

## Security
- Route and app registration metadata must avoid unsafe dynamic injection.
- Host shell should enforce safe boundaries between mini app containers.

## Logging
- Frontend diagnostics should expose route/app registration context for troubleshooting.
- Debug logs must avoid exposing secrets from integrated backends.

## Build and Test
- Local validation: `pnpm --filter devkit... test`
- Build validation: `pnpm --filter devkit... build`
- CI alignment: `node-devkit-test` and `node-devkit-build`

## Dependencies and Integrations
- Integrates with mini app contracts documented in project-specific docs.
- `@bufbuild/protobuf`, `@connectrpc/connect`, `@connectrpc/connect-query`, `@connectrpc/connect-web` for Connect RPC transport.
- `@tanstack/react-query` for server-state caching.
- Thenv backend: `servers/thenv` (port 8087).
- Commit Tracker backend: `servers/commit-tracker` (port 8088).
- Remote File Picker backend: `servers/remote-file-picker` (port 8089).

## Change Triggers
- Update `docs/project-devkit.md` and this file when host routing or mini app registration contracts change.
- Keep related mini app project indexes synchronized when IDs/routes/status/integration behavior changes.

## References
- `docs/project-devkit.md`
- `docs/project-devkit-commit-tracker.md`
- `docs/project-devkit-remote-file-picker.md`
- `docs/project-thenv.md`
- `docs/domain-template.md`
