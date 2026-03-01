import {
  MpappConnectionEvent,
  MpappDisconnectReason,
  MpappErrorCode,
  MpappMode,
} from "../contracts/enums";

export enum MpappSessionEventType {
  StartPermissionCheck = "start-permission-check",
  PermissionGranted = "permission-granted",
  PermissionDenied = "permission-denied",
  StartPairing = "start-pairing",
  StartConnecting = "start-connecting",
  ConnectSuccess = "connect-success",
  ConnectFailure = "connect-failure",
  Disconnect = "disconnect",
  DisconnectFailure = "disconnect-failure",
  ResetError = "reset-error",
}

export type MpappSessionEvent =
  | { type: MpappSessionEventType.StartPermissionCheck }
  | { type: MpappSessionEventType.PermissionGranted }
  | { type: MpappSessionEventType.PermissionDenied }
  | { type: MpappSessionEventType.StartPairing }
  | { type: MpappSessionEventType.StartConnecting }
  | { type: MpappSessionEventType.ConnectSuccess }
  | {
      type: MpappSessionEventType.ConnectFailure;
      errorCode: MpappErrorCode;
      message: string;
    }
  | { type: MpappSessionEventType.Disconnect; reason: MpappDisconnectReason }
  | {
      type: MpappSessionEventType.DisconnectFailure;
      reason: MpappDisconnectReason;
      errorCode: MpappErrorCode;
      message: string;
    }
  | { type: MpappSessionEventType.ResetError };

export type MpappSessionState = {
  mode: MpappMode;
  errorCode: MpappErrorCode | null;
  errorMessage: string | null;
  lastConnectionEvent: MpappConnectionEvent | null;
  lastDisconnectReason: MpappDisconnectReason | null;
};

export const INITIAL_SESSION_STATE: MpappSessionState = {
  mode: MpappMode.Idle,
  errorCode: null,
  errorMessage: null,
  lastConnectionEvent: null,
  lastDisconnectReason: null,
};

export function reduceSessionState(
  state: MpappSessionState,
  event: MpappSessionEvent,
): MpappSessionState {
  switch (event.type) {
    case MpappSessionEventType.StartPermissionCheck:
      return {
        mode: MpappMode.PermissionCheck,
        errorCode: null,
        errorMessage: null,
        lastConnectionEvent: state.lastConnectionEvent,
        lastDisconnectReason: state.lastDisconnectReason,
      };

    case MpappSessionEventType.PermissionGranted:
      return {
        ...state,
        mode: MpappMode.Pairing,
      };

    case MpappSessionEventType.PermissionDenied:
      return {
        mode: MpappMode.Error,
        errorCode: MpappErrorCode.PermissionDenied,
        errorMessage: "Bluetooth permissions are required.",
        lastConnectionEvent: MpappConnectionEvent.PermissionDenied,
        lastDisconnectReason: state.lastDisconnectReason,
      };

    case MpappSessionEventType.StartPairing:
      return {
        ...state,
        mode: MpappMode.Pairing,
        errorCode: null,
        errorMessage: null,
        lastConnectionEvent: MpappConnectionEvent.StartPairing,
      };

    case MpappSessionEventType.StartConnecting:
      return {
        ...state,
        mode: MpappMode.Connecting,
      };

    case MpappSessionEventType.ConnectSuccess:
      return {
        mode: MpappMode.Connected,
        errorCode: null,
        errorMessage: null,
        lastConnectionEvent: MpappConnectionEvent.ConnectSuccess,
        lastDisconnectReason: null,
      };

    case MpappSessionEventType.ConnectFailure:
      return {
        mode: MpappMode.Error,
        errorCode: event.errorCode,
        errorMessage: event.message,
        lastConnectionEvent: MpappConnectionEvent.ConnectFailure,
        lastDisconnectReason: state.lastDisconnectReason,
      };

    case MpappSessionEventType.Disconnect:
      return {
        mode: MpappMode.Idle,
        errorCode: null,
        errorMessage: null,
        lastConnectionEvent: MpappConnectionEvent.Disconnect,
        lastDisconnectReason: event.reason,
      };

    case MpappSessionEventType.DisconnectFailure:
      return {
        mode: MpappMode.Error,
        errorCode: event.errorCode,
        errorMessage: event.message,
        lastConnectionEvent: MpappConnectionEvent.DisconnectFailure,
        lastDisconnectReason: event.reason,
      };

    case MpappSessionEventType.ResetError:
      return {
        ...INITIAL_SESSION_STATE,
        lastConnectionEvent: state.lastConnectionEvent,
        lastDisconnectReason: state.lastDisconnectReason,
      };

    default:
      return state;
  }
}
