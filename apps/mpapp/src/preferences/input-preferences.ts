export const DEFAULT_INPUT_SENSITIVITY = 1.0;
export const MIN_INPUT_SENSITIVITY = 0.5;
export const MAX_INPUT_SENSITIVITY = 2.0;

export type MpappInputPreferences = {
  sensitivity: number;
  invertX: boolean;
  invertY: boolean;
};

export const DEFAULT_MPAPP_INPUT_PREFERENCES: MpappInputPreferences = {
  sensitivity: DEFAULT_INPUT_SENSITIVITY,
  invertX: false,
  invertY: false,
};

export function clampInputPreferenceSensitivity(
  rawSensitivity: number,
): number {
  if (Number.isNaN(rawSensitivity) || !Number.isFinite(rawSensitivity)) {
    return DEFAULT_INPUT_SENSITIVITY;
  }

  return Math.min(
    MAX_INPUT_SENSITIVITY,
    Math.max(MIN_INPUT_SENSITIVITY, rawSensitivity),
  );
}

export function normalizeInputPreferences(
  rawPreferences: Partial<MpappInputPreferences> | null | undefined,
): MpappInputPreferences {
  return {
    sensitivity: clampInputPreferenceSensitivity(
      rawPreferences?.sensitivity ?? DEFAULT_MPAPP_INPUT_PREFERENCES.sensitivity,
    ),
    invertX:
      typeof rawPreferences?.invertX === "boolean"
        ? rawPreferences.invertX
        : DEFAULT_MPAPP_INPUT_PREFERENCES.invertX,
    invertY:
      typeof rawPreferences?.invertY === "boolean"
        ? rawPreferences.invertY
        : DEFAULT_MPAPP_INPUT_PREFERENCES.invertY,
  };
}
