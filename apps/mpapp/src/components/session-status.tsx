import { StyleSheet, Text, View } from "react-native";
import {
  MpappConnectionEvent,
  MpappDisconnectReason,
  MpappMode,
} from "../contracts/enums";
import type { MpappSessionState } from "../state/session-machine";

type SessionStatusProps = {
  state: MpappSessionState;
};

export function readableMode(mode: MpappMode): string {
  switch (mode) {
    case MpappMode.PermissionCheck:
      return "Permission Check";
    case MpappMode.Pairing:
      return "Pairing";
    case MpappMode.Connecting:
      return "Connecting";
    case MpappMode.Connected:
      return "Connected";
    case MpappMode.Error:
      return "Error";
    case MpappMode.Idle:
    default:
      return "Idle";
  }
}

export function readableDisconnectReason(reason: MpappDisconnectReason): string {
  switch (reason) {
    case MpappDisconnectReason.UserAction:
      return "User Action";
    case MpappDisconnectReason.TransportLost:
      return "Transport Lost";
    case MpappDisconnectReason.Timeout:
      return "Timeout";
    case MpappDisconnectReason.PermissionRevoked:
      return "Permission Revoked";
    case MpappDisconnectReason.Unknown:
    default:
      return "Unknown";
  }
}

function guidanceByReason(
  reason: MpappDisconnectReason,
  mode: MpappMode,
): string {
  const encounteredError = mode === MpappMode.Error;

  switch (reason) {
    case MpappDisconnectReason.UserAction:
      return encounteredError
        ? "Disconnect was user initiated but ended with an error. Tap Pair and Connect to start a fresh session."
        : "Disconnect was user initiated. Tap Pair and Connect when you are ready to reconnect.";
    case MpappDisconnectReason.TransportLost:
      return encounteredError
        ? "Connection dropped while disconnecting. Check Bluetooth and host availability, then tap Pair and Connect."
        : "Connection was lost. Move closer to the host, verify Bluetooth is enabled, then tap Pair and Connect.";
    case MpappDisconnectReason.Timeout:
      return encounteredError
        ? "Disconnect timed out and returned an error. Wait a moment, then tap Pair and Connect."
        : "Disconnect timed out. Wait a moment, then tap Pair and Connect to recover.";
    case MpappDisconnectReason.PermissionRevoked:
      return encounteredError
        ? "Bluetooth permission is missing. Re-grant permissions and tap Pair and Connect."
        : "Bluetooth permission changed. Re-grant permissions, then tap Pair and Connect.";
    case MpappDisconnectReason.Unknown:
    default:
      return encounteredError
        ? "Disconnect failed for an unknown reason. Tap Pair and Connect to retry."
        : "Disconnect reason was not reported. Tap Pair and Connect to attempt recovery.";
  }
}

export function reconnectGuidanceForState(
  state: Pick<
    MpappSessionState,
    "mode" | "lastConnectionEvent" | "lastDisconnectReason"
  >,
): string | null {
  if (!state.lastDisconnectReason) {
    return null;
  }

  if (
    state.lastConnectionEvent !== MpappConnectionEvent.Disconnect &&
    state.lastConnectionEvent !== MpappConnectionEvent.DisconnectFailure
  ) {
    return null;
  }

  return guidanceByReason(state.lastDisconnectReason, state.mode);
}

export function SessionStatus({ state }: SessionStatusProps) {
  const reconnectGuidance = reconnectGuidanceForState(state);

  return (
    <View style={styles.wrapper}>
      <Text style={styles.label}>Session</Text>
      <Text style={styles.value}>{readableMode(state.mode)}</Text>

      {state.errorCode ? (
        <Text style={styles.errorCode}>Error Code: {state.errorCode}</Text>
      ) : null}

      {state.errorMessage ? (
        <Text style={styles.errorMessage}>{state.errorMessage}</Text>
      ) : null}

      {state.lastConnectionEvent ? (
        <Text style={styles.event}>Last Event: {state.lastConnectionEvent}</Text>
      ) : null}

      {state.lastDisconnectReason && reconnectGuidance ? (
        <Text style={styles.reason}>
          Disconnect Reason: {readableDisconnectReason(state.lastDisconnectReason)}
        </Text>
      ) : null}

      {reconnectGuidance ? (
        <Text style={styles.guidance}>Reconnect Guidance: {reconnectGuidance}</Text>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  wrapper: {
    width: "100%",
    borderRadius: 16,
    borderCurve: "continuous",
    borderWidth: 1,
    borderColor: "#d1d5db",
    backgroundColor: "#ffffff",
    padding: 12,
    gap: 4,
  },
  label: {
    fontSize: 12,
    color: "#6b7280",
    fontWeight: "600",
  },
  value: {
    fontSize: 18,
    color: "#111827",
    fontWeight: "700",
  },
  errorCode: {
    color: "#991b1b",
    fontWeight: "600",
  },
  errorMessage: {
    color: "#991b1b",
  },
  event: {
    color: "#334155",
    fontSize: 12,
  },
  reason: {
    color: "#334155",
    fontSize: 12,
    fontWeight: "600",
  },
  guidance: {
    color: "#0f766e",
    fontSize: 12,
    fontWeight: "600",
  },
});
