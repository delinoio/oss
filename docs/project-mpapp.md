# Project: mpapp

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

## Architecture
- App shell initializes runtime, checks platform capability, and hosts top-level state.
- Session state machine handles lifecycle transitions (`Idle -> PermissionCheck -> Pairing -> Connecting -> Connected -> Error`).
- Input surface module exposes:
  - A touchpad region for drag capture
  - Dedicated left-click and right-click controls
- Input translation module converts gesture deltas into pointer movement samples with sensitivity applied.
- Touchpad gesture responder instances must be recreated when movement callback dependencies change so runtime sensitivity updates take effect without reconnecting.
- Android HID transport adapter is implemented as a TypeScript `HidAdapter` contract with a stub transport implementation for MVP integration stability.
- Diagnostics module records structured events, failures, and latency observations in local storage.

## Interfaces
Canonical platform scope:

```ts
enum MpappPlatformScope {
  AndroidMvp = "android-mvp",
  IosResearch = "ios-research",
}
```

Canonical app mode identifiers:

```ts
enum MpappMode {
  Idle = "idle",
  PermissionCheck = "permission-check",
  Pairing = "pairing",
  Connecting = "connecting",
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
}
```

Canonical click button identifiers:

```ts
enum MpappClickButton {
  Left = "left",
  Right = "right",
}
```

Canonical connection events:

```ts
enum MpappConnectionEvent {
  StartPairing = "start-pairing",
  ConnectSuccess = "connect-success",
  ConnectFailure = "connect-failure",
  Disconnect = "disconnect",
  PermissionDenied = "permission-denied",
}
```

Canonical error codes:

```ts
enum MpappErrorCode {
  BluetoothUnavailable = "bluetooth-unavailable",
  PermissionDenied = "permission-denied",
  PairingTimeout = "pairing-timeout",
  TransportFailure = "transport-failure",
  UnsupportedPlatform = "unsupported-platform",
}
```

Canonical pointer movement payload:

```ts
type PointerMoveSample = {
  actionId: MpappInputAction.Move;
  deltaX: number;
  deltaY: number;
  timestampMs: number;
  sensitivity: number;
}
```

Canonical click payload:

```ts
type PointerClickSample =
  | {
      actionId: MpappInputAction.LeftClick;
      button: MpappClickButton.Left;
      timestampMs: number;
    }
  | {
      actionId: MpappInputAction.RightClick;
      button: MpappClickButton.Right;
      timestampMs: number;
    };
```

MVP interface constraints:
- Every emitted input sample must include `timestampMs`.
- `deltaX` and `deltaY` are gesture-derived relative movement values, not absolute coordinates.
- `MpappInputAction` values are stable contracts and must not be renamed without a documented migration.
- Click payloads must preserve valid `actionId` and `button` pairs by the `PointerClickSample` discriminated union.

Android and iOS scope contract:
- Android MVP supports direct Bluetooth mouse flow.
- iOS is explicitly documented as `IosResearch` and excluded from direct HID delivery in MVP.

Permissions and capability contract (Android MVP):
- Check Bluetooth availability before entering pairing flow.
- Gate pairing/connection on runtime permission results.
- Surface `MpappErrorCode.PermissionDenied` when permission requirements are not satisfied.
- Require Android API level `31+` for MVP runtime support.

Reference feasibility links:
- [Expo SDK modules](https://docs.expo.dev/versions/latest/)
- [Expo custom native code workflow](https://docs.expo.dev/workflow/customizing/)
- [Android BluetoothHidDevice API](https://developer.android.com/reference/android/bluetooth/BluetoothHidDevice)
- [Apple Bluetooth overview (Core Bluetooth / MFi context)](https://developer-mdn.apple.com/bluetooth/)
- [Apple Core Bluetooth Concepts (BLE central/peripheral model)](https://developer.apple.com/library/archive/documentation/NetworkingInternetWeb/Conceptual/CoreBluetooth_concepts/AboutCoreBluetooth/Introduction.html)

## Storage
- Local preferences only:
  - Pointer sensitivity
  - Optional axis inversion flags
- Local diagnostics ring buffer with bounded retention (`300`) for troubleshooting.
- Diagnostics storage key: `mpapp.diagnostics.v1`.
- If AsyncStorage is unavailable, diagnostics fall back to an in-memory store that still preserves recent entries during process lifetime.
- No account-linked persistence in MVP.
- No cloud upload of raw input traces in MVP.

## Security
- Request minimum required Bluetooth permissions at runtime for Android flow.
- Restrict diagnostics to local device by default.
- Do not collect or transmit unrelated device data.
- Treat connection lifecycle and input events as sensitive operational data.
- Enforce explicit unsupported-platform handling:
  - iOS direct HID path is out of MVP and must return `MpappErrorCode.UnsupportedPlatform`.

## Logging
Required baseline logs:
- Permission check outcomes
- Bluetooth lifecycle transitions
- Input translation pipeline errors
- Disconnection reasons

Required structured fields for each log event:
- `sessionId`
- `connectionState`
- `actionType`
- `latencyMs`
- `failureReason`
- `platform`
- `osVersion`

Connection state logging contract:
- `connectionState` must be captured from the latest session state snapshot at log emission time, including async lifecycle and transport callbacks.

Recommended event families:
- `permission.check`
- `connection.transition`
- `input.move`
- `input.click`
- `transport.error`

## Build and Test
Document validation checklist for this spec:
- Confirm scope only includes `Move`, `LeftClick`, and `RightClick` for MVP.
- Confirm all interface identifiers are enum-based and stable.
- Confirm failure handling includes permission-denied and transport-failure paths.
- Confirm Android-first and iOS-research-only boundaries are explicit.

Implementation commands:
- Install dependencies from repository root: `pnpm install`
- App tests from `apps/mpapp`: `pnpm test`
- Workspace-filtered app tests from repository root: `pnpm --filter mpapp test`

MVP acceptance criteria scenarios:
1. Drag start/move/end emits pointer delta samples without dropping movement segments under normal interaction.
2. Tapping the left-click button emits exactly one `left-click` action.
3. Tapping the right-click button emits exactly one `right-click` action.
4. Attempting input before a connected state shows a clear disabled or error state.
5. Permission denial shows a retry path and logs required structured fields.
6. Disconnect events follow the documented state transition order and provide reconnect guidance.
7. High input frequency follows documented sampling or throttle limits and remains observable in logs.

## Roadmap
- Phase 1: Android MVP with drag-based movement, left-click, right-click, lifecycle state UI, and diagnostics baseline.
- Phase 2: Reliability hardening, latency tuning, and sampling or throttle optimization for Android devices.
- Phase 3: Re-evaluate iOS feasibility and decide whether to add a documented alternate strategy.

## Open Questions
- Native Android HID bridge rollout timeline after stub validation.
- iOS strategy after research: keep unsupported, or introduce an alternate bridge-based approach.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- [Expo SDK modules](https://docs.expo.dev/versions/latest/)
- [Expo custom native code workflow](https://docs.expo.dev/workflow/customizing/)
- [Android BluetoothHidDevice API](https://developer.android.com/reference/android/bluetooth/BluetoothHidDevice)
- [Apple Bluetooth overview (Core Bluetooth / MFi context)](https://developer-mdn.apple.com/bluetooth/)
- [Apple Core Bluetooth Concepts (BLE central/peripheral model)](https://developer.apple.com/library/archive/documentation/NetworkingInternetWeb/Conceptual/CoreBluetooth_concepts/AboutCoreBluetooth/Introduction.html)
