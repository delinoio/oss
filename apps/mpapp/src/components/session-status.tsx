import { StyleSheet, Text, View } from "react-native";
import { MpappMode } from "../contracts/enums";
import type { MpappSessionState } from "../state/session-machine";

type SessionStatusProps = {
  state: MpappSessionState;
};

function readableMode(mode: MpappMode): string {
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

export function SessionStatus({ state }: SessionStatusProps) {
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
});
