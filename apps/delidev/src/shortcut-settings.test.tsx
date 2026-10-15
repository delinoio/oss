// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  ShortcutPreferenceProvider,
  ShortcutPreferenceProblem,
  useShortcutPreferences,
  type ShortcutPreferenceBridge,
  type ShortcutPreferenceSnapshot,
} from "./shortcut-preference-controller";
import { ShortcutSettings } from "./shortcut-settings";
import { SettingsActionScope } from "./settings-action";
import {
  ShortcutOverrideState,
  type ShortcutOverrides,
} from "./shortcut-preferences";
import {
  ShortcutProvider,
  useGlobalShortcutAria,
  useShortcuts,
} from "./shortcut-provider";
import {
  ShortcutId,
  ShortcutScope,
  ShortcutInput,
  globalShortcutBindings,
} from "./shortcuts";
import { i18n, SupportedLanguage } from "./localization";
function fixture(
  initial: ShortcutPreferenceSnapshot = {
    revision: 1,
    overrides: {},
    problem: null,
  },
) {
  let snapshot: ShortcutPreferenceSnapshot = initial;
  const listeners = new Set<(raw: unknown) => void>();
  const publish = (next: ShortcutPreferenceSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener(next);
  };
  const bridge: ShortcutPreferenceBridge = {
    read: vi.fn(async () => snapshot),
    update: vi.fn(async (overrides, revision) => {
      if (revision !== snapshot.revision)
        return { ...snapshot, problem: ShortcutPreferenceProblem.Changed };
      const next = { revision: revision + 1, overrides, problem: null };
      publish(next);
      return next;
    }),
    subscribe: vi.fn(async (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    }),
  };
  return { bridge, publish, state: () => snapshot };
}
function Probe({ run }: { run: () => void }) {
  const aria = useGlobalShortcutAria(ShortcutId.NewSession),
    { snapshot } = useShortcutPreferences();
  useShortcuts([
    {
      id: ShortcutId.NewSession,
      scope: ShortcutScope.Global,
      label: "shortcuts.newSession",
      bindings: globalShortcutBindings[ShortcutId.NewSession],
      input: ShortcutInput.Allow,
      run,
    },
  ]);
  return (
    <>
      <button aria-keyshortcuts={aria}>Ordinary action</button>
      <output data-testid="revision">{snapshot.revision}</output>
    </>
  );
}
function Owner({
  bridge,
  run = () => {},
}: {
  bridge: ShortcutPreferenceBridge;
  run?: () => void;
}) {
  const [visible, setVisible] = useState(true);
  return (
    <ShortcutPreferenceProvider bridge={bridge}>
      <ShortcutProvider>
        <button onClick={() => setVisible((value) => !value)}>
          Leave category
        </button>
        <Probe run={run} />
        {visible ? (
          <SettingsActionScope>
            <ShortcutSettings />
          </SettingsActionScope>
        ) : null}
      </ShortcutProvider>
    </ShortcutPreferenceProvider>
  );
}
const capture = async (name = "New session", key = "j") => {
  fireEvent.click(
    screen.getByRole("button", { name: `Capture shortcut for ${name}` }),
  );
  await screen.findByText(
    "Press Command on macOS or Control on Windows/Linux, optionally Shift, with an ASCII letter, digit or Enter. Escape cancels capture.",
  );
  fireEvent.keyDown(document.activeElement!, {
    key,
    ctrlKey: true,
    shiftKey: true,
  });
  await screen.findByText("Unsaved changes");
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Cancel capture" })).toBeNull(),
  );
};
it("keeps capture guidance and labeled footer controls outside the localized catalog", async () => {
  const f = fixture();
  render(<Owner bridge={f.bridge} />);
  await screen.findByText("Current saved shortcuts");
  const catalog = screen.getByRole("region", { name: "Shortcut catalog" });
  expect(catalog.getAttribute("tabindex")).toBe("0");
  expect(
    catalog.querySelectorAll('[data-settings-action-presentation="icon"]'),
  ).toHaveLength(21);
  expect(within(catalog).queryByText("Current saved shortcuts")).toBeNull();
  for (const name of [
    "Restore all defaults",
    "Discard changes",
    "Save changes",
  ]) {
    const button = screen.getByRole("button", { name });
    expect(catalog.contains(button)).toBe(false);
    expect(button.getAttribute("data-settings-action-presentation")).toBe(
      "label",
    );
  }
  fireEvent.click(
    screen.getByRole("button", { name: "Capture shortcut for New session" }),
  );
  const cancel = await screen.findByRole("button", { name: "Cancel capture" });
  expect(catalog.contains(cancel)).toBe(false);
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(screen.getByRole("region", { name: "단축키 목록" })).toBe(catalog);
  expect(screen.getByRole("button", { name: "단축키 입력 취소" })).toBe(cancel);
  expect(f.bridge.update).not.toHaveBeenCalled();
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.English);
  });
  fireEvent.click(cancel);
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Cancel capture" })).toBeNull(),
  );
});
it("uses New Chat in the English creation heading while preserving Korean and shortcut dispatch", async () => {
  const f = fixture(),
    run = vi.fn();
  render(<Owner bridge={f.bridge} run={run} />);
  await screen.findByText("Current saved shortcuts");
  const heading = screen.getByRole("heading", {
    level: 2,
    name: "New session / New Chat",
  });
  expect(i18n.t("sidebar.newGeneralChat")).toBe("New Chat");
  expect(screen.queryByText("New session / New general chat")).toBeNull();
  expect(
    screen.getByRole("button", { name: "Capture shortcut for New session" }),
  ).toBeTruthy();
  const action = screen.getByRole("button", { name: "Ordinary action" });
  expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+N");
  fireEvent.keyDown(action, { key: "n", ctrlKey: true });
  expect(run).toHaveBeenCalledOnce();
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(
    screen.getByRole("heading", { level: 2, name: "새 세션 / 새 일반 대화" }),
  ).toBe(heading);
  expect(screen.getByRole("button", { name: "Ordinary action" })).toBe(action);
  expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+N");
  fireEvent.keyDown(action, { key: "n", ctrlKey: true });
  expect(run).toHaveBeenCalledTimes(2);
  expect(f.state().overrides).toEqual({});
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("keeps draft bindings inactive until Save and updates dispatch and ARIA without remount", async () => {
  const f = fixture(),
    run = vi.fn();
  render(
    <StrictMode>
      <Owner bridge={f.bridge} run={run} />
    </StrictMode>,
  );
  await screen.findByText("Current saved shortcuts");
  expect(
    screen
      .getByRole("button", { name: "Save changes" })
      .getAttribute("data-settings-action"),
  ).toBe("save");
  expect(
    screen
      .getByRole("button", { name: "Capture shortcut for New session" })
      .getAttribute("data-settings-action-presentation"),
  ).toBe("icon");
  const action = screen.getByRole("button", { name: "Ordinary action" });
  await capture();
  expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+N");
  fireEvent.keyDown(action, { key: "j", ctrlKey: true, shiftKey: true });
  expect(run).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+Shift+J"),
  );
  expect(f.bridge.update).toHaveBeenCalledOnce();
  fireEvent.keyDown(action, { key: "j", ctrlKey: true, shiftKey: true });
  expect(run).toHaveBeenCalledOnce();
  fireEvent.keyDown(action, { key: "n", ctrlKey: true });
  expect(run).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: "Ordinary action" })).toBe(action);
});
it("consumes captures, local resets, disable and category departure without a native save", async () => {
  const f = fixture(),
    run = vi.fn();
  render(<Owner bridge={f.bridge} run={run} />);
  await screen.findByText("Current saved shortcuts");
  const opener = screen.getByRole("button", {
    name: "Capture shortcut for New session",
  });
  fireEvent.click(opener);
  await screen.findByText(
    "Press Command on macOS or Control on Windows/Linux, optionally Shift, with an ASCII letter, digit or Enter. Escape cancels capture.",
  );
  fireEvent.keyDown(opener, { key: "w", ctrlKey: true });
  expect(run).not.toHaveBeenCalled();
  await screen.findByRole("alert");
  fireEvent.keyDown(opener, { key: "Escape" });
  await waitFor(() => expect(document.activeElement).toBe(opener));
  await capture();
  fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
  expect(f.bridge.update).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Disable Open shortcut help shortcut" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
  fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
  await screen.findByText("Current saved shortcuts");
  expect(f.bridge.update).not.toHaveBeenCalled();
});
it("retains dirty drafts on a concurrent committed change and blocks until explicit discard", async () => {
  const f = fixture();
  render(<Owner bridge={f.bridge} />);
  await screen.findByText("Current saved shortcuts");
  await capture();
  await act(async () =>
    f.publish({
      revision: 2,
      overrides: { help: { state: ShortcutOverrideState.Disabled } },
      problem: null,
    }),
  );
  expect(
    screen
      .getByRole("button", { name: "Save changes" })
      .hasAttribute("disabled"),
  ).toBe(true);
  await screen.findByText(/Shortcuts changed in another window/);
  expect(screen.getByText("Ctrl + Shift + J")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
  await screen.findByText("Current saved shortcuts");
  expect(screen.getByText("Disabled")).toBeTruthy();
});
it("does not replay an uncertain native save, preserving effective bindings until explicit reinspection", async () => {
  const f = fixture();
  vi.mocked(f.bridge.update).mockRejectedValueOnce(
    new Error("Lost acknowledgment"),
  );
  render(<Owner bridge={f.bridge} />);
  await screen.findByText("Current saved shortcuts");
  await capture();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await screen.findByText(
    "The save outcome is unknown. Reinspect without repeating the save.",
  );
  expect(
    screen
      .getByRole("button", { name: "Ordinary action" })
      .getAttribute("aria-keyshortcuts"),
  ).toBe("Control+N");
  await act(async () =>
    f.publish({
      revision: 2,
      overrides: {
        "new-session": {
          state: ShortcutOverrideState.Binding,
          chord: { key: "j", shift: true },
        },
      },
      problem: null,
    }),
  );
  expect(
    screen
      .getByRole("button", { name: "Ordinary action" })
      .getAttribute("aria-keyshortcuts"),
  ).toBe("Control+N");
  fireEvent.click(
    screen.getByRole("button", { name: "Reinspect saved shortcuts" }),
  );
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "Ordinary action" })
        .getAttribute("aria-keyshortcuts"),
    ).toBe("Control+Shift+J"),
  );
  expect(f.bridge.update).toHaveBeenCalledOnce();
});
it("localizes the editor and exposes truthful browser persistence limits", async () => {
  await i18n.changeLanguage(SupportedLanguage.Korean);
  render(<ShortcutSettings />);
  expect(
    screen.getByText(/이 환경에서는 단축키를 저장할 수 없습니다/),
  ).toBeTruthy();
  expect(
    screen
      .getByRole("button", { name: "변경 사항 저장" })
      .hasAttribute("disabled"),
  ).toBe(true);
});
it("keeps an admitted save owned above the category and publishes it once after departure", async () => {
  const f = fixture();
  let complete!: (snapshot: ShortcutPreferenceSnapshot) => void;
  vi.mocked(f.bridge.update).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  render(<Owner bridge={f.bridge} />);
  await screen.findByText("Current saved shortcuts");
  await capture();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await screen.findByText("Saving shortcut settings…");
  fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
  await act(async () =>
    complete({
      revision: 2,
      overrides: {
        "new-session": {
          state: ShortcutOverrideState.Binding,
          chord: { key: "j", shift: true },
        },
      },
      problem: null,
    }),
  );
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "Ordinary action" })
        .getAttribute("aria-keyshortcuts"),
    ).toBe("Control+Shift+J"),
  );
  fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
  await screen.findByText("Current saved shortcuts");
  expect(f.bridge.update).toHaveBeenCalledOnce();
});
it("consumes keys while native admission is pending and never claims active capture after a lost ACK", async () => {
  const { shortcutCapture } = await import("./shortcut-capture");
  let complete!: (v: { deadline: number; token: string }) => void;
  const begin = vi
    .spyOn(shortcutCapture, "begin")
    .mockImplementationOnce(
      () => new Promise((resolve) => (complete = resolve)),
    );
  const end = vi.spyOn(shortcutCapture, "end").mockResolvedValue(undefined);
  try {
    const f = fixture(),
      run = vi.fn();
    render(<Owner bridge={f.bridge} run={run} />);
    await screen.findByText("Current saved shortcuts");
    const opener = screen.getByRole("button", {
      name: "Capture shortcut for New session",
    });
    fireEvent.click(opener);
    await screen.findByText("Preparing or ending native capture…");
    fireEvent.keyDown(opener, { key: "j", ctrlKey: true, shiftKey: true });
    expect(screen.queryByText("Unsaved changes")).toBeNull();
    expect(run).not.toHaveBeenCalled();
    await act(async () =>
      complete({
        deadline: performance.now() + 10000,
        token: "original-token",
      }),
    );
    await screen.findByText(/Press Command on macOS/);
    fireEvent.blur(window);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Cancel capture" }),
      ).toBeNull(),
    );
    expect(end).toHaveBeenCalled();
    expect(f.bridge.update).not.toHaveBeenCalled();
  } finally {
    begin.mockRestore();
    end.mockRestore();
  }
});
it("a late native admission after category departure cannot reopen capture or change drafts", async () => {
  const { shortcutCapture } = await import("./shortcut-capture");
  let complete!: (v: { deadline: number; token: string }) => void;
  const begin = vi
    .spyOn(shortcutCapture, "begin")
    .mockImplementationOnce(
      () => new Promise((resolve) => (complete = resolve)),
    );
  const end = vi.spyOn(shortcutCapture, "end").mockResolvedValue(undefined);
  try {
    const f = fixture();
    render(<Owner bridge={f.bridge} />);
    await screen.findByText("Current saved shortcuts");
    fireEvent.click(
      screen.getByRole("button", { name: "Capture shortcut for New session" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
    await act(async () =>
      complete({
        deadline: performance.now() + 10000,
        token: "departed-token",
      }),
    );
    expect(screen.queryByText(/Press Command on macOS/)).toBeNull();
    expect(end).toHaveBeenCalledWith("departed-token");
    expect(f.bridge.update).not.toHaveBeenCalled();
  } finally {
    begin.mockRestore();
    end.mockRestore();
  }
});

it("late duplicate retirement cannot clear or mark a newer capture uncertain", async () => {
  const { shortcutCapture } = await import("./shortcut-capture");
  let rejectOld!: (error: Error) => void, finishLatest!: () => void;
  const begin = vi.spyOn(shortcutCapture, "begin").mockResolvedValue({
    deadline: performance.now() + 10000,
    token: "native-token",
  });
  const end = vi
    .spyOn(shortcutCapture, "end")
    .mockResolvedValue(undefined)
    .mockImplementationOnce(
      () => new Promise((_, reject) => (rejectOld = reject)),
    )
    .mockImplementationOnce(
      () => new Promise((resolve) => (finishLatest = resolve)),
    );
  try {
    const f = fixture();
    render(<Owner bridge={f.bridge} />);
    await screen.findByText("Current saved shortcuts");
    const opener = screen.getByRole("button", {
      name: "Capture shortcut for New session",
    });
    fireEvent.click(opener);
    await screen.findByText(/Press Command on macOS/);
    fireEvent.click(screen.getByRole("button", { name: "Cancel capture" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel capture" }));
    await act(async () => finishLatest());
    fireEvent.click(opener);
    await screen.findByText(/Press Command on macOS/);
    await act(async () => rejectOld(Error("Old lost ACK")));
    expect(screen.getByText(/Press Command on macOS/)).toBeTruthy();
    expect(screen.queryByText(/Capture could not be confirmed/)).toBeNull();
  } finally {
    begin.mockRestore();
    end.mockRestore();
  }
});

it("shows New session as Ctrl + N without a Shift keycap or a fixed New Window row and permits saving former T", async () => {
  const f = fixture(),
    run = vi.fn();
  render(<Owner bridge={f.bridge} run={run} />);
  await screen.findByText("Current saved shortcuts");
  const row = screen
    .getByRole("heading", { name: "New session" })
    .closest(".shortcut-settings-row") as HTMLElement;
  expect(
    Array.from(row.querySelectorAll("kbd"), (node) => node.textContent),
  ).toEqual(["Ctrl", "N"]);
  expect(row.querySelector(".shortcut-binding-text")?.textContent).toBe(
    "Ctrl + N",
  );
  expect(screen.queryByText("New Window")).toBeNull();
  expect(screen.queryByText("Close tab")).not.toBeNull();
  expect(
    screen.getByText(
      /Command or Control \+ K, W, Q, H, M and 1–9 remain reserved/,
    ),
  ).toBeTruthy();
  const opener = screen.getByRole("button", {
    name: "Capture shortcut for New session",
  });
  fireEvent.click(opener);
  await screen.findByText(/Press Command on macOS/);
  fireEvent.keyDown(opener, { key: "w", ctrlKey: true });
  await screen.findByRole("alert");
  expect(screen.queryByText("Unsaved changes")).toBeNull();
  expect(run).not.toHaveBeenCalled();
  fireEvent.keyDown(opener, { key: "t", ctrlKey: true });
  await screen.findByText("Unsaved changes");
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Cancel capture" })).toBeNull(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "Ordinary action" })
        .getAttribute("aria-keyshortcuts"),
    ).toBe("Control+T"),
  );
  expect(f.state().overrides[ShortcutId.NewSession]).toEqual({
    state: ShortcutOverrideState.Binding,
    chord: { key: "t", shift: false },
  });
  fireEvent.keyDown(screen.getByRole("button", { name: "Ordinary action" }), {
    key: "t",
    ctrlKey: true,
  });
  expect(run).toHaveBeenCalledOnce();
  fireEvent.keyDown(screen.getByRole("button", { name: "Ordinary action" }), {
    key: "n",
    ctrlKey: true,
  });
  expect(run).toHaveBeenCalledOnce();
});

it("keeps a saved Shift+N New session override authoritative over the unshifted default", async () => {
  const f = fixture({
    revision: 2,
    overrides: {
      "new-session": {
        state: ShortcutOverrideState.Binding,
        chord: { key: "n", shift: true },
      },
    },
    problem: null,
  });
  const run = vi.fn();
  render(<Owner bridge={f.bridge} run={run} />);
  await screen.findByText("Current saved shortcuts");
  const action = screen.getByRole("button", { name: "Ordinary action" });
  expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+Shift+N");
  fireEvent.keyDown(action, { key: "n", ctrlKey: true });
  expect(run).not.toHaveBeenCalled();
  fireEvent.keyDown(action, { key: "n", ctrlKey: true, shiftKey: true });
  expect(run).toHaveBeenCalledOnce();
  expect(f.bridge.update).not.toHaveBeenCalled();
});

it.each([
  ["en", "Add message to queue", "Fixed: Enter"],
  ["ko", "메시지 대기열에 추가", "고정: Enter"],
])(
  "shows fixed send independently of disabled primary in %s",
  async (language, label, fixed) => {
    await act(() => i18n.changeLanguage(language));
    Object.defineProperty(navigator, "platform", {
      configurable: true,
      value: "Linux",
    });
    const f = fixture();
    render(<Owner bridge={f.bridge} />);
    const row = screen
      .getByRole("heading", { name: label })
      .closest(".shortcut-settings-row")!;
    expect(within(row as HTMLElement).getByText(fixed)).toBeTruthy();
    fireEvent.click(
      within(row as HTMLElement).getByRole("button", {
        name:
          language === "en"
            ? `Disable ${label} shortcut`
            : `${label} 단축키 사용 안 함`,
      }),
    );
    expect(within(row as HTMLElement).getByText(fixed)).toBeTruthy();
    expect(screen.getAllByText("Shift + Enter").length).toBeGreaterThan(0);
    await act(() => i18n.changeLanguage("en"));
  },
);

function OpenSettingsProbe({ run }: { run: () => void }) {
  useShortcuts([
    {
      id: ShortcutId.OpenSettings,
      scope: ShortcutScope.Global,
      label: "shortcuts.openSettings",
      bindings: globalShortcutBindings[ShortcutId.OpenSettings],
      input: ShortcutInput.Allow,
      run,
    },
  ]);
  return null;
}

it("shows localized read-only Open Settings and rejects its chord during capture", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const f = fixture();
  const run = vi.fn();
  render(
    <ShortcutPreferenceProvider bridge={f.bridge}>
      <ShortcutProvider>
        <OpenSettingsProbe run={run} />
        <ShortcutSettings />
      </ShortcutProvider>
    </ShortcutPreferenceProvider>,
  );
  await screen.findByText("Current saved shortcuts");
  const entry = screen.getByText("Open Settings").closest("div")!;
  expect(within(entry).getByText("Ctrl + ,")).toBeTruthy();
  expect(within(entry).queryByRole("button")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Capture shortcut for New session" }),
  );
  await screen.findByText(
    "Press Command on macOS or Control on Windows/Linux, optionally Shift, with an ASCII letter, digit or Enter. Escape cancels capture.",
  );
  fireEvent.keyDown(document.activeElement!, { key: ",", ctrlKey: true });
  expect(run).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toContain("Reserved");
  expect(f.state().overrides).toEqual({});
  expect(f.bridge.update).not.toHaveBeenCalled();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Cancel capture" })).toBeNull(),
  );
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(screen.getByText("설정 열기").closest("div")).toBe(entry);
  expect(within(entry).queryByRole("button")).toBeNull();
  expect(within(entry).getByText("Ctrl + ,")).toBeTruthy();
});

it("shows the read-only Open tool default and explains custom primary+T priority only after a committed save", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const f = fixture();
  render(<Owner bridge={f.bridge} />);
  await screen.findByText("Current saved shortcuts");
  const entry = () => screen.getByText("Open tool").closest("div")!;
  expect(within(entry()).getByText("Ctrl + T")).toBeTruthy();
  expect(within(entry()).queryByRole("button")).toBeNull();
  expect(within(entry()).queryByText(/custom shortcut/)).toBeNull();
  const opener = screen.getByRole("button", {
    name: "Capture shortcut for New session",
  });
  fireEvent.click(opener);
  await screen.findByText(/Press Command on macOS/);
  fireEvent.keyDown(opener, { key: "t", ctrlKey: true });
  await screen.findByText("Unsaved changes");
  // The unsaved draft never changes read-only guidance.
  expect(within(entry()).getByText("Ctrl + T")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(within(entry()).getByText(/custom shortcut uses/)).toBeTruthy(),
  );
  expect(within(entry()).getByText("Disabled")).toBeTruthy();
  expect(f.state().overrides).toEqual({
    [ShortcutId.NewSession]: {
      state: ShortcutOverrideState.Binding,
      chord: { key: "t", shift: false },
    },
  });
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(
    within(screen.getByText("도구 열기").closest("div")!).getByText(
      /사용자 지정 단축키/,
    ),
  ).toBeTruthy();
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.English);
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Restore default for New session" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(within(entry()).getByText("Ctrl + T")).toBeTruthy(),
  );
  expect(f.state().overrides).toEqual({});
});

it("shows the read-only Open Files default and explains custom primary+E priority only after a committed save", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const f = fixture();
  render(<Owner bridge={f.bridge} />);
  await screen.findByText("Current saved shortcuts");
  const entry = () => screen.getByText("Open Files").closest("div")!;
  expect(within(entry()).getByText("Ctrl + E")).toBeTruthy();
  expect(within(entry()).queryByRole("button")).toBeNull();
  expect(within(entry()).queryByText(/custom shortcut/)).toBeNull();
  const opener = screen.getByRole("button", {
    name: "Capture shortcut for New session",
  });
  fireEvent.click(opener);
  await screen.findByText(/Press Command on macOS/);
  fireEvent.keyDown(opener, { key: "e", ctrlKey: true });
  await screen.findByText("Unsaved changes");
  // The unsaved draft never changes read-only guidance.
  expect(within(entry()).getByText("Ctrl + E")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(within(entry()).getByText(/custom shortcut uses/)).toBeTruthy(),
  );
  expect(within(entry()).getByText("Disabled")).toBeTruthy();
  expect(f.state().overrides).toEqual({
    [ShortcutId.NewSession]: {
      state: ShortcutOverrideState.Binding,
      chord: { key: "e", shift: false },
    },
  });
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(
    within(screen.getByText("파일 열기").closest("div")!).getByText(
      /사용자 지정 단축키/,
    ),
  ).toBeTruthy();
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.English);
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Restore default for New session" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() =>
    expect(within(entry()).getByText("Ctrl + E")).toBeTruthy(),
  );
  expect(f.state().overrides).toEqual({});
});

it("shows Open Files and Open tool as pending in the read-only catalog until the initial read settles", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
  const f = fixture();
  let resolve: (value: unknown) => void = () => {};
  f.bridge.read = vi.fn(() => new Promise((done) => (resolve = done)));
  render(<Owner bridge={f.bridge} />);
  await screen.findByRole("region", { name: "Shortcut catalog" });
  for (const label of ["Open Files", "Open tool"]) {
    const entry = screen.getByText(label).closest("div")!;
    expect(within(entry).getByText("Disabled")).toBeTruthy();
    expect(within(entry).getByText(/finish loading/)).toBeTruthy();
    expect(within(entry).queryByText(/Ctrl \+ [ET]/)).toBeNull();
  }
  await act(async () => resolve({ revision: 1, overrides: {}, problem: null }));
  const files = screen.getByText("Open Files").closest("div")!;
  expect(within(files).getByText("Ctrl + E")).toBeTruthy();
  expect(within(files).queryByText(/finish loading/)).toBeNull();
  expect(
    within(screen.getByText("Open tool").closest("div")!).getByText("Ctrl + T"),
  ).toBeTruthy();
});
