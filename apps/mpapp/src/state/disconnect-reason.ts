import { MpappDisconnectReason, MpappErrorCode } from "../contracts/enums";
import { MpappAndroidHidNativeErrorCode } from "../../modules/mpapp-android-hid";

function resolveDisconnectReasonFromNativeError(
  nativeErrorCode?: string | null,
): MpappDisconnectReason | null {
  switch (nativeErrorCode) {
    case MpappAndroidHidNativeErrorCode.PermissionDenied:
      return MpappDisconnectReason.PermissionRevoked;
    case MpappAndroidHidNativeErrorCode.PairingTimeout:
      return MpappDisconnectReason.Timeout;
    case MpappAndroidHidNativeErrorCode.TransportFailure:
    case MpappAndroidHidNativeErrorCode.BluetoothUnavailable:
    case MpappAndroidHidNativeErrorCode.UnsupportedPlatform:
    case MpappAndroidHidNativeErrorCode.HostAddressRequired:
    case MpappAndroidHidNativeErrorCode.InvalidHostAddress:
      return MpappDisconnectReason.TransportLost;
    default:
      return null;
  }
}

function resolveDisconnectReasonFromErrorCode(
  errorCode: MpappErrorCode,
): MpappDisconnectReason {
  switch (errorCode) {
    case MpappErrorCode.PermissionDenied:
      return MpappDisconnectReason.PermissionRevoked;
    case MpappErrorCode.PairingTimeout:
      return MpappDisconnectReason.Timeout;
    case MpappErrorCode.TransportFailure:
    case MpappErrorCode.BluetoothUnavailable:
    case MpappErrorCode.UnsupportedPlatform:
      return MpappDisconnectReason.TransportLost;
    default:
      return MpappDisconnectReason.Unknown;
  }
}

export function resolveDisconnectReasonFromFailure(
  errorCode: MpappErrorCode,
  nativeErrorCode?: string | null,
): MpappDisconnectReason {
  const nativeReason = resolveDisconnectReasonFromNativeError(nativeErrorCode);
  if (nativeReason) {
    return nativeReason;
  }

  return resolveDisconnectReasonFromErrorCode(errorCode);
}
