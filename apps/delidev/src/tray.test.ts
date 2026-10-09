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
    expect(result.usage).toEqual({ known_tokens: "0", incomplete: true, estimates: [] });
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
  it("preserves separate exact currency estimates and rejects malformed currency graphs", () => {
    const value = overview();
    const usage = create(GetUsageSummaryResponseSchema, { fromUnixMs: value.todayFromUnixMs, untilUnixMs: value.todayUntilUnixMs, coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES, estimates: { currencies: [
      { currency: "USD", knownAmount: "0.000000001" }, { currency: "KRW", knownAmount: "9007199254740993.000" }, { currency: "EUR", knownAmount: "" },
    ] } });
    expect(traySummary(value, false, usage, undefined).usage?.estimates).toEqual([
      { currency: "USD", known_amount: "0.000000001" }, { currency: "KRW", known_amount: "9007199254740993.000" }, { currency: "EUR", known_amount: null },
    ]);
    usage.estimates!.currencies[2].currency = "USD";
    expect(traySummary(value, false, usage, undefined).usage).toBeNull();
    usage.estimates!.currencies[2].currency = "EUR";
    usage.estimates!.currencies[0].knownAmount = "1e-9";
    expect(traySummary(value, false, usage, undefined).usage).toBeNull();
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

it.each([
  "bearer ", "BeArEr ", "sk-", "SK-", "ghp_", "GhP_", "github_pat_", "GitHub_Pat_",
  "token=", "TOKEN=", "password", "Password", "api_key", "API_KEY", "owner@",
])("masks recognized %s account syntax before native IPC without rewriting metadata", pattern => {
  const alias = `Fixture ${pattern}SECRET_SENTINEL`;
  const resource = create(ResourceSchema, { kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ alias, quota: [] }) });
  const original = resource.documentJson.slice();
  const accounts = create(ListResourcesResponseSchema, { resources: [resource] });
  const summary = traySummary(overview(), false, undefined, accounts);
  expect(summary.accounts?.entries[0]).toMatchObject({ alias: "Account alias hidden", alias_hidden: true });
  expect(JSON.stringify(summary)).not.toContain("SECRET_SENTINEL");
  expect(resource.documentJson).toEqual(original);
});

it("retains ordinary account names and lets native menu escaping handle controls", () => {
  const alias = "A&B\taccount";
  const accounts = create(ListResourcesResponseSchema, { resources: [create(ResourceSchema, { kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ alias, quota: [] }) })] });
  expect(traySummary(overview(), false, undefined, accounts).accounts?.entries[0].alias).toBe(alias);
});

it("projects only explicit subscription services and safe original quota IDs", () => {
  const original = create(ResourceSchema, { kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ type: "subscription", alias: "Claude named alias", subscription_service: "chatgpt", quota: [
    { id: "codex:primary", state: "observed", remaining: .5701, observed_at: "2026-09-27T00:01:00Z" },
    { id: "sk-SECRET_SENTINEL", state: "failed" },
    { id: "contains spaces", state: "unknown" },
  ] }) });
  const value = traySummary(overview(), false, undefined, create(ListResourcesResponseSchema, { resources: [original] }));
  expect(value.accounts?.entries[0]).toMatchObject({ subscription_service: "chatgpt", windows: [{ id: "codex:primary", remaining_basis_points: 5701 }, { state: "failed" }, { state: "unknown" }] });
  expect(value.accounts?.entries[0].windows[1].id).toBeUndefined();
  expect(value.accounts?.entries[0].windows[2].id).toBeUndefined();
  expect(JSON.stringify(value)).not.toContain("SECRET_SENTINEL");
  original.documentJson = encode({ type: "api", alias: "ChatGPT", subscription_service: "chatgpt", quota: [] });
  expect(traySummary(overview(), false, undefined, create(ListResourcesResponseSchema, { resources: [original] })).accounts?.entries[0].subscription_service).toBeUndefined();
});
