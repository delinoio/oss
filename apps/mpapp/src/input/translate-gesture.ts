import {
  MpappClickButton,
  MpappInputAction,
  MpappMode,
} from "../contracts/enums";
import type { PointerClickSample, PointerMoveSample } from "../contracts/types";

export const DEFAULT_SENSITIVITY = 1.0;
export const MIN_SENSITIVITY = 0.5;
export const MAX_SENSITIVITY = 2.0;

export function clampSensitivity(rawSensitivity: number): number {
  if (Number.isNaN(rawSensitivity) || !Number.isFinite(rawSensitivity)) {
    return DEFAULT_SENSITIVITY;
  }

  return Math.min(MAX_SENSITIVITY, Math.max(MIN_SENSITIVITY, rawSensitivity));
}

export function createPointerMoveSample(
  deltaX: number,
  deltaY: number,
  sensitivity: number,
  timestampMs: number = Date.now(),
): PointerMoveSample {
  const normalizedSensitivity = clampSensitivity(sensitivity);

  return {
    actionId: MpappInputAction.Move,
    deltaX: deltaX * normalizedSensitivity,
    deltaY: deltaY * normalizedSensitivity,
    timestampMs,
    sensitivity: normalizedSensitivity,
  };
}

export function applyAxisInversion(
  deltaX: number,
  deltaY: number,
  invertX: boolean,
  invertY: boolean,
): {
  deltaX: number;
  deltaY: number;
} {
  return {
    deltaX: invertX ? -deltaX : deltaX,
    deltaY: invertY ? -deltaY : deltaY,
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
