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
- Stable screen identifier contract:
  - `main-console`
- Stable route/deep-link contract:
  - `main-console` -> route path `/main`, deep-link path `main`
- Unknown screen identifiers must safely fall back to `main-console`.
- Platform capability contracts (including Bluetooth permissions) must be explicitly documented.
- Android permission contract for Bluetooth HID workflow:
  - `android.permission.BLUETOOTH_CONNECT`
  - `android.permission.BLUETOOTH_SCAN`
- App-level server-state integration should prefer React Query patterns when available.

## Storage
- Input preference storage key: `mpapp.input-preferences.v1`.
- Session snapshot storage key: `mpapp.session-snapshot.v1`.
- Session snapshot contract stores only:
  - `lastConnectionEvent`
  - `lastDisconnectReason`
  - `errorCode`
  - `errorMessage`
  - `updatedAt`
- Session snapshot restore must always start in `Idle` mode and only hydrate metadata (no auto-reconnect).
- Diagnostics storage key: `mpapp.diagnostics.v1` with ring-buffer retention limit `300`.
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
- Native Android HID integration uses `apps/mpapp/modules/mpapp-android-hid`.

## Change Triggers
- Update `docs/project-mpapp.md` and this file when app architecture, permissions, or route contracts change.
- Keep `apps/AGENTS.md` synchronized for policy changes impacting app development.

## References
- `docs/project-mpapp.md`
- `docs/domain-template.md`
