# Feature: operations

## Storage
- Session-level web state in browser storage as needed.
- Server-backed state depends on each mini app and is documented per mini-app file.
- Shared platform config kept in repository configuration files.


## Security
- Enforce route-level access control through shared platform guards.
- Keep mini-app boundaries explicit to avoid accidental cross-app data access.
- Do not hardcode secrets in mini-app frontend code.


## Logging
Required baseline logs:
- Mini app route resolution and load failures
- Shared shell errors
- Navigation and route render events with stable route and mini-app identifiers
- API request failures with request correlation identifiers


## Build and Test
Current commands:
- Dev: `pnpm --filter devkit... dev` (fixed port `5990`)
- Build: `pnpm --filter devkit... build`
- Test: `pnpm --filter devkit... test`
- Test runner: Vitest (`apps/devkit/vitest.config.ts`)

