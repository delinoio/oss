import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./runtime/startup", () => ({
  loadRuntimeInfo: vi.fn(async () => ({
    applicationId: "dev.deli.devhud",
    bundledOrigin: "http://tauri.localhost",
    operatingSystem: "linux",
    runtime: "cef",
    sandboxEnabled: true,
    updatePolicy: "Desktop updater unavailable",
  })),
  tauriRuntimeBridge: {},
}));

import { App } from "./App";
import {
  SETTINGS_STORAGE_KEY,
  ThemePreference,
} from "./persistence/contracts";
import {
  MemoryStorageAdapter,
  type LocalStorageAdapter,
} from "./persistence/storage";
import type { DesktopBridge } from "./runtime/desktop";
import { loadRuntimeInfo } from "./runtime/startup";

afterEach(cleanup);

function desktopBridge(
  overrides: Partial<DesktopBridge> = {},
): DesktopBridge {
  return {
    showHud: vi.fn(async () => ({ status: "shown" as const })),
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
    publishTheme: vi.fn(),
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
  it("focuses the desktop search field and presents the exact empty state", async () => {
    renderApp();
    const search = screen.getByRole("searchbox", { name: "Search tools" });
    expect(search).toHaveFocus();
    expect(screen.getByText("No tools are available in this foundation preview.")).toBeVisible();
  });

  it("traps focus in settings, closes with Escape, and restores focus", async () => {
    const user = userEvent.setup();
    renderApp();
    const settings = screen.getAllByRole("button", { name: "Settings" })[0];
    if (settings === undefined) throw new Error("Settings trigger is missing");
    await user.click(settings);
    expect(screen.getByRole("dialog", { name: "DevHud settings" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Close settings" })).toHaveFocus();
    await user.keyboard("{Shift>}{Tab}{/Shift}");
    expect(screen.getByRole("button", { name: "Reset DevHud" })).toHaveFocus();
    await user.keyboard("{Tab}");
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

  it("keeps launch-at-login disabled until the user explicitly enables it", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
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
      reset: async () => undefined,
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
      reset: async () => undefined,
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

  it("preserves the previous shortcut after conflict and cancellation", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
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

  it("offers a skippable first run while preserving native tray access", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
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

  it("renders persistence failures in the native settings window", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      operatingSystem: "linux",
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
      reset: async () => undefined,
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
