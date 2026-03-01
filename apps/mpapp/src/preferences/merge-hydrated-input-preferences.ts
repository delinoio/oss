import type { MpappInputPreferences } from "../contracts/types";

export enum MpappInputPreferenceKey {
  Sensitivity = "sensitivity",
  InvertX = "invertX",
  InvertY = "invertY",
}

export function mergeHydratedInputPreferences(params: {
  localPreferences: MpappInputPreferences;
  savedPreferences: MpappInputPreferences;
  locallyEditedKeys: ReadonlySet<MpappInputPreferenceKey>;
}): MpappInputPreferences {
  const { localPreferences, savedPreferences, locallyEditedKeys } = params;

  if (locallyEditedKeys.size === 0) {
    return savedPreferences;
  }

  return {
    sensitivity: locallyEditedKeys.has(MpappInputPreferenceKey.Sensitivity)
      ? localPreferences.sensitivity
      : savedPreferences.sensitivity,
    invertX: locallyEditedKeys.has(MpappInputPreferenceKey.InvertX)
      ? localPreferences.invertX
      : savedPreferences.invertX,
    invertY: locallyEditedKeys.has(MpappInputPreferenceKey.InvertY)
      ? localPreferences.invertY
      : savedPreferences.invertY,
  };
}
