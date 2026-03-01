import { MpappMoveSamplingPolicy } from "../contracts/enums";

export const MOVE_THROTTLE_INTERVAL_MS = 16;

export type MoveSamplingDiagnostics = {
  samplingPolicy: MpappMoveSamplingPolicy;
  samplingIntervalMs: number;
  samplingWindowMs: number;
  samplingRawSampleCount: number;
  samplingCoalescedSampleCount: number;
  samplingDroppedSampleCount: number;
  samplingEmittedSampleCount: number;
};

export type MoveSamplingEmission = {
  deltaX: number;
  deltaY: number;
  diagnostics: MoveSamplingDiagnostics;
};

export type MoveSamplingPolicy = {
  record(deltaX: number, deltaY: number): MoveSamplingEmission | null;
  flush(): MoveSamplingEmission | null;
  reset(): void;
};

type CreateCoalescedMoveSamplingPolicyOptions = {
  now?: () => number;
};

export function createCoalescedMoveSamplingPolicy(
  options: CreateCoalescedMoveSamplingPolicyOptions = {},
): MoveSamplingPolicy {
  const now = options.now ?? Date.now;

  let pendingDeltaX = 0;
  let pendingDeltaY = 0;
  let pendingRawSampleCount = 0;
  let lastEmissionTimestampMs: number | null = null;

  const clearPending = () => {
    pendingDeltaX = 0;
    pendingDeltaY = 0;
    pendingRawSampleCount = 0;
  };

  const emitPending = (emissionTimestampMs: number): MoveSamplingEmission | null => {
    if (pendingRawSampleCount === 0) {
      return null;
    }

    const samplingWindowMs =
      lastEmissionTimestampMs === null
        ? 0
        : Math.max(0, emissionTimestampMs - lastEmissionTimestampMs);
    const emission: MoveSamplingEmission = {
      deltaX: pendingDeltaX,
      deltaY: pendingDeltaY,
      diagnostics: {
        samplingPolicy: MpappMoveSamplingPolicy.CoalescedThrottle,
        samplingIntervalMs: MOVE_THROTTLE_INTERVAL_MS,
        samplingWindowMs,
        samplingRawSampleCount: pendingRawSampleCount,
        samplingCoalescedSampleCount: Math.max(0, pendingRawSampleCount - 1),
        samplingDroppedSampleCount: 0,
        samplingEmittedSampleCount: 1,
      },
    };

    clearPending();
    lastEmissionTimestampMs = emissionTimestampMs;
    return emission;
  };

  return {
    record(deltaX: number, deltaY: number): MoveSamplingEmission | null {
      if (deltaX === 0 && deltaY === 0) {
        return null;
      }

      const nowMs = now();
      pendingDeltaX += deltaX;
      pendingDeltaY += deltaY;
      pendingRawSampleCount += 1;

      if (lastEmissionTimestampMs === null) {
        return emitPending(nowMs);
      }

      if (nowMs - lastEmissionTimestampMs < MOVE_THROTTLE_INTERVAL_MS) {
        return null;
      }

      return emitPending(nowMs);
    },

    flush(): MoveSamplingEmission | null {
      return emitPending(now());
    },

    reset() {
      clearPending();
      lastEmissionTimestampMs = null;
    },
  };
}
