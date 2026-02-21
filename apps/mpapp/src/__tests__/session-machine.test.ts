import { MpappErrorCode, MpappMode } from "../contracts/enums";
import {
  INITIAL_SESSION_STATE,
  MpappSessionEventType,
  reduceSessionState,
  type MpappSessionEvent,
} from "../state/session-machine";

describe("session machine", () => {
  it("follows successful connection transition order", () => {
    const events: MpappSessionEvent[] = [
      { type: MpappSessionEventType.StartPermissionCheck },
      { type: MpappSessionEventType.PermissionGranted },
      { type: MpappSessionEventType.StartPairing },
      { type: MpappSessionEventType.StartConnecting },
      { type: MpappSessionEventType.ConnectSuccess },
    ];

    const finalState = events.reduce(reduceSessionState, INITIAL_SESSION_STATE);
    expect(finalState.mode).toBe(MpappMode.Connected);
    expect(finalState.errorCode).toBeNull();
  });

  it("maps permission denied into error state", () => {
    const state = reduceSessionState(INITIAL_SESSION_STATE, {
      type: MpappSessionEventType.PermissionDenied,
    });

    expect(state.mode).toBe(MpappMode.Error);
    expect(state.errorCode).toBe(MpappErrorCode.PermissionDenied);
  });

  it("maps connect failure into error state", () => {
    const state = reduceSessionState(INITIAL_SESSION_STATE, {
      type: MpappSessionEventType.ConnectFailure,
      errorCode: MpappErrorCode.TransportFailure,
      message: "transport down",
    });

    expect(state.mode).toBe(MpappMode.Error);
    expect(state.errorCode).toBe(MpappErrorCode.TransportFailure);
    expect(state.errorMessage).toBe("transport down");
  });
});
