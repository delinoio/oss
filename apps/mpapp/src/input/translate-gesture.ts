import {
  MpappClickButton,
  MpappInputAction,
  MpappMode,
} from "../contracts/enums";
import type { PointerClickSample, PointerMoveSample } from "../contracts/types";
import {
  clampInputPreferenceSensitivity,
  DEFAULT_INPUT_SENSITIVITY,
  MAX_INPUT_SENSITIVITY,
  MIN_INPUT_SENSITIVITY,
  type MpappInputPreferences,
} from "../preferences/input-preferences";

export const DEFAULT_SENSITIVITY = DEFAULT_INPUT_SENSITIVITY;
export const MIN_SENSITIVITY = MIN_INPUT_SENSITIVITY;
export const MAX_SENSITIVITY = MAX_INPUT_SENSITIVITY;

export function clampSensitivity(rawSensitivity: number): number {
  return clampInputPreferenceSensitivity(rawSensitivity);
}

export function createPointerMoveSample(
  deltaX: number,
  deltaY: number,
  preferences: MpappInputPreferences,
  timestampMs: number = Date.now(),
): PointerMoveSample {
  const normalizedSensitivity = clampSensitivity(preferences.sensitivity);
  const effectiveDeltaX = preferences.invertX ? -deltaX : deltaX;
  const effectiveDeltaY = preferences.invertY ? -deltaY : deltaY;

  return {
    actionId: MpappInputAction.Move,
    deltaX: effectiveDeltaX * normalizedSensitivity,
    deltaY: effectiveDeltaY * normalizedSensitivity,
    timestampMs,
    sensitivity: normalizedSensitivity,
  };
}

export function createPointerClickSample(
  button: MpappClickButton,
  timestampMs: number = Date.now(),
): PointerClickSample {
  if (button === MpappClickButton.Left) {
    return {
      actionId: MpappInputAction.LeftClick,
      button: MpappClickButton.Left,
      timestampMs,
    };
  }

  return {
    actionId: MpappInputAction.RightClick,
    button: MpappClickButton.Right,
    timestampMs,
  };
}

export function createConnectedClickSample(
  mode: MpappMode,
  button: MpappClickButton,
  timestampMs: number = Date.now(),
): PointerClickSample | null {
  if (mode !== MpappMode.Connected) {
    return null;
  }

  return createPointerClickSample(button, timestampMs);
}
