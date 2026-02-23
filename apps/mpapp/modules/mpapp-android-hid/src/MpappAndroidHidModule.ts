import { requireNativeModule } from "expo-modules-core";

export enum MpappAndroidHidNativeErrorCode {
  BluetoothUnavailable = "bluetooth-unavailable",
  PermissionDenied = "permission-denied",
  PairingTimeout = "pairing-timeout",
  UnsupportedPlatform = "unsupported-platform",
  TransportFailure = "transport-failure",
  HostAddressRequired = "host-address-required",
  InvalidHostAddress = "invalid-host-address",
}

export enum MpappAndroidHidNativeButton {
  Left = "left",
  Right = "right",
}

export type MpappAndroidHidNativeSuccessResult = {
  ok: true;
};

export type MpappAndroidHidNativeFailureResult = {
  ok: false;
  code: string;
  message: string;
  details?: Record<string, unknown>;
};

export type MpappAndroidHidNativeResult =
  | MpappAndroidHidNativeSuccessResult
  | MpappAndroidHidNativeFailureResult;

export type MpappAndroidHidNativeModule = {
  pairAndConnect(hostAddress: string): Promise<MpappAndroidHidNativeResult>;
  disconnect(): Promise<MpappAndroidHidNativeResult>;
  sendMove(deltaX: number, deltaY: number): Promise<MpappAndroidHidNativeResult>;
  sendClick(button: MpappAndroidHidNativeButton): Promise<MpappAndroidHidNativeResult>;
};

let cachedModule: MpappAndroidHidNativeModule | null = null;

export function getMpappAndroidHidNativeModule(): MpappAndroidHidNativeModule {
  if (!cachedModule) {
    cachedModule = requireNativeModule<MpappAndroidHidNativeModule>(
      "MpappAndroidHid",
    );
  }

  return cachedModule;
}
