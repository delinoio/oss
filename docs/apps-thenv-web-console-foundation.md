# apps-thenv-web-console-foundation

## Scope
- Project/component: thenv web console contract
- Canonical path: `apps/devkit/src/apps/thenv`

## Runtime and Language
- Runtime: Next.js mini app module
- Primary language: TypeScript

## Users and Operators
- Developers operating environment secret workflows through UI
- Maintainers ensuring secure, auditable secret operations UX

## Interfaces and Contracts
- Stable component identifier: `web-console`.
- Route contract: `/apps/thenv`.
- UI actions must align with thenv CLI and server trust model semantics.

## Storage
- Uses transient form/session state and query caches.
- Does not persist plaintext secrets in browser storage.

## Security
- Secret values must be redacted in UI logs and error messages.
- UI flows must enforce explicit trust and authorization checks before operations.

## Logging
- Include structured diagnostics for operation type, target environment, and sanitized status.
- Avoid sensitive payload logging in frontend telemetry.

## Build and Test
- Local validation: `pnpm --filter devkit... test`
- Build validation: `pnpm --filter devkit... build`

## Dependencies and Integrations
- Integrates with `servers/thenv` API contracts.
- Must remain behaviorally aligned with `cmds/thenv` CLI workflows.

## Change Triggers
- Update `docs/project-thenv.md` and this file for web console behavior or route changes.
- Synchronize trust model changes with CLI and server thenv contracts.

## References
- `docs/project-thenv.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/cmds-thenv-cli-foundation.md`
- `docs/apps-devkit-foundation.md`
- `docs/domain-template.md`
