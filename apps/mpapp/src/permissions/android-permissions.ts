import {
  MpappErrorCode,
  MpappPlatformScope,
} from "../contracts/enums";

export const ANDROID_MIN_API_LEVEL = 31;

export enum MpappAndroidPermission {
  BluetoothConnect = "android.permission.BLUETOOTH_CONNECT",
  BluetoothScan = "android.permission.BLUETOOTH_SCAN",
}

export type SupportedPlatformResult = {
  supported: boolean;
  scope: MpappPlatformScope;
  errorCode: MpappErrorCode | null;
  reason: string;
};

export type PlatformDescriptor = {
  os: string;
  version: number | string;
};

export type AndroidPermissionResult = {
  granted: boolean;
  missing: MpappAndroidPermission[];
};

export function normalizeAndroidApiLevel(version: number | string): number {
  if (typeof version === "number") {
    return version;
  }

  const parsedVersion = Number.parseInt(version, 10);
  if (Number.isNaN(parsedVersion)) {
    return 0;
  }

  return parsedVersion;
}

export function evaluatePlatformSupport(
  descriptor: PlatformDescriptor,
): SupportedPlatformResult {
  if (descriptor.os !== "android") {
    return {
      supported: false,
      scope: MpappPlatformScope.IosResearch,
      errorCode: MpappErrorCode.UnsupportedPlatform,
      reason: "Only Android is supported in the MVP.",
    };
  }

  const apiLevel = normalizeAndroidApiLevel(descriptor.version);
  if (apiLevel < ANDROID_MIN_API_LEVEL) {
    return {
      supported: false,
      scope: MpappPlatformScope.AndroidMvp,
      errorCode: MpappErrorCode.UnsupportedPlatform,
      reason: `Android API ${ANDROID_MIN_API_LEVEL}+ is required.`,
    };
  }

  return {
    supported: true,
    scope: MpappPlatformScope.AndroidMvp,
    errorCode: null,
    reason: "Platform supported.",
  };
}

export function getRequiredAndroidBluetoothPermissions(): MpappAndroidPermission[] {
  return [
    MpappAndroidPermission.BluetoothConnect,
    MpappAndroidPermission.BluetoothScan,
  ];
}

export function evaluatePermissionResult(
  statuses: Record<MpappAndroidPermission, boolean>,
): AndroidPermissionResult {
  const missing = getRequiredAndroidBluetoothPermissions().filter(
    (permission) => !statuses[permission],
  );

  return {
    granted: missing.length === 0,
    missing,
  };
}

export async function requestAndroidBluetoothPermissions(
  requestPermission: (permission: MpappAndroidPermission) => Promise<boolean>,
): Promise<AndroidPermissionResult> {
  const statuses = {
    [MpappAndroidPermission.BluetoothConnect]: false,
    [MpappAndroidPermission.BluetoothScan]: false,
  };

  for (const permission of getRequiredAndroidBluetoothPermissions()) {
    statuses[permission] = await requestPermission(permission);
  }

  return evaluatePermissionResult(statuses);
}
