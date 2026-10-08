// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { i18n } from "./localization";
import { Timestamp, TimestampMode, TimestampText } from "./timestamp-display";
import { DateFormatPreference, formatTimestampLabel, timestampInstant } from "./timestamp-format";

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
