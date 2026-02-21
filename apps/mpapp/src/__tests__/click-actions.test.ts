import { MpappClickButton, MpappInputAction, MpappMode } from "../contracts/enums";
import { createConnectedClickSample } from "../input/translate-gesture";

describe("click actions", () => {
  it("emits one left click sample in connected mode", () => {
    const sample = createConnectedClickSample(
      MpappMode.Connected,
      MpappClickButton.Left,
      1700000000123,
    );

    expect(sample).not.toBeNull();
    expect(sample?.actionId).toBe(MpappInputAction.LeftClick);
    expect(sample?.timestampMs).toBe(1700000000123);
  });

  it("emits one right click sample in connected mode", () => {
    const sample = createConnectedClickSample(
      MpappMode.Connected,
      MpappClickButton.Right,
      1700000000456,
    );

    expect(sample).not.toBeNull();
    expect(sample?.actionId).toBe(MpappInputAction.RightClick);
    expect(sample?.timestampMs).toBe(1700000000456);
  });

  it("blocks click creation while disconnected", () => {
    const sample = createConnectedClickSample(MpappMode.Idle, MpappClickButton.Left);
    expect(sample).toBeNull();
  });
});
