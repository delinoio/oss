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
  MpappBluetoothAvailabilityState,
  MpappClickButton,
  MpappDisconnectReason,
  MpappErrorCode,
  MpappLogEventFamily,
  MpappMode,
} from "./src/contracts/enums";
import { resolveMpappRuntimeConfig } from "./src/config/mpapp-runtime-config";
import {
  createCoalescedMoveSamplingPolicy,
  type MoveSamplingEmission,
} from "./src/input/move-sampling-policy";
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
  type MpappSessionEvent,
  type MpappSessionState,
  MpappSessionEventType,
  reduceSessionState,
} from "./src/state/session-machine";
import { resolveDisconnectReasonFromFailure } from "./src/state/disconnect-reason";
import { createHidAdapter } from "./src/transport/hid-adapter-factory";
import { ClickControls } from "./src/components/click-controls";
import { SessionStatus } from "./src/components/session-status";
import { TouchpadSurface } from "./src/components/touchpad-surface";
import { AsyncStorageInputPreferencesStore } from "./src/preferences/input-preferences-store";
import {
  clampInputPreferenceSensitivity,
  DEFAULT_MPAPP_INPUT_PREFERENCES,
  type MpappInputPreferences,
} from "./src/preferences/input-preferences";

const SENSITIVITY_STEP = 0.1;
enum MpappInversionAxis {
  InvertX = "invert-x",
  InvertY = "invert-y",
}

function roundToSingleDecimal(value: number): number {
  return Number.parseFloat(value.toFixed(1));
}

function createSessionId(): string {
  return `session-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function getBluetoothUnavailableMessage(
  availabilityState: MpappBluetoothAvailabilityState,
): string {
  switch (availabilityState) {
    case MpappBluetoothAvailabilityState.Disabled:
      return "Bluetooth is turned off. Enable Bluetooth and try connecting again.";
    case MpappBluetoothAvailabilityState.AdapterUnavailable:
      return "Bluetooth is unavailable on this device, so pairing cannot start.";
    case MpappBluetoothAvailabilityState.Unknown:
    case MpappBluetoothAvailabilityState.Available:
    default:
      return "Bluetooth availability check failed. Enable Bluetooth and retry.";
  }
}

export default function App() {
  const [sessionState, dispatch] = useReducer(
    reduceSessionState,
    INITIAL_SESSION_STATE,
  );
  const sessionStateRef = useRef<MpappSessionState>(INITIAL_SESSION_STATE);
  const [inputPreferences, setInputPreferences] = useState<MpappInputPreferences>(
    DEFAULT_MPAPP_INPUT_PREFERENCES,
  );

  const runtimeConfig = useMemo(() => resolveMpappRuntimeConfig(), []);
  const adapter = useMemo(
    () =>
      createHidAdapter({
        runtimeConfig,
      }),
    [runtimeConfig],
  );
  const diagnosticsStoreRef = useRef<DiagnosticsStore>(
    new AsyncStorageDiagnosticsStore(),
  );
  const inputPreferencesStoreRef = useRef(
    new AsyncStorageInputPreferencesStore(),
  );
  const moveSamplingPolicyRef = useRef(createCoalescedMoveSamplingPolicy());
  const moveDueTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scheduleDueMoveEmissionRef = useRef<() => void>(() => {});
  const sessionIdRef = useRef<string>(createSessionId());
  const transportLogContext = useMemo(
    () => ({
      transportMode: runtimeConfig.hidTransportMode,
      targetHostAddress: runtimeConfig.hidTargetHostAddress,
      targetHostConfigured: Boolean(runtimeConfig.hidTargetHostAddress),
    }),
    [runtimeConfig.hidTargetHostAddress, runtimeConfig.hidTransportMode],
  );

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

  const dispatchSessionEvent = useCallback((event: MpappSessionEvent) => {
    sessionStateRef.current = reduceSessionState(sessionStateRef.current, event);
    dispatch(event);
  }, []);

  const clearMoveDueTimeout = useCallback(() => {
    if (moveDueTimeoutRef.current === null) {
      return;
    }

    clearTimeout(moveDueTimeoutRef.current);
    moveDueTimeoutRef.current = null;
  }, []);

  useEffect(() => {
    sessionStateRef.current = sessionState;
  }, [sessionState]);

  useEffect(() => {
    if (sessionState.mode === MpappMode.Connected) {
      return;
    }

    clearMoveDueTimeout();
    moveSamplingPolicyRef.current.reset();
  }, [clearMoveDueTimeout, sessionState.mode]);

  useEffect(() => {
    let mounted = true;

    void inputPreferencesStoreRef.current.load().then((preferences) => {
      if (!mounted) {
        return;
      }

      setInputPreferences(preferences);
    });

    return () => {
      mounted = false;
    };
  }, []);

  useEffect(() => {
    return () => {
      clearMoveDueTimeout();
    };
  }, [clearMoveDueTimeout]);

  const updateInputPreferences = useCallback(
    (updater: (previous: MpappInputPreferences) => MpappInputPreferences) => {
      setInputPreferences((previous) => {
        const nextPreferences = updater(previous);
        void inputPreferencesStoreRef.current.save(nextPreferences);
        return nextPreferences;
      });
    },
    [],
  );

  const handleSensitivityUpdate = useCallback(
    (delta: number) => {
      updateInputPreferences((previous) => ({
        ...previous,
        sensitivity: roundToSingleDecimal(
          clampInputPreferenceSensitivity(previous.sensitivity + delta),
        ),
      }));
    },
    [updateInputPreferences],
  );

  const handleInversionToggle = useCallback(
    (axis: MpappInversionAxis) => {
      updateInputPreferences((previous) => {
        if (axis === MpappInversionAxis.InvertX) {
          return {
            ...previous,
            invertX: !previous.invertX,
          };
        }

        return {
          ...previous,
          invertY: !previous.invertY,
        };
      });
    },
    [updateInputPreferences],
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
        connectionState: sessionStateRef.current.mode,
        latencyMs: params.latencyMs,
        failureReason: params.failureReason,
        payload: {
          ...transportLogContext,
          ...(params.payload ?? {}),
        },
        platform: Platform.OS,
        osVersion: String(Platform.Version),
      });

      await diagnosticsStoreRef.current.append(event);
      console.info("[mpapp][log]", event.eventFamily, event.actionType, {
        failureReason: event.failureReason,
        latencyMs: event.latencyMs,
      });
    },
    [transportLogContext],
  );

  useEffect(() => {
    if (platformSupport.supported) {
      return;
    }

    dispatchSessionEvent({
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
  }, [appendLog, dispatchSessionEvent, platformDescriptor, platformSupport]);

  const handleConnect = useCallback(async () => {
    if (!platformSupport.supported) {
      return;
    }

    dispatchSessionEvent({ type: MpappSessionEventType.StartPermissionCheck });

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
      dispatchSessionEvent({ type: MpappSessionEventType.PermissionDenied });
      return;
    }

    const availabilityStart = Date.now();
    const availabilityResult = await adapter.checkBluetoothAvailability();

    await appendLog({
      eventFamily: availabilityResult.ok
        ? MpappLogEventFamily.ConnectionTransition
        : MpappLogEventFamily.TransportError,
      actionType: MpappActionType.Connect,
      latencyMs: Date.now() - availabilityStart,
      failureReason: availabilityResult.ok ? null : availabilityResult.message,
      payload: {
        availabilityState: availabilityResult.availabilityState,
        gate: "post-permission-pre-pairing",
      },
    });

    if (!availabilityResult.ok) {
      const errorCode =
        availabilityResult.errorCode === MpappErrorCode.BluetoothUnavailable
          ? MpappErrorCode.BluetoothUnavailable
          : availabilityResult.errorCode;
      const message =
        errorCode === MpappErrorCode.BluetoothUnavailable
          ? getBluetoothUnavailableMessage(availabilityResult.availabilityState)
          : availabilityResult.message;

      dispatchSessionEvent({
        type: MpappSessionEventType.ConnectFailure,
        errorCode,
        message,
      });
      return;
    }

    dispatchSessionEvent({ type: MpappSessionEventType.PermissionGranted });
    dispatchSessionEvent({ type: MpappSessionEventType.StartPairing });
    dispatchSessionEvent({ type: MpappSessionEventType.StartConnecting });

    const connectStart = Date.now();
    const connectResult = await adapter.pairAndConnect();

    if (!connectResult.ok) {
      dispatchSessionEvent({
        type: MpappSessionEventType.ConnectFailure,
        errorCode: connectResult.errorCode,
        message: connectResult.message,
      });

      await appendLog({
        eventFamily: MpappLogEventFamily.ConnectionTransition,
        actionType: MpappActionType.Connect,
        latencyMs: Date.now() - connectStart,
        failureReason: connectResult.message,
        payload: {
          nativeErrorCode: connectResult.nativeErrorCode ?? null,
        },
      });
      return;
    }

    dispatchSessionEvent({ type: MpappSessionEventType.ConnectSuccess });
    await appendLog({
      eventFamily: MpappLogEventFamily.ConnectionTransition,
      actionType: MpappActionType.Connect,
      latencyMs: Date.now() - connectStart,
    });
  }, [adapter, appendLog, dispatchSessionEvent, platformSupport.supported]);

  const handleDisconnect = useCallback(async () => {
    const disconnectStart = Date.now();
    const disconnectResult = await adapter.disconnect();

    if (!disconnectResult.ok) {
      const disconnectReason = resolveDisconnectReasonFromFailure(
        disconnectResult.errorCode,
        disconnectResult.nativeErrorCode,
      );
      dispatchSessionEvent({
        type: MpappSessionEventType.DisconnectFailure,
        reason: disconnectReason,
        errorCode: disconnectResult.errorCode,
        message: disconnectResult.message,
      });
      await appendLog({
        eventFamily: MpappLogEventFamily.TransportError,
        actionType: MpappActionType.Disconnect,
        latencyMs: Date.now() - disconnectStart,
        failureReason: disconnectResult.message,
        payload: {
          disconnectReason,
          nativeErrorCode: disconnectResult.nativeErrorCode ?? null,
        },
      });
      return;
    }

    dispatchSessionEvent({
      type: MpappSessionEventType.Disconnect,
      reason: MpappDisconnectReason.UserAction,
    });
    await appendLog({
      eventFamily: MpappLogEventFamily.ConnectionTransition,
      actionType: MpappActionType.Disconnect,
      latencyMs: Date.now() - disconnectStart,
      payload: {
        disconnectReason: MpappDisconnectReason.UserAction,
      },
    });
  }, [adapter, appendLog, dispatchSessionEvent]);

  const emitSampledMove = useCallback(
    (moveEmission: MoveSamplingEmission) => {
      const sample = createPointerMoveSample(
        moveEmission.deltaX,
        moveEmission.deltaY,
        inputPreferences,
      );
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
              nativeErrorCode: sendResult.nativeErrorCode ?? null,
              ...moveEmission.diagnostics,
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
            invertX: inputPreferences.invertX,
            invertY: inputPreferences.invertY,
            ...moveEmission.diagnostics,
          },
        });
      });
    },
    [adapter, appendLog, inputPreferences],
  );

  const emitDueMoveEmission = useCallback(() => {
    if (sessionStateRef.current.mode !== MpappMode.Connected) {
      return;
    }

    const moveEmission = moveSamplingPolicyRef.current.emitWhenDue();
    if (moveEmission) {
      emitSampledMove(moveEmission);
    }

    scheduleDueMoveEmissionRef.current();
  }, [emitSampledMove]);

  const scheduleDueMoveEmission = useCallback(() => {
    clearMoveDueTimeout();
    if (sessionStateRef.current.mode !== MpappMode.Connected) {
      return;
    }

    const dueInMs = moveSamplingPolicyRef.current.timeUntilDueMs();
    if (dueInMs === null) {
      return;
    }

    moveDueTimeoutRef.current = setTimeout(() => {
      moveDueTimeoutRef.current = null;
      emitDueMoveEmission();
    }, dueInMs);
  }, [clearMoveDueTimeout, emitDueMoveEmission]);

  useEffect(() => {
    scheduleDueMoveEmissionRef.current = scheduleDueMoveEmission;
  }, [scheduleDueMoveEmission]);

  const handleMove = useCallback(
    (deltaX: number, deltaY: number) => {
      if (sessionState.mode !== MpappMode.Connected) {
        return;
      }

      const moveEmission = moveSamplingPolicyRef.current.record(deltaX, deltaY);
      if (!moveEmission) {
        scheduleDueMoveEmission();
        return;
      }

      emitSampledMove(moveEmission);
      scheduleDueMoveEmission();
    },
    [emitSampledMove, scheduleDueMoveEmission, sessionState.mode],
  );

  const handleMoveGestureEnd = useCallback(() => {
    if (sessionState.mode !== MpappMode.Connected) {
      return;
    }

    clearMoveDueTimeout();
    const moveEmission = moveSamplingPolicyRef.current.flush();
    if (!moveEmission) {
      return;
    }

    emitSampledMove(moveEmission);
  }, [clearMoveDueTimeout, emitSampledMove, sessionState.mode]);

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
              nativeErrorCode: sendResult.nativeErrorCode ?? null,
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
            Sensitivity: {inputPreferences.sensitivity.toFixed(1)}
          </Text>

          <View style={styles.preferenceControls}>
            <View style={styles.sensitivityControls}>
              <Pressable
                onPress={() => {
                  handleSensitivityUpdate(-SENSITIVITY_STEP);
                }}
                style={styles.sensitivityButton}
              >
                <Text style={styles.sensitivityButtonText}>-</Text>
              </Pressable>

              <Pressable
                onPress={() => {
                  handleSensitivityUpdate(SENSITIVITY_STEP);
                }}
                style={styles.sensitivityButton}
              >
                <Text style={styles.sensitivityButtonText}>+</Text>
              </Pressable>
            </View>

            <View style={styles.inversionControls}>
              <Pressable
                accessibilityRole="switch"
                accessibilityState={{ checked: inputPreferences.invertX }}
                onPress={() => {
                  handleInversionToggle(MpappInversionAxis.InvertX);
                }}
                style={[
                  styles.inversionButton,
                  inputPreferences.invertX ? styles.inversionButtonActive : null,
                ]}
              >
                <Text
                  style={[
                    styles.inversionButtonText,
                    inputPreferences.invertX
                      ? styles.inversionButtonTextActive
                      : null,
                  ]}
                >
                  Invert X
                </Text>
              </Pressable>

              <Pressable
                accessibilityRole="switch"
                accessibilityState={{ checked: inputPreferences.invertY }}
                onPress={() => {
                  handleInversionToggle(MpappInversionAxis.InvertY);
                }}
                style={[
                  styles.inversionButton,
                  inputPreferences.invertY ? styles.inversionButtonActive : null,
                ]}
              >
                <Text
                  style={[
                    styles.inversionButtonText,
                    inputPreferences.invertY
                      ? styles.inversionButtonTextActive
                      : null,
                  ]}
                >
                  Invert Y
                </Text>
              </Pressable>
            </View>
          </View>
        </View>

        <TouchpadSurface
          disabled={sessionState.mode !== MpappMode.Connected}
          onMove={handleMove}
          onGestureEnd={handleMoveGestureEnd}
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
  preferenceControls: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
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
  inversionControls: {
    flexDirection: "row",
    gap: 6,
  },
  inversionButton: {
    borderRadius: 8,
    borderCurve: "continuous",
    paddingHorizontal: 8,
    paddingVertical: 7,
    borderWidth: 1,
    borderColor: "#94a3b8",
    backgroundColor: "#f1f5f9",
  },
  inversionButtonActive: {
    borderColor: "#0f766e",
    backgroundColor: "#0f766e",
  },
  inversionButtonText: {
    color: "#0f172a",
    fontWeight: "700",
    fontSize: 12,
  },
  inversionButtonTextActive: {
    color: "#f8fafc",
  },
});
