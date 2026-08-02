import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./runtime/startup", () => ({
  exportDiagnostics: vi.fn(async () => ({ status: "cancelled" as const })),
  loadRuntimeInfo: vi.fn(async () => ({
    applicationId: "dev.deli.devhud",
    bundledOrigin: "http://tauri.localhost",
    operatingSystem: "linux",
    toolOperatingSystem: "ubuntu",
    runtime: "cef",
    sandboxEnabled: true,
    updatePolicy: "Desktop updater unavailable",
  })),
  tauriRuntimeBridge: {},
}));

import { App } from "./App";
import {
  defaultSettings,
  encodeSettings,
  SETTINGS_STORAGE_KEY,
  ShortcutKey,
  ShortcutModifier,
  ThemePreference,
  WIDGET_CONFIGURATION_STORAGE_KEY,
} from "./persistence/contracts";
import {
  MemoryStorageAdapter,
  type LocalStorageAdapter,
  type PersistenceResetOutcome,
} from "./persistence/storage";
import type { DesktopBridge } from "./runtime/desktop";
import * as desktopRuntime from "./runtime/desktop";
import { exportDiagnostics, loadRuntimeInfo } from "./runtime/startup";
import * as themeRuntime from "./runtime/theme";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function desktopBridge(
  overrides: Partial<DesktopBridge> = {},
): DesktopBridge {
  return {
    hideHud: vi.fn(async () => ({ status: "hidden" as const })),
    showSettings: vi.fn(async () => undefined),
    hideSettings: vi.fn(async () => undefined),
    replaceGlobalShortcut: vi.fn(async (shortcut) =>
      shortcut === null
        ? { status: "cancelled" as const }
        : { status: "replaced" as const, shortcut },
    ),
    setLaunchAtLogin: vi.fn(async (enabled) => ({
      status: "applied" as const,
      enabled,
    })),
    completeFirstRun: vi.fn(async () => ({ status: "completed" as const })),
    requestUpdateAction: vi.fn(async () => ({
      status: "unavailable" as const,
      reason: "scoped-updater-unavailable" as const,
    })),
    publishReset: vi.fn(),
    publishTheme: vi.fn(),
    subscribeReset: vi.fn(() => () => undefined),
    subscribeTheme: vi.fn(() => () => undefined),
    ...overrides,
  };
}

function renderApp(
  properties: Omit<ComponentProps<typeof App>, "storage"> & {
    storage?: LocalStorageAdapter;
  } = {},
) {
  return render(
    <App storage={properties.storage ?? new MemoryStorageAdapter()} {...properties} />,
  );
}

describe("DevHud application surfaces", () => {
  it("focuses the desktop search field and presents authenticated RealQA entry", async () => {
    renderApp();
    const search = screen.getByRole("searchbox", { name: "Search tools" });
    expect(search).toHaveFocus();
    expect(await screen.findByRole("heading", { name: "RealQA" })).toBeVisible();
    expect(screen.queryByText("No tools are available in this foundation preview.")).not.toBeInTheDocument();
  });

  it("does not advertise RealQA on an unsupported Linux distribution", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: null,
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
    });

    renderApp();

    expect(
      await screen.findByText("No tools are available in this foundation preview."),
    ).toBeVisible();
    expect(screen.queryByRole("heading", { name: "RealQA" })).not.toBeInTheDocument();
  });

  it("closes settings with Escape and restores focus", async () => {
    const user = userEvent.setup();
    renderApp();
    const settings = screen.getAllByRole("button", { name: "Settings" })[0];
    if (settings === undefined) throw new Error("Settings trigger is missing");
    await user.click(settings);
    expect(screen.getByRole("dialog", { name: "DevHud settings" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Close settings" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(settings).toHaveFocus();
  });

  it("defaults to System and applies an explicit theme choice", async () => {
    const user = userEvent.setup();
    renderApp();
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    const theme = screen.getByRole("combobox", { name: "Theme preference" });
    expect(theme).toHaveValue("system");
    await user.selectOptions(theme, "dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(theme).toHaveFocus();
  });

  it("publishes persisted desktop theme changes to the HUD window", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const bridge = desktopBridge();
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });
    const theme = await screen.findByRole("combobox", {
      name: "Theme preference",
    });
    await waitFor(() => expect(theme).toBeEnabled());
    await user.selectOptions(
      theme,
      "dark",
    );
    await waitFor(() =>
      expect(bridge.publishTheme).toHaveBeenCalledWith("dark"),
    );
  });

  it("adopts theme changes published by the settings window", async () => {
    let publishToHud: ((theme: ThemePreference) => void) | undefined;
    const bridge = desktopBridge({
      subscribeTheme: vi.fn((listener) => {
        publishToHud = listener;
        return () => undefined;
      }),
    });
    renderApp({ desktopBridge: bridge });

    act(() => publishToHud?.(ThemePreference.Dark));

    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("reloads both retained HUD records after a reset is published", async () => {
    let publishReset:
      | ((outcome: PersistenceResetOutcome) => void)
      | undefined;
    const storage = new MemoryStorageAdapter();
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      encodeSettings({ ...defaultSettings, theme: ThemePreference.Dark }),
    );
    storage.values.set(WIDGET_CONFIGURATION_STORAGE_KEY, "{not-json}");
    const read = vi.spyOn(storage, "read");
    const bridge = desktopBridge({
      subscribeReset: vi.fn((listener) => {
        publishReset = listener;
        return () => undefined;
      }),
    });
    renderApp({ desktopBridge: bridge, storage });

    expect(await screen.findByRole("alert")).toHaveTextContent("Reset DevHud");
    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe(ThemePreference.Dark),
    );
    const settingsReadsBeforeReset = read.mock.calls.filter(
      ([key]) => key === SETTINGS_STORAGE_KEY,
    ).length;
    const widgetReadsBeforeReset = read.mock.calls.filter(
      ([key]) => key === WIDGET_CONFIGURATION_STORAGE_KEY,
    ).length;
    storage.values.clear();
    act(() => publishReset?.({ status: "complete" }));

    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe(ThemePreference.System),
    );
    expect(
      read.mock.calls.filter(([key]) => key === SETTINGS_STORAGE_KEY),
    ).toHaveLength(settingsReadsBeforeReset + 1);
    expect(
      read.mock.calls.filter(([key]) => key === WIDGET_CONFIGURATION_STORAGE_KEY),
    ).toHaveLength(widgetReadsBeforeReset + 1);
  });

  it("reconciles the retained HUD theme after its native bridge subscribes", async () => {
    let resolveRuntime: ((runtime: Awaited<ReturnType<typeof loadRuntimeInfo>>) => void) | undefined;
    vi.mocked(loadRuntimeInfo).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRuntime = resolve;
      }),
    );
    const storage = new MemoryStorageAdapter();
    const read = vi.spyOn(storage, "read");
    const bridge = desktopBridge();
    vi.spyOn(desktopRuntime, "nativeDesktopBridge").mockReturnValue(bridge);
    renderApp({ storage });
    await waitFor(() =>
      expect(read).toHaveBeenCalledWith(SETTINGS_STORAGE_KEY),
    );
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      encodeSettings({ ...defaultSettings, theme: ThemePreference.Dark }),
    );

    resolveRuntime?.({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "hud",
      firstRun: true,
    });

    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe("dark"),
    );
  });

  it("keeps launch-at-login disabled until the user explicitly enables it", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: desktopBridge() });
    const launchAtLogin = await screen.findByRole("checkbox", {
      name: "Launch DevHud at login",
    });
    expect(launchAtLogin).not.toBeChecked();
    await user.click(launchAtLogin);
    await waitFor(() => expect(launchAtLogin).toBeChecked());
    expect(screen.getByRole("status")).toHaveTextContent(
      "DevHud will launch at login.",
    );
  });

  it("holds setting changes until local persistence finishes loading", async () => {
    const user = userEvent.setup();
    let completeRead: ((value: string | null) => void) | undefined;
    const pendingRead = new Promise<string | null>((resolve) => {
      completeRead = resolve;
    });
    const storage: LocalStorageAdapter = {
      read: async () => pendingRead,
      reset: async () => ({ status: "complete" as const }),
      write: async () => undefined,
    };

    renderApp({ storage });
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    const theme = screen.getByRole("combobox", { name: "Theme preference" });
    expect(theme).toBeDisabled();
    completeRead?.(null);
    await waitFor(() => expect(theme).toBeEnabled());
  });

  it("hides the application shell from assistive technology while settings is open", async () => {
    const user = userEvent.setup();
    renderApp();
    const settings = screen.getAllByRole("button", { name: "Settings" })[0];
    if (settings === undefined) throw new Error("Settings trigger is missing");
    await user.click(settings);
    expect(settings.closest("div[aria-hidden]")).toHaveAttribute("aria-hidden", "true");
    expect(settings.closest("div[inert]")).toHaveAttribute("inert");
  });

  it("hides persistence alerts from assistive technology while settings is open", async () => {
    const user = userEvent.setup();
    const storage: LocalStorageAdapter = {
      read: async () => {
        throw new Error("storage unavailable");
      },
      reset: async () => ({ status: "complete" as const }),
      write: async () => undefined,
    };

    renderApp({ storage });
    const alerts = await screen.findAllByRole("alert");
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    for (const alert of alerts) {
      expect(alert.closest("div[aria-hidden]")).toHaveAttribute("aria-hidden", "true");
      expect(alert.closest("div[inert]")).toHaveAttribute("inert");
    }
  });

  it("has no automated accessibility violations", async () => {
    const { container } = renderApp();
    const results = await axe.run(container, {
      rules: {
        "color-contrast": { enabled: false },
      },
    });
    expect(results.violations).toEqual([]);
  });

  it("hides the native HUD on Escape and focus loss", async () => {
    const bridge = desktopBridge();
    renderApp({ desktopBridge: bridge });
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.blur(window);
    expect(bridge.hideHud).toHaveBeenCalledTimes(2);
  });

  it("surfaces unchanged outcomes when Escape cannot hide the native HUD", async () => {
    const bridge = desktopBridge({
      hideHud: vi.fn(async () => ({
        status: "unchanged" as const,
        reason: "window-unavailable" as const,
      })),
    });
    renderApp({ desktopBridge: bridge });

    fireEvent.keyDown(document, { key: "Escape" });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not hide this window",
    );
  });

  it("surfaces rejected native HUD hide invocations", async () => {
    const bridge = desktopBridge({
      hideHud: vi.fn(async () => {
        throw new Error("window unavailable");
      }),
    });
    renderApp({ desktopBridge: bridge });

    fireEvent.keyDown(document, { key: "Escape" });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not hide this window",
    );
  });

  it("surfaces failures when the HUD cannot open native settings", async () => {
    const bridge = desktopBridge({
      showSettings: vi.fn(async () => {
        throw new Error("window unavailable");
      }),
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });

    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not open Settings",
    );
  });

  it("preserves the previous shortcut after conflict and cancellation", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const bridge = desktopBridge({
      replaceGlobalShortcut: vi.fn(async () => ({
        status: "unchanged" as const,
        reason: "conflict" as const,
      })),
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });
    const record = await screen.findByRole("button", { name: "Record shortcut" });
    await waitFor(() => expect(record).toBeEnabled());
    await user.click(record);
    fireEvent.keyDown(record, {
      code: "KeyP",
      key: "p",
      ctrlKey: true,
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "previous shortcut is still active",
    );
    expect(screen.getByText("Not configured")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Record shortcut" }));
    fireEvent.keyDown(record, { code: "Escape", key: "Escape" });
    expect(screen.getByRole("status")).toHaveTextContent(
      "Shortcut capture cancelled",
    );
    expect(bridge.replaceGlobalShortcut).toHaveBeenCalledOnce();
  });

  it("adopts the effective shortcut when persistence and rollback fail", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const bridge = desktopBridge({
      replaceGlobalShortcut: vi.fn(async (shortcut) => {
        if (shortcut === null) return { status: "cancelled" as const };
        return {
          status: "unchanged" as const,
          reason: "storage-failed" as const,
          shortcut,
        };
      }),
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });
    const record = await screen.findByRole("button", {
      name: "Record shortcut",
    });
    await waitFor(() => expect(record).toBeEnabled());
    await user.click(record);
    fireEvent.keyDown(record, {
      code: "KeyP",
      key: "p",
      ctrlKey: true,
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "effective shortcut is shown",
    );
    expect(screen.getByText("Ctrl + P")).toBeVisible();
  });

  it("clears session integration statuses after reset", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: desktopBridge() });
    const record = await screen.findByRole("button", {
      name: "Record shortcut",
    });
    await waitFor(() => expect(record).toBeEnabled());
    await user.click(record);
    fireEvent.keyDown(record, {
      code: "KeyP",
      key: "p",
      ctrlKey: true,
    });
    expect(await screen.findByText("Shortcut updated.")).toBeVisible();

    await user.click(
      screen.getByRole("checkbox", { name: "Launch DevHud at login" }),
    );
    expect(
      await screen.findByText("DevHud will launch at login."),
    ).toBeVisible();
    expect(screen.getByText("chrome://extensions")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByText("DevHud local data was reset.")).toBeVisible();
    expect(screen.queryByText("Shortcut updated.")).not.toBeInTheDocument();
    expect(
      screen.queryByText("DevHud will launch at login."),
    ).not.toBeInTheDocument();
  });

  it("offers a skippable first run while preserving native tray access", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: true,
    });
    const bridge = desktopBridge();
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });
    expect(await screen.findByRole("heading", { name: "Set up DevHud" })).toBeVisible();
    const recordShortcut = screen.getByRole("button", {
      name: "Record shortcut",
    });
    await waitFor(() => {
      expect(recordShortcut).toBeEnabled();
      expect(recordShortcut).toHaveFocus();
    });
    expect(
      screen.getByText(/remains available from the tray either way/u),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Skip for now" }));
    expect(bridge.completeFirstRun).toHaveBeenCalledOnce();
    await waitFor(() => expect(bridge.hideSettings).toHaveBeenCalledOnce());
    expect(
      screen.getByRole("heading", { name: "DevHud settings" }),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Skip for now" }),
    ).not.toBeInTheDocument();
  });

  it("completes first run from the Done action", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: true,
    });
    const bridge = desktopBridge();
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });

    await user.click(await screen.findByRole("button", { name: "Done" }));

    expect(bridge.completeFirstRun).toHaveBeenCalledOnce();
    await waitFor(() => expect(bridge.hideSettings).toHaveBeenCalledOnce());
    expect(
      screen.getByRole("heading", { name: "DevHud settings" }),
    ).toBeVisible();
  });

  it("surfaces a native settings-window hide failure", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const bridge = desktopBridge({
      hideSettings: vi.fn(async () => {
        throw new Error("window unavailable");
      }),
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge });

    await user.click(await screen.findByRole("button", { name: "Close settings" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not close Settings",
    );
  });

  it("renders persistence failures in the native settings window", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const storage: LocalStorageAdapter = {
      read: async () => {
        throw new Error("storage unavailable");
      },
      reset: async () => ({ status: "complete" as const }),
      write: async () => undefined,
    };

    renderApp({ desktopBridge: desktopBridge(), storage });
    const alerts = await screen.findAllByRole("alert");
    expect(alerts[0]).toHaveTextContent(
      "DevHud could not access local storage",
    );
  });

  it("surfaces native shortcut and autostart restoration failures", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
      shortcutStartupFailure: "conflict",
      autostartStartupOutcome: {
        status: "unchanged",
        enabled: true,
        reason: "permission-denied",
      },
    });
    const settingsRecord = JSON.stringify({
      version: 1,
      settings: {
        theme: "system",
        launchAtLogin: true,
        shortcut: {
          modifiers: ["control"],
          key: "k",
        },
      },
    });
    const storage = new MemoryStorageAdapter();
    storage.values.set(SETTINGS_STORAGE_KEY, settingsRecord);
    const bridge = desktopBridge();
    const user = userEvent.setup();

    renderApp({ desktopBridge: bridge, storage });

    expect(
      await screen.findByText(/saved shortcut is already in use/u),
    ).toBeVisible();
    expect(screen.getByText("Not configured")).toBeVisible();
    expect(
      await screen.findByText(/actual system setting is shown/u),
    ).toBeVisible();
    await waitFor(() =>
      expect(
        screen.getByRole("checkbox", {
          name: "Launch DevHud at login",
        }),
      ).toBeChecked(),
    );

    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));
    expect(await screen.findByText("DevHud local data was reset.")).toBeVisible();
    expect(
      screen.queryByText(/saved shortcut is already in use/u),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/actual system setting is shown/u),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", {
        name: "Launch DevHud at login",
      }),
    ).not.toBeChecked();
    expect(bridge.publishTheme).toHaveBeenCalledWith(ThemePreference.System);
    expect(bridge.publishReset).toHaveBeenCalledOnce();
  });

  it("adopts cleared settings while reporting reset staging cleanup failure", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const storage = new MemoryStorageAdapter();
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      JSON.stringify({
        version: 1,
        settings: {
          theme: ThemePreference.Dark,
          launchAtLogin: true,
          shortcut: null,
        },
      }),
    );
    storage.reset = async () => {
      storage.values.clear();
      return { status: "cleanup-failed" };
    };
    const bridge = desktopBridge();
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge, storage });

    const launchAtLogin = await screen.findByRole("checkbox", {
      name: "Launch DevHud at login",
    });
    await waitFor(() => expect(launchAtLogin).toBeChecked());
    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "temporary reset data or application browsing data may remain",
    );
    expect(
      screen.getByRole("checkbox", { name: "Launch DevHud at login" }),
    ).not.toBeChecked();
    expect(screen.getByRole("combobox", { name: "Theme preference" })).toHaveValue(
      ThemePreference.System,
    );
    expect(bridge.publishTheme).toHaveBeenCalledWith(ThemePreference.System);
    expect(bridge.publishReset).toHaveBeenCalledOnce();
  });

  it("adopts effective integrations when reset rollback fails", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const storage = new MemoryStorageAdapter();
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      encodeSettings({
        ...defaultSettings,
        launchAtLogin: true,
        shortcut: {
          modifiers: [ShortcutModifier.Control],
          key: ShortcutKey.K,
        },
      }),
    );
    const outcome = {
      status: "integration-rollback-failed" as const,
      shortcut: {
        modifiers: [ShortcutModifier.Control],
        key: ShortcutKey.P,
      },
      launchAtLogin: false,
    };
    storage.reset = async () => outcome;
    const bridge = desktopBridge();
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge, storage });

    await waitFor(() =>
      expect(
        screen.getByRole("checkbox", { name: "Launch DevHud at login" }),
      ).toBeChecked(),
    );
    expect(screen.getByText("Ctrl + K")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "effective shortcut and launch-at-login settings are shown",
    );
    expect(screen.getByText("Ctrl + P")).toBeVisible();
    expect(
      screen.getByRole("checkbox", { name: "Launch DevHud at login" }),
    ).not.toBeChecked();
    expect(bridge.publishReset).toHaveBeenCalledWith(outcome);
    expect(bridge.publishTheme).not.toHaveBeenCalled();
  });

  it("reports a partially retained reset separately from staging cleanup failure", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const storage = new MemoryStorageAdapter();
    storage.reset = async () => ({ status: "partially-retained" });
    const user = userEvent.setup();
    renderApp({ desktopBridge: desktopBridge(), storage });

    await user.click(await screen.findByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Some saved settings or widget state remain",
    );
  });

  it("preserves the saved autostart setting when native state is unknown", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "settings",
      firstRun: false,
    });
    const storage = new MemoryStorageAdapter();
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      JSON.stringify({
        version: 1,
        settings: {
          theme: "system",
          launchAtLogin: true,
          shortcut: null,
        },
      }),
    );
    const bridge = desktopBridge({
      setLaunchAtLogin: vi.fn(async () => ({
        status: "unknown" as const,
        reason: "permission-denied" as const,
      })),
    });
    const user = userEvent.setup();
    renderApp({ desktopBridge: bridge, storage });
    const launchAtLogin = await screen.findByRole("checkbox", {
      name: "Launch DevHud at login",
    });
    await waitFor(() => expect(launchAtLogin).toBeChecked());

    await user.click(launchAtLogin);

    expect(launchAtLogin).toBeChecked();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "saved setting is still shown",
    );
  });

  it("provides explicit mobile content states without visible widgets", async () => {
    const user = userEvent.setup();
    renderApp({ platform: "mobile" });
    expect(
      await screen.findByRole("heading", { name: "No tools yet" }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Open settings" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Open settings" }));
    expect(screen.getByRole("heading", { name: "Appearance" })).toBeVisible();
    expect(
      screen.queryByRole("heading", { name: "Global shortcut" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", { name: "Launch DevHud at login" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Widgets" }));
    expect(screen.getByRole("heading", { name: "Widgets" })).toHaveFocus();
    expect(screen.getByRole("heading", { name: "No widgets available" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Diagnostics" }));
    expect(screen.getByRole("heading", { name: "Local diagnostics" })).toHaveFocus();
    expect(screen.getByRole("heading", { name: "Runtime details" })).toBeVisible();
    expect(screen.getByText("dev.deli.devhud")).toBeVisible();
  });

  it("publishes mobile settings resets for session reconciliation", async () => {
    const publishReset = vi
      .spyOn(themeRuntime, "publishPersistenceReset")
      .mockImplementation(() => undefined);
    const user = userEvent.setup();
    renderApp({ platform: "mobile" });

    await user.click(await screen.findByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "DevHud local data was reset.",
    );
    expect(publishReset).toHaveBeenCalledWith({ status: "complete" });
  });

  it("exports diagnostics only after an explicit action and reports cancellation", async () => {
    const user = userEvent.setup();
    renderApp({ platform: "mobile" });
    await screen.findByRole("heading", { name: "No tools yet" });

    expect(exportDiagnostics).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Diagnostics" }));
    expect(exportDiagnostics).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Export diagnostics" }));

    expect(exportDiagnostics).toHaveBeenCalledOnce();
    expect(await screen.findByRole("status")).toHaveTextContent(
      "No file was changed",
    );
  });

  it("reports a completed user-selected diagnostics export without a path", async () => {
    vi.mocked(exportDiagnostics).mockResolvedValueOnce({ status: "exported" });
    const user = userEvent.setup();
    renderApp({ platform: "mobile" });
    await user.click(await screen.findByRole("button", { name: "Diagnostics" }));
    await user.click(screen.getByRole("button", { name: "Export diagnostics" }));

    const status = await screen.findByRole("status");
    expect(status).toHaveTextContent("selected destination");
    expect(status).not.toHaveTextContent("/");
    expect(status).not.toHaveTextContent("\\");
  });

  it("shows a runtime startup failure on the mobile Home screen", async () => {
    vi.mocked(loadRuntimeInfo).mockRejectedValueOnce(new Error("runtime unavailable"));
    renderApp({ platform: "mobile" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not initialize its local runtime.",
    );
    expect(screen.getByRole("button", { name: "Try again" })).toBeVisible();
  });

  it("hides native settings controls when the desktop bridge is unavailable", async () => {
    vi.mocked(loadRuntimeInfo).mockRejectedValueOnce(new Error("runtime unavailable"));
    const user = userEvent.setup();
    renderApp({ platform: "desktop" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not initialize its local runtime.",
    );

    await user.click(screen.getByRole("button", { name: "Settings" }));

    expect(
      screen.queryByRole("heading", { name: "Global shortcut" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", { name: "Launch DevHud at login" }),
    ).not.toBeInTheDocument();
  });

  it("publishes resets when runtime initialization leaves no native bridge", async () => {
    vi.mocked(loadRuntimeInfo).mockRejectedValueOnce(new Error("runtime unavailable"));
    const publishReset = vi
      .spyOn(themeRuntime, "publishPersistenceReset")
      .mockImplementation(() => undefined);
    const user = userEvent.setup();
    renderApp({ platform: "desktop" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not initialize its local runtime.",
    );

    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "DevHud local data was reset.",
    );
    expect(publishReset).toHaveBeenCalledWith({ status: "complete" });
  });

  it("keeps desktop controls out of an open HUD fallback dialog", async () => {
    let finishRuntime:
      | ((value: Awaited<ReturnType<typeof loadRuntimeInfo>>) => void)
      | undefined;
    vi.mocked(loadRuntimeInfo).mockReturnValueOnce(
      new Promise((resolve) => {
        finishRuntime = resolve;
      }),
    );
    const bridge = desktopBridge();
    const nativeBridge = vi
      .spyOn(desktopRuntime, "nativeDesktopBridge")
      .mockReturnValue(bridge);
    const user = userEvent.setup();
    renderApp({ platform: "desktop" });
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);

    finishRuntime?.({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
      toolOperatingSystem: "ubuntu",
      runtime: "cef",
      sandboxEnabled: true,
      updatePolicy: "Desktop updater unavailable",
      surface: "hud",
      firstRun: false,
    });
    await waitFor(() => expect(nativeBridge).toHaveBeenCalledWith("cef"));

    expect(screen.getByRole("dialog", { name: "DevHud settings" })).toBeVisible();
    expect(
      screen.queryByRole("heading", { name: "Global shortcut" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", { name: "Launch DevHud at login" }),
    ).not.toBeInTheDocument();
    nativeBridge.mockRestore();
  });

  it("shows explicit mobile loading states", async () => {
    const user = userEvent.setup();
    let finishRuntime:
      | ((value: Awaited<ReturnType<typeof loadRuntimeInfo>>) => void)
      | undefined;
    vi.mocked(loadRuntimeInfo).mockReturnValueOnce(
      new Promise((resolve) => {
        finishRuntime = resolve;
      }),
    );
    renderApp({ platform: "mobile" });
    expect(screen.getByRole("status")).toHaveTextContent(
      "Loading the local application runtime",
    );
    finishRuntime?.({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "android",
      toolOperatingSystem: null,
      runtime: "system-webview",
      sandboxEnabled: false,
      updatePolicy: "Unsupported",
    });
    expect(
      await screen.findByRole("heading", { name: "No tools yet" }),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Diagnostics" }));
    expect(screen.getByText("Unsupported")).toBeVisible();
  });

  it("persists a mobile theme choice across application mounts", async () => {
    const user = userEvent.setup();
    const storage = new MemoryStorageAdapter();
    const first = renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    const theme = screen.getByRole("combobox", { name: "Theme preference" });
    await waitFor(() => expect(theme).toBeEnabled());
    await user.selectOptions(theme, "dark");
    await waitFor(() =>
      expect(storage.values.get("devhud.settings.v1")).toContain('"theme":"dark"'),
    );
    first.unmount();

    renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    await waitFor(() =>
      expect(
        screen.getByRole("combobox", { name: "Theme preference" }),
      ).toHaveValue("dark"),
    );
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("requires confirmation before resetting a rejected local record", async () => {
    const user = userEvent.setup();
    const storage = new MemoryStorageAdapter();
    storage.values.set("devhud.settings.v1", "{not-json}");

    renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Reset DevHud");

    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    expect(storage.values.get("devhud.settings.v1")).toBe("{not-json}");
    const confirm = screen.getByRole("button", { name: "Confirm reset" });
    expect(confirm).toHaveFocus();

    await user.click(confirm);
    expect(await screen.findByRole("status")).toHaveTextContent(
      "DevHud local data was reset.",
    );
    expect(storage.values.size).toBe(0);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Theme preference" })).toHaveValue(
      "system",
    );
  });

  it("traps confirmation focus and cancels with Escape without changing data", async () => {
    const user = userEvent.setup();
    const storage = new MemoryStorageAdapter();
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      encodeSettings({ ...defaultSettings, theme: ThemePreference.Dark }),
    );
    const reset = vi.spyOn(storage, "reset");

    renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    const trigger = screen.getByRole("button", { name: "Reset DevHud" });
    await waitFor(() => expect(trigger).toBeEnabled());
    await user.click(trigger);

    const confirmation = screen.getByRole("dialog", {
      name: "Confirm Reset DevHud",
    });
    expect(confirmation).toBeVisible();
    const accessibility = await axe.run(confirmation, {
      rules: {
        "color-contrast": { enabled: false },
      },
    });
    expect(accessibility.violations).toEqual([]);
    const confirm = screen.getByRole("button", { name: "Confirm reset" });
    const cancel = screen.getByRole("button", { name: "Cancel" });
    expect(confirm).toHaveFocus();
    expect(screen.getByText(/previously exported will not be changed/u)).toBeVisible();
    fireEvent.keyDown(confirm, { key: "Tab", shiftKey: true });
    expect(cancel).toHaveFocus();
    fireEvent.keyDown(cancel, { key: "Tab" });
    expect(confirm).toHaveFocus();

    await user.keyboard("{Escape}");

    expect(
      screen.queryByRole("dialog", { name: "Confirm Reset DevHud" }),
    ).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
    expect(reset).not.toHaveBeenCalled();
    expect(storage.values.get(SETTINGS_STORAGE_KEY)).toContain('"theme":"dark"');

    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(trigger).toHaveFocus();
    expect(reset).not.toHaveBeenCalled();
  });

  it("keeps focus in the dialog while reset controls are disabled", async () => {
    const user = userEvent.setup();
    const storage = new MemoryStorageAdapter();
    let completeReset: (outcome: PersistenceResetOutcome) => void = () => undefined;
    storage.reset = vi.fn(
      () =>
        new Promise<PersistenceResetOutcome>((resolve) => {
          completeReset = resolve;
        }),
    );

    renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    const trigger = screen.getByRole("button", { name: "Reset DevHud" });
    await waitFor(() => expect(trigger).toBeEnabled());
    await user.click(trigger);

    const confirmation = screen.getByRole("dialog", {
      name: "Confirm Reset DevHud",
    });
    const confirm = screen.getByRole("button", { name: "Confirm reset" });
    await user.click(confirm);

    await waitFor(() => {
      expect(confirm).toBeDisabled();
      expect(confirmation).toHaveFocus();
    });
    fireEvent.keyDown(confirmation, { key: "Tab" });
    expect(confirmation).toHaveFocus();

    await act(async () => completeReset({ status: "complete" }));
    expect(await screen.findByText("DevHud local data was reset.")).toBeVisible();
  });

  it("supports repeated confirmed reset without recreating retained records", async () => {
    const user = userEvent.setup();
    const storage = new MemoryStorageAdapter();
    storage.values.set(
      SETTINGS_STORAGE_KEY,
      encodeSettings({ ...defaultSettings, theme: ThemePreference.Dark }),
    );
    const reset = vi.spyOn(storage, "reset");

    renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    const trigger = screen.getByRole("button", { name: "Reset DevHud" });
    await waitFor(() => expect(trigger).toBeEnabled());

    for (let attempt = 1; attempt <= 2; attempt += 1) {
      await user.click(trigger);
      await user.click(screen.getByRole("button", { name: "Confirm reset" }));
      await waitFor(() => expect(reset).toHaveBeenCalledTimes(attempt));
      expect(storage.values.size).toBe(0);
      expect(trigger).toHaveFocus();
    }
  });

  it("reconciles provider state after a partial reset failure", async () => {
    const user = userEvent.setup();
    const storage = new MemoryStorageAdapter();
    storage.reset = async () => {
      storage.values.delete("devhud.settings.v1");
      throw new Error("injected partial reset");
    };

    renderApp({ platform: "mobile", storage });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    const theme = screen.getByRole("combobox", { name: "Theme preference" });
    await waitFor(() => expect(theme).toBeEnabled());
    await user.selectOptions(theme, "dark");
    await waitFor(() =>
      expect(storage.values.get("devhud.settings.v1")).toContain('"theme":"dark"'),
    );

    await user.click(screen.getByRole("button", { name: "Reset DevHud" }));
    await user.click(screen.getByRole("button", { name: "Confirm reset" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "DevHud could not reset local data.",
    );
    expect(theme).toHaveValue("system");
  });

  it("has no automated accessibility violations on the mobile shell", async () => {
    const { container } = renderApp({ platform: "mobile" });
    await screen.findByRole("heading", { name: "No tools yet" });
    const results = await axe.run(container, {
      rules: {
        "color-contrast": { enabled: false },
      },
    });
    expect(results.violations).toEqual([]);
  });
});
