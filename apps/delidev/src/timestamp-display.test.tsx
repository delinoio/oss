// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { i18n } from "./localization";
import { Timestamp, TimestampMode, TimestampText } from "./timestamp-display";
import { DateFormatPreference, formatTimestampLabel, nextTimestampTransition, timestampInstant } from "./timestamp-format";

afterEach(() => { vi.useRealTimers(); });
const now = Date.parse("2026-10-08T12:00:00Z");
test.each([[0, "now"], [59, "now"], [60, "1 minute ago"], [3599, "59 minutes ago"], [3600, "1 hour ago"], [86399, "23 hours ago"]])("relative boundary at %s seconds", async (seconds, label) => {
  await i18n.changeLanguage("en");
  expect(formatTimestampLabel(new Date(now - Number(seconds) * 1000).toISOString(), { now })).toBe(label);
});
test("absolute presets, future values, strict dates and nanosecond inspection", async () => {
  await i18n.changeLanguage("en");
  const value = "2026-10-07T01:02:03.123456789+09:00";
  expect(formatTimestampLabel(value, { now, preference: DateFormatPreference.Ymd, timeZone: "UTC" })).toBe("2026-10-06, 16:02:03.123456789 UTC");
  expect(formatTimestampLabel(value, { now, preference: DateFormatPreference.Mdy, timeZone: "UTC" })).toContain("10/06/2026");
  expect(formatTimestampLabel(value, { now, preference: DateFormatPreference.Dmy, timeZone: "UTC" })).toContain("06/10/2026");
  expect(formatTimestampLabel(new Date(now + 1000).toISOString(), { now, timeZone: "UTC" })).not.toMatch(/ago|now/);
  expect(formatTimestampLabel(new Date(now - 86400000).toISOString(), { now, timeZone: "UTC" })).toContain("Oct 7, 2026");
  for (const invalid of ["2026-02-30T23:59:59Z", "2026-01-01T25:00:00Z", "2026-01-01T00:00:00+24:00", "not a date", "2026-10-08T12:00:00.1234567890Z"]) {
    expect(timestampInstant(invalid)).toBeUndefined();
    expect(formatTimestampLabel(invalid, { now })).toBe(invalid);
  }
  const view = render(<Timestamp value={value} mode={TimestampMode.Exact} />);
  expect(view.container.querySelector("time")?.dateTime).toBe(value);
  expect(view.container.querySelector("time")?.title).toBe(value);
  expect(screen.getByText(value)).toBeTruthy();
});
test("Korean relative labels retain floored units", async () => {
  await i18n.changeLanguage("ko");
  expect(formatTimestampLabel(new Date(now).toISOString(), { now })).toBe("방금");
  expect(formatTimestampLabel(new Date(now - 119000).toISOString(), { now })).toBe("1분 전");
  expect(formatTimestampLabel(new Date(now - 7199000).toISOString(), { now })).toBe("1시간 전");
});
test("mounted labels share one transition timer and stop while hidden or disposed", async () => {
  await i18n.changeLanguage("en");
  vi.useFakeTimers(); vi.setSystemTime(now);
  const visible = vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  const value = new Date(now - 59000).toISOString();
  const view = render(<><Timestamp value={value} /><Timestamp value={value} /><input aria-label="Draft" defaultValue="retained" /></>);
  expect(vi.getTimerCount()).toBe(1);
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.getAllByText("1 minute ago")).toHaveLength(2);
  visible.mockReturnValue("hidden"); fireEvent(document, new Event("visibilitychange"));
  expect(vi.getTimerCount()).toBe(0);
  act(() => vi.setSystemTime(now + 3600000));
  visible.mockReturnValue("visible"); fireEvent.focus(window);
  expect(screen.getAllByText("1 hour ago")).toHaveLength(2);
  expect(screen.getByRole("textbox")).toHaveProperty("value", "retained");
  view.unmount(); expect(vi.getTimerCount()).toBe(0); visible.mockRestore();
});
test("missing fallback and localized timestamp interpolation stay inert", async () => {
  await i18n.changeLanguage("en");
  render(<><span><Timestamp fallback="Unavailable" /></span><TimestampText id="api-verification.checked" values={{ v0: <Timestamp value="invalid <img>" /> }} /></>);
  expect(screen.getByText("Unavailable")).toBeTruthy();
  expect(screen.getByText("invalid <img>")).toBeTruthy();
  expect(document.querySelector("img")).toBeNull();
});

test("retained inactive views release presentation subscriptions and recompute on activation", async () => {
  await i18n.changeLanguage("en");
  vi.useFakeTimers(); vi.setSystemTime(now);
  const value = new Date(now - 59000).toISOString();
  const view = render(<Timestamp value={value} />);
  expect(vi.getTimerCount()).toBe(1);
  view.rerender(<Timestamp value={value} active={false} />);
  expect(vi.getTimerCount()).toBe(0);
  act(() => vi.setSystemTime(now + 61000));
  view.rerender(<Timestamp value={value} />);
  expect(screen.getByText("2 minutes ago")).toBeTruthy();
  view.unmount(); expect(vi.getTimerCount()).toBe(0);
});


test.each([
  [529200, "Resets in 6 days 3 hours", "6일 3시간 뒤 리셋"],
  [86400, "Resets in 1 day", "1일 뒤 리셋"],
  [86340, "Resets in 23 hours 59 minutes", "23시간 59분 뒤 리셋"],
  [12000, "Resets in 3 hours 20 minutes", "3시간 20분 뒤 리셋"],
  [3540, "Resets in 59 minutes", "59분 뒤 리셋"],
  [60, "Resets in 1 minute", "1분 뒤 리셋"], [59, "Resets soon", "곧 리셋"],
  [3600, "Resets in 1 hour", "1시간 뒤 리셋"],
  [3660, "Resets in 1 hour 1 minute", "1시간 1분 뒤 리셋"],
  [90000, "Resets in 1 day 1 hour", "1일 1시간 뒤 리셋"],
  [176400, "Resets in 2 days 1 hour", "2일 1시간 뒤 리셋"],
  [7320, "Resets in 2 hours 2 minutes", "2시간 2분 뒤 리셋"],
])("quota countdown floors %s seconds in both languages", async (seconds, english, korean) => {
  const value = new Date(now + Number(seconds) * 1000 + 999).toISOString();
  await i18n.changeLanguage("en");
  expect(formatTimestampLabel(value, { now, mode: TimestampMode.QuotaCountdown })).toBe(english);
  await i18n.changeLanguage("ko");
  expect(formatTimestampLabel(value, { now, mode: TimestampMode.QuotaCountdown })).toBe(korean);
});
test("countdowns compare instants across offsets and DST and retain strict fallback", async () => {
  await i18n.changeLanguage("en");
  const displayNow = Date.parse("2026-10-08T00:00:00Z");
  for (const value of ["2026-10-14T03:00:00Z", "2026-10-14T12:00:00+09:00"]) {
    expect(formatTimestampLabel(value, { now: displayNow, mode: TimestampMode.QuotaCountdown, timeZone: "America/New_York" })).toBe("Resets in 6 days 3 hours");
  }
  expect(formatTimestampLabel("2026-11-01T02:00:00-05:00", { now: Date.parse("2026-11-01T01:00:00-04:00"), mode: TimestampMode.QuotaCountdown })).toBe("Resets in 2 hours");
  for (const value of ["2026-02-30T00:00:00Z", "2026-10-08T25:00:00Z", "invalid"]) expect(formatTimestampLabel(value, { now, mode: TimestampMode.QuotaCountdown })).toBe(value);
  for (const value of [new Date(now).toISOString(), new Date(now - 60000).toISOString()]) expect(formatTimestampLabel(value, { now, mode: TimestampMode.QuotaCountdown })).toBe(formatTimestampLabel(value, { now }));
});
test("countdown boundaries use one shared timer, preserve source evidence and expire without callbacks", async () => {
  await i18n.changeLanguage("en"); vi.useFakeTimers(); vi.setSystemTime(now);
  const visible = vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  const first = new Date(now + 60000).toISOString(), second = new Date(now + 120000).toISOString();
  const request = vi.fn();
  const content = (active = true) => <><Timestamp value={first} mode={TimestampMode.QuotaCountdown} active={active} expired={value => <>Reset {value}</>} /><Timestamp value={second} mode={TimestampMode.QuotaCountdown} active={active} /><input aria-label="Retained draft" defaultValue="retained" onChange={request} /></>;
  const view = render(content());
  const input = screen.getByRole("textbox"); input.focus();
  expect(screen.getByText("Resets in 1 minute")).toBeTruthy(); expect(screen.getByText("Resets in 2 minutes")).toBeTruthy();
  expect(vi.getTimerCount()).toBe(1);
  const time = view.container.querySelector("time")!;
  expect(time.dateTime).toBe(first); expect(time.title).toBe(first); expect(time.getAttribute("aria-description")).toBe(first);
  act(() => vi.advanceTimersByTime(1)); expect(screen.getByText("Resets soon")).toBeTruthy();
  act(() => vi.advanceTimersByTime(59999)); expect(screen.getByText("now")).toBeTruthy();
  expect(request).not.toHaveBeenCalled(); expect(document.activeElement).toBe(input); expect(input).toHaveProperty("value", "retained");
  expect(time.dateTime).toBe(first);
  visible.mockReturnValue("hidden"); fireEvent(document, new Event("visibilitychange")); expect(vi.getTimerCount()).toBe(0);
  act(() => vi.setSystemTime(now + 180000)); visible.mockReturnValue("visible"); fireEvent.focus(window);
  expect(screen.getByText("2 minutes ago")).toBeTruthy(); expect(screen.getByText("1 minute ago")).toBeTruthy();
  view.rerender(content(false)); expect(vi.getTimerCount()).toBe(0);
  act(() => vi.setSystemTime(now + 240000)); view.rerender(content()); expect(screen.getByText("3 minutes ago")).toBeTruthy();
  view.unmount(); expect(vi.getTimerCount()).toBe(0); visible.mockRestore();
});
test("countdown transition scheduling crosses floored units and exact expiry without loops", () => {
  for (const remaining of [86400000, 3600000, 60000]) expect(nextTimestampTransition(now + remaining, now, TimestampMode.QuotaCountdown)).toBe(now + 1);
  expect(nextTimestampTransition(now + 60000.5, now, TimestampMode.QuotaCountdown)).toBe(now + 1.5);
  expect(nextTimestampTransition(now + 59999, now, TimestampMode.QuotaCountdown)).toBe(now + 59999);
  expect(nextTimestampTransition(now, now, TimestampMode.QuotaCountdown)).toBe(now + 60000);
});
