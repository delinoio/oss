// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { ShortcutProvider } from "./shortcut-provider";
import { i18n } from "./localization";
afterEach(async () => { await i18n.changeLanguage("en"); vi.restoreAllMocks(); });
it("contains focus, skips disabled rows and dismisses without activating tools", () => {
  const selected = vi.fn();
  const view = render(<SessionToolMenu active><button role="menuitem" disabled>Terminals</button><button role="menuitem" onClick={selected}>Files</button><button role="menuitem">Diagnostics</button></SessionToolMenu>);
  const trigger = screen.getByRole("button", { name: "Open tool" });
  trigger.focus(); fireEvent.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "Open tool" });
  const entries = screen.getAllByRole("menuitem");
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "ArrowDown" }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Home" }); expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "End" }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Tab" }); expect(document.activeElement).toBe(within(dialog).getByRole("button"));
  fireEvent.keyDown(document.activeElement!, { key: "Tab", shiftKey: true }); expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Escape" }); expect(document.activeElement).toBe(trigger); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(selected).not.toHaveBeenCalled();
  fireEvent.click(trigger); fireEvent.click(screen.getByRole("menuitem", { name: "Terminals" })); expect(selected).not.toHaveBeenCalled(); expect(dialog).toHaveProperty("open", true);
  fireEvent.click(within(dialog).getByRole("button")); expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger); fireEvent.click(dialog, { clientX: -1, clientY: -1 }); expect(dialog).toHaveProperty("open", false); expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger); view.rerender(<SessionToolMenu active={false}><button role="menuitem">Files</button></SessionToolMenu>); expect(screen.queryByRole("menuitem")).toBeNull(); expect(dialog).toHaveProperty("open", false);
});

it.each([['en', 'Open tool'], ['ko', '도구 열기']])("retains the icon trigger and named compact dialog in %s", async (language, label) => {
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
  fireEvent.click(trigger);
  const dialog = screen.getByRole('dialog', { name: label });
  expect(trigger.getAttribute('aria-controls')).toBe(dialog.id);
  expect(document.activeElement).toBe(screen.getByRole('menuitem'));
  fireEvent.keyDown(screen.getByRole('menuitem'), { key: 'Tab' });
  expect(trigger.getAttribute('aria-expanded')).toBe('true');
});

it.each(["MacIntel", "Win32", "Linux x86_64"])("shares button and primary T entry with once-only callbacks on %s", platform => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
  const selected = vi.fn(() => {
    expect(screen.queryByRole("dialog")).toBeNull();
    screen.getByRole("button", { name: "Destination" }).focus();
  });
  render(<StrictMode><ShortcutProvider><input aria-label="Draft" defaultValue="Unsent" /><SessionToolMenu active><button role="menuitem" onClick={selected}>Files</button></SessionToolMenu><button>Destination</button></ShortcutProvider></StrictMode>);
  const input = screen.getByRole("textbox"); input.focus();
  fireEvent.keyDown(input, { key: "t", ...(platform === "MacIntel" ? { metaKey: true } : { ctrlKey: true }) });
  expect(screen.getAllByRole("dialog")).toHaveLength(1); expect(selected).not.toHaveBeenCalled(); expect(input).toHaveProperty("value", "Unsent");
  fireEvent.keyDown(screen.getByRole("menuitem"), { key: "Escape" }); expect(document.activeElement).toBe(input);
  fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
  fireEvent.click(screen.getByRole("menuitem")); expect(selected).toHaveBeenCalledOnce(); expect(document.activeElement).toBe(screen.getByRole("button", { name: "Destination" }));
});
