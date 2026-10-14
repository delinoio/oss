// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { ReloadShortcutOwner } from "./reload-shortcut";
import {
  ShortcutPreferenceProblem,
  ShortcutPreferenceProvider,
  type ShortcutPreferenceBridge,
} from "./shortcut-preference-controller";
import {
  ShortcutProvider,
  useShortcutHelp,
  useShortcutSurface,
  useShortcuts,
} from "./shortcut-provider";
import { ShortcutSettings } from "./shortcut-settings";
import { SettingsActionScope } from "./settings-action";
import {
  globalShortcutBindings,
  ShortcutExecution,
  ShortcutId,
  ShortcutInput,
  ShortcutScope,
} from "./shortcuts";
import { Surface } from "./surface";

const platforms = [
  ["Win32", { ctrlKey: true }, { metaKey: true }],
  ["MacIntel", { metaKey: true }, { ctrlKey: true }],
] as const;
const originalLocation = window.location;
afterEach(() => {
  Object.defineProperty(window, "location", {
    configurable: true,
    value: originalLocation,
  });
  vi.restoreAllMocks();
});
function stubLocationReload() {
  const reload = vi.fn();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { ...originalLocation, reload },
  });
  return reload;
}
function bridgeOf(
  read: ShortcutPreferenceBridge["read"],
): ShortcutPreferenceBridge {
  return {
    read,
    update: vi.fn(async () => ({})),
    subscribe: async () => () => {},
  };
}
const committed = (key: string, shift = false) => ({
  revision: 1,
  problem: null,
  overrides: { "session-focus": { state: "binding", chord: { key, shift } } },
});
const settled = async () => {
  await act(async () => {});
};

describe.each(platforms)("reload shortcut owner on %s", (platform, primary, wrong) => {
  const press = (target: Element | Window = document.body, init = {}) => {
    const event = new KeyboardEvent("keydown", {
      key: "r",
      code: "KeyR",
      bubbles: true,
      cancelable: true,
      ...primary,
      ...init,
    });
    act(() => {
      target.dispatchEvent(event);
    });
    return event;
  };
  function mount(
    read: ShortcutPreferenceBridge["read"] = async () => ({
      revision: 0,
      overrides: {},
      problem: null,
    }),
  ) {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const reload = vi.fn();
    const view = render(
      <ShortcutPreferenceProvider bridge={bridgeOf(read)}>
        <ReloadShortcutOwner reload={reload} />
        <input aria-label="Field" />
        <dialog open aria-modal="true">
          <button>Inside modal</button>
        </dialog>
      </ShortcutPreferenceProvider>,
    );
    return { reload, view };
  }

  it("reloads once per eligible press, consuming the event before inputs, modals and terminal hosts", async () => {
    const { reload } = mount();
    await settled();
    const field = screen.getByLabelText("Field");
    const terminal = document.createElement("div");
    terminal.dataset.shortcuts = "passthrough";
    const host = vi.fn();
    terminal.addEventListener("keydown", host, true);
    document.body.append(terminal);
    const inputListener = vi.fn();
    field.addEventListener("keydown", inputListener);
    const first = press(field);
    expect(first.defaultPrevented).toBe(true);
    expect(inputListener).not.toHaveBeenCalled();
    expect(reload).toHaveBeenCalledTimes(1);
    press(terminal);
    expect(host).not.toHaveBeenCalled();
    expect(reload).toHaveBeenCalledTimes(2);
    press(screen.getByRole("button", { name: "Inside modal" }));
    expect(reload).toHaveBeenCalledTimes(3);
    terminal.remove();
  });

  it("matches only the exact local primary+R chord", async () => {
    const { reload } = mount();
    await settled();
    const ignored: Record<string, object> = {
      plain: { ctrlKey: false, metaKey: false },
      shifted: { shiftKey: true },
      wrongPlatform: { ctrlKey: false, metaKey: false, ...wrong },
      both: { ctrlKey: true, metaKey: true },
      alt: { altKey: true },
      composing: { isComposing: true },
      imeKeyCode: { keyCode: 229 },
      repeated: { repeat: true },
      otherKey: { key: "t", code: "KeyT" },
    };
    for (const init of Object.values(ignored)) {
      const event = press(document.body, init);
      expect(event.defaultPrevented).toBe(false);
    }
    const altGraph = new KeyboardEvent("keydown", {
      key: "r",
      code: "KeyR",
      bubbles: true,
      cancelable: true,
      modifierAltGraph: true,
      ...primary,
    });
    act(() => {
      document.body.dispatchEvent(altGraph);
    });
    expect(altGraph.defaultPrevented).toBe(false);
    // An already handled event is never claimed.
    const handled = new KeyboardEvent("keydown", {
      key: "r",
      code: "KeyR",
      bubbles: true,
      cancelable: true,
      ...primary,
    });
    handled.preventDefault();
    act(() => {
      document.body.dispatchEvent(handled);
    });
    expect(reload).not.toHaveBeenCalled();
  });

  it("stays inactive until the first preference read settles", async () => {
    let resolve: (value: unknown) => void = () => {};
    const { reload } = mount(() => new Promise((done) => (resolve = done)));
    await settled();
    expect(press().defaultPrevented).toBe(false);
    expect(reload).not.toHaveBeenCalled();
    await act(async () => {
      resolve({ revision: 0, overrides: {}, problem: null });
    });
    await settled();
    press();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("is suppressed by a committed unshifted primary+R override but not by Shift variants", async () => {
    const { reload } = mount(async () => committed("r"));
    await settled();
    expect(press().defaultPrevented).toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });

  it("ignores a Shift+R override", async () => {
    const { reload } = mount(async () => committed("r", true));
    await settled();
    press();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it.each([
    ShortcutPreferenceProblem.ReadFailed,
    ShortcutPreferenceProblem.InvalidDocument,
    ShortcutPreferenceProblem.UnsupportedVersion,
    ShortcutPreferenceProblem.OutcomeUnknown,
    ShortcutPreferenceProblem.WriteFailed,
    ShortcutPreferenceProblem.Changed,
  ])("fails closed when the saved map is unverified (%s)", async (problem) => {
    const { reload } = mount(async () => ({
      revision: 1,
      overrides: {},
      problem,
    }));
    await settled();
    expect(press().defaultPrevented).toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });

  it("fails closed after a rejected read", async () => {
    const { reload } = mount(async () => {
      throw new Error("native read failed");
    });
    await settled();
    press();
    expect(reload).not.toHaveBeenCalled();
  });

  it("uses the default binding when persistence is unavailable", async () => {
    const { reload } = mount(async () => ({
      revision: 0,
      overrides: {},
      problem: ShortcutPreferenceProblem.Unavailable,
    }));
    await settled();
    press();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("restores reload after the override is removed by a committed update", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    let changed: (value: unknown) => void = () => {};
    const bridge: ShortcutPreferenceBridge = {
      read: async () => committed("r"),
      update: vi.fn(async () => ({})),
      subscribe: async (listener) => {
        changed = listener;
        return () => {};
      },
    };
    const reload = vi.fn();
    render(
      <ShortcutPreferenceProvider bridge={bridge}>
        <ReloadShortcutOwner reload={reload} />
      </ShortcutPreferenceProvider>,
    );
    await settled();
    press();
    expect(reload).not.toHaveBeenCalled();
    act(() => changed({ revision: 2, problem: null, overrides: {} }));
    press();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("removes its listener on unmount", async () => {
    const { reload, view } = mount();
    await settled();
    view.unmount();
    press();
    expect(reload).not.toHaveBeenCalled();
  });

  it("calls window.location.reload by default", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    const reload = stubLocationReload();
    render(
      <ShortcutPreferenceProvider
        bridge={bridgeOf(async () => ({
          revision: 0,
          overrides: {},
          problem: null,
        }))}
      >
        <ReloadShortcutOwner />
      </ShortcutPreferenceProvider>,
    );
    await settled();
    press();
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

describe("reload shortcut and Settings capture", () => {
  it("records R as the chord while capture is active and does not reload", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
    const reload = vi.fn();
    const bridge: ShortcutPreferenceBridge = {
      read: async () => ({ revision: 1, overrides: {}, problem: null }),
      update: vi.fn(async () => ({})),
      subscribe: async () => () => {},
    };
    render(
      <ShortcutPreferenceProvider bridge={bridge}>
        <ReloadShortcutOwner reload={reload} />
        <ShortcutProvider>
          <SettingsActionScope>
            <ShortcutSettings />
          </SettingsActionScope>
        </ShortcutProvider>
      </ShortcutPreferenceProvider>,
    );
    await screen.findByText("Current saved shortcuts");
    fireEvent.click(
      screen.getByRole("button", { name: "Capture shortcut for New session" }),
    );
    await screen.findByText(/Press Command on macOS or Control on Windows/);
    const event = new KeyboardEvent("keydown", {
      key: "r",
      code: "KeyR",
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    act(() => {
      document.activeElement!.dispatchEvent(event);
    });
    expect(reload).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(true);
    // The chord became the draft binding for New session.
    await waitFor(() => expect(screen.getAllByText("Ctrl + R")).toHaveLength(2));
    // After capture ends the owner is active again, until the draft is saved.
    await act(async () => {});
    fireEvent.keyDown(document.body, {
      key: "r",
      code: "KeyR",
      ctrlKey: true,
    });
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

describe("reload shortcut catalogs", () => {
  function Screen() {
    useShortcutSurface(Surface.Sessions);
    const openHelp = useShortcutHelp(),
      ref = useRef(null);
    void ref;
    useShortcuts([
      {
        id: ShortcutId.ReloadPage,
        scope: ShortcutScope.Global,
        label: "shortcuts.reloadPage",
        bindings: globalShortcutBindings[ShortcutId.ReloadPage],
        input: ShortcutInput.Allow,
        execution: ShortcutExecution.Native,
      },
    ]);
    return <button onClick={openHelp}>Help opener</button>;
  }
  const mountCatalogs = async (read: ShortcutPreferenceBridge["read"]) => {
    render(
      <ShortcutPreferenceProvider bridge={bridgeOf(read)}>
        <ShortcutProvider>
          <Screen />
          <SettingsActionScope>
            <ShortcutSettings />
          </SettingsActionScope>
        </ShortcutProvider>
      </ShortcutPreferenceProvider>,
    );
    await settled();
    fireEvent.click(screen.getByRole("button", { name: "Help opener" }));
    return within(
      screen.getByRole("dialog", { name: "Keyboard shortcuts" }),
    );
  };
  const settingsRow = (text = "Reload page") =>
    within(document.querySelector<HTMLElement>(".shortcut-settings-catalog")!)
      .getAllByText(text)[0]!
      .closest("div")!;

  it("lists the read-only Reload page row with platform keycaps in Help and Settings", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    const help = await mountCatalogs(async () => ({
      revision: 0,
      overrides: {},
      problem: null,
    }));
    const helpRow = help.getByText("Reload page").closest("div")!;
    expect([...helpRow.querySelectorAll("kbd")].map((k) => k.textContent)).toEqual(
      ["⌘", "R"],
    );
    expect(helpRow.className).not.toContain("shortcut-disabled");
    const row = settingsRow();
    expect(within(row).getByText("⌘ + R")).toBeTruthy();
    expect(within(row).queryByRole("button")).toBeNull();
  });

  it("explains an R override in English and Korean with the disabled treatment", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
    const help = await mountCatalogs(async () => committed("r"));
    const helpRow = help.getByText("Reload page").closest("div")!;
    expect(helpRow.className).toContain("shortcut-disabled");
    expect(within(helpRow).getByText("Disabled")).toBeTruthy();
    expect(within(helpRow).getByText(/custom shortcut uses Command or Control \+ R/)).toBeTruthy();
    expect(within(settingsRow()).getByText(/custom shortcut uses Command or Control \+ R/)).toBeTruthy();
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.Korean);
    });
    expect(within(settingsRow("페이지 새로고침")).getByText(/Command 또는 Control \+ R을 사용/)).toBeTruthy();
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.English);
    });
  });

  it("explains an unverified saved map instead of advertising the chord", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
    const help = await mountCatalogs(async () => {
      throw new Error("native read failed");
    });
    const helpRow = help.getByText("Reload page").closest("div")!;
    expect(helpRow.className).toContain("shortcut-disabled");
    expect(within(helpRow).getByText(/until your saved shortcut preferences are read successfully/)).toBeTruthy();
    expect(helpRow.querySelectorAll("kbd")).toHaveLength(0);
    expect(within(settingsRow()).getByText(/until your saved shortcut preferences are read successfully/)).toBeTruthy();
  });
});
