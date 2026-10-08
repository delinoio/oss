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
  const control = screen.getByRole("combobox", { name: "Date format" });
  await waitFor(() => expect(control).not.toHaveProperty("disabled", true));
  const draft = screen.getByRole("textbox"); act(() => draft.focus());
  fireEvent.change(control, { target: { value: DateFormatPreference.Ymd } });
  await screen.findByText("2026-01-02, 03:04:05 UTC");
  expect(document.activeElement).toBe(draft);
  expect(draft).toHaveProperty("value", "retained");
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(DateFormatPreference.Ymd, 1);
});
test("committed events reach other and newly opened controllers", async () => {
  const value = fixture();
  const first = render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const second = render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const control = within(first.container).getByRole("combobox", { name: "Date format" });
  await waitFor(() => expect(control).not.toHaveProperty("disabled", true));
  fireEvent.change(control, { target: { value: DateFormatPreference.Mdy } });
  await waitFor(() => expect(within(second.container).getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.Mdy));
  first.unmount(); second.unmount();
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  await waitFor(() => expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.Mdy));
});
test("old events cannot replace a newer committed preset", async () => {
  const value = fixture();
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 7, date_format: DateFormatPreference.Dmy, problem: null }));
  act(() => value.publish({ revision: 2, date_format: DateFormatPreference.Ymd, problem: null }));
  expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.Dmy);
});
test("unknown writes preserve the committed preset and require explicit reinspection", async () => {
  const value = fixture(); vi.mocked(value.bridge.update).mockRejectedValueOnce(new Error("private diagnostic"));
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const control = screen.getByRole("combobox", { name: "Date format" });
  await waitFor(() => expect(control).not.toHaveProperty("disabled", true)); fireEvent.change(control, { target: { value: DateFormatPreference.Ymd } });
  const reload = await screen.findByRole("button", { name: "Reload date format" });
  expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.System);
  expect(control.closest("fieldset")?.getAttribute("aria-disabled")).toBe("true");
  vi.mocked(value.bridge.read).mockClear(); fireEvent.focus(window);
  expect(value.bridge.read).not.toHaveBeenCalled();
  act(() => value.publish({ revision: 2, date_format: DateFormatPreference.Dmy, problem: null }));
  expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.System);
  expect(screen.getByRole("button", { name: "Reload date format" })).toBeTruthy();
  fireEvent.click(reload); await waitFor(() => expect(control).not.toHaveProperty("disabled", true));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/private diagnostic/)).toBeNull();
});
test("Settings disposal and Strict Mode retain the device-owned save", async () => {
  const value = fixture(); let finish!: (value: unknown) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
  function Opening() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(!open)}>Toggle</button>{open ? <DateFormatSettings /> : null}</>; }
  render(<StrictMode><DateFormatProvider bridge={value.bridge}><Opening /></DateFormatProvider></StrictMode>);
  const control = screen.getByRole("combobox", { name: "Date format" });
  await waitFor(() => expect(control).not.toHaveProperty("disabled", true)); fireEvent.change(control, { target: { value: DateFormatPreference.Ymd } }); fireEvent.click(screen.getByText("Toggle"));
  await act(async () => finish({ revision: 2, date_format: DateFormatPreference.Ymd, problem: null }));
  fireEvent.click(screen.getByText("Toggle"));
  expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.Ymd);
  expect(value.listeners.size).toBe(1);
});
test("closed preference snapshots reject malformed storage and unknown fields", () => {
  const valid = { revision: 1, date_format: "system", problem: null };
  for (const value of [null, {}, { ...valid, revision: -1 }, { ...valid, revision: 1.5 }, { ...valid, date_format: "custom" }, { ...valid, problem: "path" }, { ...valid, extra: true }]) expect(() => parseDateFormat(value)).toThrow();
});

test("an unrelated event before a rejected save cannot clear original save uncertainty", async () => {
  const value = fixture();
  let reject!: (error: Error) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(new Promise((_, fail) => { reject = fail; }));
  render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const control = screen.getByRole("combobox", { name: "Date format" });
  await waitFor(() => expect(control).not.toHaveProperty("disabled", true));
  fireEvent.change(control, { target: { value: DateFormatPreference.Ymd } });
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(DateFormatPreference.Ymd, 1);
  act(() => value.publish({ revision: 2, date_format: DateFormatPreference.Dmy, problem: null }));
  await act(async () => reject(new Error("private rejected-save diagnostic")));
  const reload = await screen.findByRole("button", { name: "Reload date format" });
  expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.Dmy);
  expect(control.closest("fieldset")?.getAttribute("aria-disabled")).toBe("true");
  expect(screen.getByText("Date format is not saved. Inspect the original preference before trying again.")).toBeTruthy();
  fireEvent.change(screen.getByRole("combobox", { name: "Date format" }), { target: { value: DateFormatPreference.Mdy } });
  fireEvent.focus(window);
  act(() => value.publish({ revision: 3, date_format: DateFormatPreference.Ymd, problem: null }));
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(value.bridge.read).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("combobox", { name: "Date format" })).toHaveProperty("value", DateFormatPreference.Dmy);
  expect(screen.getByRole("button", { name: "Reload date format" })).toBeTruthy();
  expect(screen.queryByText(/private rejected-save diagnostic/)).toBeNull();
  fireEvent.click(reload);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Reload date format" })).toBeNull());
  expect(control).toHaveProperty("value", DateFormatPreference.Ymd);
  expect(value.bridge.read).toHaveBeenCalledTimes(2);
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});

test("renders exactly four ordered choices and one committed example, retaining focus during save", async () => {
  const value = fixture(); let finish!: (value: unknown) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
  const mounted = render(<DateFormatProvider bridge={value.bridge}><DateFormatSettings /></DateFormatProvider>);
  const control = screen.getByRole("combobox", { name: "Date format" });
  await waitFor(() => expect(control).not.toHaveProperty("disabled", true));
  expect(screen.queryByRole("radio")).toBeNull();
  expect(screen.getAllByRole("option").map(option => option.textContent)).toEqual(["Language default", "YYYY-MM-DD", "MM/DD/YYYY", "DD/MM/YYYY"]);
  expect(mounted.container.querySelectorAll(".date-format-example")).toHaveLength(1);
  act(() => control.focus());
  fireEvent.change(control, { target: { value: DateFormatPreference.Ymd } });
  expect(control).toHaveProperty("value", DateFormatPreference.System);
  fireEvent.change(control, { target: { value: DateFormatPreference.Mdy } });
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(DateFormatPreference.Ymd, 1);
  expect(document.activeElement).toBe(control);
  await act(async () => finish({ revision: 2, date_format: DateFormatPreference.Ymd, problem: null }));
  expect(control).toHaveProperty("value", DateFormatPreference.Ymd);
  expect(document.activeElement).toBe(control);
});
