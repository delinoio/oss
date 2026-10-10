// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { i18n } from "./localization";
afterEach(async () => { await i18n.changeLanguage("en"); });
it("opens at the first enabled entry and owns arrows, Escape and outside dismissal without activating tools", () => {
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
  fireEvent.click(trigger); const destination = screen.getByRole("button", { name: "Destination" }); destination.focus(); fireEvent.pointerDown(destination); expect(document.activeElement).toBe(destination); expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger); view.rerender(<SessionToolMenu active={false}><button role="menuitem">Files</button></SessionToolMenu>); expect(screen.queryByRole("menuitem")).toBeNull();
});

it.each([['en', 'Open tool'], ['ko', '도구 열기']])("keeps a decorative icon and localized accessible trigger in %s", async (language, label) => {
  await act(() => i18n.changeLanguage(language));
  render(<SessionToolMenu active><button role="menuitem">Files</button></SessionToolMenu>);
  const trigger = screen.getByRole("button", { name: label });
  expect(trigger.textContent).toBe(""); expect(trigger.title).toBe(label);
  expect(trigger.getAttribute("aria-haspopup")).toBe("menu");
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  const icon = trigger.querySelector('svg');
  expect(icon?.classList.contains('session-icon')).toBe(true);
  expect(icon?.getAttribute('aria-hidden')).toBe('true');
  expect(icon?.getAttribute('focusable')).toBe('false');
  expect(icon?.querySelector('path')).not.toBeNull();
  fireEvent.click(trigger);
  const menu = screen.getByRole('menu', { name: label });
  expect(trigger.getAttribute('aria-controls')).toBe(menu.id);
  expect(trigger.getAttribute('aria-expanded')).toBe('true');
  expect(document.activeElement).toBe(screen.getByRole('menuitem'));
  fireEvent.keyDown(screen.getByRole('menuitem'), { key: 'Tab' });
  expect(trigger.getAttribute('aria-expanded')).toBe('false');
});
