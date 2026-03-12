# Feature: operations

## Storage
- Local preferences only:
  - Pointer sensitivity
  - Optional axis inversion flags
- Input preferences storage key: `mpapp.input-preferences.v1`.
- If AsyncStorage is unavailable or runtime storage operations fail, input preferences must fall back to an in-memory store and persist for process lifetime.
- Preference writes must be gated on successful startup read so transient hydration failures cannot overwrite existing persisted settings.
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
- `transportMode`
- `targetHostConfigured`

Additional transport diagnostics fields when available:
- `targetHostAddress`
- `nativeErrorCode`
- `availabilityState`

Move sampling diagnostics payload contract for `input.move` and move-transport-failure logs:
- `samplingPolicy` (`coalesced-throttle`)
- `samplingIntervalMs` (`16`)
- `samplingWindowMs` (elapsed window represented by one emitted movement sample)
- `samplingRawSampleCount` (raw gesture movement samples observed in the window)
- `samplingCoalescedSampleCount` (`samplingRawSampleCount - 1`)
- `samplingDroppedSampleCount` (`0` for coalescing policy)
- `samplingEmittedSampleCount` (`1` per emitted movement sample)
- `invertX` (boolean axis inversion state at emission time)
- `invertY` (boolean axis inversion state at emission time)

Disconnect diagnostics payload contract:
- Disconnect transition logs must include `disconnectReason` as a machine-readable `MpappDisconnectReason` value for both success and failure paths.
- Disconnect failure logs must continue to include `nativeErrorCode` when available.

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
- App lint from `apps/mpapp`: `pnpm lint`
- Workspace-filtered app lint from repository root: `pnpm --filter mpapp lint`

Lint configuration contract:
- `apps/mpapp/eslint.config.js` must exist and load `eslint-config-expo/flat` explicitly.
- `eslint` and `eslint-config-expo` must remain declared in `apps/mpapp` `devDependencies` so CI never relies on Expo's interactive auto-configuration path.

MVP acceptance criteria scenarios:
1. Drag start/move/end emits pointer delta samples without dropping movement segments under normal interaction.
2. Tapping the left-click button emits exactly one `left-click` action.
3. Tapping the right-click button emits exactly one `right-click` action.
4. Attempting input before a connected state shows a clear disabled or error state.
5. Permission denial shows a retry path and logs required structured fields.
6. Disconnect events follow the documented state transition order, persist machine-readable disconnect reasons, and provide reason-specific reconnect guidance in the session status UI.
7. High input frequency follows the documented `16ms` coalesced throttle policy, flushes pending movement on gesture end, and keeps sampling diagnostics observable in logs.
8. Runtime transport switch can intentionally select `native-android-hid` or `stub` and logs selected mode.
9. Native transport failures preserve canonical `MpappErrorCode` while recording `nativeErrorCode` in diagnostics.
10. Bluetooth-unavailable and Bluetooth-disabled preflight branches block pairing/connecting and emit structured diagnostics.

