import { useMemo, useRef } from "react";
import {
  PanResponder,
  Pressable,
  StyleSheet,
  Text,
  View,
  type GestureResponderEvent,
  type PanResponderGestureState,
} from "react-native";

type TouchpadSurfaceProps = {
  disabled: boolean;
  onMove: (deltaX: number, deltaY: number) => void;
};

export function TouchpadSurface({ disabled, onMove }: TouchpadSurfaceProps) {
  const previousDxRef = useRef(0);
  const previousDyRef = useRef(0);

  const resetDeltaAccumulator = () => {
    previousDxRef.current = 0;
    previousDyRef.current = 0;
  };

  const handleMove = (
    _event: GestureResponderEvent,
    gestureState: PanResponderGestureState,
  ) => {
    if (disabled) {
      return;
    }

    const deltaX = gestureState.dx - previousDxRef.current;
    const deltaY = gestureState.dy - previousDyRef.current;
    previousDxRef.current = gestureState.dx;
    previousDyRef.current = gestureState.dy;

    if (deltaX === 0 && deltaY === 0) {
      return;
    }

    onMove(deltaX, deltaY);
  };

  const panResponder = useMemo(
    () =>
      PanResponder.create({
        onStartShouldSetPanResponder: () => !disabled,
        onMoveShouldSetPanResponder: () => !disabled,
        onPanResponderGrant: resetDeltaAccumulator,
        onPanResponderMove: handleMove,
        onPanResponderRelease: resetDeltaAccumulator,
        onPanResponderTerminate: resetDeltaAccumulator,
      }),
    [disabled],
  );

  return (
    <View style={styles.wrapper}>
      <Pressable
        disabled={disabled}
        style={[styles.surface, disabled ? styles.surfaceDisabled : undefined]}
        {...panResponder.panHandlers}
      >
        <Text style={styles.title}>Touchpad</Text>
        <Text style={styles.caption}>
          Drag in this area to emit cursor movement.
        </Text>
        {disabled ? (
          <Text style={styles.warning}>
            Connect first to enable pointer movement.
          </Text>
        ) : null}
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  wrapper: {
    width: "100%",
  },
  surface: {
    width: "100%",
    minHeight: 280,
    borderRadius: 20,
    borderCurve: "continuous",
    borderWidth: 1,
    borderColor: "#d1d5db",
    backgroundColor: "#f8fafc",
    padding: 16,
    gap: 8,
    justifyContent: "flex-start",
  },
  surfaceDisabled: {
    backgroundColor: "#f3f4f6",
  },
  title: {
    fontSize: 18,
    fontWeight: "700",
    color: "#111827",
  },
  caption: {
    fontSize: 14,
    color: "#4b5563",
  },
  warning: {
    marginTop: 6,
    color: "#b91c1c",
    fontWeight: "600",
  },
});
