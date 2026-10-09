// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { SidebarPreference, SidebarProblem, SidebarProvider, SidebarPreferenceNotice, memorySidebarBridge, parseSidebar, useSidebarPreference, type SidebarBridge, type SidebarSnapshot } from "./sidebar-preference";
function Controls() {
  const { snapshot, operation, select } = useSidebarPreference();
  return <><button aria-disabled={Boolean(operation || snapshot.problem)} onClick={() => select(snapshot.sidebar_preference === SidebarPreference.Expanded ? SidebarPreference.Collapsed : SidebarPreference.Expanded)}>Toggle</button><output>{snapshot.sidebar_preference}</output><SidebarPreferenceNotice /><textarea aria-label="Draft" defaultValue="retained" /></>;
}
function fixture() {
  let current: SidebarSnapshot = { revision: 1, sidebar_preference: SidebarPreference.Expanded, problem: null };
  const listeners = new Set<(value: unknown) => void>();
  const publish = (next: SidebarSnapshot) => { current = next; listeners.forEach(changed => changed(next)); };
  const bridge: SidebarBridge = { read: vi.fn(async () => current), update: vi.fn(async (sidebar_preference, revision) => { const next = { revision: revision + 1, sidebar_preference, problem: null }; publish(next); return next; }), subscribe: async changed => { listeners.add(changed); return () => { listeners.delete(changed); }; } };
  return { bridge, publish };
}
test("committed changes synchronize mounted and later windows without replacing drafts", async () => {
  const bridge = memorySidebarBridge();
  const first = render(<SidebarProvider bridge={bridge}><Controls /></SidebarProvider>);
  const second = render(<SidebarProvider bridge={bridge}><Controls /></SidebarProvider>);
  const draft = within(first.container).getByRole("textbox");
  await waitFor(() => expect(within(first.container).getByRole("button", { name: "Toggle" }).getAttribute("aria-disabled")).toBe("false"));
  act(() => draft.focus()); fireEvent.click(within(first.container).getByRole("button", { name: "Toggle" }));
  await waitFor(() => expect(within(second.container).getByText("collapsed")).toBeTruthy());
  expect(document.activeElement).toBe(draft); expect(draft).toHaveProperty("value", "retained");
  first.unmount(); second.unmount(); render(<SidebarProvider bridge={bridge}><Controls /></SidebarProvider>);
  await screen.findByText("collapsed");
});
test("pending and uncertain writes never replay; unrelated events cannot clear recovery", async () => {
  const value = fixture(); let reject!: (error: Error) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(new Promise((_, fail) => { reject = fail; }));
  render(<SidebarProvider bridge={value.bridge}><Controls /></SidebarProvider>);
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() => expect(toggle.getAttribute("aria-disabled")).toBe("false"));
  fireEvent.click(toggle); fireEvent.click(toggle); expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(SidebarPreference.Collapsed, 1);
  await act(async () => reject(new Error("private storage detail")));
  const reload = await screen.findByRole("button", { name: "Reload sidebar preference" });
  act(() => value.publish({ revision: 2, sidebar_preference: SidebarPreference.Collapsed, problem: null }));
  expect(screen.getByText("expanded")).toBeTruthy();
  fireEvent.click(toggle); fireEvent.focus(window); expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/private storage detail/)).toBeNull();
  fireEvent.click(reload); await screen.findByText("collapsed");
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});
test("old responses and contradictory same-generation events cannot overwrite verified preference", async () => {
  const value = fixture(); render(<SidebarProvider bridge={value.bridge}><Controls /></SidebarProvider>);
  await waitFor(() => expect(screen.getByRole("button", { name: "Toggle" }).getAttribute("aria-disabled")).toBe("false"));
  act(() => value.publish({ revision: 7, sidebar_preference: SidebarPreference.Collapsed, problem: null }));
  act(() => value.publish({ revision: 2, sidebar_preference: SidebarPreference.Expanded, problem: null }));
  expect(screen.getByText("collapsed")).toBeTruthy();
  act(() => value.publish({ revision: 7, sidebar_preference: SidebarPreference.Expanded, problem: null }));
  expect(screen.getByText("collapsed")).toBeTruthy(); await screen.findByRole("button", { name: "Reload sidebar preference" });
});
test("closed snapshots reject malformed documents, revisions, enums and fields", () => {
  const valid = { revision: 1, sidebar_preference: "expanded", problem: null };
  for (const value of [null, {}, { ...valid, revision: -1 }, { ...valid, revision: 1.5 }, { ...valid, revision: 2 ** 32 }, { ...valid, sidebar_preference: "other" }, { ...valid, problem: "private-path" }, { ...valid, extra: true }]) expect(() => parseSidebar(value)).toThrow();
});

test("primary+B accepts ordinary inputs on each platform while retaining all dispatch exclusions", async () => {
  const { dispatchShortcut, globalShortcutBindings, ShortcutId, ShortcutInput, ShortcutPlatform, ShortcutScope } = await import("./shortcuts");
  const { Surface } = await import("./surface");
  render(<textarea aria-label="Typing" defaultValue="draft" />);
  const input = screen.getByRole("textbox"); const run = vi.fn();
  const definitions = [{ id: ShortcutId.ToggleSidebar, scope: ShortcutScope.Global, label: "sidebar-preference.toggle" as const, bindings: globalShortcutBindings[ShortcutId.ToggleSidebar], input: ShortcutInput.Allow, run }];
  for (const platform of [ShortcutPlatform.Mac, ShortcutPlatform.Other]) {
    const primary = platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    const event = (options: KeyboardEventInit = {}) => { const value = new KeyboardEvent("keydown", { key: "b", cancelable: true, ...primary, ...options }); Object.defineProperty(value, "target", { value: input }); return value; };
    run.mockClear(); expect(dispatchShortcut(event(), definitions, Surface.Sessions, platform)).toBe(true); expect(run).toHaveBeenCalledTimes(1);
    for (const options of [{ repeat: true }, { isComposing: true }, { altKey: true }, { shiftKey: true }, { metaKey: true, ctrlKey: true }, platform === ShortcutPlatform.Mac ? { metaKey: false, ctrlKey: true } : { ctrlKey: false, metaKey: true }]) expect(dispatchShortcut(event(options), definitions, Surface.Sessions, platform)).toBe(false);
    const handled = event(); handled.preventDefault(); expect(dispatchShortcut(handled, definitions, Surface.Sessions, platform)).toBe(false);
    input.setAttribute("data-shortcuts", "passthrough"); expect(dispatchShortcut(event(), definitions, Surface.Sessions, platform)).toBe(false); input.removeAttribute("data-shortcuts");
    input.setAttribute("inert", ""); expect(dispatchShortcut(event(), definitions, Surface.Sessions, platform)).toBe(false); input.removeAttribute("inert");
    expect(run).toHaveBeenCalledTimes(1);
  }
});
