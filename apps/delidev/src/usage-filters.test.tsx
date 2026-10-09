// SPDX-License-Identifier: Apache-2.0
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SubscriptionServiceIdentity } from "@delinoio/delidev-api-client";
import { UsageRangePreset, useUsageFilters } from "./usage-filters";
import { localDateTimeToUnixMs } from "./usage-time";

const deviceZone = vi.hoisted(() => ({ value: "UTC" }));
vi.mock("./usage-time", async (original) => ({ ...await original<typeof import("./usage-time")>(), detectDeviceTimeZone: () => deviceZone.value }));
afterEach(() => { vi.useRealTimers(); deviceZone.value = "UTC"; });
function controller() {
  vi.useFakeTimers();
  return renderHook(({ active }) => useUsageFilters(active), { initialProps: { active: true } });
}
function dates(result: ReturnType<typeof controller>["result"], from = "2026-09-01T10:00", until = "2026-09-02T10:00") {
  act(() => { result.current.change("from", from); result.current.change("until", until); });
}

it("applies selections atomically and only debounces dates for 300ms after the last edit", () => {
  const { result } = controller();
  act(() => result.current.change("accountId", "account"));
  expect(result.current.selection.accountId).toBe("account");
  dates(result);
  act(() => vi.advanceTimersByTime(299));
  expect(result.current.selection.fromUnixMs).toBe(0n);
  act(() => result.current.change("until", "2026-09-03T10:00"));
  act(() => vi.advanceTimersByTime(299));
  expect(result.current.selection.fromUnixMs).toBe(0n);
  act(() => vi.advanceTimersByTime(1));
  expect(result.current.selection.untilUnixMs).toBe(localDateTimeToUnixMs("2026-09-03T10:00", result.current.selection.timeZone));
  expect(result.current.pending).toBe(false);
  act(() => result.current.edit({ providerId: "provider", subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED }));
  expect(result.current.selection).toMatchObject({ providerId: "provider", subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED });
  act(() => result.current.edit({ providerId: "", subscriptionService: SubscriptionServiceIdentity.CHATGPT }));
  expect(result.current.selection).toMatchObject({ providerId: "", subscriptionService: SubscriptionServiceIdentity.CHATGPT });
  act(() => result.current.change("projectId", "project"));
  act(() => result.current.edit({ projectId: "", generalChat: true }));
  expect(result.current.selection).toMatchObject({ projectId: "", generalChat: true });
});

it("selection during a date wait applies the latest complete snapshot immediately", () => {
  const { result } = controller(); dates(result);
  act(() => result.current.change("sessionId", "session"));
  const selection = result.current.selection;
  expect(selection.sessionId).toBe("session");
  expect(selection.fromUnixMs).not.toBe(0n);
  act(() => vi.advanceTimersByTime(600));
  expect(result.current.selection).toBe(selection);
});

it.each([
  ["2026-09-02T10:00", "2026-09-01T10:00"],
  ["2026-09-01T10:00", "2026-09-01T10:00"],
  ["2025-01-01T10:00", "2026-09-01T10:00"],
  ["1970-01-01T00:00", "1970-01-02T00:00"],
])("retains the valid selection for invalid range %s–%s and applies its correction", (from, until) => {
  const { result } = controller();
  act(() => result.current.change("nativeId", "model"));
  const before = result.current.selection;
  dates(result, from, until);
  expect(result.current.invalid).toBe(false);
  act(() => vi.advanceTimersByTime(300));
  expect(result.current.invalid).toBe(true);
  expect(result.current.selection).toBe(before);
  act(() => result.current.change("accountId", "new-account"));
  expect(result.current.selection).toBe(before);
  expect(result.current.draft.accountId).toBe("new-account");
  dates(result);
  act(() => vi.advanceTimersByTime(300));
  expect(result.current.invalid).toBe(false);
  expect(result.current.selection).toMatchObject({ accountId: "new-account", nativeId: "model" });
});

it("Reset cancels old dates, clears errors and keeps default endpoints", () => {
  const { result } = controller(); dates(result);
  act(() => result.current.reset());
  const reset = result.current.selection;
  act(() => vi.advanceTimersByTime(600));
  expect(result.current.selection).toBe(reset);
  expect(reset).toMatchObject({ fromUnixMs: 0n, untilUnixMs: 0n, accountId: "" });
  expect(result.current.invalid).toBe(false);
  expect(result.current.pending).toBe(false);
  dates(result, "2026-09-02T10:00", "2026-09-01T10:00");
  act(() => vi.advanceTimersByTime(300));
  expect(result.current.invalid).toBe(true);
  act(() => result.current.reset());
  expect(result.current.invalid).toBe(false);
});

it("cancels dates while inactive, resumes them on return and disposes the timer on unmount", () => {
  const { result, rerender, unmount } = controller(); dates(result);
  rerender({ active: false });
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current.selection.fromUnixMs).toBe(0n);
  expect(result.current.draft.from).toBe("2026-09-01T10:00");
  rerender({ active: true });
  act(() => vi.advanceTimersByTime(299));
  expect(result.current.selection.fromUnixMs).toBe(0n);
  act(() => vi.advanceTimersByTime(1));
  expect(result.current.selection.fromUnixMs).not.toBe(0n);
  dates(result, "2026-09-03T10:00", "2026-09-04T10:00");
  unmount();
  expect(vi.getTimerCount()).toBe(0);
});

it("preserves exact entry instants and zone across selections, changing only the edited endpoint", () => {
  vi.useFakeTimers();
  deviceZone.value = "America/New_York";
  // The later fold instant has the same wall time as the earlier occurrence.
  const entry = { key: "one", accountId: "account", fromUnixMs: BigInt(Date.parse("2026-11-01T06:30:00.123Z")), untilUnixMs: BigInt(Date.parse("2026-11-02T06:30:00.789Z")) };
  const { result, rerender } = renderHook(({ value, active }) => useUsageFilters(active, value), { initialProps: { value: entry, active: true } });
  deviceZone.value = "Asia/Seoul";
  act(() => result.current.change("nativeId", "model"));
  expect(result.current.selection).toMatchObject({ fromUnixMs: entry.fromUnixMs, untilUnixMs: entry.untilUnixMs, timeZone: "America/New_York" });
  act(() => result.current.change("until", "2026-11-03T01:30"));
  act(() => vi.advanceTimersByTime(300));
  expect(result.current.selection.fromUnixMs).toBe(entry.fromUnixMs);
  expect(result.current.selection.untilUnixMs).toBe(BigInt(Date.parse("2026-11-03T06:30:00Z")));
  rerender({ value: { ...entry }, active: false }); rerender({ value: { ...entry }, active: true });
  expect(result.current.selection.nativeId).toBe("model");
  act(() => result.current.change("from", "2026-11-02T01:30"));
  rerender({ value: { ...entry, key: "two", accountId: "next" }, active: true });
  act(() => vi.advanceTimersByTime(600));
  expect(result.current.selection).toMatchObject({ accountId: "next", fromUnixMs: entry.fromUnixMs, untilUnixMs: entry.untilUnixMs, nativeId: "" });
});

it("rejects a DST gap without disturbing the original selection", () => {
  deviceZone.value = "America/New_York";
  const { result } = controller(); const before = result.current.selection;
  dates(result, "2026-03-08T02:30", "2026-03-09T02:30");
  act(() => vi.advanceTimersByTime(300));
  expect(result.current.invalid).toBe(true);
  expect(result.current.selection).toBe(before);
});


it.each([[UsageRangePreset.Hours24, 1n], [UsageRangePreset.Days7, 7n], [UsageRangePreset.Days30, 30n]])("captures %s once with exact half-open UTC endpoints and all draft filters", (preset, days) => {
  const { result } = controller();
  vi.setSystemTime(new Date("2026-10-09T03:00:00.123Z"));
  dates(result, "2026-09-02T10:00", "2026-09-01T10:00");
  act(() => vi.advanceTimersByTime(300));
  act(() => result.current.edit({ sessionId: "session", projectId: "project", accountId: "account", providerId: "provider", subscriptionService: SubscriptionServiceIdentity.CHATGPT, nativeId: "model", generalChat: true }));
  expect(result.current.invalid).toBe(true);
  vi.setSystemTime(new Date("2026-10-09T03:00:00.123Z"));
  const clock = vi.spyOn(Date, "now");
  act(() => result.current.selectPreset(preset));
  expect(clock).toHaveBeenCalledTimes(1);
  clock.mockRestore();
  const until = BigInt(Date.parse("2026-10-09T03:00:00.123Z"));
  expect(result.current.selection).toMatchObject({ fromUnixMs: until - days * 86_400_000n, untilUnixMs: until, sessionId: "session", projectId: "project", accountId: "account", providerId: "provider", subscriptionService: SubscriptionServiceIdentity.CHATGPT, nativeId: "model", generalChat: true, timeZone: "UTC" });
  expect(result.current).toMatchObject({ preset, invalid: false, pending: false });
  const selection = result.current.selection;
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current.selection).toBe(selection);
});

it("supersedes pending dates, retains its indicator through filters/navigation, and recaptures only on selection", () => {
  const { result, rerender } = controller();
  vi.setSystemTime(new Date("2026-10-09T03:00:00.123Z"));
  dates(result);
  act(() => result.current.selectPreset(UsageRangePreset.Days7));
  const first = result.current.selection;
  act(() => vi.advanceTimersByTime(600));
  expect(result.current.selection).toBe(first);
  act(() => result.current.change("nativeId", "model"));
  expect(result.current.preset).toBe(UsageRangePreset.Days7);
  expect(result.current.selection.untilUnixMs).toBe(first.untilUnixMs);
  rerender({ active: false }); rerender({ active: true });
  expect(result.current.preset).toBe(UsageRangePreset.Days7);
  act(() => result.current.selectPreset(UsageRangePreset.Days7));
  expect(result.current.selection.untilUnixMs).toBe(first.untilUnixMs + 600n);
  act(() => result.current.change("until", "2026-10-10T03:00"));
  expect(result.current.preset).toBeUndefined();
  const last = result.current.selection;
  act(() => vi.advanceTimersByTime(299));
  expect(result.current.selection).toBe(last);
  act(() => vi.advanceTimersByTime(1));
  expect(result.current.selection.untilUnixMs).not.toBe(last.untilUnixMs);
  act(() => result.current.selectPreset(UsageRangePreset.Hours24));
  act(() => result.current.reset());
  expect(result.current.preset).toBeUndefined();
  expect(result.current.selection).toMatchObject({ fromUnixMs: 0n, untilUnixMs: 0n });
});

it.each(["2026-11-01T06:30:00.123Z", "2026-11-02T06:30:00.123Z"])("retains later-fold preset milliseconds at %s and pinned timezone across non-date edits and consumes new entries once", clock => {
  vi.useFakeTimers(); deviceZone.value = "America/New_York";
  vi.setSystemTime(new Date(clock));
  const entry = { key: "one", accountId: "account", fromUnixMs: 1n, untilUnixMs: 2n };
  const { result, rerender } = renderHook(({ value }) => useUsageFilters(true, value), { initialProps: { value: entry } });
  act(() => result.current.selectPreset(UsageRangePreset.Hours24));
  const first = result.current.selection;
  expect(clock.includes("11-01") ? result.current.draft.until : result.current.draft.from).toBe("2026-11-01T01:30:00.123");
  deviceZone.value = "Asia/Seoul";
  act(() => result.current.change("accountId", "other"));
  expect(result.current.selection).toMatchObject({ untilUnixMs: first.untilUnixMs, fromUnixMs: first.fromUnixMs, timeZone: "America/New_York" });
  rerender({ value: { ...entry } });
  expect(result.current.preset).toBe(UsageRangePreset.Hours24);
  rerender({ value: { ...entry, key: "two" } });
  expect(result.current.preset).toBeUndefined();
  expect(result.current.selection).toMatchObject({ accountId: "account", fromUnixMs: 1n, untilUnixMs: 2n, timeZone: "Asia/Seoul" });
});

it.each([
  [{providerId:"original-provider"},{providerId:""}],
  [{providerId:"original-provider"},{providerId:"another-provider"}],
  [{subscriptionService:SubscriptionServiceIdentity.CHATGPT},{subscriptionService:SubscriptionServiceIdentity.UNSPECIFIED}],
  [{subscriptionService:SubscriptionServiceIdentity.CHATGPT},{subscriptionService:SubscriptionServiceIdentity.CLAUDE}],
])("clears the applied native ID when its original source changes from %o to %o", (original, next) => {
  const {result}=controller(); act(()=>result.current.edit(original)); act(()=>result.current.change("nativeId","Exact/Native"));
  expect(result.current.selection.nativeId).toBe("Exact/Native");
  act(()=>result.current.edit(next)); expect(result.current.draft.nativeId).toBe(""); expect(result.current.selection.nativeId).toBe("");
  expect(result.current.selection).toMatchObject(next); expect(result.current.invalid).toBe(false);
});
