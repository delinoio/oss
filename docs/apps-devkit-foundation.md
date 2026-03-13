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
- Shared shell modules must remain separate from mini app business logic.

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
- Integrates with backend APIs via stable contract boundaries.

## Change Triggers
- Update `docs/project-devkit.md` and this file when host routing or mini app registration contracts change.
- Keep related mini app project indexes synchronized when IDs/routes/integration behavior changes.

## References
- `docs/project-devkit.md`
- `docs/project-devkit-commit-tracker.md`
- `docs/project-devkit-remote-file-picker.md`
- `docs/project-thenv.md`
- `docs/domain-template.md`
