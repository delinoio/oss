// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { bindingAria, bindingKeys, bindingMatches, dispatchShortcut, globalShortcutBindings, ShortcutExecution, ShortcutId, ShortcutInput, ShortcutPlatform, ShortcutScope, ShortcutStore, type ShortcutDefinition } from "./shortcuts";
import { Surface } from "./surface";

const platform = ShortcutPlatform.Other;
function definition(extra: Partial<ShortcutDefinition> = {}): ShortcutDefinition { return { id: ShortcutId.Search, scope: ShortcutScope.Global, label: "shortcuts.openSearch", bindings: [{ key: "k", primary: true }], run: vi.fn(), ...extra }; }
function dispatch(items: ShortcutDefinition[], options: KeyboardEventInit = {}, target: HTMLElement = document.body) {
  const event = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "k", ctrlKey: true, ...options });
  const handle = () => dispatchShortcut(event, items, Surface.Search, platform);
  target.addEventListener("keydown", handle, { once: true }); target.dispatchEvent(event);
  return event;
}
it("resolves exact local primary modifiers and logical question marks", () => {
  expect(bindingMatches(new KeyboardEvent("keydown", { key: "K", metaKey: true }), { key: "k", primary: true }, ShortcutPlatform.Mac)).toBe(true);
  expect(bindingMatches(new KeyboardEvent("keydown", { key: "k", ctrlKey: true }), { key: "k", primary: true }, ShortcutPlatform.Mac)).toBe(false);
  for (const options of [{ shiftKey: true }, { altKey: true }, { metaKey: true }]) expect(bindingMatches(new KeyboardEvent("keydown", { key: "k", ctrlKey: true, ...options }), { key: "k", primary: true }, platform)).toBe(false);
  for (const shiftKey of [false, true]) expect(bindingMatches(new KeyboardEvent("keydown", { key: "?", shiftKey }), { key: "?" }, platform)).toBe(true);
  expect(bindingKeys({ key: "n", primary: true, shift: true }, ShortcutPlatform.Mac)).toEqual(["⌘", "⇧", "N"]);
  expect(bindingAria({ key: "Enter", primary: true }, platform)).toBe("Control+Enter");
  expect(bindingKeys(globalShortcutBindings[ShortcutId.Help][0], platform)).toEqual(["?"]);
  expect(bindingAria(globalShortcutBindings[ShortcutId.Help][0], platform)).toBe("Shift+/");
});
it.each([{ isComposing: true }, { keyCode: 229 }, { repeat: true }])("ignores composition and repeated events %j", options => {
  const item = definition(); dispatch([item], options); expect(item.run).not.toHaveBeenCalled();
});
it("ignores handled events and AltGraph without preventing native input", () => {
  const item = definition(); const event = new KeyboardEvent("keydown", { key: "k", ctrlKey: true, cancelable: true });
  event.preventDefault(); expect(dispatchShortcut(event, [item], Surface.Search, platform)).toBe(false);
  const altGraph = new KeyboardEvent("keydown", { key: "k", ctrlKey: true }); vi.spyOn(altGraph, "getModifierState").mockImplementation(key => key === "AltGraph");
  expect(dispatchShortcut(altGraph, [item], Surface.Search, platform)).toBe(false); expect(item.run).not.toHaveBeenCalled();
});
it("gives target then screen precedence and never falls through a disabled match", () => {
  const input = document.createElement("input"); document.body.append(input);
  const global = definition({ input: ShortcutInput.Allow });
  const screen = definition({ id: ShortcutId.SearchFocus, scope: Surface.Search, input: ShortcutInput.Allow });
  const local = definition({ id: ShortcutId.SearchSubmit, scope: Surface.Search, target: { current: input }, input: ShortcutInput.Target });
  dispatch([global, screen, local], {}, input); expect(local.run).toHaveBeenCalledTimes(1); expect(screen.run).not.toHaveBeenCalled(); expect(global.run).not.toHaveBeenCalled();
  local.enabled = false; dispatch([global, screen, local], {}, input); expect(local.run).toHaveBeenCalledTimes(1); expect(screen.run).not.toHaveBeenCalled();
  input.remove(); dispatch([global, screen]); expect(screen.run).toHaveBeenCalledTimes(1); expect(global.run).not.toHaveBeenCalled();
});
it("blocks conflicts and logs only stable action metadata", () => {
  const log = vi.spyOn(console, "warn").mockImplementation(() => {});
  const first = definition(), second = definition({ id: ShortcutId.NewSession }); dispatch([first, second]);
  expect(first.run).not.toHaveBeenCalled(); expect(second.run).not.toHaveBeenCalled();
  expect(log).toHaveBeenCalledWith({ event: "delidev_shortcut_conflict", scope: ShortcutScope.Global, actionIds: [ShortcutId.Search, ShortcutId.NewSession] });
});
it.each(["input", "textarea", "select", "editable", "textbox", "combobox"])("preserves question mark text in %s", kind => {
  const element = document.createElement(["input", "textarea", "select"].includes(kind) ? kind : "div");
  if (kind === "editable") element.setAttribute("contenteditable", "true");
  if (kind === "textbox" || kind === "combobox") element.setAttribute("role", kind);
  document.body.append(element); const help = definition({ bindings: [{ key: "?" }] });
  expect(dispatch([help], { key: "?", ctrlKey: false }, element).defaultPrevented).toBe(false); expect(help.run).not.toHaveBeenCalled(); element.remove();
});
it("preserves native behavior and excludes inactive, hidden and passthrough targets", () => {
  const input = document.createElement("textarea"); document.body.append(input);
  const native = definition({ bindings: [{ key: "Enter" }], execution: ShortcutExecution.Native, target: { current: input }, input: ShortcutInput.Target });
  expect(dispatch([native], { key: "Enter", ctrlKey: false }, input).defaultPrevented).toBe(false); expect(native.run).not.toHaveBeenCalled();
  const item = definition({ input: ShortcutInput.Allow }); input.dataset.shortcuts = "passthrough"; dispatch([item], {}, input);
  delete input.dataset.shortcuts; input.hidden = true; dispatch([item], {}, input); input.remove();
  dispatch([item], {}); expect(item.run).toHaveBeenCalledTimes(1);
  dispatch([definition({ active: false }), definition({ scope: Surface.Sessions })]);
});
it("blocks modals and compact drawers but permits a wide sidebar region", () => {
  const dialog = document.createElement("dialog"); dialog.setAttribute("open", ""); document.body.append(dialog);
  const item = definition(); dispatch([item]); expect(item.run).not.toHaveBeenCalled();
  dialog.setAttribute("role", "region"); dispatch([item]); expect(item.run).toHaveBeenCalledTimes(1);
  dialog.removeAttribute("role"); dialog.hidden = true; dispatch([item]); expect(item.run).toHaveBeenCalledTimes(2); dialog.remove();
});
it("scopes snapshots and removes original registration owners independently", () => {
  const store = new ShortcutStore(), first = Symbol(), second = Symbol();
  store.register(first, [definition()]); store.register(second, [definition({ id: ShortcutId.SearchFocus, scope: Surface.Search }), definition({ active: false })]);
  expect(store.getSnapshot().map(item => item.id)).toEqual([ShortcutId.Search]);
  store.setSurface(Surface.Search); expect(store.getSnapshot()).toHaveLength(2);
  store.remove(first); expect(store.getSnapshot().map(item => item.id)).toEqual([ShortcutId.SearchFocus]);
  store.remove(second); expect(store.getSnapshot()).toEqual([]);
});
