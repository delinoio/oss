# Project: mpapp

## Goal
`mpapp` is an Expo-based React Native app that allows a mobile device to act as a Bluetooth mouse.
It focuses on reliable pointer and click control from mobile hardware.

## Path
- `apps/mpapp`

## Runtime and Language
- Expo React Native (TypeScript)

## Users
- End users who want to use a phone as a Bluetooth mouse
- QA engineers validating cross-platform mobile input behavior

## In Scope
- Bluetooth mouse mode lifecycle (pair, connect, disconnect)
- Pointer movement and click actions from mobile gestures
- Basic in-app connection state and diagnostics
- Permission and capability checks per platform

## Out of Scope
- Keyboard emulation in initial versions
- Desktop companion application features beyond Bluetooth mouse role
- Account system and cloud synchronization

## Architecture
- App shell initializes Expo runtime and platform capability checks.
- Bluetooth controller module manages pairing and connection lifecycle.
- Input translation module maps gestures to pointer/click events.
- Status UI module displays connection and diagnostics state.

## Interfaces
Canonical app mode identifiers:

```ts
enum MpappMode {
  Idle = "idle",
  Pairing = "pairing",
  Connected = "connected",
  Error = "error",
}
```

Canonical input action identifiers:

```ts
enum MpappInputAction {
  Move = "move",
  LeftClick = "left-click",
  RightClick = "right-click",
  Scroll = "scroll",
}
```

## Storage
- Local app settings for user preferences.
- Optional session diagnostics for troubleshooting.
- No mandatory account-linked persistent storage in initial scope.

## Security
- Request only minimum Bluetooth permissions.
- Keep connection/session metadata local by default.
- Avoid transmitting unrelated device data.

## Logging
Required baseline logs:
- Permission check outcomes
- Bluetooth lifecycle transitions
- Input translation pipeline errors
- Disconnection reasons

## Build and Test
Planned commands:
- Install dependencies: `pnpm install`
- App tests: `pnpm test --filter mpapp...`
- Expo runtime checks will be defined when project scaffolding is added.

## Roadmap
- Phase 1: Pair/connect/disconnect and basic pointer actions.
- Phase 2: Gesture tuning, latency improvements, diagnostics UI.
- Phase 3: Reliability hardening across supported mobile platforms.
- Phase 4: Advanced input profiles and accessibility enhancements.

## Open Questions
- Final supported OS/device matrix for first release.
- Gesture mapping defaults for one-handed vs two-handed use.
- Background behavior constraints by platform policy.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
