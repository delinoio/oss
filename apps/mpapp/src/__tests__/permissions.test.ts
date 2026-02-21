import { MpappErrorCode } from "../contracts/enums";
import {
  ANDROID_MIN_API_LEVEL,
  MpappAndroidPermission,
  evaluatePermissionResult,
  evaluatePlatformSupport,
} from "../permissions/android-permissions";

describe("android permissions", () => {
  it("supports android api 31+", () => {
    const supported = evaluatePlatformSupport({
      os: "android",
      version: ANDROID_MIN_API_LEVEL,
    });

    const unsupported = evaluatePlatformSupport({
      os: "android",
      version: ANDROID_MIN_API_LEVEL - 1,
    });

    expect(supported.supported).toBe(true);
    expect(unsupported.supported).toBe(false);
    expect(unsupported.errorCode).toBe(MpappErrorCode.UnsupportedPlatform);
  });

  it("marks ios as unsupported", () => {
    const result = evaluatePlatformSupport({ os: "ios", version: "18.0" });
    expect(result.supported).toBe(false);
    expect(result.errorCode).toBe(MpappErrorCode.UnsupportedPlatform);
  });

  it("detects missing bluetooth permissions", () => {
    const result = evaluatePermissionResult({
      [MpappAndroidPermission.BluetoothConnect]: true,
      [MpappAndroidPermission.BluetoothScan]: false,
    });

    expect(result.granted).toBe(false);
    expect(result.missing).toEqual([MpappAndroidPermission.BluetoothScan]);
  });
});
