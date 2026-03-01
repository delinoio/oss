import { MpappClickButton, MpappInputAction } from "../contracts/enums";
import {
  clampSensitivity,
  createPointerClickSample,
  createPointerMoveSample,
} from "../input/translate-gesture";
import { DEFAULT_MPAPP_INPUT_PREFERENCES } from "../preferences/input-preferences";

describe("translate gesture", () => {
  it("creates movement sample with sensitivity and timestamp", () => {
    const sample = createPointerMoveSample(
      10,
      -5,
      {
        ...DEFAULT_MPAPP_INPUT_PREFERENCES,
        sensitivity: 1.2,
      },
      1700000000000,
    );

    expect(sample.actionId).toBe(MpappInputAction.Move);
    expect(sample.deltaX).toBeCloseTo(12);
    expect(sample.deltaY).toBeCloseTo(-6);
    expect(sample.timestampMs).toBe(1700000000000);
    expect(sample.sensitivity).toBeCloseTo(1.2);
  });

  it("flips x-axis when invertX is enabled", () => {
    const sample = createPointerMoveSample(
      10,
      5,
      {
        ...DEFAULT_MPAPP_INPUT_PREFERENCES,
        invertX: true,
      },
      1700000000000,
    );

    expect(sample.deltaX).toBeCloseTo(-10);
    expect(sample.deltaY).toBeCloseTo(5);
  });

  it("flips y-axis when invertY is enabled", () => {
    const sample = createPointerMoveSample(
      10,
      5,
      {
        ...DEFAULT_MPAPP_INPUT_PREFERENCES,
        invertY: true,
      },
      1700000000000,
    );

    expect(sample.deltaX).toBeCloseTo(10);
    expect(sample.deltaY).toBeCloseTo(-5);
  });

  it("flips both axes when both inversion flags are enabled", () => {
    const sample = createPointerMoveSample(
      10,
      -5,
      {
        ...DEFAULT_MPAPP_INPUT_PREFERENCES,
        invertX: true,
        invertY: true,
      },
      1700000000000,
    );

    expect(sample.deltaX).toBeCloseTo(-10);
    expect(sample.deltaY).toBeCloseTo(5);
  });

  it("clamps sensitivity before applying inversion and scaling", () => {
    const sample = createPointerMoveSample(
      10,
      5,
      {
        ...DEFAULT_MPAPP_INPUT_PREFERENCES,
        sensitivity: 5,
        invertY: true,
      },
      1700000000000,
    );

    expect(clampSensitivity(5)).toBe(2);
    expect(sample.deltaX).toBeCloseTo(20);
    expect(sample.deltaY).toBeCloseTo(-10);
    expect(sample.sensitivity).toBe(2);
  });

  it("creates left and right click samples", () => {
    const left = createPointerClickSample(MpappClickButton.Left, 1);
    const right = createPointerClickSample(MpappClickButton.Right, 2);

    expect(left.actionId).toBe(MpappInputAction.LeftClick);
    expect(right.actionId).toBe(MpappInputAction.RightClick);
  });
});
