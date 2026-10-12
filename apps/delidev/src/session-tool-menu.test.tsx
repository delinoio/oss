// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { i18n } from "./localization";
import { ShortcutProvider, useShortcutHelp } from "./shortcut-provider";
import { ShortcutId, ShortcutPlatform, shortcutPlatform } from "./shortcuts";
import {
  ShortcutPreferenceProvider,
  type ShortcutPreferenceSnapshot,
  type ShortcutPreferenceBridge,
} from "./shortcut-preference-controller";
import { ShortcutOverrideState } from "./shortcut-preferences";
afterEach(async () => {
  await i18n.changeLanguage("en");
});
it("opens at the first enabled entry and owns arrows, Tab, Escape and backdrop dismissal without activating tools", () => {
  const selected = vi.fn(() => {
    expect(screen.queryByRole("dialog")).toBeNull();
    screen.getByRole("button", { name: "Destination" }).focus();
  });
  const view = render(
    <>
      <SessionToolMenu active>
        <button role="menuitem" disabled>
          Terminals
        </button>
        <button role="menuitem" onClick={selected}>
          Files
        </button>
        <button role="menuitem">Diagnostics</button>
      </SessionToolMenu>
      <button>Destination</button>
    </>,
  );
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  const entries = screen.getAllByRole("menuitem");
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "ArrowDown" });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Home" });
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "End" });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Tab" });
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Close Open tool" }),
  );
  fireEvent.keyDown(document.activeElement!, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Escape" });
  expect(document.activeElement).toBe(trigger);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(selected).not.toHaveBeenCalled();
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("menuitem", { name: "Files" }));
  expect(selected).toHaveBeenCalledOnce();
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Destination" }),
  );
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger);
  const destination = screen.getByRole("button", { name: "Destination" });
  destination.focus();
  fireEvent.click(screen.getByRole("dialog"), { clientX: -1, clientY: -1 });
  expect(document.activeElement).toBe(trigger);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger);
  view.rerender(
    <SessionToolMenu active={false}>
      <button role="menuitem">Files</button>
    </SessionToolMenu>,
  );
  expect(screen.queryByRole("menuitem")).toBeNull();
});

it.each([
  ["en", "Open tool"],
  ["ko", "도구 열기"],
])(
  "keeps a decorative icon and localized accessible trigger in %s",
  async (language, label) => {
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
    const menu = screen.getByRole("dialog", { name: label });
    expect(trigger.getAttribute("aria-controls")).toBe(menu.id);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(document.activeElement).toBe(screen.getByRole("menuitem"));
    fireEvent.keyDown(screen.getByRole("menuitem"), { key: "Tab" });
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
  },
);

it("opens one dialog from composer T and preserves input, modal fences and dismissal focus", () => {
  const selected = vi.fn();
  render(
    <ShortcutProvider>
      <input aria-label="Message" defaultValue="Retained draft" />
      <SessionToolMenu active>
        <button role="menuitem" onClick={selected}>
          Files
        </button>
      </SessionToolMenu>
    </ShortcutProvider>,
  );
  const input = screen.getByRole("textbox", { name: "Message" });
  const primary =
    shortcutPlatform() === ShortcutPlatform.Mac
      ? { metaKey: true }
      : { ctrlKey: true };
  input.focus();
  for (const flags of [
    { shiftKey: true },
    { altKey: true },
    { repeat: true },
    { isComposing: true },
  ]) {
    fireEvent.keyDown(input, { key: "t", code: "KeyT", ...primary, ...flags });
    expect(screen.queryByRole("dialog")).toBeNull();
  }
  fireEvent.keyDown(input, { key: "t", code: "KeyT", ...primary });
  const dialog = screen.getByRole("dialog", { name: "Open tool" });
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(input).toHaveProperty("value", "Retained draft");
  expect(selected).not.toHaveBeenCalled();
  fireEvent.keyDown(document.activeElement!, {
    key: "t",
    code: "KeyT",
    ...primary,
  });
  expect(screen.getByRole("dialog")).toBe(dialog);
  fireEvent.click(screen.getByRole("button", { name: "Close Open tool" }));
  expect(document.activeElement).toBe(input);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(selected).not.toHaveBeenCalled();
});

function HelpOpener() {
  const openHelp = useShortcutHelp();
  return <button onClick={openHelp}>Help opener</button>;
}

it("uses committed T suppression in dispatch, Help and trigger ARIA and restores live", async () => {
  let snapshot: ShortcutPreferenceSnapshot = {
    revision: 1,
    problem: null,
    overrides: {
      [ShortcutId.SearchFocus]: {
        state: ShortcutOverrideState.Binding,
        chord: { key: "t", shift: false },
      },
    },
  };
  let publish: (value: ShortcutPreferenceSnapshot) => void = () => {};
  const bridge: ShortcutPreferenceBridge = {
    read: async () => snapshot,
    update: vi.fn(),
    subscribe: async (changed) => {
      publish = changed;
      return () => {};
    },
  };
  render(
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <HelpOpener />
        <input aria-label="Message" defaultValue="Retained draft" />
        <SessionToolMenu active>
          <button role="menuitem">Files</button>
        </SessionToolMenu>
      </ShortcutProvider>
    </ShortcutPreferenceProvider>,
  );
  await act(async () => {});
  const trigger = screen.getByRole("button", { name: "Open tool" });
  expect(trigger.hasAttribute("aria-keyshortcuts")).toBe(false);
  const input = screen.getByRole("textbox", { name: "Message" });
  input.focus();
  const primary =
    shortcutPlatform() === ShortcutPlatform.Mac
      ? { metaKey: true }
      : { ctrlKey: true };
  fireEvent.keyDown(input, { key: "t", code: "KeyT", ...primary });
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  expect(
    screen.getByText(
      "The default tool shortcut is disabled because a saved custom shortcut uses Command or Control + T.",
    ),
  ).toBeTruthy();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  await act(async () => {
    snapshot = { revision: 2, problem: null, overrides: {} };
    publish(snapshot);
  });
  expect(trigger.getAttribute("aria-keyshortcuts")).toContain("+T");
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  fireEvent.keyDown(document.activeElement!, {
    key: "t",
    code: "KeyT",
    ...primary,
  });
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(screen.getByRole("dialog", { name: "Open tool" })).toBeTruthy();
  expect(
    screen.queryByRole("dialog", { name: "Keyboard shortcuts" }),
  ).toBeNull();
  expect(document.activeElement).toBe(
    screen.getByRole("menuitem", { name: "Files" }),
  );
  expect(bridge.update).not.toHaveBeenCalled();
});
