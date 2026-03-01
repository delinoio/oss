import { MpappMoveSamplingPolicy } from "../contracts/enums";
import {
  MOVE_THROTTLE_INTERVAL_MS,
  createCoalescedMoveSamplingPolicy,
} from "../input/move-sampling-policy";

describe("move sampling policy", () => {
  function createPolicyHarness() {
    let nowMs = 0;

    return {
      policy: createCoalescedMoveSamplingPolicy({
        now: () => nowMs,
      }),
      advance(ms: number) {
        nowMs += ms;
      },
    };
  }

  it("emits first movement sample immediately", () => {
    const { policy } = createPolicyHarness();

    const firstEmission = policy.record(6, -3);
    expect(firstEmission).not.toBeNull();
    expect(firstEmission?.deltaX).toBe(6);
    expect(firstEmission?.deltaY).toBe(-3);
    expect(firstEmission?.diagnostics).toMatchObject({
      samplingPolicy: MpappMoveSamplingPolicy.CoalescedThrottle,
      samplingIntervalMs: MOVE_THROTTLE_INTERVAL_MS,
      samplingWindowMs: 0,
      samplingRawSampleCount: 1,
      samplingCoalescedSampleCount: 0,
      samplingDroppedSampleCount: 0,
      samplingEmittedSampleCount: 1,
    });
  });

  it("coalesces samples before the throttle interval", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(1, 1);

    advance(5);
    expect(policy.record(2, -1)).toBeNull();

    advance(5);
    expect(policy.record(3, -2)).toBeNull();
  });

  it("emits trailing coalesced movement when the interval elapses without new input", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(1, 1);

    advance(6);
    expect(policy.record(2, -1)).toBeNull();

    advance(9);
    expect(policy.emitWhenDue()).toBeNull();

    advance(1);
    const trailingEmission = policy.emitWhenDue();
    expect(trailingEmission).not.toBeNull();
    expect(trailingEmission?.deltaX).toBe(2);
    expect(trailingEmission?.deltaY).toBe(-1);
    expect(trailingEmission?.diagnostics.samplingRawSampleCount).toBe(1);
    expect(trailingEmission?.diagnostics.samplingCoalescedSampleCount).toBe(0);
  });

  it("emits exactly at the interval boundary", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(1, 1);

    advance(10);
    expect(policy.record(2, 2)).toBeNull();

    advance(6);
    const boundaryEmission = policy.record(4, -1);
    expect(boundaryEmission).not.toBeNull();
    expect(boundaryEmission?.deltaX).toBe(6);
    expect(boundaryEmission?.deltaY).toBe(1);
    expect(boundaryEmission?.diagnostics.samplingWindowMs).toBe(
      MOVE_THROTTLE_INTERVAL_MS,
    );
  });

  it("emits summed deltas for coalesced samples", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(0.5, 0.5);

    advance(3);
    expect(policy.record(4, -2)).toBeNull();

    advance(3);
    expect(policy.record(-1, 5)).toBeNull();

    advance(10);
    const coalescedEmission = policy.record(2, 1);
    expect(coalescedEmission).not.toBeNull();
    expect(coalescedEmission?.deltaX).toBe(5);
    expect(coalescedEmission?.deltaY).toBe(4);
  });

  it("flush emits pending movement before interval completion", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(1, 1);

    advance(4);
    expect(policy.record(1, 0)).toBeNull();

    advance(4);
    expect(policy.record(2, -1)).toBeNull();

    const flushedEmission = policy.flush();
    expect(flushedEmission).not.toBeNull();
    expect(flushedEmission?.deltaX).toBe(3);
    expect(flushedEmission?.deltaY).toBe(-1);
    expect(flushedEmission?.diagnostics.samplingWindowMs).toBe(8);
  });

  it("emitWhenDue returns null when there is no pending movement", () => {
    const { policy, advance } = createPolicyHarness();
    expect(policy.emitWhenDue()).toBeNull();

    policy.record(1, 1);
    advance(MOVE_THROTTLE_INTERVAL_MS);
    expect(policy.emitWhenDue()).toBeNull();
  });

  it("reset clears pending samples and timing history", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(1, 1);

    advance(5);
    expect(policy.record(2, 2)).toBeNull();

    policy.reset();
    advance(1);

    const emissionAfterReset = policy.record(3, 3);
    expect(emissionAfterReset).not.toBeNull();
    expect(emissionAfterReset?.diagnostics.samplingWindowMs).toBe(0);
    expect(emissionAfterReset?.diagnostics.samplingRawSampleCount).toBe(1);
  });

  it("reports deterministic diagnostics for burst input", () => {
    const { policy, advance } = createPolicyHarness();
    policy.record(1, 0);

    advance(4);
    expect(policy.record(1, 0)).toBeNull();

    advance(4);
    expect(policy.record(1, 0)).toBeNull();

    advance(4);
    expect(policy.record(1, 0)).toBeNull();

    advance(4);
    const burstEmission = policy.record(1, 0);
    expect(burstEmission).not.toBeNull();
    expect(burstEmission?.diagnostics).toMatchObject({
      samplingRawSampleCount: 4,
      samplingCoalescedSampleCount: 3,
      samplingDroppedSampleCount: 0,
      samplingEmittedSampleCount: 1,
    });
  });
});
