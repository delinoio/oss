// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { DateFormatPreference, DateFormatProblem, DateFormatProvider, DateFormatSettings, parseDateFormat, type DateFormatBridge, type DateFormatSnapshot } from "./date-format";
import { Timestamp, TimestampMode } from "./timestamp-display";

function fixture() {
  let snapshot: DateFormatSnapshot = { revision: 1, date_format: DateFormatPreference.System, problem: null };
  const listeners = new Set<(value: unknown) => void>();
  const publish = (next: DateFormatSnapshot) => { snapshot = next; listeners.forEach(changed => changed(next)); };
  const bridge: DateFormatBridge = {
    read: vi.fn(async () => snapshot),
    update: vi.fn(async (date_format, revision) => {
      if (revision !== snapshot.revision) return { ...snapshot, problem: DateFormatProblem.Changed };
      const next = { revision: revision + 1, date_format, problem: null }; publish(next); return next;
    }),
    subscribe: vi.fn(async changed => { listeners.add(changed); return () => { listeners.delete(changed); }; }),
  };
  return { bridge, listeners, publish };
}

test("presets update mounted labels and examples without replacing a focused draft", async () => {
  const value = fixture();
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /><Timestamp value="2026-01-02T03:04:05Z" mode={TimestampMode.Absolute} timeZone="UTC" /><textarea aria-label="Draft" defaultValue="retained" /></DateFormatProvider>);
  const radio = screen.getByRole("radio", { name: /YYYY-MM-DD/ });
  await waitFor(() => expect(radio).not.toHaveProperty("disabled", true));
  const draft = screen.getByRole("textbox"); act(() => draft.focus());
  fireEvent.click(radio);
  await screen.findByText("2026-01-02, 03:04:05 UTC");
  expect(document.activeElement).toBe(draft);
  expect(draft).toHaveProperty("value", "retained");
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(DateFormatPreference.Ymd, 1);
});
test("committed events reach other and newly opened controllers", async () => {
  const value = fixture();
  const first = render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const second = render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const radio = within(first.container).getByRole("radio", { name: /MM\/DD\/YYYY/ });
  await waitFor(() => expect(radio).not.toHaveProperty("disabled", true));
  fireEvent.click(radio);
  await waitFor(() => expect(within(second.container).getByRole("radio", { name: /MM\/DD\/YYYY/ })).toHaveProperty("checked", true));
  first.unmount(); second.unmount();
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  await waitFor(() => expect(screen.getByRole("radio", { name: /MM\/DD\/YYYY/ })).toHaveProperty("checked", true));
});
test("old events cannot replace a newer committed preset", async () => {
  const value = fixture();
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 7, date_format: DateFormatPreference.Dmy, problem: null }));
  act(() => value.publish({ revision: 2, date_format: DateFormatPreference.Ymd, problem: null }));
  expect(screen.getByRole("radio", { name: /DD\/MM\/YYYY/ })).toHaveProperty("checked", true);
});
test("unknown writes preserve the committed preset and require explicit reinspection", async () => {
  const value = fixture(); vi.mocked(value.bridge.update).mockRejectedValueOnce(new Error("private diagnostic"));
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const radio = screen.getByRole("radio", { name: /YYYY-MM-DD/ });
  await waitFor(() => expect(radio).not.toHaveProperty("disabled", true)); fireEvent.click(radio);
  const reload = await screen.findByRole("button", { name: "Reload date format" });
  expect(screen.getByRole("radio", { name: /Language default/ })).toHaveProperty("checked", true);
  expect(radio.closest("fieldset")?.getAttribute("aria-disabled")).toBe("true");
  vi.mocked(value.bridge.read).mockClear(); fireEvent.focus(window);
  expect(value.bridge.read).not.toHaveBeenCalled();
  act(() => value.publish({ revision: 2, date_format: DateFormatPreference.Dmy, problem: null }));
  expect(screen.getByRole("radio", { name: /Language default/ })).toHaveProperty("checked", true);
  expect(screen.getByRole("button", { name: "Reload date format" })).toBeTruthy();
  fireEvent.click(reload); await waitFor(() => expect(radio).not.toHaveProperty("disabled", true));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/private diagnostic/)).toBeNull();
});
test("Settings disposal and Strict Mode retain the device-owned save", async () => {
  const value = fixture(); let finish!: (value: unknown) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
  function Opening() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(!open)}>Toggle</button>{open ? <DateFormatSettings /> : null}</>; }
  render(<StrictMode><DateFormatProvider bridge={value.bridge}><Opening /></DateFormatProvider></StrictMode>);
  const radio = screen.getByRole("radio", { name: /YYYY-MM-DD/ });
  await waitFor(() => expect(radio).not.toHaveProperty("disabled", true)); fireEvent.click(radio); fireEvent.click(screen.getByText("Toggle"));
  await act(async () => finish({ revision: 2, date_format: DateFormatPreference.Ymd, problem: null }));
  fireEvent.click(screen.getByText("Toggle"));
  expect(screen.getByRole("radio", { name: /YYYY-MM-DD/ })).toHaveProperty("checked", true);
  expect(value.listeners.size).toBe(1);
});
test("closed preference snapshots reject malformed storage and unknown fields", () => {
  const valid = { revision: 1, date_format: "system", problem: null };
  for (const value of [null, {}, { ...valid, revision: -1 }, { ...valid, revision: 1.5 }, { ...valid, date_format: "custom" }, { ...valid, problem: "path" }, { ...valid, extra: true }]) expect(() => parseDateFormat(value)).toThrow();
});
