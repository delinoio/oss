// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ShortcutProvider, useShortcutHelp, useShortcutSurface, useShortcuts } from "./shortcut-provider";
import { openTerminalsShortcut } from "./session-terminal-shortcut";
import { ShortcutId } from "./shortcuts";
import { Surface } from "./surface";
import { i18n } from "./localization";

function SessionTools({ open }: { open: () => void }) {
  useShortcutSurface(Surface.Sessions);
  const help = useShortcutHelp();
  const shortcuts = useShortcuts([openTerminalsShortcut(true, false, false, open)]);
  return <><button onClick={help}>Help opener</button><button aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenTerminals)} onClick={open}>Terminals</button><form onSubmit={event => { event.preventDefault(); throw new Error("Unexpected message submission"); }}><textarea aria-label="Message" defaultValue="Original draft" onKeyDown={shortcuts.onKeyDown} /></form></>;
}
it("registers once through Strict Mode, shares tool ARIA, and localizes retained Help", async () => {
  const original = i18n.language;
  await i18n.changeLanguage("en");
  const open = vi.fn();
  const view = render(<StrictMode><ShortcutProvider><SessionTools open={open} /></ShortcutProvider></StrictMode>);
  try {
    const input = screen.getByRole("textbox", { name: "Message" });
    fireEvent.keyDown(input, { key: "`", ctrlKey: true });
    expect(open).toHaveBeenCalledTimes(1); expect((input as HTMLTextAreaElement).value).toBe("Original draft");
    expect(screen.getByRole("button", { name: "Terminals" }).getAttribute("aria-keyshortcuts")).toBe("Control+`");
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    const dialog = screen.getByRole("dialog", { name: "Keyboard shortcuts" });
    expect(within(dialog).getByText("Open terminals")).toBeTruthy();
    const row = within(dialog).getByText("Open terminals").parentElement!;
    expect(within(row).getByText("Ctrl")).toBeTruthy(); expect(within(row).getByText("`")).toBeTruthy();
    await act(() => i18n.changeLanguage("ko"));
    expect(within(dialog).getByText("터미널 열기")).toBeTruthy();
    expect(screen.getByRole("dialog")).toBe(dialog); expect(open).toHaveBeenCalledTimes(1);
  } finally { view.unmount(); await i18n.changeLanguage(original); }
});
