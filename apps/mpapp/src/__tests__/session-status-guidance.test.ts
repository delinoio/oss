import {
  MpappConnectionEvent,
  MpappDisconnectReason,
  MpappMode,
} from "../contracts/enums";
import {
  readableDisconnectReason,
  reconnectGuidanceForState,
} from "../components/session-status";

describe("session status reconnect guidance", () => {
  it("does not return guidance when disconnect reason is missing", () => {
    expect(
      reconnectGuidanceForState({
        mode: MpappMode.Idle,
        lastConnectionEvent: MpappConnectionEvent.Disconnect,
        lastDisconnectReason: null,
      }),
    ).toBeNull();
  });

  it("does not return guidance when last connection event is unrelated", () => {
    expect(
      reconnectGuidanceForState({
        mode: MpappMode.Error,
        lastConnectionEvent: MpappConnectionEvent.ConnectFailure,
        lastDisconnectReason: MpappDisconnectReason.TransportLost,
      }),
    ).toBeNull();
  });

  it("returns idle guidance for user-action disconnect", () => {
    expect(
      reconnectGuidanceForState({
        mode: MpappMode.Idle,
        lastConnectionEvent: MpappConnectionEvent.Disconnect,
        lastDisconnectReason: MpappDisconnectReason.UserAction,
      }),
    ).toBe(
      "Disconnect was user initiated. Tap Pair and Connect when you are ready to reconnect.",
    );
  });

  it("returns error guidance for transport-lost disconnect failure", () => {
    expect(
      reconnectGuidanceForState({
        mode: MpappMode.Error,
        lastConnectionEvent: MpappConnectionEvent.DisconnectFailure,
        lastDisconnectReason: MpappDisconnectReason.TransportLost,
      }),
    ).toBe(
      "Connection dropped while disconnecting. Check Bluetooth and host availability, then tap Pair and Connect.",
    );
  });

  it("returns expected reason labels", () => {
    expect(readableDisconnectReason(MpappDisconnectReason.PermissionRevoked)).toBe(
      "Permission Revoked",
    );
    expect(readableDisconnectReason(MpappDisconnectReason.Unknown)).toBe("Unknown");
  });
});
