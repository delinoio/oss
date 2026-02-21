import { StatusBar } from "expo-status-bar";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import {
  PermissionsAndroid,
  Platform,
  Pressable,
  SafeAreaView,
  StyleSheet,
  Text,
  View,
  type Permission,
} from "react-native";
import {
  MpappActionType,
  MpappClickButton,
  MpappErrorCode,
  MpappLogEventFamily,
  MpappMode,
} from "./src/contracts/enums";
import { createConnectedClickSample, createPointerMoveSample } from "./src/input/translate-gesture";
import {
  evaluatePlatformSupport,
  requestAndroidBluetoothPermissions,
  type PlatformDescriptor,
  type MpappAndroidPermission,
} from "./src/permissions/android-permissions";
import {
  AsyncStorageDiagnosticsStore,
  buildLogEvent,
  type DiagnosticsStore,
} from "./src/diagnostics/diagnostics-store";
import {
  INITIAL_SESSION_STATE,
  MpappSessionEventType,
  reduceSessionState,
} from "./src/state/session-machine";
import { AndroidHidStubAdapter } from "./src/transport/android-hid-stub-adapter";
import { ClickControls } from "./src/components/click-controls";
import { SessionStatus } from "./src/components/session-status";
import { TouchpadSurface } from "./src/components/touchpad-surface";

const SENSITIVITY_STEP = 0.1;
const SENSITIVITY_MIN = 0.5;
const SENSITIVITY_MAX = 2;

function clampSensitivity(value: number): number {
  const rounded = Number.parseFloat(value.toFixed(1));
  return Math.max(SENSITIVITY_MIN, Math.min(SENSITIVITY_MAX, rounded));
}

function createSessionId(): string {
  return `session-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export default function App() {
  const [sessionState, dispatch] = useReducer(
    reduceSessionState,
    INITIAL_SESSION_STATE,
  );
  const [sensitivity, setSensitivity] = useState(1.0);

  const adapter = useMemo(() => new AndroidHidStubAdapter(), []);
  const diagnosticsStoreRef = useRef<DiagnosticsStore>(
    new AsyncStorageDiagnosticsStore(),
  );
  const sessionIdRef = useRef<string>(createSessionId());

  const platformDescriptor = useMemo<PlatformDescriptor>(
    () => ({
      os: Platform.OS,
      version: Platform.Version,
    }),
    [],
  );

  const platformSupport = useMemo(
    () => evaluatePlatformSupport(platformDescriptor),
    [platformDescriptor],
  );

  const appendLog = useCallback(
    async (params: {
      eventFamily: MpappLogEventFamily;
      actionType: MpappActionType;
      latencyMs: number;
      failureReason?: string | null;
      payload?: Record<string, unknown>;
    }) => {
      const event = buildLogEvent({
        eventFamily: params.eventFamily,
        actionType: params.actionType,
        sessionId: sessionIdRef.current,
        connectionState: sessionState.mode,
        latencyMs: params.latencyMs,
        failureReason: params.failureReason,
        payload: params.payload,
        platform: Platform.OS,
        osVersion: String(Platform.Version),
      });

      await diagnosticsStoreRef.current.append(event);
      console.info("[mpapp][log]", event.eventFamily, event.actionType, {
        failureReason: event.failureReason,
        latencyMs: event.latencyMs,
      });
    },
    [sessionState.mode],
  );

  useEffect(() => {
    if (platformSupport.supported) {
      return;
    }

    dispatch({
      type: MpappSessionEventType.ConnectFailure,
      errorCode: MpappErrorCode.UnsupportedPlatform,
      message: platformSupport.reason,
    });

    void appendLog({
      eventFamily: MpappLogEventFamily.TransportError,
      actionType: MpappActionType.Transport,
      latencyMs: 0,
      failureReason: platformSupport.reason,
      payload: {
        os: platformDescriptor.os,
        version: platformDescriptor.version,
      },
    });
  }, [appendLog, platformDescriptor, platformSupport]);

  const handleConnect = useCallback(async () => {
    if (!platformSupport.supported) {
      return;
    }

    dispatch({ type: MpappSessionEventType.StartPermissionCheck });

    const permissionStart = Date.now();
    const permissionResult = await requestAndroidBluetoothPermissions(
      async (permission: MpappAndroidPermission) => {
        const response = await PermissionsAndroid.request(
          permission as unknown as Permission,
          {
            title: "Bluetooth access is required",
            message:
              "mpapp needs Bluetooth permissions to pair and connect as a mouse.",
            buttonPositive: "Allow",
          },
        );

        return response === PermissionsAndroid.RESULTS.GRANTED;
      },
    );

    await appendLog({
      eventFamily: MpappLogEventFamily.PermissionCheck,
      actionType: MpappActionType.PermissionCheck,
      latencyMs: Date.now() - permissionStart,
      failureReason: permissionResult.granted
        ? null
        : "Bluetooth permission denied",
      payload: {
        granted: permissionResult.granted,
        missing: permissionResult.missing,
      },
    });

    if (!permissionResult.granted) {
      dispatch({ type: MpappSessionEventType.PermissionDenied });
      return;
    }

    dispatch({ type: MpappSessionEventType.PermissionGranted });
    dispatch({ type: MpappSessionEventType.StartPairing });
    dispatch({ type: MpappSessionEventType.StartConnecting });

    const connectStart = Date.now();
    const connectResult = await adapter.pairAndConnect();

    if (!connectResult.ok) {
      dispatch({
        type: MpappSessionEventType.ConnectFailure,
        errorCode: connectResult.errorCode,
        message: connectResult.message,
      });

      await appendLog({
        eventFamily: MpappLogEventFamily.ConnectionTransition,
        actionType: MpappActionType.Connect,
        latencyMs: Date.now() - connectStart,
        failureReason: connectResult.message,
      });
      return;
    }

    dispatch({ type: MpappSessionEventType.ConnectSuccess });
    await appendLog({
      eventFamily: MpappLogEventFamily.ConnectionTransition,
      actionType: MpappActionType.Connect,
      latencyMs: Date.now() - connectStart,
    });
  }, [adapter, appendLog, platformSupport.supported]);

  const handleDisconnect = useCallback(async () => {
    const disconnectStart = Date.now();
    const disconnectResult = await adapter.disconnect();

    if (!disconnectResult.ok) {
      dispatch({
        type: MpappSessionEventType.ConnectFailure,
        errorCode: disconnectResult.errorCode,
        message: disconnectResult.message,
      });
      await appendLog({
        eventFamily: MpappLogEventFamily.TransportError,
        actionType: MpappActionType.Disconnect,
        latencyMs: Date.now() - disconnectStart,
        failureReason: disconnectResult.message,
      });
      return;
    }

    dispatch({ type: MpappSessionEventType.Disconnect });
    await appendLog({
      eventFamily: MpappLogEventFamily.ConnectionTransition,
      actionType: MpappActionType.Disconnect,
      latencyMs: Date.now() - disconnectStart,
    });
  }, [adapter, appendLog]);

  const handleMove = useCallback(
    (deltaX: number, deltaY: number) => {
      if (sessionState.mode !== MpappMode.Connected) {
        return;
      }

      const sample = createPointerMoveSample(deltaX, deltaY, sensitivity);
      const sendStart = Date.now();

      void adapter.sendMove(sample).then(async (sendResult) => {
        if (!sendResult.ok) {
          await appendLog({
            eventFamily: MpappLogEventFamily.TransportError,
            actionType: MpappActionType.Move,
            latencyMs: Date.now() - sendStart,
            failureReason: sendResult.message,
            payload: {
              actionId: sample.actionId,
            },
          });
          return;
        }

        await appendLog({
          eventFamily: MpappLogEventFamily.InputMove,
          actionType: MpappActionType.Move,
          latencyMs: Date.now() - sendStart,
          payload: {
            deltaX: sample.deltaX,
            deltaY: sample.deltaY,
            sensitivity: sample.sensitivity,
          },
        });
      });
    },
    [adapter, appendLog, sensitivity, sessionState.mode],
  );

  const handleClick = useCallback(
    (button: MpappClickButton) => {
      const sample = createConnectedClickSample(sessionState.mode, button);
      if (!sample) {
        return;
      }

      const sendStart = Date.now();
      void adapter.sendClick(sample).then(async (sendResult) => {
        if (!sendResult.ok) {
          await appendLog({
            eventFamily: MpappLogEventFamily.TransportError,
            actionType:
              button === MpappClickButton.Left
                ? MpappActionType.LeftClick
                : MpappActionType.RightClick,
            latencyMs: Date.now() - sendStart,
            failureReason: sendResult.message,
            payload: {
              actionId: sample.actionId,
            },
          });
          return;
        }

        await appendLog({
          eventFamily: MpappLogEventFamily.InputClick,
          actionType:
            button === MpappClickButton.Left
              ? MpappActionType.LeftClick
              : MpappActionType.RightClick,
          latencyMs: Date.now() - sendStart,
          payload: {
            actionId: sample.actionId,
          },
        });
      });
    },
    [adapter, appendLog, sessionState.mode],
  );

  const canConnect =
    platformSupport.supported &&
    sessionState.mode !== MpappMode.Connected &&
    sessionState.mode !== MpappMode.Connecting &&
    sessionState.mode !== MpappMode.PermissionCheck;
  const canDisconnect = sessionState.mode === MpappMode.Connected;

  return (
    <SafeAreaView style={styles.safeArea}>
      <StatusBar style="dark" />
      <View style={styles.container}>
        <Text style={styles.heading}>mpapp Android MVP</Text>

        <SessionStatus state={sessionState} />

        <View style={styles.actions}>
          <Pressable
            disabled={!canConnect}
            onPress={handleConnect}
            style={[styles.actionButton, !canConnect ? styles.actionDisabled : null]}
          >
            <Text style={styles.actionText}>Pair and Connect</Text>
          </Pressable>

          <Pressable
            disabled={!canDisconnect}
            onPress={handleDisconnect}
            style={[
              styles.actionButton,
              styles.actionDisconnect,
              !canDisconnect ? styles.actionDisabled : null,
            ]}
          >
            <Text style={styles.actionText}>Disconnect</Text>
          </Pressable>
        </View>

        <View style={styles.sensitivityRow}>
          <Text style={styles.sensitivityLabel}>
            Sensitivity: {sensitivity.toFixed(1)}
          </Text>

          <View style={styles.sensitivityControls}>
            <Pressable
              onPress={() => {
                setSensitivity((previous) =>
                  clampSensitivity(previous - SENSITIVITY_STEP),
                );
              }}
              style={styles.sensitivityButton}
            >
              <Text style={styles.sensitivityButtonText}>-</Text>
            </Pressable>

            <Pressable
              onPress={() => {
                setSensitivity((previous) =>
                  clampSensitivity(previous + SENSITIVITY_STEP),
                );
              }}
              style={styles.sensitivityButton}
            >
              <Text style={styles.sensitivityButtonText}>+</Text>
            </Pressable>
          </View>
        </View>

        <TouchpadSurface
          disabled={sessionState.mode !== MpappMode.Connected}
          onMove={handleMove}
        />

        <ClickControls
          disabled={sessionState.mode !== MpappMode.Connected}
          onLeftClick={() => {
            handleClick(MpappClickButton.Left);
          }}
          onRightClick={() => {
            handleClick(MpappClickButton.Right);
          }}
        />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: {
    flex: 1,
    backgroundColor: "#e5e7eb",
  },
  container: {
    flex: 1,
    paddingHorizontal: 16,
    paddingVertical: 12,
    gap: 14,
  },
  heading: {
    fontSize: 24,
    fontWeight: "800",
    color: "#0f172a",
  },
  actions: {
    flexDirection: "row",
    gap: 10,
  },
  actionButton: {
    flex: 1,
    borderRadius: 14,
    borderCurve: "continuous",
    paddingVertical: 12,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "#0f766e",
  },
  actionDisconnect: {
    backgroundColor: "#1d4ed8",
  },
  actionDisabled: {
    backgroundColor: "#9ca3af",
  },
  actionText: {
    color: "#ffffff",
    fontSize: 14,
    fontWeight: "700",
  },
  sensitivityRow: {
    width: "100%",
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  sensitivityLabel: {
    color: "#111827",
    fontWeight: "700",
    fontSize: 15,
  },
  sensitivityControls: {
    flexDirection: "row",
    gap: 8,
  },
  sensitivityButton: {
    width: 36,
    height: 36,
    borderRadius: 8,
    borderCurve: "continuous",
    backgroundColor: "#334155",
    alignItems: "center",
    justifyContent: "center",
  },
  sensitivityButtonText: {
    color: "#f9fafb",
    fontWeight: "700",
    fontSize: 18,
    lineHeight: 18,
  },
});
