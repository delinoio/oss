import { Pressable, StyleSheet, Text, View } from "react-native";

type ClickControlsProps = {
  disabled: boolean;
  onLeftClick: () => void;
  onRightClick: () => void;
};

export function ClickControls({
  disabled,
  onLeftClick,
  onRightClick,
}: ClickControlsProps) {
  return (
    <View style={styles.wrapper}>
      <Pressable
        disabled={disabled}
        onPress={onLeftClick}
        style={[styles.button, disabled ? styles.buttonDisabled : undefined]}
      >
        <Text style={styles.buttonText}>Left Click</Text>
      </Pressable>

      <Pressable
        disabled={disabled}
        onPress={onRightClick}
        style={[styles.button, disabled ? styles.buttonDisabled : undefined]}
      >
        <Text style={styles.buttonText}>Right Click</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  wrapper: {
    width: "100%",
    flexDirection: "row",
    gap: 12,
  },
  button: {
    flex: 1,
    borderRadius: 14,
    borderCurve: "continuous",
    backgroundColor: "#0f172a",
    paddingVertical: 14,
    alignItems: "center",
    justifyContent: "center",
  },
  buttonDisabled: {
    backgroundColor: "#9ca3af",
  },
  buttonText: {
    color: "#f9fafb",
    fontSize: 15,
    fontWeight: "700",
  },
});
