// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { bindingAria, bindingKeys, dispatchShortcut, ShortcutId, ShortcutPlatform } from "./shortcuts";
import { openTerminalsShortcut } from "./session-terminal-shortcut";
import { Surface } from "./surface";
import { SessionTabsStore, SessionTabKind } from "./session-tabs";
import { copy, i18n } from "./localization";
import { editableShortcutCatalog, parseShortcutOverrides, readOnlyShortcutCatalog } from "./shortcut-preferences";

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])("opens through the original callback once without changing the draft on %s", platform => {
  const composer = document.createElement("textarea"); composer.value = "Original draft"; document.body.append(composer);
  const store = new SessionTabsStore();
  const open = vi.fn(() => store.open("session", { kind: SessionTabKind.Terminals }));
  const action = openTerminalsShortcut(true, false, false, open);
  const press = () => {
    const event = new KeyboardEvent("keydown", { key: "`", bubbles: true, cancelable: true, metaKey: platform === ShortcutPlatform.Mac, ctrlKey: platform === ShortcutPlatform.Other });
    composer.addEventListener("keydown", () => dispatchShortcut(event, [action], Surface.Sessions, platform), { once: true });
    composer.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
  };
  press(); expect(open).toHaveBeenCalledTimes(1);
  const original = store.snapshot("session").tabs[1], controller = store.terminalPresentation("session");
  press(); expect(open).toHaveBeenCalledTimes(2);
  expect(store.snapshot("session").tabs).toHaveLength(2);
  expect(store.snapshot("session").tabs[1]).toEqual(original);
  expect(store.terminalPresentation("session")).toBe(controller);
  expect(store.snapshot("session").selected).toBe(SessionTabKind.Terminals);
  expect(composer.value).toBe("Original draft"); composer.remove();
});
it("keeps Session eligibility and Sidechat admission identical to the tool entry", () => {
  const open = vi.fn();
  for (const [active, embedded, sidechat] of [[false, false, false], [true, true, false], [true, false, true]]) {
    const event = new KeyboardEvent("keydown", { key: "`", ctrlKey: true, cancelable: true });
    dispatchShortcut(event, [openTerminalsShortcut(active, embedded, sidechat, open)], Surface.Sessions, ShortcutPlatform.Other);
  }
  dispatchShortcut(new KeyboardEvent("keydown", { key: "`", ctrlKey: true }), [openTerminalsShortcut(true, false, false, open)], Surface.Search, ShortcutPlatform.Other);
  expect(open).not.toHaveBeenCalled();
});
it("retains logical grave matching and all existing input fences", () => {
  const open = vi.fn(), action = openTerminalsShortcut(true, false, false, open);
  for (const flags of [{ ctrlKey: false }, { metaKey: true }, { shiftKey: true }, { altKey: true }, { repeat: true }, { isComposing: true }, { keyCode: 229 }, { key: "~", code: "Backquote" }, { key: "Dead", code: "Backquote" }]) {
    dispatchShortcut(new KeyboardEvent("keydown", { key: "`", ctrlKey: true, ...flags }), [action], Surface.Sessions, ShortcutPlatform.Other);
  }
  const handled = new KeyboardEvent("keydown", { key: "`", ctrlKey: true, cancelable: true }); handled.preventDefault();
  dispatchShortcut(handled, [action], Surface.Sessions, ShortcutPlatform.Other);
  const altGraph = new KeyboardEvent("keydown", { key: "`", ctrlKey: true }); vi.spyOn(altGraph, "getModifierState").mockImplementation(key => key === "AltGraph");
  dispatchShortcut(altGraph, [action], Surface.Sessions, ShortcutPlatform.Other);
  const input = document.createElement("textarea"); document.body.append(input);
  for (const attribute of ["hidden", "inert", "data-shortcuts"]) {
    input.setAttribute(attribute, attribute === "data-shortcuts" ? "passthrough" : "");
    const event = new KeyboardEvent("keydown", { key: "`", ctrlKey: true, bubbles: true, cancelable: true });
    input.addEventListener("keydown", () => dispatchShortcut(event, [action], Surface.Sessions, ShortcutPlatform.Other), { once: true }); input.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false); input.removeAttribute(attribute);
  }
  input.remove();
  const modal = document.createElement("dialog"); modal.open = true; document.body.append(modal);
  dispatchShortcut(new KeyboardEvent("keydown", { key: "`", ctrlKey: true }), [action], Surface.Sessions, ShortcutPlatform.Other); modal.remove();
  expect(open).not.toHaveBeenCalled();
});
it("shares localized read-only Help/catalog labels and platform ARIA without changing preferences", async () => {
  const action = openTerminalsShortcut(true, false, false, vi.fn());
  const catalog = readOnlyShortcutCatalog.find(row => row.id === ShortcutId.OpenTerminals)!;
  expect(catalog.defaults).toEqual(action.bindings); expect(catalog.label).toBe(action.label);
  expect(editableShortcutCatalog).toHaveLength(7);
  expect(() => parseShortcutOverrides({ [ShortcutId.OpenTerminals]: { state: "disabled" } })).toThrow();
  expect(bindingKeys(action.bindings[0]!, ShortcutPlatform.Mac)).toEqual(["⌘", "`"]);
  expect(bindingKeys(action.bindings[0]!, ShortcutPlatform.Other)).toEqual(["Ctrl", "`"]);
  expect(bindingAria(action.bindings[0]!, ShortcutPlatform.Mac)).toBe("Meta+`");
  expect(bindingAria(action.bindings[0]!, ShortcutPlatform.Other)).toBe("Control+`");
  const original = i18n.language;
  try { for (const [language, label] of [["en", "Open terminals"], ["ko", "터미널 열기"]]) { await i18n.changeLanguage(language); expect(copy(action.label)).toBe(label); } }
  finally { await i18n.changeLanguage(original); }
});
