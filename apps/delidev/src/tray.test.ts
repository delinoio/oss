import { create } from "@bufbuild/protobuf";
import { describe, expect, it, vi } from "vitest";
import { EntityKind, GetOverviewResponseSchema, GetUsageSummaryResponseSchema, ListResourcesResponseSchema, ResourceSchema, UsageCoverage } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { TrayPublisher, TrayQuotaState, traySummary, unavailableTray } from "./tray";

const overview = () => create(GetOverviewResponseSchema, { observedAt: "2026-09-27T00:02:00Z", activeSessions: 9007199254740993n, pendingInteractions: 0n, connectedWorkers: 1n, registeredWorkers: 2n, todayFromUnixMs: 1790467200000n, todayUntilUnixMs: 1790467320000n });
describe("tray read projection", () => {
  it("preserves exact known zero and large counters without claiming complete telemetry", () => {
    const value = overview();
    const usage = create(GetUsageSummaryResponseSchema, { fromUnixMs: value.todayFromUnixMs, untilUnixMs: value.todayUntilUnixMs, coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES, totals: { total: { knownTotal: "0", measuredResponses: 1 } } });
    const result = traySummary(value, false, usage, undefined);
    expect(result.overview?.active_sessions).toBe("9007199254740993");
    expect(result.overview?.pending_interactions).toBe("0");
    expect(result.usage).toEqual({ known_tokens: "0", incomplete: true });
    usage.totals!.total!.knownTotal = "90071992547409930000";
    expect(traySummary(value, false, usage, undefined).usage?.known_tokens).toBe("90071992547409930000");
    usage.untilUnixMs++;
    expect(traySummary(value, false, usage, undefined).usage).toBeNull();
    expect(traySummary(undefined, true, undefined, undefined)).toEqual(unavailableTray());
  });
  it("keeps windows separate, masks email aliases and marks expired observations stale", () => {
    const value = overview();
    const accounts = create(ListResourcesResponseSchema, { nextPageToken: "more", resources: [create(ResourceSchema, { kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ alias: "private@example.test", quota: [
      { state: "observed", remaining: 0, observed_at: "2026-09-27T00:01:00Z" },
      { state: "observed", remaining: 0.999, observed_at: "2026-09-26T23:50:00Z" },
      { state: "observed", remaining: 0.5, observed_at: "2026-09-27T00:01:00Z", reset_at: "2026-09-27T00:02:00Z" },
      { state: "unsupported", observed_at: "2026-09-27T00:01:00Z" },
    ] }) })] });
    const result = traySummary(value, false, undefined, accounts).accounts!;
    expect(result.more).toBe(true);
    expect(result.entries[0].alias).toBe("Account alias hidden");
    expect(result.entries[0].windows.map((v) => [v.state, v.remaining_basis_points])).toEqual([[TrayQuotaState.Observed, 0], [TrayQuotaState.Stale, 9990], [TrayQuotaState.Stale, 5000], [TrayQuotaState.Unsupported, null]]);
    const stale = traySummary(value, true, undefined, accounts);
    expect(stale.overview?.stale).toBe(true);
    expect(stale.accounts?.entries[0].windows[0].state).toBe(TrayQuotaState.Stale);
  });
});

it("serializes publication, coalesces refreshes and clears only the original scope on disposal", async () => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  const calls: [string, number, ReturnType<typeof unavailableTray>][] = [];
  const bridge = { begin: vi.fn(async () => "original-scope"), publish: vi.fn(async (scope: string, revision: number, value: ReturnType<typeof unavailableTray>) => { calls.push([scope, revision, value]); if (revision === 1) await held; }) };
  const publisher = new TrayPublisher(bridge, () => {});
  publisher.update(unavailableTray());
  await vi.waitFor(() => expect(calls).toHaveLength(1));
  publisher.update(traySummary(overview(), false, undefined, undefined));
  publisher.update(traySummary(overview(), true, undefined, undefined));
  expect(calls).toHaveLength(1);
  release();
  await vi.waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1][0]).toBe("original-scope");
  expect(calls[1][1]).toBe(2);
  expect(calls[1][2].overview?.stale).toBe(true);
  publisher.close();
  publisher.update(traySummary(overview(), false, undefined, undefined));
  await vi.waitFor(() => expect(calls).toHaveLength(3));
  expect(calls[2]).toEqual(["original-scope", 3, unavailableTray()]);
  expect(bridge.begin).toHaveBeenCalledTimes(1);
});

it("recovers presentation initialization on a later refresh without replaying product work", async () => {
  const problem = vi.fn();
  const bridge = { begin: vi.fn().mockRejectedValueOnce(new Error("lost presentation response")).mockResolvedValue("new-scope"), publish: vi.fn(async () => {}) };
  const publisher = new TrayPublisher(bridge, problem);
  publisher.update(unavailableTray());
  await vi.waitFor(() => expect(problem).toHaveBeenCalledWith(true));
  publisher.update(traySummary(overview(), false, undefined, undefined));
  await vi.waitFor(() => expect(bridge.publish).toHaveBeenCalledOnce());
  expect(bridge.publish.mock.calls[0]?.length).toBe(3);
  publisher.close();
});
