# Feature: architecture

## Architecture
- App shell initializes runtime, checks platform capability, and hosts top-level state.
- Session state machine handles lifecycle transitions (`Idle -> PermissionCheck -> Pairing -> Connecting -> Connected -> Error`).
- Input surface module exposes:
  - A touchpad region for drag capture
  - Dedicated left-click and right-click controls
- Input translation module applies optional axis inversion flags and sensitivity when converting gesture deltas into pointer movement samples.
- Input movement sampling policy coalesces drag deltas and emits at most one movement sample every `16ms` (`~60Hz`) while preserving summed pointer distance.
- If coalesced movement remains pending after the latest drag update, the app must emit it when the `16ms` throttle window elapses even without additional movement callbacks.
- Throttle interval checks must use a monotonic clock source so device wall-clock adjustments cannot stall movement emission.
- The trailing movement emission timer must be scheduled from the computed due timestamp, not fixed-phase polling.
- Touchpad gesture end (`release` or `terminate`) flushes any pending coalesced movement so no in-progress segment is stranded.
- Touchpad gesture responder instances must be recreated when movement callback dependencies change so runtime sensitivity updates take effect without reconnecting.
- Input preferences module persists `sensitivity`, `invertX`, and `invertY` locally and hydrates them at app startup.
- Startup hydration must preserve user-driven edits made before hydration completes while still applying persisted values for untouched preference fields.
- Settings controls expose sensitivity increment/decrement plus `Invert X` and `Invert Y` toggles independently.
- Android HID transport adapter is implemented as a TypeScript `HidAdapter` contract with:
  - `native-android-hid` mode backed by a local Expo native module at `apps/mpapp/modules/mpapp-android-hid`
  - `stub` mode backed by `AndroidHidStubAdapter` for deterministic tests and local simulation
  - `checkBluetoothAvailability()` preflight gate after runtime permission grant and before pairing
- Runtime transport mode selection resolves in priority order:
  - `EXPO_PUBLIC_MPAPP_HID_TRANSPORT_MODE` env override
  - `expo.extra.mpapp.hidTransportMode` in app config
  - default `native-android-hid`
- Native mode host target selection resolves in priority order:
  - `EXPO_PUBLIC_MPAPP_HID_TARGET_HOST_ADDRESS` env override
  - `expo.extra.mpapp.hidTargetHostAddress` in app config
  - `null` (which is an explicit connect-time error in native mode)
- Optional env overrides are documented in `apps/mpapp/.env.example`.
- Diagnostics module records structured events, failures, and latency observations in local storage.

