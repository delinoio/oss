import { MpappAndroidHidNativeErrorCode } from "../../modules/mpapp-android-hid";
import { MpappDisconnectReason, MpappErrorCode } from "../contracts/enums";
import { resolveDisconnectReasonFromFailure } from "../state/disconnect-reason";

describe("disconnect reason resolver", () => {
  it("prefers native error mapping when both native and canonical codes exist", () => {
    const reason = resolveDisconnectReasonFromFailure(
      MpappErrorCode.PairingTimeout,
      MpappAndroidHidNativeErrorCode.PermissionDenied,
    );

    expect(reason).toBe(MpappDisconnectReason.PermissionRevoked);
  });

  it.each([
    [MpappErrorCode.PermissionDenied, MpappDisconnectReason.PermissionRevoked],
    [MpappErrorCode.PairingTimeout, MpappDisconnectReason.Timeout],
    [MpappErrorCode.TransportFailure, MpappDisconnectReason.TransportLost],
    [MpappErrorCode.BluetoothUnavailable, MpappDisconnectReason.TransportLost],
    [MpappErrorCode.UnsupportedPlatform, MpappDisconnectReason.TransportLost],
  ])("maps %s canonical error to %s", (errorCode, expectedReason) => {
    const reason = resolveDisconnectReasonFromFailure(errorCode);
    expect(reason).toBe(expectedReason);
  });

  it("maps known transport-related native errors to transport-lost", () => {
    const reason = resolveDisconnectReasonFromFailure(
      MpappErrorCode.PermissionDenied,
      MpappAndroidHidNativeErrorCode.TransportFailure,
    );

    expect(reason).toBe(MpappDisconnectReason.TransportLost);
  });

  it("falls back to unknown for unmapped native and canonical codes", () => {
    const reason = resolveDisconnectReasonFromFailure(
      "future-error" as MpappErrorCode,
      "native-future-error",
    );

    expect(reason).toBe(MpappDisconnectReason.Unknown);
  });
});
