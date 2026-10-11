// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { i18n } from "./localization";
import { ShortcutProvider, useShortcutHelp } from "./shortcut-provider";
import { ShortcutPreferenceProvider, type ShortcutPreferenceBridge } from "./shortcut-preference-controller";
import { editableShortcutCatalog, ShortcutOverrideState, type ShortcutOverrides } from "./shortcut-preferences";
afterEach(async () => { vi.restoreAllMocks(); await i18n.changeLanguage("en"); });
it("opens at the first enabled entry and owns arrows, Escape and backdrop dismissal without activating tools", () => {
  const selected = vi.fn();
  const view = render(<><SessionToolMenu active><button role="menuitem" disabled>Terminals</button><button role="menuitem" onClick={selected}>Files</button><button role="menuitem">Diagnostics</button></SessionToolMenu><button>Destination</button></>);
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  const entries = screen.getAllByRole("menuitem");
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "ArrowDown" }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Home" }); expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "End" }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Escape" }); expect(document.activeElement).toBe(trigger); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(selected).not.toHaveBeenCalled();
  fireEvent.click(trigger); fireEvent.click(screen.getByRole("menuitem", { name: "Files" })); expect(selected).toHaveBeenCalledOnce(); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger); const modal = screen.getByRole("dialog"); fireEvent.click(modal, {clientX: -1}); expect(document.activeElement).toBe(trigger); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger); view.rerender(<SessionToolMenu active={false}><button role="menuitem">Files</button></SessionToolMenu>); expect(screen.queryByRole("menuitem")).toBeNull();
});

it.each([['en', 'Open tool'], ['ko', '도구 열기']])("keeps a decorative icon and localized accessible trigger in %s", async (language, label) => {
  await act(() => i18n.changeLanguage(language));
  render(<SessionToolMenu active><button role="menuitem">Files</button></SessionToolMenu>);
  const trigger = screen.getByRole("button", { name: label });
  expect(trigger.textContent).toBe(""); expect(trigger.title).toBe(label);
  expect(trigger.getAttribute("aria-haspopup")).toBe("dialog");
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  const icon = trigger.querySelector('svg');
  expect(icon?.classList.contains('session-icon')).toBe(true);
  expect(icon?.getAttribute('aria-hidden')).toBe('true');
  expect(icon?.getAttribute('focusable')).toBe('false');
  expect(icon?.querySelector('path')).not.toBeNull();
  fireEvent.click(trigger);
  expect(screen.getByRole('menu', { name: label })).toBeTruthy();
  expect(trigger.getAttribute('aria-controls')).toBe(screen.getByRole("dialog").id);
  expect(trigger.getAttribute('aria-expanded')).toBe('true');
  expect(document.activeElement).toBe(screen.getByRole('menuitem'));
  fireEvent.keyDown(screen.getByRole('menuitem'), { key: 'Tab' });
  expect(trigger.getAttribute('aria-expanded')).toBe('true');
  expect(screen.getByRole("dialog").contains(document.activeElement)).toBe(true);
});

it("closes the native modal before an original callback and preserves destination focus and mounted owners", () => {
 const destination = document.createElement("button"); destination.textContent = "Destination"; document.body.append(destination);
 const selected = vi.fn(() => { expect(screen.queryByRole("dialog")).toBeNull(); destination.focus(); });
 const view = render(<SessionToolMenu active><button role="menuitem" onClick={selected}>Files</button></SessionToolMenu>);
 fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
 const original = screen.getByRole("menuitem");
 fireEvent.click(original);
 expect(selected).toHaveBeenCalledOnce(); expect(document.activeElement).toBe(destination);
 fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
 expect(screen.getByRole("menuitem")).toBe(original);
 view.unmount(); destination.remove();
});

it("contains Tab including Close, skips disabled rows and disposes on departure without activating", () => {
 const selected = vi.fn();
 const view = render(<SessionToolMenu active><button role="menuitem" disabled onClick={selected}>Terminals</button><button role="menuitem">Files</button><button role="menuitem">Diff</button></SessionToolMenu>);
 fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
 const first = screen.getByRole("menuitem", {name:"Files"}), last = screen.getByRole("menuitem", {name:"Diff"}), close = screen.getByRole("button", {name:"Close Open tool"});
 fireEvent.keyDown(first, {key:"Tab", shiftKey:true}); expect(document.activeElement).toBe(close);
 fireEvent.keyDown(close, {key:"Tab", shiftKey:true}); expect(document.activeElement).toBe(last);
 fireEvent.keyDown(last, {key:"ArrowDown"}); expect(document.activeElement).toBe(first);
 fireEvent.click(screen.getByRole("menuitem", {name:"Terminals"})); expect(selected).not.toHaveBeenCalled(); expect(screen.getByRole("dialog")).toBeTruthy();
 view.rerender(<SessionToolMenu active={false}><button role="menuitem">Files</button></SessionToolMenu>);
 expect(screen.queryByRole("dialog")).toBeNull(); expect(selected).not.toHaveBeenCalled();
});


it.each(["MacIntel", "Win32", "Linux x86_64"])("opens one shared dialog from the platform chord and composer with existing event fences on %s", platform => {
 vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
 render(<ShortcutProvider><textarea aria-label="Composer" defaultValue="Retained draft"/><SessionToolMenu active><button role="menuitem">Files</button></SessionToolMenu></ShortcutProvider>);
 const composer = screen.getByRole("textbox"), modifier = platform === "MacIntel" ? {metaKey:true} : {ctrlKey:true};
 composer.focus();
 for (const flags of [{shiftKey:true}, {altKey:true}, {repeat:true}, {isComposing:true}, {keyCode:229}, {metaKey:true,ctrlKey:true}]) {
  fireEvent.keyDown(composer, {key:"t",code:"KeyT", ...modifier, ...flags}); expect(screen.queryByRole("dialog")).toBeNull();
 }
 fireEvent.keyDown(composer, {key:"t",code:"KeyT", ...modifier});
 expect(screen.getAllByRole("dialog")).toHaveLength(1); expect(document.activeElement).toBe(screen.getByRole("menuitem"));
 expect(composer).toHaveProperty("value", "Retained draft");
 fireEvent.keyDown(screen.getByRole("menuitem"), {key:"Escape"}); expect(document.activeElement).toBe(composer);
 fireEvent.click(screen.getByRole("button", {name:"Open tool"})); expect(screen.getAllByRole("dialog")).toHaveLength(1);
});

function HelpButton() { const open = useShortcutHelp(); return <button onClick={open}>Show Help</button>; }

it("restores the default immediately after a committed cross-scope T binding changes", async () => {
 vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
 let changed!: (snapshot: unknown) => void;
 const overrides: ShortcutOverrides = { [editableShortcutCatalog.at(-1)!.id]: {state:ShortcutOverrideState.Binding,chord:{key:"t",shift:false}} };
 const bridge: ShortcutPreferenceBridge = {read:async()=>({revision:1,overrides,problem:null}),update:vi.fn(),subscribe:async callback=>{changed=callback;return()=>{};}};
 render(<ShortcutPreferenceProvider bridge={bridge}><ShortcutProvider><HelpButton/><textarea aria-label="Composer"/><SessionToolMenu active><button role="menuitem">Files</button></SessionToolMenu></ShortcutProvider></ShortcutPreferenceProvider>);
 const trigger = screen.getByRole("button", {name:"Open tool"}), composer = screen.getByRole("textbox");
 await waitFor(()=>expect(trigger.getAttribute("aria-keyshortcuts")).toBe(""));
 expect(trigger.title).toContain("saved custom shortcut");
 fireEvent.click(screen.getByRole("button",{name:"Show Help"}));
 const helpRow = screen.getByText("Open tool",{selector:"dt"}).parentElement!;
 expect(helpRow.textContent).toContain("Inactive because a saved custom shortcut takes priority.");
 expect(helpRow.textContent).toContain("Disabled");
 fireEvent.click(screen.getByRole("button",{name:"Close keyboard shortcuts"}));
 composer.focus(); fireEvent.keyDown(composer,{key:"t",code:"KeyT",ctrlKey:true}); expect(screen.queryByRole("dialog")).toBeNull();
 fireEvent.click(trigger); expect(screen.getByRole("dialog")).toBeTruthy(); fireEvent.keyDown(screen.getByRole("menuitem"),{key:"Escape"});
 await act(async()=>changed({revision:2,overrides:{},problem:null}));
 expect(trigger.getAttribute("aria-keyshortcuts")).toBe("Control+T");
 composer.focus(); fireEvent.keyDown(composer,{key:"t",code:"KeyT",ctrlKey:true}); expect(screen.getByRole("dialog")).toBeTruthy();
});
