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
- Registration status contract: `placeholder`.
- Current UI contract renders the shared Devkit placeholder view only.
- Devkit local API surface under `/api/thenv/*` is not active during scaffold phase.

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
- Planned integration targets remain `servers/thenv` and `cmds/thenv`.
- Active Devkit behavior is shell-only and does not call thenv backend APIs.

## Change Triggers
- Update `docs/project-thenv.md` and this file when web-console behavior changes.
- If `/api/thenv/*` routes are reintroduced, synchronize this contract with server and CLI docs in the same change.

## References
- `docs/project-thenv.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/cmds-thenv-cli-foundation.md`
- `docs/apps-devkit-foundation.md`
- `docs/domain-template.md`
