// SPDX-License-Identifier: Apache-2.0
import {
  EntityKind,
  UsageAccountingProfile,
  UsageCoverage,
  type GetUsageSummaryResponse,
  type Resource,
  type UsageMeasure,
} from "@delinoio/delidev-api-client";
import { uuid } from "./github-query-model";

export const usageWindowMs = 366n * 24n * 60n * 60n * 1000n;
const calendarEnd = 253402300799999n;
export type UsageRange = { fromUnixMs: bigint; untilUnixMs: bigint };
export type LifetimeMeasure = {
  known?: bigint;
  measured: bigint;
  unavailable: bigint;
};
export type LifetimeUsage = {
  input: LifetimeMeasure;
  output: LifetimeMeasure;
  ranges: Array<
    UsageRange & {
      input: LifetimeMeasure;
      output: LifetimeMeasure;
      missingExecutions: number;
      missingCompactions: number;
    }
  >;
};

export function sessionCreatedAt(resource: Resource): bigint | undefined {
  if (
    resource.kind !== EntityKind.SESSION ||
    !uuid(resource.id) ||
    resource.schemaVersion !== 1 ||
    resource.revision <= 0n ||
    resource.revision >= 1n << 63n ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(
      resource.createdAt,
    )
  )
    return;
  const [year, month, day, hour, minute, second] = resource.createdAt
    .slice(0, 19)
    .split(/[-T:]/)
    .map(Number);
  if (
    year! < 1970 ||
    month! < 1 ||
    month! > 12 ||
    day! < 1 ||
    day! > new Date(Date.UTC(year!, month!, 0)).getUTCDate() ||
    hour! > 23 ||
    minute! > 59 ||
    second! > 59
  )
    return;
  const parsed = Date.parse(resource.createdAt);
  if (
    !Number.isSafeInteger(parsed) ||
    parsed <= 0 ||
    BigInt(parsed) > calendarEnd
  )
    return;
  // Round fractional milliseconds down: this includes the creation instant
  // rather than skipping a response retained within its first millisecond.
  return BigInt(parsed);
}

function count(value: number) {
  return Number.isInteger(value) && value >= 0 && value <= 4294967295;
}
function measure(
  value: UsageMeasure | undefined,
  responses: number,
): LifetimeMeasure {
  if (
    !value ||
    !count(value.measuredResponses) ||
    !count(value.unavailableResponses) ||
    value.measuredResponses + value.unavailableResponses !== responses ||
    (value.measuredResponses === 0
      ? value.knownTotal !== ""
      : !/^(0|[1-9][0-9]*)$/.test(value.knownTotal))
  ) {
    throw new Error("Invalid response-only usage measure");
  }
  return {
    known: value.measuredResponses ? BigInt(value.knownTotal) : undefined,
    measured: BigInt(value.measuredResponses),
    unavailable: BigInt(value.unavailableResponses),
  };
}
function add(target: LifetimeMeasure, next: LifetimeMeasure) {
  if (next.known !== undefined)
    target.known = (target.known ?? 0n) + next.known;
  target.measured += next.measured;
  target.unavailable += next.unavailable;
}

/** Each read is independently observed. A completed collection is never an
 * atomic ledger snapshot. Missing-execution coverage stays per range because
 * independently read windows cannot safely deduplicate those counts. */
export async function readLifetimeUsage(
  createdAt: bigint,
  read: (range?: UsageRange) => Promise<GetUsageSummaryResponse>,
  signal: AbortSignal,
): Promise<LifetimeUsage> {
  if (createdAt <= 0n || createdAt > calendarEnd)
    throw new Error("Invalid session creation time");
  const result: LifetimeUsage = {
    input: { measured: 0n, unavailable: 0n },
    output: { measured: 0n, unavailable: 0n },
    ranges: [],
  };
  let request: UsageRange | undefined;
  for (;;) {
    signal.throwIfAborted();
    const reply = await read(request);
    signal.throwIfAborted();
    const { fromUnixMs: from, untilUnixMs: until, totals } = reply;
    if (
      from <= 0n ||
      until <= from ||
      until > calendarEnd ||
      until - from > usageWindowMs ||
      (!request && until <= createdAt) ||
      (request &&
        (from !== request.fromUnixMs || until !== request.untilUnixMs)) ||
      reply.accountingProfile !== UsageAccountingProfile.UNSPECIFIED ||
      reply.coverage !== UsageCoverage.OBSERVED_ROOT_RESPONSES ||
      !totals ||
      !count(totals.responses) ||
      !count(reply.acceptedExecutionsWithoutResponse) ||
      !count(reply.acceptedCompactionsWithoutResponse)
    ) {
      throw new Error("Invalid response-only usage range or profile");
    }
    const input = measure(totals.input, totals.responses);
    const output = measure(totals.output, totals.responses);
    result.ranges.push({
      fromUnixMs: from,
      untilUnixMs: until,
      input,
      output,
      missingExecutions: reply.acceptedExecutionsWithoutResponse,
      missingCompactions: reply.acceptedCompactionsWithoutResponse,
    });
    add(result.input, input);
    add(result.output, output);
    if (from <= createdAt) return result;
    request = {
      fromUnixMs:
        from - createdAt > usageWindowMs ? from - usageWindowMs : createdAt,
      untilUnixMs: from,
    };
  }
}
