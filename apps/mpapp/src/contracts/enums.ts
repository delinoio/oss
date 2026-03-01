export enum MpappPlatformScope {
  AndroidMvp = "android-mvp",
  IosResearch = "ios-research",
}

export enum MpappMode {
  Idle = "idle",
  PermissionCheck = "permission-check",
  Pairing = "pairing",
  Connecting = "connecting",
  Connected = "connected",
  Error = "error",
}

export enum MpappInputAction {
  Move = "move",
  LeftClick = "left-click",
  RightClick = "right-click",
}

export enum MpappClickButton {
  Left = "left",
  Right = "right",
}

export enum MpappConnectionEvent {
  StartPairing = "start-pairing",
  ConnectSuccess = "connect-success",
  ConnectFailure = "connect-failure",
  Disconnect = "disconnect",
  DisconnectFailure = "disconnect-failure",
  PermissionDenied = "permission-denied",
}

export enum MpappDisconnectReason {
  UserAction = "user-action",
  TransportLost = "transport-lost",
  Timeout = "timeout",
  PermissionRevoked = "permission-revoked",
  Unknown = "unknown",
}

export enum MpappErrorCode {
  BluetoothUnavailable = "bluetooth-unavailable",
  PermissionDenied = "permission-denied",
  PairingTimeout = "pairing-timeout",
  TransportFailure = "transport-failure",
  UnsupportedPlatform = "unsupported-platform",
}

export enum MpappBluetoothAvailabilityState {
  Available = "available",
  AdapterUnavailable = "adapter-unavailable",
  Disabled = "disabled",
  Unknown = "unknown",
}

export enum MpappHidTransportMode {
  NativeAndroidHid = "native-android-hid",
  Stub = "stub",
}

export enum MpappLogEventFamily {
  PermissionCheck = "permission.check",
  ConnectionTransition = "connection.transition",
  InputMove = "input.move",
  InputClick = "input.click",
  TransportError = "transport.error",
}

export enum MpappActionType {
  PermissionCheck = "permission-check",
  Connect = "connect",
  Disconnect = "disconnect",
  Move = "move",
  LeftClick = "left-click",
  RightClick = "right-click",
  Transport = "transport",
}
