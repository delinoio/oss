import { MpappClickButton, MpappInputAction } from "../contracts/enums";
import {
  createPointerClickSample,
  createPointerMoveSample,
} from "../input/translate-gesture";

describe("translate gesture", () => {
  it("creates movement sample with sensitivity and timestamp", () => {
    const sample = createPointerMoveSample(10, -5, 1.2, 1700000000000);

    expect(sample.actionId).toBe(MpappInputAction.Move);
    expect(sample.deltaX).toBeCloseTo(12);
    expect(sample.deltaY).toBeCloseTo(-6);
    expect(sample.timestampMs).toBe(1700000000000);
    expect(sample.sensitivity).toBeCloseTo(1.2);
  });

  it("creates left and right click samples", () => {
    const left = createPointerClickSample(MpappClickButton.Left, 1);
    const right = createPointerClickSample(MpappClickButton.Right, 2);

    expect(left.actionId).toBe(MpappInputAction.LeftClick);
    expect(right.actionId).toBe(MpappInputAction.RightClick);
  });
});
