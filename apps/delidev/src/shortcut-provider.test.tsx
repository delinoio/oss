// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useRef } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { i18n } from "./localization";
import { ShortcutProvider, useShortcutHelp, useShortcutSurface, useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput, ShortcutScope } from "./shortcuts";
import { Surface } from "./surface";

function Consumer({ surface = Surface.Sessions, enabled = false, run = () => {}, opener = true }: { surface?: Surface; enabled?: boolean; run?: () => void; opener?: boolean }) {
  useShortcutSurface(surface); const openHelp = useShortcutHelp(), input = useRef<HTMLInputElement>(null);
  useShortcuts([
    { id: ShortcutId.Help, scope: ShortcutScope.Global, label: "shortcuts.help", bindings: [{ key: "?" }], run: openHelp },
    { id: ShortcutId.SessionSend, scope: Surface.Sessions, label: "shortcuts.queueMessage", bindings: [{ key: "Enter", primary: true }], target: input, input: ShortcutInput.Target, enabled, unavailableReason: "shortcuts.messageRequired", run },
  ]);
  return <main id="main" tabIndex={-1}>{opener ? <button onClick={openHelp}>Help opener</button> : null}<input ref={input} aria-label="Message" /></main>;
}
it("shows current definitions/reasons, contains focus, and updates language without reopening", async () => {
  const show = vi.spyOn(HTMLDialogElement.prototype, "showModal");
  render(<ShortcutProvider><Consumer /></ShortcutProvider>);
  const opener = screen.getByRole("button", { name: "Help opener" }); opener.focus(); fireEvent.keyDown(opener, { key: "?", shiftKey: true });
  const dialog = screen.getByRole("dialog", { name: "Keyboard shortcuts" }), close = within(dialog).getByRole("button", { name: "Close keyboard shortcuts" });
  expect(document.activeElement).toBe(close); expect(within(dialog).getByText("Add message to queue")).toBeTruthy(); expect(within(dialog).getByText("Enter a message first.")).toBeTruthy();
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true }); expect(document.activeElement).toBe(close);
  fireEvent.keyDown(close, { key: "Tab" }); expect(document.activeElement).toBe(close);
  await act(() => i18n.changeLanguage("ko")); expect(screen.getByRole("dialog", { name: "키보드 단축키" })).toBe(dialog); expect(document.activeElement).toBe(close); expect(show).toHaveBeenCalledTimes(1);
  fireEvent.keyDown(close, { key: "Escape" }); expect(screen.queryByRole("dialog")).toBeNull(); expect(document.activeElement).toBe(opener);
});
it("falls back to main when the original opener is removed", () => {
  const view = render(<ShortcutProvider><Consumer /></ShortcutProvider>);
  const opener = screen.getByRole("button", { name: "Help opener" }); opener.focus(); fireEvent.click(opener);
  view.rerender(<ShortcutProvider><Consumer opener={false} /></ShortcutProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Close keyboard shortcuts" })); expect(document.activeElement).toBe(document.getElementById("main"));
});
it("restores main after help opens without a usable focused control", () => {
  render(<ShortcutProvider><Consumer /></ShortcutProvider>);
  expect(document.activeElement).toBe(document.body);
  fireEvent.keyDown(document.body, { key: "?", shiftKey: true });
  fireEvent.click(screen.getByRole("button", { name: "Close keyboard shortcuts" }));
  expect(document.activeElement).toBe(document.getElementById("main"));
});
it("has no inactive screen entries and neither typing nor another modal opens help", () => {
  render(<ShortcutProvider><Consumer surface={Surface.Usage} /></ShortcutProvider>);
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "?" }); expect(screen.queryByRole("dialog")).toBeNull();
  const modal = document.createElement("dialog"); modal.showModal(); document.body.append(modal);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" })); expect(screen.queryByText("Keyboard shortcuts")).toBeNull(); modal.remove();
  fireEvent.click(screen.getByRole("button", { name: "Help opener" })); const help = screen.getByRole("dialog", { name: "Keyboard shortcuts" });
  expect(within(help).getByText("No screen-specific shortcuts are registered.")).toBeTruthy(); expect(within(help).queryByText("Add message to queue")).toBeNull();
});
it("Strict Mode dispatches once with the latest guards/callback and connection replacement disposes help", () => {
  const first = vi.fn(), second = vi.fn();
  const view = render(<StrictMode><ShortcutProvider key="first"><Consumer enabled run={first} /></ShortcutProvider></StrictMode>);
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true }); expect(first).toHaveBeenCalledTimes(1);
  view.rerender(<StrictMode><ShortcutProvider key="first"><Consumer enabled run={second} /></ShortcutProvider></StrictMode>);
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true }); expect(second).toHaveBeenCalledTimes(1); expect(first).toHaveBeenCalledTimes(1);
  view.rerender(<StrictMode><ShortcutProvider key="first"><Consumer run={second} /></ShortcutProvider></StrictMode>);
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true }); expect(second).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  view.rerender(<StrictMode><ShortcutProvider key="replacement"><Consumer enabled run={second} /></ShortcutProvider></StrictMode>);
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true }); expect(second).toHaveBeenCalledTimes(2);
  view.unmount(); fireEvent.keyDown(document.body, { key: "?" }); expect(screen.queryByRole("dialog")).toBeNull();
});
it("checks all registered input actions before a local form handler can execute", () => {
  const first = vi.fn(), second = vi.fn(), warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  function ConflictingInput() {
    const target = useRef<HTMLInputElement>(null);
    const local = useShortcuts([{ id: ShortcutId.SessionSend, scope: Surface.Sessions, label: "shortcuts.queueMessage", bindings: [{ key: "Enter", primary: true }], target, input: ShortcutInput.Target, run: first }]);
    useShortcuts([{ id: ShortcutId.SessionNewline, scope: Surface.Sessions, label: "shortcuts.newline", bindings: [{ key: "Enter", primary: true }], target, input: ShortcutInput.Target, run: second }]);
    return <input ref={target} onKeyDown={local.onKeyDown} aria-label="Conflicting message" />;
  }
  render(<ShortcutProvider><ConflictingInput /></ShortcutProvider>);
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true });
  expect(first).not.toHaveBeenCalled(); expect(second).not.toHaveBeenCalled();
  expect(warn).toHaveBeenCalledExactlyOnceWith({ event: "delidev_shortcut_conflict", scope: Surface.Sessions, actionIds: [ShortcutId.SessionSend, ShortcutId.SessionNewline] });
});

function HelpActions({ surface = Surface.Sessions, enabled = true, conflict = false, run }: { surface?: Surface; enabled?: boolean; conflict?: boolean; run: () => void }) {
  useShortcutSurface(surface);
  const input = useRef<HTMLInputElement>(null), openHelp = useShortcutHelp();
  const focusId = surface === Surface.Search ? ShortcutId.SearchFocus : surface === Surface.NewSession ? ShortcutId.NewSessionFocus : ShortcutId.SessionFocus;
  useShortcuts([
    { id: ShortcutId.Help, scope: ShortcutScope.Global, label: "shortcuts.help", bindings: [{ key: "?" }], run: openHelp },
    { id: ShortcutId.NewSession, scope: ShortcutScope.Global, label: "shortcuts.newSession", bindings: [{ key: "n", primary: true, shift: true }], input: ShortcutInput.Allow, run },
    { id: focusId, scope: surface, label: "shortcuts.focusMessage", bindings: [{ key: "i", primary: true }], enabled, input: ShortcutInput.Allow, run: () => { run(); input.current?.focus(); } },
    { id: ShortcutId.SessionSend, scope: surface, label: "shortcuts.queueMessage", bindings: [{ key: "Enter", primary: true }, { key: "Enter" }, { key: "Enter", shift: true }], target: input, input: ShortcutInput.Target, run },
    ...(conflict ? [{ id: ShortcutId.SearchSubmit, scope: surface, label: "shortcuts.searchSubmit" as const, bindings: [{ key: "i", primary: true }], run }] : []),
  ]);
  return <main id="main" tabIndex={-1}><button onClick={openHelp}>Help opener</button><input ref={input} aria-label="Destination" /></main>;
}
it.each([Surface.Sessions, Surface.NewSession, Surface.Search])("closes help before the %s input focus callback and preserves destination focus", async surface => {
  const run = vi.fn(() => expect(document.querySelector(".shortcut-help")).toBeNull());
  render(<StrictMode><ShortcutProvider><HelpActions surface={surface} run={run} /></ShortcutProvider></StrictMode>);
  const opener = screen.getByRole("button", { name: "Help opener" }); opener.focus(); fireEvent.click(opener);
  fireEvent.keyDown(screen.getByRole("button", { name: "Close keyboard shortcuts" }), { key: "i", ctrlKey: true });
  expect(run).toHaveBeenCalledTimes(1); expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => {});
  expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Destination" }));
});
it.each(["MacIntel", "Win32", "Linux x86_64"])("runs New session once after closing help on %s", platform => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
  const run = vi.fn(() => expect(document.querySelector(".shortcut-help")).toBeNull());
  const view = render(<StrictMode><ShortcutProvider><HelpActions run={run} /></ShortcutProvider></StrictMode>);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  fireEvent.keyDown(screen.getByRole("button", { name: "Close keyboard shortcuts" }), { key: "n", shiftKey: true, metaKey: platform === "MacIntel", ctrlKey: platform !== "MacIntel" });
  expect(run).toHaveBeenCalledTimes(1); expect(screen.queryByRole("dialog")).toBeNull();
  view.unmount(); fireEvent.keyDown(document.body, { key: "n", shiftKey: true, ctrlKey: true }); expect(run).toHaveBeenCalledTimes(1);
});
it.each([false, true])("keeps help open for a disabled or conflicting action (conflict %s)", conflict => {
  const run = vi.fn(); vi.spyOn(console, "warn").mockImplementation(() => {});
  render(<ShortcutProvider><HelpActions enabled={conflict} conflict={conflict} run={run} /></ShortcutProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  const help = screen.getByRole("dialog"), close = screen.getByRole("button", { name: "Close keyboard shortcuts" });
  expect(fireEvent.keyDown(close, { key: "i", ctrlKey: true })).toBe(false);
  expect(run).not.toHaveBeenCalled(); expect(screen.getByRole("dialog")).toBe(help); expect(document.activeElement).toBe(close);
});
it("preserves help for excluded events, repeated help and background input actions", () => {
  const run = vi.fn(), show = vi.spyOn(HTMLDialogElement.prototype, "showModal");
  render(<ShortcutProvider><HelpActions run={run} /></ShortcutProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  const help = screen.getByRole("dialog"), close = screen.getByRole("button", { name: "Close keyboard shortcuts" });
  for (const options of [{ isComposing: true }, { keyCode: 229 }, { repeat: true }, { altKey: true }, { ctrlKey: false, metaKey: true }]) expect(fireEvent.keyDown(close, { key: "i", ctrlKey: true, ...options })).toBe(true);
  const handled = new KeyboardEvent("keydown", { key: "i", ctrlKey: true, bubbles: true, cancelable: true }); handled.preventDefault(); fireEvent(close, handled);
  const altGraph = new KeyboardEvent("keydown", { key: "i", ctrlKey: true, bubbles: true, cancelable: true }); vi.spyOn(altGraph, "getModifierState").mockImplementation(key => key === "AltGraph"); fireEvent(close, altGraph); expect(altGraph.defaultPrevented).toBe(false);
  for (const options of [{}, { ctrlKey: true }, { shiftKey: true }]) fireEvent.keyDown(close, { key: "Enter", ...options });
  fireEvent.keyDown(close, { key: "?" });
  expect(run).not.toHaveBeenCalled(); expect(screen.getByRole("dialog")).toBe(help); expect(show).toHaveBeenCalledTimes(1);
  const other = document.createElement("dialog"); other.showModal(); document.body.append(other);
  fireEvent.keyDown(close, { key: "i", ctrlKey: true }); expect(run).not.toHaveBeenCalled(); expect(screen.getAllByRole("dialog")).toHaveLength(2); other.remove();
});

it("lists native window bindings as read-only help without adding dispatch actions",()=>{
 const run=vi.fn();render(<ShortcutProvider><Consumer enabled run={run}/></ShortcutProvider>);fireEvent.click(screen.getByRole("button",{name:"Help opener"}));const dialog=screen.getByRole("dialog");expect(within(dialog).getByText("New Window")).toBeTruthy();expect(within(dialog).getByText("Close Window")).toBeTruthy();expect(run).not.toHaveBeenCalled();
});
