# apps-mpapp-foundation

## Scope
- Project/component: `mpapp` mobile app foundation contract
- Canonical path: `apps/mpapp`

## Runtime and Language
- Runtime: Expo React Native
- Primary language: TypeScript

## Users and Operators
- End users running mobile workflows
- App engineers and release operators maintaining mobile behavior

## Interfaces and Contracts
- Route and screen identifiers must remain stable for shared navigation logic.
- Platform capability contracts (including Bluetooth permissions) must be explicitly documented.
- App-level server-state integration should prefer React Query patterns when available.

## Storage
- Defines local storage contracts for user preferences and app session state.
- Device-side cache behavior and retention must remain explicit and bounded.

## Security
- Permission prompts and capability usage must follow least-privilege patterns.
- Sensitive local data must avoid plaintext persistence where avoidable.

## Logging
- Operational logs should be structured and actionable in development and QA environments.
- Debug logging must avoid secret/token exposure.

## Build and Test
- Local validation: `pnpm --filter mpapp test`
- Lint validation: `pnpm --filter mpapp lint`
- CI alignment: `node-mpapp-test` and `node-mpapp-lint` jobs

## Dependencies and Integrations
- Integrates with mobile platform APIs and backend services through documented contracts.
- Aligns UX/UI decisions with Toss Design Guidelines.

## Change Triggers
- Update `docs/project-mpapp.md` and this file when app architecture, permissions, or route contracts change.
- Keep `apps/AGENTS.md` synchronized for policy changes impacting app development.

## References
- `docs/project-mpapp.md`
- `docs/domain-template.md`
