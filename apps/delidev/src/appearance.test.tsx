// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AppearanceProblem, AppearanceProvider, AppearanceSettings, Theme, parseAppearance, type AppearanceBridge, type AppearanceSnapshot } from "./appearance";

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function fixture(theme = Theme.System) {
  let snapshot: AppearanceSnapshot = { revision: 1, theme, problem: null };
  const listeners = new Set<(snapshot: unknown) => void>();
  const bridge: AppearanceBridge = {
    read: vi.fn(async () => snapshot),
    update: vi.fn(async (theme, revision) => {
      if (revision !== snapshot.revision) return { ...snapshot, problem: AppearanceProblem.Changed };
      snapshot = { revision: revision + 1, theme, problem: null };
      listeners.forEach(changed => changed(snapshot));
      return snapshot;
    }),
    subscribe: vi.fn(async (changed) => { listeners.add(changed); return () => { listeners.delete(changed); }; }),
  };
  return { bridge, listeners, publish: (value: AppearanceSnapshot) => { snapshot = value; listeners.forEach(changed => changed(value)); } };
}
function scheme(dark: boolean) {
  const listeners = new Set<() => void>();
  const media = { matches: dark, addEventListener: (_: string, listener: () => void) => listeners.add(listener), removeEventListener: (_: string, listener: () => void) => listeners.delete(listener) };
  vi.stubGlobal("matchMedia", () => media);
  return (next: boolean) => act(() => { media.matches = next; listeners.forEach(changed => changed()); });
}
afterEach(() => { vi.unstubAllGlobals(); delete document.documentElement.dataset.theme; });

test("System follows live OS changes, explicit choices ignore them and drafts survive", async () => {
  const changeScheme = scheme(true), value = fixture();
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /><textarea aria-label="Composer draft" defaultValue="unsent" /></AppearanceProvider>);
  await waitFor(() => expect((screen.getByRole("radio", { name: "System" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  changeScheme(false); expect(document.documentElement.dataset.theme).toBe(Theme.Light);
  fireEvent.click(screen.getByRole("radio", { name: "Dark" }));
  await waitFor(() => expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked).toBe(true));
  changeScheme(true); changeScheme(false);
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  expect((screen.getByRole("textbox", { name: "Composer draft" }) as HTMLTextAreaElement).value).toBe("unsent");
  expect(value.bridge.update).toHaveBeenCalledWith(Theme.Dark, 1);
});

test("a delayed initial read and old events cannot replace a newer commit", async () => {
  scheme(false); const value = fixture(), initial = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 7, theme: Theme.Dark, problem: null }));
  await act(async () => initial.resolve({ revision: 1, theme: Theme.Light, problem: null }));
  act(() => value.publish({ revision: 3, theme: Theme.Light, problem: null }));
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked).toBe(true);
});

test("failure retains the committed choice, exposes pending state and requires inspection", async () => {
  scheme(false); const value = fixture(Theme.Dark), update = deferred<unknown>();
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await waitFor(() => expect((screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("radio", { name: "Light" }));
  expect(screen.getByText("Saving theme…")).toBeTruthy();
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  await act(async () => update.reject(new Error("private diagnostic must not render")));
  expect(screen.queryByText(/private diagnostic/)).toBeNull();
  expect(screen.getAllByText(/save outcome is unknown/).length).toBeGreaterThan(0);
  expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked).toBe(true);
  expect((screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(":disabled")).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Reload appearance" }));
  await waitFor(() => expect((screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("radio", { name: "Light" }));
  await waitFor(() => expect(document.documentElement.dataset.theme).toBe(Theme.Light));
});

test("committed events update every live controller and new windows read that choice", async () => {
  scheme(false); const value = fixture();
  const first = render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  const second = render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await waitFor(() => expect((within(first.container).getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  await waitFor(() => expect((within(second.container).getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  fireEvent.click(within(first.container).getByRole("radio", { name: "Light" }));
  await waitFor(() => expect((within(second.container).getByRole("radio", { name: "Light" }) as HTMLInputElement).checked).toBe(true));
  first.unmount(); second.unmount();
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await waitFor(() => expect((screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).checked).toBe(true));
});

test("closing Settings during a save keeps the device controller alive", async () => {
  scheme(false); const value = fixture(), update = deferred<unknown>();
  vi.mocked(value.bridge.update).mockReturnValueOnce(update.promise);
  function Opening() { const [open, setOpen] = useState(true); return <><button onClick={() => setOpen(!open)}>Toggle Settings</button>{open ? <AppearanceSettings /> : null}</>; }
  render(<StrictMode><AppearanceProvider bridge={value.bridge}><Opening /></AppearanceProvider></StrictMode>);
  await waitFor(() => expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("radio", { name: "Dark" })); fireEvent.click(screen.getByText("Toggle Settings"));
  await act(async () => update.resolve({ revision: 2, theme: Theme.Dark, problem: null }));
  fireEvent.click(screen.getByText("Toggle Settings"));
  expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked).toBe(true);
  expect(value.listeners.size).toBe(1);
});

test("invalid or newer storage stays visible with System fallback and disabled writes", async () => {
  scheme(true); const value = fixture();
  vi.mocked(value.bridge.read).mockResolvedValue({ revision: 1, theme: Theme.System, problem: AppearanceProblem.UnsupportedVersion });
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await screen.findAllByText(/unsupported version/);
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
  expect((screen.getByRole("radio", { name: "Light" }) as HTMLInputElement).matches(":disabled")).toBe(true);
  expect(value.bridge.update).not.toHaveBeenCalled();
});

test("a failed older read cannot hide a newer committed event", async () => {
  scheme(false); const value = fixture(), initial = deferred<unknown>();
  vi.mocked(value.bridge.read).mockReturnValueOnce(initial.promise);
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalled());
  act(() => value.publish({ revision: 9, theme: Theme.Dark, problem: null }));
  await act(async () => initial.reject(new Error("failed delayed read")));
  expect(screen.queryByRole("button", { name: "Reload appearance" })).toBeNull();
  expect(document.documentElement.dataset.theme).toBe(Theme.Dark);
});

test("validates the complete native snapshot before adoption", () => {
  for (const value of [null, {}, { revision: -1, theme: Theme.Dark, problem: null }, { revision: 1.5, theme: Theme.Dark, problem: null }, { revision: 1, theme: "other", problem: null }, { revision: 1, theme: Theme.Dark, problem: "private" }, { revision: 1, theme: Theme.Dark, problem: null, extra: true }]) expect(() => parseAppearance(value)).toThrow();
});
