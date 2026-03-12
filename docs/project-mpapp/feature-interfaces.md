# Feature: interfaces

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
  DisconnectFailure = "disconnect-failure",
  PermissionDenied = "permission-denied",
}
```

Canonical disconnect reasons:

```ts
enum MpappDisconnectReason {
  UserAction = "user-action",
  TransportLost = "transport-lost",
  Timeout = "timeout",
  PermissionRevoked = "permission-revoked",
  Unknown = "unknown",
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

Canonical Bluetooth availability states:

```ts
enum MpappBluetoothAvailabilityState {
  Available = "available",
  AdapterUnavailable = "adapter-unavailable",
  Disabled = "disabled",
  Unknown = "unknown",
}
```

Canonical Bluetooth availability result:

```ts
type BluetoothAvailabilityResult =
  | {
      ok: true;
      availabilityState: MpappBluetoothAvailabilityState.Available;
    }
  | {
      ok: false;
      availabilityState:
        | MpappBluetoothAvailabilityState.AdapterUnavailable
        | MpappBluetoothAvailabilityState.Disabled
        | MpappBluetoothAvailabilityState.Unknown;
      errorCode: MpappErrorCode;
      message: string;
      nativeErrorCode?: string;
    };
```

Canonical HID adapter contract:

```ts
interface HidAdapter {
  checkBluetoothAvailability(): Promise<BluetoothAvailabilityResult>;
  pairAndConnect(): Promise<Result>;
  disconnect(): Promise<Result>;
  sendMove(sample: PointerMoveSample): Promise<Result>;
  sendClick(sample: PointerClickSample): Promise<Result>;
}
```

Canonical HID transport mode identifiers:

```ts
enum MpappHidTransportMode {
  NativeAndroidHid = "native-android-hid",
  Stub = "stub",
}
```

Canonical move sampling policy identifiers:

```ts
enum MpappMoveSamplingPolicy {
  CoalescedThrottle = "coalesced-throttle",
}
```

Canonical runtime transport config contract:

```ts
type MpappRuntimeConfig = {
  hidTransportMode: MpappHidTransportMode;
  hidTargetHostAddress: string | null;
}
```

Canonical input preferences contract:

```ts
type MpappInputPreferences = {
  sensitivity: number;
  invertX: boolean;
  invertY: boolean;
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
- `MpappHidTransportMode` values are stable runtime contract values and must not be renamed without migration.
- `MpappMoveSamplingPolicy` values are stable runtime contract values and must not be renamed without migration.
- `MpappDisconnectReason` values are stable lifecycle contract values and must not be renamed without migration.
- Native transport failures may include `nativeErrorCode` for diagnostics, but `MpappErrorCode` remains the canonical app-facing error contract.

Android and iOS scope contract:
- Android MVP supports direct Bluetooth mouse flow.
- iOS is explicitly documented as `IosResearch` and excluded from direct HID delivery in MVP.

Permissions and capability contract (Android MVP):
- Check Bluetooth availability before entering pairing flow.
- Run `HidAdapter.checkBluetoothAvailability()` after permission grant and before `StartPairing`.
- If Bluetooth is unavailable or disabled, stop the connect flow and surface `MpappErrorCode.BluetoothUnavailable` with actionable remediation text.
- Gate pairing/connection on runtime permission results.
- Surface `MpappErrorCode.PermissionDenied` when permission requirements are not satisfied.
- Require Android API level `31+` for MVP runtime support.
- In `native-android-hid` mode, pairing requires a configured Bluetooth host address (`XX:XX:XX:XX:XX:XX`).

Reference feasibility links:
- [Expo SDK modules](https://docs.expo.dev/versions/latest/)
- [Expo custom native code workflow](https://docs.expo.dev/workflow/customizing/)
- [Android BluetoothHidDevice API](https://developer.android.com/reference/android/bluetooth/BluetoothHidDevice)
- [Apple Bluetooth overview (Core Bluetooth / MFi context)](https://developer-mdn.apple.com/bluetooth/)
- [Apple Core Bluetooth Concepts (BLE central/peripheral model)](https://developer.apple.com/library/archive/documentation/NetworkingInternetWeb/Conceptual/CoreBluetooth_concepts/AboutCoreBluetooth/Introduction.html)

