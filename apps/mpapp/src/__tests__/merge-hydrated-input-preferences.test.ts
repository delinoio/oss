import type { MpappInputPreferences } from "../contracts/types";
import {
  mergeHydratedInputPreferences,
  MpappInputPreferenceKey,
} from "../preferences/merge-hydrated-input-preferences";

describe("merge hydrated input preferences", () => {
  const savedPreferences: MpappInputPreferences = {
    sensitivity: 1.7,
    invertX: true,
    invertY: true,
  };

  const localPreferences: MpappInputPreferences = {
    sensitivity: 1.1,
    invertX: false,
    invertY: false,
  };

  it("uses saved preferences when no local keys were edited", () => {
    const merged = mergeHydratedInputPreferences({
      localPreferences,
      savedPreferences,
      locallyEditedKeys: new Set<MpappInputPreferenceKey>(),
    });

    expect(merged).toEqual(savedPreferences);
  });

  it("preserves locally edited sensitivity while hydrating untouched toggles", () => {
    const merged = mergeHydratedInputPreferences({
      localPreferences,
      savedPreferences,
      locallyEditedKeys: new Set([MpappInputPreferenceKey.Sensitivity]),
    });

    expect(merged).toEqual({
      sensitivity: localPreferences.sensitivity,
      invertX: savedPreferences.invertX,
      invertY: savedPreferences.invertY,
    });
  });

  it("preserves locally edited invertX while hydrating other keys", () => {
    const merged = mergeHydratedInputPreferences({
      localPreferences,
      savedPreferences,
      locallyEditedKeys: new Set([MpappInputPreferenceKey.InvertX]),
    });

    expect(merged).toEqual({
      sensitivity: savedPreferences.sensitivity,
      invertX: localPreferences.invertX,
      invertY: savedPreferences.invertY,
    });
  });

  it("preserves all locally edited keys", () => {
    const merged = mergeHydratedInputPreferences({
      localPreferences,
      savedPreferences,
      locallyEditedKeys: new Set([
        MpappInputPreferenceKey.Sensitivity,
        MpappInputPreferenceKey.InvertX,
        MpappInputPreferenceKey.InvertY,
      ]),
    });

    expect(merged).toEqual(localPreferences);
  });
});
