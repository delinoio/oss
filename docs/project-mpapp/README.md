# Project: mpapp

## Documentation Layout
- Canonical entrypoint for this project: docs/project-mpapp/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`mpapp` is an Expo-based React Native app that turns a phone into a Bluetooth mouse pointer.
The core user flow is:
- Drag on an on-screen touchpad area to move the remote cursor.
- Press fixed on-screen buttons for left click and right click.
- Pair, connect, and disconnect with clear in-app session state.


## Path
- `apps/mpapp`


## Runtime and Language
- Expo React Native (TypeScript)
- Custom native Android integration through Expo development builds for Bluetooth HID support
- Expo development client + EAS build profile configuration is included in the repository.


## Users
- End users who want to control a cursor from a phone
- QA engineers validating gesture-to-pointer behavior and Bluetooth lifecycle reliability


## In Scope
- Android-first Bluetooth mouse lifecycle: capability check, permission check, pair, connect, disconnect, recover
- Pointer movement from touchpad drag gestures only
- Two explicit click controls: left-click button and right-click button
- In-app connection state feedback and deterministic error messaging
- Local diagnostics and structured logs for connection and input flow troubleshooting


## Out of Scope
- iOS direct Bluetooth HID mouse behavior in MVP (tracked as research-only)
- Scroll, middle-click, keyboard emulation, and advanced gesture profiles in MVP
- User accounts, cloud sync, and server-side telemetry pipelines
- Desktop companion functionality unless required by a future iOS strategy decision


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
