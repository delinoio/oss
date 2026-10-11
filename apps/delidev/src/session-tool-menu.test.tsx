// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useEffect } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { i18n } from "./localization";
import {
  ShortcutProvider,
  useShortcutHelp,
  useShortcuts,
} from "./shortcut-provider";
import { ShortcutSettings } from "./shortcut-settings";
import {
  ShortcutPreferenceProvider,
  type ShortcutPreferenceBridge,
} from "./shortcut-preference-controller";
import { ShortcutId, ShortcutInput, ShortcutScope } from "./shortcuts";
import {
  ShortcutOverrideState,
  type ShortcutOverrides,
} from "./shortcut-preferences";

it("contains focus, skips disabled rows and closes before the original callback once", () => {
  const selected = vi.fn(() => {
    expect(screen.queryByRole("dialog", { name: "Open tool" })).toBeNull();
    screen.getByRole("button", { name: "Destination" }).focus();
  });
  render(
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
  fireEvent.click(entries[0]);
  expect(selected).not.toHaveBeenCalled();
  fireEvent.keyDown(entries[1], { key: "ArrowDown" });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Home" });
  expect(document.activeElement).toBe(entries[1]);
  fireEvent.keyDown(entries[1], { key: "End" });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Tab" });
  const close = screen.getByRole("button", { name: "Close Open tool" });
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(entries[2]);
  fireEvent.keyDown(entries[2], { key: "Escape" });
  expect(document.activeElement).toBe(trigger);
  expect(selected).not.toHaveBeenCalled();
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("menuitem", { name: "Files" }));
  expect(selected).toHaveBeenCalledOnce();
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Destination" }),
  );
  fireEvent.click(entries[1]);
  expect(selected).toHaveBeenCalledOnce();
});

it.each([
  ["en", "Open tool", "Close Open tool"],
  ["ko", "도구 열기", "도구 열기 닫기"],
])(
  "shares localized dialog/trigger identity and dismissal in %s",
  async (language, label, closeName) => {
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
    expect(trigger.querySelector("svg")?.getAttribute("aria-hidden")).toBe(
      "true",
    );
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: label });
    expect(trigger.getAttribute("aria-controls")).toBe(dialog.id);
    expect(document.activeElement).toBe(screen.getByRole("menuitem"));
    fireEvent.click(within(dialog).getByRole("button", { name: closeName }));
    expect(document.activeElement).toBe(trigger);
    fireEvent.click(trigger);
    fireEvent.click(dialog, { clientX: -10, clientY: -10 });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(trigger);
  },
);

it("preserves mounted controllers and disposes the modal on departure without stale focus", () => {
  const mounted = vi.fn(),
    disposed = vi.fn(),
    selected = vi.fn();
  function Controller() {
    useEffect(() => {
      mounted();
      return disposed;
    }, []);
    return (
      <button role="menuitem" onClick={selected}>
        Files
      </button>
    );
  }
  const view = render(
    <SessionToolMenu active>
      <Controller />
    </SessionToolMenu>,
  );
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  fireEvent.keyDown(screen.getByRole("menuitem"), { key: "Escape" });
  fireEvent.click(trigger);
  expect(mounted).toHaveBeenCalledOnce();
  expect(disposed).not.toHaveBeenCalled();
  const stale = screen.getByRole("menuitem");
  view.rerender(
    <SessionToolMenu active={false}>
      <Controller />
    </SessionToolMenu>,
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).not.toBe(trigger);
  fireEvent.click(stale);
  expect(selected).not.toHaveBeenCalled();
  view.unmount();
  expect(disposed).toHaveBeenCalledOnce();
});

function ShortcutFixture({
  active = true,
  custom = () => {},
}: {
  active?: boolean;
  custom?: () => void;
}) {
  const help = useShortcutHelp();
  useShortcuts([
    {
      id: ShortcutId.NewSession,
      scope: ShortcutScope.Global,
      label: "shortcuts.newSession",
      bindings: [{ key: "n", primary: true, shift: true }],
      input: ShortcutInput.Allow,
      run: custom,
    },
  ]);
  return (
    <>
      <input aria-label="Draft" defaultValue="original draft" />
      <button onClick={help}>Help</button>
      <SessionToolMenu active={active}>
        <button role="menuitem">Files</button>
      </SessionToolMenu>
    </>
  );
}

it.each(["MacIntel", "Win32", "Linux x86_64"])(
  "opens from ordinary input on %s while preserving keyboard fences",
  (platform) => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const modifiers =
      platform === "MacIntel" ? { metaKey: true } : { ctrlKey: true };
    render(
      <StrictMode>
        <ShortcutProvider>
          <ShortcutFixture />
        </ShortcutProvider>
      </StrictMode>,
    );
    const input = screen.getByRole("textbox", { name: "Draft" });
    input.focus();
    for (const flags of [
      { shiftKey: true },
      { altKey: true },
      { repeat: true },
      { isComposing: true },
      { keyCode: 229 },
      platform === "MacIntel"
        ? { metaKey: false, ctrlKey: true }
        : { ctrlKey: false, metaKey: true },
    ]) {
      fireEvent.keyDown(input, { key: "t", ...modifiers, ...flags });
      expect(screen.queryByRole("dialog")).toBeNull();
    }
    fireEvent.keyDown(input, { key: "t", ...modifiers });
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
    expect((input as HTMLInputElement).value).toBe("original draft");
    fireEvent.keyDown(screen.getByRole("menuitem"), { key: "Escape" });
    expect(document.activeElement).toBe(input);
    input.setAttribute("data-shortcuts", "passthrough");
    fireEvent.keyDown(input, { key: "t", ...modifiers });
    expect(screen.queryByRole("dialog")).toBeNull();
    input.removeAttribute("data-shortcuts");
    const event = new KeyboardEvent("keydown", {
      key: "t",
      ...modifiers,
      bubbles: true,
      cancelable: true,
    });
    event.preventDefault();
    fireEvent(input, event);
    expect(screen.queryByRole("dialog")).toBeNull();
  },
);

it("keeps committed custom T priority consistent across dispatch, Help, Settings and ARIA then restores the default", async () => {
  let changed: ((value: unknown) => void) | undefined;
  const overrides: ShortcutOverrides = {
    [ShortcutId.NewSession]: {
      state: ShortcutOverrideState.Binding,
      chord: { key: "t", shift: false },
    },
  };
  const bridge: ShortcutPreferenceBridge = {
    read: vi.fn(async () => ({ revision: 1, overrides, problem: null })),
    update: vi.fn(),
    subscribe: async (listener) => {
      changed = listener;
      return () => {};
    },
  };
  const custom = vi.fn();
  render(
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <ShortcutFixture custom={custom} />
        <ShortcutSettings />
      </ShortcutProvider>
    </ShortcutPreferenceProvider>,
  );
  const trigger = screen.getByRole("button", { name: "Open tool" });
  await waitFor(() =>
    expect(trigger.getAttribute("aria-keyshortcuts")).toBe(""),
  );
  expect(trigger.getAttribute("aria-describedby")).toBeTruthy();
  expect(
    screen
      .getAllByText("Open tool")
      .find((node) => node.tagName === "DT")
      ?.closest("div")
      ?.querySelector("dd")?.textContent,
  ).toBe("Disabled");
  const input = screen.getByRole("textbox", { name: "Draft" });
  input.focus();
  fireEvent.keyDown(input, { key: "t", ctrlKey: true });
  expect(custom).toHaveBeenCalledOnce();
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Help" }));
  const help = screen.getByRole("dialog", { name: "Keyboard shortcuts" });
  expect(
    within(help).getByText(
      "Inactive because a custom shortcut uses this key combination.",
    ),
  ).toBeTruthy();
  fireEvent.keyDown(help, { key: "Escape" });
  await act(async () => {
    changed?.({ revision: 2, overrides: {}, problem: null });
  });
  await waitFor(() =>
    expect(trigger.getAttribute("aria-keyshortcuts")).toBe("Control+T"),
  );
  expect(trigger.getAttribute("aria-describedby")).toBeNull();
  input.focus();
  fireEvent.keyDown(input, { key: "t", ctrlKey: true });
  expect(screen.getByRole("dialog", { name: "Open tool" })).toBeTruthy();
  expect(bridge.update).not.toHaveBeenCalled();
});
