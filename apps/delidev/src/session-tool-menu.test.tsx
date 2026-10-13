// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { i18n } from "./localization";
import { ShortcutProvider, useShortcutSurface } from "./shortcut-provider";
import {
  ShortcutPreferenceProvider,
  type ShortcutPreferenceBridge,
} from "./shortcut-preference-controller";
import { Surface } from "./surface";

afterEach(async () => {
  await i18n.changeLanguage("en");
});
function Surfaces({ children }: { children: React.ReactNode }) {
  useShortcutSurface(Surface.Sessions);
  return <>{children}</>;
}
function Rows({ selected = vi.fn() }: { selected?: () => void }) {
  return (
    <>
      <button role="menuitem" disabled>
        Terminals
      </button>
      <button role="menuitem" onClick={selected}>
        Files
      </button>
      <button role="menuitem">Diagnostics</button>
    </>
  );
}
function harness(
  active = true,
  overrides: Record<string, unknown> = {},
  selected: () => void = vi.fn(),
) {
  let changed: (value: unknown) => void = () => {};
  const bridge: ShortcutPreferenceBridge = {
    read: async () => ({ revision: 1, problem: null, overrides }),
    update: vi.fn(),
    subscribe: async (listener) => {
      changed = listener;
      return () => {};
    },
  };
  const tree = (isActive: boolean, ready = true) => (
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <Surfaces>
          <textarea aria-label="Draft" defaultValue="keep me" />
          <SessionToolMenu active={isActive} ready={ready}>
            <Rows selected={selected} />
          </SessionToolMenu>
          <button>Destination</button>
        </Surfaces>
      </ShortcutProvider>
    </ShortcutPreferenceProvider>
  );
  const view = render(tree(active));
  return {
    view,
    tree,
    selected,
    publish: (next: Record<string, unknown>) =>
      act(() => changed({ revision: 2, problem: null, overrides: next })),
  };
}
const dialog = () => document.querySelector("dialog")!;

it("opens at the first enabled row and owns arrows, Home/End, Tab, Escape and backdrop dismissal without activating tools", async () => {
  const { selected } = harness();
  await act(async () => {});
  const trigger = screen.getByRole("button", { name: "Open tool" });
  expect(dialog().open).toBe(false);
  fireEvent.click(trigger);
  expect(dialog().open).toBe(true);
  expect(trigger.getAttribute("aria-expanded")).toBe("true");
  expect(
    screen
      .getByRole("dialog", { name: "Open tool" })
      .contains(screen.getByRole("menu", { name: "Open tool" })),
  ).toBe(true);
  const entries = screen.getAllByRole("menuitem");
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1]!, { key: "ArrowDown" });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2]!, { key: "ArrowDown" });
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1]!, { key: "ArrowUp" });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2]!, { key: "Home" });
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1]!, { key: "End" });
  expect(document.activeElement).toBe(entries[2]);
  // Tab cycles through the close button and enabled rows only.
  const close = screen.getByRole("button", { name: "Close Open tool" });
  fireEvent.keyDown(entries[2]!, { key: "Tab" });
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2]!, { key: "Tab" });
  fireEvent.keyDown(close, { key: "Tab" });
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1]!, { key: "Escape" });
  expect(dialog().open).toBe(false);
  expect(document.activeElement).toBe(trigger);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  // Named close and backdrop dismissal restore the opener.
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("button", { name: "Close Open tool" }));
  expect(dialog().open).toBe(false);
  expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  fireEvent.click(dialog(), { clientX: 0, clientY: 0 });
  expect(dialog().open).toBe(true);
  fireEvent.click(dialog(), { clientX: -5, clientY: 0 });
  expect(dialog().open).toBe(false);
  expect(document.activeElement).toBe(trigger);
  expect(selected).not.toHaveBeenCalled();
});

it("selects one row by closing first and invokes the original callback once", async () => {
  const order: string[] = [];
  harness(true, {}, () => order.push(`select:${dialog().open}`));
  await act(async () => {});
  fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Files" }));
  expect(order).toEqual(["select:false"]);
  // A disabled row cannot activate.
  fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
  const disabled = screen.getByRole("menuitem", { name: "Terminals" });
  expect(disabled.hasAttribute("disabled")).toBe(true);
  fireEvent.click(disabled);
  expect(dialog().open).toBe(true);
  expect(order).toEqual(["select:false"]);
});

it.each([
  ["MacIntel", { metaKey: true }],
  ["Win32", { ctrlKey: true }],
  ["Linux x86_64", { ctrlKey: true }],
])(
  "opens the same dialog from the platform primary+T chord on %s without changing the draft",
  async (platform, modifier) => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const { selected } = harness();
    await act(async () => {});
    const draft = screen.getByRole("textbox", { name: "Draft" });
    draft.focus();
    const wrongModifier =
      "metaKey" in modifier ? { ctrlKey: true } : { metaKey: true };
    fireEvent.keyDown(draft, { key: "t", code: "KeyT", ...wrongModifier });
    expect(dialog().open).toBe(false);
    fireEvent.keyDown(draft, {
      key: "t",
      code: "KeyT",
      ...modifier,
      shiftKey: true,
    });
    expect(dialog().open).toBe(false);
    fireEvent.keyDown(draft, {
      key: "t",
      code: "KeyT",
      ...modifier,
      altKey: true,
    });
    expect(dialog().open).toBe(false);
    fireEvent.keyDown(draft, {
      key: "t",
      code: "KeyT",
      ...modifier,
      repeat: true,
    });
    expect(dialog().open).toBe(false);
    const event = new KeyboardEvent("keydown", {
      key: "t",
      code: "KeyT",
      bubbles: true,
      cancelable: true,
      ...modifier,
    });
    act(() => {
      draft.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
    expect(document.querySelectorAll("dialog[open]")).toHaveLength(1);
    expect(screen.getAllByRole("menuitem")).toHaveLength(3);
    expect(document.activeElement).toBe(
      screen.getByRole("menuitem", { name: "Files" }),
    );
    expect((draft as HTMLTextAreaElement).value).toBe("keep me");
    expect(selected).not.toHaveBeenCalled();
    // Repeating the chord while the modal is visible does not open another.
    fireEvent.keyDown(document.activeElement!, {
      key: "t",
      code: "KeyT",
      ...modifier,
    });
    expect(document.querySelectorAll("dialog[open]")).toHaveLength(1);
    // Dismissal restores the composer opener.
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    expect(document.activeElement).toBe(draft);
    expect((draft as HTMLTextAreaElement).value).toBe("keep me");
  },
);

it("does not open from handled, composing or terminal-passthrough events", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  harness();
  await act(async () => {});
  const draft = screen.getByRole("textbox", { name: "Draft" });
  const base = { key: "t", code: "KeyT", ctrlKey: true };
  const handled = new KeyboardEvent("keydown", {
    ...base,
    bubbles: true,
    cancelable: true,
  });
  handled.preventDefault();
  draft.dispatchEvent(handled);
  fireEvent.keyDown(draft, { ...base, isComposing: true });
  fireEvent.keyDown(draft, { ...base, keyCode: 229 });
  const passthrough = document.createElement("div");
  passthrough.dataset.shortcuts = "passthrough";
  passthrough.tabIndex = 0;
  document.body.append(passthrough);
  passthrough.focus();
  fireEvent.keyDown(passthrough, base);
  passthrough.remove();
  expect(dialog().open).toBe(false);
});

it("disposes without restoring focus when the session departs and ignores the chord while inactive", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const { view, tree } = harness();
  await act(async () => {});
  const draft = screen.getByRole("textbox", { name: "Draft" });
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  expect(dialog().open).toBe(true);
  view.rerender(tree(false));
  expect(dialog().open).toBe(false);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  draft.focus();
  fireEvent.keyDown(draft, { key: "t", code: "KeyT", ctrlKey: true });
  expect(dialog().open).toBe(false);
  fireEvent.click(trigger);
  expect(dialog().open).toBe(false);
  view.unmount();
  expect(document.querySelector("dialog")).toBeNull();
});

it("retires the dialog when connection readiness is lost, blocks reopening, and stays closed on recovery", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const { view, tree, selected } = harness();
  await act(async () => {});
  const draft = screen.getByRole("textbox", { name: "Draft" });
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  expect(dialog().open).toBe(true);
  expect(trigger.getAttribute("aria-expanded")).toBe("true");
  // Same active Session, readiness lost: the modal closes without focus restore.
  const destination = screen.getByRole("button", { name: "Destination" });
  destination.focus();
  view.rerender(tree(true, false));
  expect(dialog().open).toBe(false);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(destination);
  expect((draft as HTMLTextAreaElement).value).toBe("keep me");
  // Neither the trigger nor the OpenTool chord opens it while unavailable.
  fireEvent.click(trigger);
  expect(dialog().open).toBe(false);
  draft.focus();
  fireEvent.keyDown(draft, { key: "t", code: "KeyT", ctrlKey: true });
  expect(dialog().open).toBe(false);
  // Recovery does not reopen; an explicit action does, and selection still
  // closes first and invokes the original callback once.
  view.rerender(tree(true, true));
  expect(dialog().open).toBe(false);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.keyDown(draft, { key: "t", code: "KeyT", ctrlKey: true });
  expect(dialog().open).toBe(true);
  fireEvent.click(screen.getByRole("menuitem", { name: "Files" }));
  expect(dialog().open).toBe(false);
  expect(selected).toHaveBeenCalledTimes(1);
  expect((draft as HTMLTextAreaElement).value).toBe("keep me");
});

it("lets a committed custom primary+T override win, explains it, and restores the default immediately", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const { publish } = harness(true, {
    "session-focus": { state: "binding", chord: { key: "t", shift: false } },
  });
  await act(async () => {});
  const trigger = screen.getByRole("button", { name: "Open tool" });
  expect(trigger.hasAttribute("aria-keyshortcuts")).toBe(false);
  const draft = screen.getByRole("textbox", { name: "Draft" });
  fireEvent.keyDown(draft, { key: "t", code: "KeyT", ctrlKey: true });
  expect(dialog().open).toBe(false);
  // Disabled and Shift+T overrides leave the default active.
  publish({ "session-focus": { state: "disabled" } });
  expect(trigger.getAttribute("aria-keyshortcuts")).toBe("Control+T");
  publish({
    "session-focus": { state: "binding", chord: { key: "t", shift: true } },
  });
  expect(trigger.getAttribute("aria-keyshortcuts")).toBe("Control+T");
  fireEvent.keyDown(draft, { key: "t", code: "KeyT", ctrlKey: true });
  expect(dialog().open).toBe(true);
});

it.each([
  ["en", "Open tool", "Close Open tool"],
  ["ko", "도구 열기", "도구 열기 닫기"],
])(
  "keeps a decorative icon and localized accessible trigger and dialog in %s",
  async (language, label, closeLabel) => {
    await act(() => i18n.changeLanguage(language));
    render(
      <SessionToolMenu active>
        <button role="menuitem">Files</button>
      </SessionToolMenu>,
    );
    const trigger = screen.getByRole("button", { name: label });
    expect(trigger.textContent).toBe("");
    expect(trigger.title).toBe(label);
    expect(trigger.getAttribute("aria-haspopup")).toBe("dialog");
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    const icon = trigger.querySelector("svg");
    expect(icon?.classList.contains("session-icon")).toBe(true);
    expect(icon?.getAttribute("aria-hidden")).toBe("true");
    expect(icon?.getAttribute("focusable")).toBe("false");
    expect(icon?.querySelector("path")).not.toBeNull();
    fireEvent.click(trigger);
    const modal = screen.getByRole("dialog", { name: label });
    expect(trigger.getAttribute("aria-controls")).toBe(modal.id);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("menu", { name: label })).not.toBeNull();
    expect(screen.getByRole("button", { name: closeLabel })).not.toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("menuitem"));
  },
);
