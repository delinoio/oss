// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useRef } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { i18n } from "./localization";
import {
  ShortcutProvider,
  useShortcutHelp,
  useHeldShortcutHelp,
  useShortcutSurface,
  useShortcuts,
} from "./shortcut-provider";
import {
  globalShortcutBindings,
  sessionShortcutBindings,
  ShortcutId,
  ShortcutInput,
  ShortcutScope,
} from "./shortcuts";
import {
  ShortcutPreferenceProvider,
  useShortcutPreferences,
  type ShortcutPreferenceBridge,
} from "./shortcut-preference-controller";
import { Surface } from "./surface";

function Consumer({
  surface = Surface.Sessions,
  enabled = false,
  run = () => {},
  opener = true,
}: {
  surface?: Surface;
  enabled?: boolean;
  run?: () => void;
  opener?: boolean;
}) {
  useShortcutSurface(surface);
  const openHelp = useShortcutHelp(),
    holdHelp = useHeldShortcutHelp(),
    input = useRef<HTMLInputElement>(null);
  useShortcuts([
    {
      id: ShortcutId.Help,
      scope: ShortcutScope.Global,
      label: "shortcuts.help",
      bindings: [{ key: "?" }],
      helpKeydown: holdHelp,
    },
    {
      id: ShortcutId.SessionSend,
      scope: Surface.Sessions,
      label: "shortcuts.queueMessage",
      bindings: [{ key: "Enter", primary: true }],
      target: input,
      input: ShortcutInput.Target,
      enabled,
      unavailableReason: "shortcuts.messageRequired",
      run,
    },
  ]);
  return (
    <main id="main" tabIndex={-1}>
      {opener ? <button onClick={openHelp}>Help opener</button> : null}
      <input ref={input} aria-label="Message" />
    </main>
  );
}
it("shows current definitions/reasons, contains focus, and updates language without reopening", async () => {
  const show = vi.spyOn(HTMLDialogElement.prototype, "showModal");
  render(
    <ShortcutProvider>
      <Consumer />
    </ShortcutProvider>,
  );
  const opener = screen.getByRole("button", { name: "Help opener" });
  opener.focus();
  fireEvent.keyDown(opener, { key: "?", shiftKey: true });
  const dialog = screen.getByRole("dialog", { name: "Keyboard shortcuts" }),
    close = within(dialog).getByRole("button", {
      name: "Close keyboard shortcuts",
    });
  expect(document.activeElement).toBe(close);
  expect(within(dialog).getByText("Add message to queue")).toBeTruthy();
  expect(within(dialog).getByText("Enter a message first.")).toBeTruthy();
  fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(close, { key: "Tab" });
  expect(document.activeElement).toBe(close);
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("dialog", { name: "키보드 단축키" })).toBe(dialog);
  expect(document.activeElement).toBe(close);
  expect(show).toHaveBeenCalledTimes(1);
  fireEvent.keyDown(close, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
});
it("falls back to main when the original opener is removed", () => {
  const view = render(
    <ShortcutProvider>
      <Consumer />
    </ShortcutProvider>,
  );
  const opener = screen.getByRole("button", { name: "Help opener" });
  opener.focus();
  fireEvent.click(opener);
  view.rerender(
    <ShortcutProvider>
      <Consumer opener={false} />
    </ShortcutProvider>,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Close keyboard shortcuts" }),
  );
  expect(document.activeElement).toBe(document.getElementById("main"));
});
it("restores main after help opens without a usable focused control", () => {
  render(
    <ShortcutProvider>
      <Consumer />
    </ShortcutProvider>,
  );
  expect(document.activeElement).toBe(document.body);
  fireEvent.keyDown(document.body, { key: "?", shiftKey: true });
  fireEvent.click(
    screen.getByRole("button", { name: "Close keyboard shortcuts" }),
  );
  expect(document.activeElement).toBe(document.getElementById("main"));
});
it("has no inactive screen entries and neither typing nor another modal opens help", () => {
  render(
    <ShortcutProvider>
      <Consumer surface={Surface.Usage} />
    </ShortcutProvider>,
  );
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "?" });
  expect(screen.queryByRole("dialog")).toBeNull();
  const modal = document.createElement("dialog");
  modal.showModal();
  document.body.append(modal);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  expect(screen.queryByText("Keyboard shortcuts")).toBeNull();
  modal.remove();
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  const help = screen.getByRole("dialog", { name: "Keyboard shortcuts" });
  expect(
    within(help).getByText("No screen-specific shortcuts are registered."),
  ).toBeTruthy();
  expect(within(help).queryByText("Add message to queue")).toBeNull();
});
it("Strict Mode dispatches once with the latest guards/callback and connection replacement disposes help", () => {
  const first = vi.fn(),
    second = vi.fn();
  const view = render(
    <StrictMode>
      <ShortcutProvider key="first">
        <Consumer enabled run={first} />
      </ShortcutProvider>
    </StrictMode>,
  );
  fireEvent.keyDown(screen.getByRole("textbox"), {
    key: "Enter",
    ctrlKey: true,
  });
  expect(first).toHaveBeenCalledTimes(1);
  view.rerender(
    <StrictMode>
      <ShortcutProvider key="first">
        <Consumer enabled run={second} />
      </ShortcutProvider>
    </StrictMode>,
  );
  fireEvent.keyDown(screen.getByRole("textbox"), {
    key: "Enter",
    ctrlKey: true,
  });
  expect(second).toHaveBeenCalledTimes(1);
  expect(first).toHaveBeenCalledTimes(1);
  view.rerender(
    <StrictMode>
      <ShortcutProvider key="first">
        <Consumer run={second} />
      </ShortcutProvider>
    </StrictMode>,
  );
  fireEvent.keyDown(screen.getByRole("textbox"), {
    key: "Enter",
    ctrlKey: true,
  });
  expect(second).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  view.rerender(
    <StrictMode>
      <ShortcutProvider key="replacement">
        <Consumer enabled run={second} />
      </ShortcutProvider>
    </StrictMode>,
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.keyDown(screen.getByRole("textbox"), {
    key: "Enter",
    ctrlKey: true,
  });
  expect(second).toHaveBeenCalledTimes(2);
  view.unmount();
  fireEvent.keyDown(document.body, { key: "?" });
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("checks all registered input actions before a local form handler can execute", () => {
  const first = vi.fn(),
    second = vi.fn(),
    warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  function ConflictingInput() {
    const target = useRef<HTMLInputElement>(null);
    const local = useShortcuts([
      {
        id: ShortcutId.SessionSend,
        scope: Surface.Sessions,
        label: "shortcuts.queueMessage",
        bindings: [{ key: "Enter", primary: true }],
        target,
        input: ShortcutInput.Target,
        run: first,
      },
    ]);
    useShortcuts([
      {
        id: ShortcutId.SessionNewline,
        scope: Surface.Sessions,
        label: "shortcuts.newline",
        bindings: [{ key: "Enter", primary: true }],
        target,
        input: ShortcutInput.Target,
        run: second,
      },
    ]);
    return (
      <input
        ref={target}
        onKeyDown={local.onKeyDown}
        aria-label="Conflicting message"
      />
    );
  }
  render(
    <ShortcutProvider>
      <ConflictingInput />
    </ShortcutProvider>,
  );
  fireEvent.keyDown(screen.getByRole("textbox"), {
    key: "Enter",
    ctrlKey: true,
  });
  expect(first).not.toHaveBeenCalled();
  expect(second).not.toHaveBeenCalled();
  expect(warn).toHaveBeenCalledExactlyOnceWith({
    event: "delidev_shortcut_conflict",
    scope: Surface.Sessions,
    actionIds: [ShortcutId.SessionSend, ShortcutId.SessionNewline],
  });
});

function HelpActions({
  surface = Surface.Sessions,
  enabled = true,
  conflict = false,
  run,
}: {
  surface?: Surface;
  enabled?: boolean;
  conflict?: boolean;
  run: () => void;
}) {
  useShortcutSurface(surface);
  const input = useRef<HTMLInputElement>(null),
    openHelp = useShortcutHelp(),
    holdHelp = useHeldShortcutHelp();
  const focusId =
    surface === Surface.Search
      ? ShortcutId.SearchFocus
      : surface === Surface.NewSession
        ? ShortcutId.NewSessionFocus
        : ShortcutId.SessionFocus;
  useShortcuts([
    {
      id: ShortcutId.Help,
      scope: ShortcutScope.Global,
      label: "shortcuts.help",
      bindings: [{ key: "?" }],
      helpKeydown: holdHelp,
    },
    {
      id: ShortcutId.OpenTerminals,
      scope: Surface.Sessions,
      label: "shortcuts.openTerminals",
      bindings: sessionShortcutBindings[ShortcutId.OpenTerminals],
      input: ShortcutInput.Allow,
      run: () => {
        run();
        input.current?.focus();
      },
    },
    {
      id: ShortcutId.OpenSettings,
      scope: ShortcutScope.Global,
      label: "shortcuts.openSettings",
      bindings: globalShortcutBindings[ShortcutId.OpenSettings],
      input: ShortcutInput.Allow,
      run: () => {
        run();
        input.current?.focus();
      },
    },
    {
      id: ShortcutId.NewSession,
      scope: ShortcutScope.Global,
      label: "shortcuts.newSession",
      bindings: [{ key: "n", primary: true, shift: true }],
      input: ShortcutInput.Allow,
      run,
    },
    {
      id: focusId,
      scope: surface,
      label: "shortcuts.focusMessage",
      bindings: [{ key: "i", primary: true }],
      enabled,
      input: ShortcutInput.Allow,
      run: () => {
        run();
        input.current?.focus();
      },
    },
    {
      id: ShortcutId.SessionSend,
      scope: surface,
      label: "shortcuts.queueMessage",
      bindings: [
        { key: "Enter", primary: true },
        { key: "Enter" },
        { key: "Enter", shift: true },
      ],
      target: input,
      input: ShortcutInput.Target,
      run,
    },
    ...(conflict
      ? [
          {
            id: ShortcutId.SearchSubmit,
            scope: surface,
            label: "shortcuts.searchSubmit" as const,
            bindings: [{ key: "i", primary: true }],
            run,
          },
        ]
      : []),
  ]);
  return (
    <main id="main" tabIndex={-1}>
      <button onClick={openHelp}>Help opener</button>
      <input ref={input} aria-label="Destination" />
    </main>
  );
}
it.each([Surface.Sessions, Surface.NewSession, Surface.Search])(
  "closes help before the %s input focus callback and preserves destination focus",
  async (surface) => {
    const run = vi.fn(() =>
      expect(document.querySelector(".shortcut-help")).toBeNull(),
    );
    render(
      <StrictMode>
        <ShortcutProvider>
          <HelpActions surface={surface} run={run} />
        </ShortcutProvider>
      </StrictMode>,
    );
    const opener = screen.getByRole("button", { name: "Help opener" });
    opener.focus();
    fireEvent.click(opener);
    fireEvent.keyDown(
      screen.getByRole("button", { name: "Close keyboard shortcuts" }),
      { key: "i", ctrlKey: true },
    );
    expect(run).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).toBeNull();
    await act(async () => {});
    expect(document.activeElement).toBe(
      screen.getByRole("textbox", { name: "Destination" }),
    );
  },
);
it.each(["MacIntel", "Win32", "Linux x86_64"])(
  "runs New session once after closing help on %s",
  (platform) => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const run = vi.fn(() =>
      expect(document.querySelector(".shortcut-help")).toBeNull(),
    );
    const view = render(
      <StrictMode>
        <ShortcutProvider>
          <HelpActions run={run} />
        </ShortcutProvider>
      </StrictMode>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    fireEvent.keyDown(
      screen.getByRole("button", { name: "Close keyboard shortcuts" }),
      {
        key: "n",
        shiftKey: true,
        metaKey: platform === "MacIntel",
        ctrlKey: platform !== "MacIntel",
      },
    );
    expect(run).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).toBeNull();
    view.unmount();
    fireEvent.keyDown(document.body, {
      key: "n",
      shiftKey: true,
      ctrlKey: true,
    });
    expect(run).toHaveBeenCalledTimes(1);
  },
);
it.each([false, true])(
  "keeps help open for a disabled or conflicting action (conflict %s)",
  (conflict) => {
    const run = vi.fn();
    vi.spyOn(console, "warn").mockImplementation(() => {});
    render(
      <ShortcutProvider>
        <HelpActions enabled={conflict} conflict={conflict} run={run} />
      </ShortcutProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    const help = screen.getByRole("dialog"),
      close = screen.getByRole("button", { name: "Close keyboard shortcuts" });
    expect(fireEvent.keyDown(close, { key: "i", ctrlKey: true })).toBe(false);
    expect(run).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBe(help);
    expect(document.activeElement).toBe(close);
  },
);
it("preserves help for excluded events, repeated help and background input actions", () => {
  const run = vi.fn(),
    show = vi.spyOn(HTMLDialogElement.prototype, "showModal");
  render(
    <ShortcutProvider>
      <HelpActions run={run} />
    </ShortcutProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  const help = screen.getByRole("dialog"),
    close = screen.getByRole("button", { name: "Close keyboard shortcuts" });
  for (const options of [
    { isComposing: true },
    { keyCode: 229 },
    { repeat: true },
    { altKey: true },
    { ctrlKey: false, metaKey: true },
  ])
    expect(
      fireEvent.keyDown(close, { key: "i", ctrlKey: true, ...options }),
    ).toBe(true);
  const handled = new KeyboardEvent("keydown", {
    key: "i",
    ctrlKey: true,
    bubbles: true,
    cancelable: true,
  });
  handled.preventDefault();
  fireEvent(close, handled);
  const altGraph = new KeyboardEvent("keydown", {
    key: "i",
    ctrlKey: true,
    bubbles: true,
    cancelable: true,
  });
  vi.spyOn(altGraph, "getModifierState").mockImplementation(
    (key) => key === "AltGraph",
  );
  fireEvent(close, altGraph);
  expect(altGraph.defaultPrevented).toBe(false);
  for (const options of [{}, { ctrlKey: true }, { shiftKey: true }])
    fireEvent.keyDown(close, { key: "Enter", ...options });
  fireEvent.keyDown(close, { key: "?" });
  expect(run).not.toHaveBeenCalled();
  expect(screen.getByRole("dialog")).toBe(help);
  expect(show).toHaveBeenCalledTimes(1);
  const other = document.createElement("dialog");
  other.showModal();
  document.body.append(other);
  fireEvent.keyDown(close, { key: "i", ctrlKey: true });
  expect(run).not.toHaveBeenCalled();
  expect(screen.getAllByRole("dialog")).toHaveLength(2);
  other.remove();
});

it("lists native window bindings as read-only help without adding dispatch actions", () => {
  const run = vi.fn();
  render(
    <ShortcutProvider>
      <Consumer enabled run={run} />
    </ShortcutProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  const dialog = screen.getByRole("dialog");
  expect(
    Array.from(
      within(dialog)
        .getByText("New Window")
        .closest("div")!
        .querySelectorAll("kbd"),
      (node) => node.textContent,
    ),
  ).toEqual(["Ctrl", "N"]);
  expect(within(dialog).queryByText("Close Window")).toBeNull();
  expect(within(dialog).queryByText("Close tab")).toBeNull();
  expect(run).not.toHaveBeenCalled();
});

it.each(["Slash", "IntlRo", "", "Unidentified"])(
  "retires only the original held key (%s)",
  (code) => {
    const show = vi.spyOn(HTMLDialogElement.prototype, "showModal");
    render(
      <ShortcutProvider>
        <Consumer />
      </ShortcutProvider>,
    );
    const opener = screen.getByRole("button", { name: "Help opener" });
    opener.focus();
    fireEvent.keyDown(opener, { key: "?", code, shiftKey: code === "Slash" });
    const dialog = screen.getByRole("dialog");
    fireEvent.keyDown(dialog, { key: "?", code, repeat: true });
    fireEvent.keyUp(dialog, { key: "Shift", code: "ShiftLeft" });
    fireEvent.keyUp(dialog, { key: "a", code: "KeyA" });
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect(show).toHaveBeenCalledTimes(1);
    expect(
      fireEvent.keyUp(dialog, { key: code === "Slash" ? "/" : "?", code }),
    ).toBe(true);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(opener);
  },
);
it.each(["blur", "hidden"])(
  "retires held Help on %s without reopening on return",
  (cause) => {
    render(
      <ShortcutProvider>
        <Consumer />
      </ShortcutProvider>,
    );
    const opener = screen.getByRole("button", { name: "Help opener" });
    fireEvent.keyDown(opener, { key: "?", code: "Slash" });
    if (cause === "blur") fireEvent(window, new Event("blur"));
    else {
      vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
      fireEvent(document, new Event("visibilitychange"));
    }
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent(window, new Event("focus"));
    fireEvent(document, new Event("visibilitychange"));
    fireEvent.keyDown(opener, { key: "?", code: "Slash", repeat: true });
    expect(screen.queryByRole("dialog")).toBeNull();
  },
);
it.each(["Escape", "Close", "action"])(
  "early %s dismissal blocks the same press and release preserves destination focus",
  (kind) => {
    const run = vi.fn();
    render(
      <ShortcutProvider>
        <HelpActions run={run} />
      </ShortcutProvider>,
    );
    const opener = screen.getByRole("button", { name: "Help opener" });
    opener.focus();
    fireEvent.keyDown(opener, { key: "?", code: "Slash" });
    const close = screen.getByRole("button", {
      name: "Close keyboard shortcuts",
    });
    if (kind === "Escape") fireEvent.keyDown(close, { key: "Escape" });
    else if (kind === "Close") fireEvent.click(close);
    else fireEvent.keyDown(close, { key: "i", ctrlKey: true });
    expect(screen.queryByRole("dialog")).toBeNull();
    const destination = screen.getByRole("textbox");
    destination.focus();
    fireEvent.keyDown(opener, { key: "?", code: "Slash" });
    fireEvent.keyDown(opener, { key: "?", code: "Slash", repeat: true });
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.keyUp(document, { key: "/", code: "Slash" });
    expect(document.activeElement).toBe(destination);
    expect(run).toHaveBeenCalledTimes(kind === "action" ? 1 : 0);
    fireEvent.keyDown(opener, { key: "?", code: "Slash" });
    expect(screen.getByRole("dialog")).toBeTruthy();
  },
);
it("keeps button Help persistent through keyboard release, blur and hiding", () => {
  render(
    <ShortcutProvider>
      <Consumer />
    </ShortcutProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
  const dialog = screen.getByRole("dialog");
  fireEvent.keyDown(dialog, { key: "?", code: "Slash" });
  fireEvent.keyUp(dialog, { key: "/", code: "Slash" });
  fireEvent(window, new Event("blur"));
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
  fireEvent(document, new Event("visibilitychange"));
  expect(screen.getByRole("dialog")).toBe(dialog);
  fireEvent.keyDown(
    screen.getByRole("button", { name: "Close keyboard shortcuts" }),
    { key: "Escape" },
  );
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("holds the committed custom Help chord and preserves it through a preference update", async () => {
  let changed: (value: unknown) => void = () => {};
  const bridge: ShortcutPreferenceBridge = {
    read: async () => ({
      revision: 1,
      problem: null,
      overrides: {
        help: { state: "binding", chord: { key: "j", shift: false } },
      },
    }),
    update: vi.fn(),
    subscribe: async (listener) => {
      changed = listener;
      return () => {};
    },
  };
  render(
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <Consumer />
      </ShortcutProvider>
    </ShortcutPreferenceProvider>,
  );
  await act(async () => {});
  const opener = screen.getByRole("button", { name: "Help opener" });
  fireEvent.keyDown(opener, { key: "?", code: "Slash" });
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.keyDown(opener, { key: "j", code: "KeyJ", ctrlKey: true });
  const dialog = screen.getByRole("dialog");
  act(() =>
    changed({
      revision: 2,
      problem: null,
      overrides: { help: { state: "disabled" } },
    }),
  );
  expect(screen.getByRole("dialog")).toBe(dialog);
  fireEvent.keyUp(dialog, { key: "Control", code: "ControlLeft" });
  expect(screen.getByRole("dialog")).toBe(dialog);
  fireEvent.keyUp(dialog, { key: "j", code: "KeyJ" });
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.keyDown(opener, { key: "j", code: "KeyJ", ctrlKey: true });
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("connection replacement disposes held ownership and its release listeners", () => {
  const view = render(
    <StrictMode>
      <ShortcutProvider key="old">
        <Consumer />
      </ShortcutProvider>
    </StrictMode>,
  );
  fireEvent.keyDown(screen.getByRole("button", { name: "Help opener" }), {
    key: "?",
    code: "Slash",
  });
  expect(screen.getByRole("dialog")).toBeTruthy();
  view.rerender(
    <StrictMode>
      <ShortcutProvider key="new">
        <Consumer />
      </ShortcutProvider>
    </StrictMode>,
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.keyUp(document, { key: "/", code: "Slash" });
  fireEvent.keyDown(screen.getByRole("button", { name: "Help opener" }), {
    key: "?",
    code: "IntlRo",
  });
  expect(screen.getByRole("dialog")).toBeTruthy();
  view.unmount();
  fireEvent.keyUp(document, { key: "?", code: "IntlRo" });
  fireEvent.keyDown(document.body, { key: "?" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("discloses the exact external browser numeric exception without disabling ordinary tab shortcuts", () => {
  function TabHelp() {
    const open = useShortcutHelp();
    useShortcutSurface(Surface.Sessions);
    useShortcuts([
      {
        id: ShortcutId.SessionTab1,
        scope: Surface.Sessions,
        label: "shortcuts.tab1",
        bindings: [{ key: "1", primary: true }],
        terminal: true,
        run: () => {},
      },
    ]);
    return <button onClick={open}>Tab help</button>;
  }
  render(
    <ShortcutProvider>
      <TabHelp />
    </ShortcutProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Tab help" }));
  expect(
    screen.getByText(
      "On Windows and Linux, use the tab bar while an external browser page has focus. Number shortcuts work elsewhere in the app.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("Select tab 1")).toBeTruthy();
});

it.each(["MacIntel", "Win32", "Linux x86_64"])(
  "closes Help before Open Settings and preserves its destination focus on %s",
  async (platform) => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const run = vi.fn(() =>
      expect(document.querySelector(".shortcut-help")).toBeNull(),
    );
    render(
      <ShortcutProvider>
        <HelpActions run={run} />
      </ShortcutProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    const dialog = screen.getByRole("dialog", { name: "Keyboard shortcuts" });
    expect(within(dialog).getByText("Open Settings")).toBeTruthy();
    fireEvent.keyDown(within(dialog).getByRole("button"), {
      key: ",",
      metaKey: platform === "MacIntel",
      ctrlKey: platform !== "MacIntel",
    });
    expect(run).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).toBeNull();
    await act(async () => {});
    expect(document.activeElement).toBe(
      screen.getByRole("textbox", { name: "Destination" }),
    );
  },
);

it.each([
  ["en", "MacIntel", "Open terminals"],
  ["en", "Win32", "Open terminals"],
  ["ko", "MacIntel", "터미널 열기"],
  ["ko", "Linux x86_64", "터미널 열기"],
])(
  "closes %s Help before opening terminals on %s and keeps destination focus",
  async (language, platform, label) => {
    await act(() => i18n.changeLanguage(language));
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const run = vi.fn(() =>
      expect(document.querySelector(".shortcut-help")).toBeNull(),
    );
    render(
      <ShortcutProvider>
        <HelpActions run={run} />
      </ShortcutProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(label)).toBeTruthy();
    fireEvent.keyDown(within(dialog).getByRole("button"), {
      key: "`",
      metaKey: platform === "MacIntel",
      ctrlKey: platform !== "MacIntel",
    });
    expect(run).toHaveBeenCalledOnce();
    await act(async () => {});
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("textbox", { name: "Destination" }),
    );
    await act(() => i18n.changeLanguage("en"));
  },
);

it("marks the inactive default Open tool shortcut in Help while a committed custom primary+T binding exists", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const run = vi.fn();
  function ToolProbe() {
    useShortcutSurface(Surface.Sessions);
    const openHelp = useShortcutHelp();
    useShortcuts([
      {
        id: ShortcutId.OpenTool,
        scope: Surface.Sessions,
        label: "session.openTool",
        bindings: sessionShortcutBindings[ShortcutId.OpenTool],
        input: ShortcutInput.Allow,
        run,
      },
    ]);
    return <button onClick={openHelp}>Help opener</button>;
  }
  let changed: (value: unknown) => void = () => {};
  const bridge: ShortcutPreferenceBridge = {
    read: async () => ({
      revision: 1,
      problem: null,
      overrides: {
        "session-focus": {
          state: "binding",
          chord: { key: "t", shift: false },
        },
      },
    }),
    update: vi.fn(),
    subscribe: async (listener) => {
      changed = listener;
      return () => {};
    },
  };
  render(
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <ToolProbe />
      </ShortcutProvider>
    </ShortcutPreferenceProvider>,
  );
  await act(async () => {});
  const opener = screen.getByRole("button", { name: "Help opener" });
  fireEvent.keyDown(opener, { key: "t", code: "KeyT", ctrlKey: true });
  expect(run).not.toHaveBeenCalled();
  fireEvent.click(opener);
  const row = within(screen.getByRole("dialog", { name: "Keyboard shortcuts" }))
    .getByText("Open tool")
    .closest("div")!;
  expect(within(row).getByText("Disabled")).toBeTruthy();
  expect(within(row).getByText(/custom shortcut uses/)).toBeTruthy();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  act(() => changed({ revision: 2, problem: null, overrides: {} }));
  fireEvent.keyDown(opener, { key: "t", code: "KeyT", ctrlKey: true });
  expect(run).toHaveBeenCalledOnce();
});

it("marks the inactive default Open Files shortcut in Help while a committed custom primary+E binding exists", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const run = vi.fn();
  function FilesProbe() {
    useShortcutSurface(Surface.Sessions);
    const openHelp = useShortcutHelp();
    useShortcuts([
      {
        id: ShortcutId.OpenFiles,
        scope: Surface.Sessions,
        label: "shortcuts.openFiles",
        bindings: sessionShortcutBindings[ShortcutId.OpenFiles],
        input: ShortcutInput.Allow,
        run,
      },
    ]);
    return <button onClick={openHelp}>Help opener</button>;
  }
  let changed: (value: unknown) => void = () => {};
  const bridge: ShortcutPreferenceBridge = {
    read: async () => ({
      revision: 1,
      problem: null,
      overrides: {
        "session-focus": {
          state: "binding",
          chord: { key: "e", shift: false },
        },
      },
    }),
    update: vi.fn(),
    subscribe: async (listener) => {
      changed = listener;
      return () => {};
    },
  };
  render(
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <FilesProbe />
      </ShortcutProvider>
    </ShortcutPreferenceProvider>,
  );
  await act(async () => {});
  const opener = screen.getByRole("button", { name: "Help opener" });
  fireEvent.keyDown(opener, { key: "e", code: "KeyE", ctrlKey: true });
  expect(run).not.toHaveBeenCalled();
  fireEvent.click(opener);
  const row = within(screen.getByRole("dialog", { name: "Keyboard shortcuts" }))
    .getByText("Open Files")
    .closest("div")!;
  expect(within(row).getByText("Disabled")).toBeTruthy();
  expect(within(row).getByText(/custom shortcut uses/)).toBeTruthy();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  act(() => changed({ revision: 2, problem: null, overrides: {} }));
  fireEvent.keyDown(opener, { key: "e", code: "KeyE", ctrlKey: true });
  expect(run).toHaveBeenCalledOnce();
});

describe.each([
  ["Win32", { ctrlKey: true }],
  ["MacIntel", { metaKey: true }],
])("initial shortcut preference read gate on %s", (platform, modifier) => {
  const overridden = {
    revision: 1,
    problem: null,
    overrides: {
      "session-focus": {
        state: "binding",
        chord: { key: "e", shift: false },
      },
    },
  };
  function setup(read: ShortcutPreferenceBridge["read"]) {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const files = vi.fn(),
      tool = vi.fn(),
      focus = vi.fn();
    let reload: () => void = () => {};
    function GateProbe() {
      useShortcutSurface(Surface.Sessions);
      const openHelp = useShortcutHelp();
      reload = useShortcutPreferences().reload;
      const shortcuts = useShortcuts([
        {
          id: ShortcutId.OpenFiles,
          scope: Surface.Sessions,
          label: "shortcuts.openFiles",
          bindings: sessionShortcutBindings[ShortcutId.OpenFiles],
          input: ShortcutInput.Allow,
          run: files,
        },
        {
          id: ShortcutId.OpenTool,
          scope: Surface.Sessions,
          label: "session.openTool",
          bindings: sessionShortcutBindings[ShortcutId.OpenTool],
          input: ShortcutInput.Allow,
          run: tool,
        },
        {
          id: ShortcutId.SessionFocus,
          scope: Surface.Sessions,
          label: "shortcuts.focusMessage",
          bindings: [{ key: "i", primary: true }],
          input: ShortcutInput.Allow,
          run: focus,
        },
      ]);
      return (
        <main>
          <button onClick={openHelp}>Help opener</button>
          <span
            data-testid="files-hint"
            aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenFiles)}
          >
            {shortcuts.bindings(ShortcutId.OpenFiles).length}
          </span>
        </main>
      );
    }
    const bridge: ShortcutPreferenceBridge = {
      read,
      update: vi.fn(),
      subscribe: async () => () => {},
    };
    render(
      <ShortcutPreferenceProvider bridge={bridge}>
        <ShortcutProvider>
          <GateProbe />
        </ShortcutProvider>
      </ShortcutPreferenceProvider>,
    );
    return { files, tool, focus, reload: () => reload() };
  }
  const press = (key: string, node: Element = document.body) =>
    fireEvent.keyDown(node, {
      key,
      code: `Key${key.toUpperCase()}`,
      ...modifier,
    });

  it("keeps Files and Tool inactive, unadvertised and explained in Help while the read is pending, then runs the empty-override default", async () => {
    let resolve: (value: unknown) => void = () => {};
    const { files, tool } = setup(
      () => new Promise((done) => (resolve = done)),
    );
    await act(async () => {});
    press("e");
    press("t");
    expect(files).not.toHaveBeenCalled();
    expect(tool).not.toHaveBeenCalled();
    const hint = screen.getByTestId("files-hint");
    expect(hint.getAttribute("aria-keyshortcuts")).toBe("");
    expect(hint.textContent).toBe("0");
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    const row = within(
      screen.getByRole("dialog", { name: "Keyboard shortcuts" }),
    )
      .getByText("Open Files")
      .closest("div")!;
    expect(within(row).getByText("Disabled")).toBeTruthy();
    expect(within(row).getByText(/finish loading/)).toBeTruthy();
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    await act(async () =>
      resolve({ revision: 1, problem: null, overrides: {} }),
    );
    expect(screen.getByTestId("files-hint").textContent).toBe("1");
    expect(
      screen.getByTestId("files-hint").getAttribute("aria-keyshortcuts"),
    ).toContain("E");
    press("e");
    press("t");
    expect(files).toHaveBeenCalledOnce();
    expect(tool).toHaveBeenCalledOnce();
  });

  it("keeps Files suppressed after the read settles with a saved primary+E override and runs the saved action", async () => {
    let resolve: (value: unknown) => void = () => {};
    const { files, focus } = setup(
      () => new Promise((done) => (resolve = done)),
    );
    await act(async () => {});
    press("e");
    expect(files).not.toHaveBeenCalled();
    await act(async () => resolve(overridden));
    press("e");
    expect(files).not.toHaveBeenCalled();
    expect(focus).toHaveBeenCalledOnce();
    expect(screen.getByTestId("files-hint").textContent).toBe("0");
  });

  it("opens Files after a failed initial read and keeps accepted bindings during a later pending refresh", async () => {
    const read = vi
      .fn<ShortcutPreferenceBridge["read"]>()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(overridden)
      .mockImplementationOnce(() => new Promise(() => {}));
    const { files, focus, reload } = setup(read);
    await act(async () => {});
    press("e");
    expect(files).toHaveBeenCalledOnce();
    expect(screen.getByTestId("files-hint").textContent).toBe("1");
    files.mockClear();
    // Explicit retry accepts the saved override and suppresses Files again.
    await act(async () => reload());
    press("e");
    expect(files).not.toHaveBeenCalled();
    expect(focus).toHaveBeenCalledOnce();
    // A later refresh pends without reverting to the startup gate or the
    // accepted override.
    await act(async () => reload());
    expect(read).toHaveBeenCalledTimes(3);
    press("e");
    expect(files).not.toHaveBeenCalled();
    expect(focus).toHaveBeenCalledTimes(2);
  });
});
