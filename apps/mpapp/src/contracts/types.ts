import {
  MpappActionType,
  MpappClickButton,
  MpappErrorCode,
  MpappInputAction,
  MpappLogEventFamily,
  MpappMode,
} from "./enums";

export type PointerMoveSample = {
  actionId: MpappInputAction.Move;
  deltaX: number;
  deltaY: number;
  timestampMs: number;
  sensitivity: number;
};

export type PointerClickSample =
  | {
      actionId: MpappInputAction.LeftClick;
      button: MpappClickButton.Left;
      timestampMs: number;
    }
  | {
      actionId: MpappInputAction.RightClick;
      button: MpappClickButton.Right;
      timestampMs: number;
    };

export type Result =
  | {
      ok: true;
    }
  | {
      ok: false;
      errorCode: MpappErrorCode;
      message: string;
      nativeErrorCode?: string;
    };

export type MpappLogEvent = {
  eventId: string;
  eventFamily: MpappLogEventFamily;
  sessionId: string;
  connectionState: MpappMode;
  actionType: MpappActionType;
  latencyMs: number;
  failureReason: string | null;
  platform: string;
  osVersion: string;
  timestampMs: number;
  payload: Record<string, unknown>;
};
