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
  expect(value.bridge.update).toHaveBeenCalledWith(Theme.Dark, 1, expect.any(Object));
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

test("reload restores a failed event subscription before enabling writes", async () => {
  scheme(false); const value = fixture();
  vi.mocked(value.bridge.subscribe).mockRejectedValueOnce(new Error("event subscription unavailable"));
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  const reload = await screen.findByRole("button", { name: "Reload appearance" });
  // The initial fallback also renders Reload while subscription is pending.
  // Retry only after that first failed attempt has released its operation guard.
  await waitFor(() => expect((reload as HTMLButtonElement).disabled).toBe(false));
  expect(value.bridge.read).not.toHaveBeenCalled();
  expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(":disabled")).toBe(true);
  fireEvent.click(reload);
  await waitFor(() => expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  expect(value.listeners.size).toBe(1);
  act(() => value.publish({ revision: 2, theme: Theme.Dark, problem: null }));
  expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).checked).toBe(true);
});

test("foreground inspection reconciles a missed event without clearing uncertainty", async () => {
  scheme(false); const value = fixture();
  render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings /></AppearanceProvider>);
  await waitFor(() => expect((screen.getByRole("radio", { name: "Dark" }) as HTMLInputElement).matches(":disabled")).toBe(false));
  vi.mocked(value.bridge.read).mockResolvedValue({ revision: 3, theme: Theme.Dark, problem: null });
  fireEvent.focus(window);
  await waitFor(() => expect(document.documentElement.dataset.theme).toBe(Theme.Dark));
  vi.mocked(value.bridge.update).mockRejectedValueOnce(new Error("uncertain delivery"));
  fireEvent.click(screen.getByRole("radio", { name: "Light" }));
  await screen.findByRole("button", { name: "Reload appearance" });
  vi.mocked(value.bridge.read).mockClear();
  fireEvent.focus(window);
  expect(value.bridge.read).not.toHaveBeenCalled();
});


test("v2 validates custom theme imports and preserves immutable defaults", async () => {
 const {defaultPreferences,parsePreferences,parseThemeFile,palettes}=await import("./appearance-preferences");
 const original=defaultPreferences();expect(parsePreferences(original)).toEqual(original);
 const theme={version:1,name:"Fixture",...structuredClone(palettes.default)};
 expect(parseThemeFile(JSON.stringify(theme))).toEqual(theme);
 for(const value of [{...theme,script:"alert(1)"},{...theme,light:{...theme.light,text:"#FFFFFF"}},{...theme,light:{...theme.light,accent:"url(private)"}},{...theme,name:"x".repeat(81)}])expect(()=>parseThemeFile(JSON.stringify(value))).toThrow();
 expect(()=>parseThemeFile(" ".repeat(32769))).toThrow();
 expect(()=>parsePreferences({...original,light_palette:"foreign"})).toThrow();
});

test("v2 ordinary choices autosave full snapshots without replacing a draft", async () => {
 const {defaultPreferences}=await import("./appearance-preferences");scheme(false);const value=fixture();
 vi.mocked(value.bridge.read).mockResolvedValue({revision:1,theme:Theme.System,problem:null,preferences:defaultPreferences()});
 vi.mocked(value.bridge.update).mockImplementation(async(theme,revision,preferences)=>({theme,revision:revision+1,problem:null,preferences}));
 render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings/><textarea aria-label="Persistent draft" defaultValue="original"/></AppearanceProvider>);
 const density=await screen.findByRole("combobox",{name:"Display density"});await waitFor(()=>expect((density as HTMLSelectElement).matches(":disabled")).toBe(false));
 fireEvent.change(density,{target:{value:"compact"}});await waitFor(()=>expect(document.documentElement.dataset.density).toBe("compact"));
 expect((screen.getByRole("textbox",{name:"Persistent draft"}) as HTMLTextAreaElement).value).toBe("original");
 expect((screen.getByRole("checkbox",{name:"Markdown"}) as HTMLInputElement).matches(":disabled")).toBe(true);
});


test("custom theme confirmation retains a draft on concurrent appearance commits", async()=>{
 const {defaultPreferences}=await import("./appearance-preferences");scheme(false);const value=fixture();
 const preferences=defaultPreferences();vi.mocked(value.bridge.read).mockResolvedValue({revision:1,theme:Theme.System,problem:null,preferences});
 render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings/></AppearanceProvider>);
 const duplicate=await screen.findByRole("button",{name:"Duplicate theme"});await waitFor(()=>expect((duplicate as HTMLButtonElement).disabled).toBe(false));fireEvent.click(duplicate);
 const name=screen.getByRole("textbox",{name:"Theme name"});fireEvent.change(name,{target:{value:"Retained custom draft"}});
 expect(value.bridge.update).not.toHaveBeenCalled();
 act(()=>value.publish({revision:2,theme:Theme.Dark,problem:null,preferences}));
 expect((name as HTMLInputElement).value).toBe("Retained custom draft");expect((screen.getByRole("button",{name:"Save theme"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Cancel"}));expect(screen.queryByRole("textbox",{name:"Theme name"})).toBeNull();expect(value.bridge.update).not.toHaveBeenCalled();
});

test("custom theme save waits for the positively committed native map identity",async()=>{
 const {defaultPreferences}=await import("./appearance-preferences");scheme(false);const value=fixture();const pending=deferred<unknown>();
 vi.mocked(value.bridge.read).mockResolvedValue({revision:1,theme:Theme.System,problem:null,preferences:defaultPreferences()});vi.mocked(value.bridge.update).mockReturnValueOnce(pending.promise);
 render(<AppearanceProvider bridge={value.bridge}><AppearanceSettings/></AppearanceProvider>);const duplicate=await screen.findByRole("button",{name:"Duplicate theme"});await waitFor(()=>expect((duplicate as HTMLButtonElement).disabled).toBe(false));fireEvent.click(duplicate);
 fireEvent.change(screen.getByRole("textbox",{name:"Theme name"}),{target:{value:"Confirmed theme"}});fireEvent.click(screen.getByRole("button",{name:"Save theme"}));
 expect(screen.getByRole("textbox",{name:"Theme name"})).toBeTruthy();
 const [theme,revision,preferences]=vi.mocked(value.bridge.update).mock.calls[0];const sorted={...preferences!,custom_themes:preferences!.custom_themes.map(t=>({...t,light:Object.fromEntries(Object.entries(t.light).sort()),dark:Object.fromEntries(Object.entries(t.dark).sort())}))};
 await act(async()=>pending.resolve({theme,revision:revision+1,problem:null,preferences:sorted}));
 expect(screen.queryByRole("textbox",{name:"Theme name"})).toBeNull();expect(screen.getAllByText("Confirmed theme").length).toBeGreaterThan(0);
});
