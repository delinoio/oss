import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup } from "@testing-library/react";

vi.mock("./runtime/startup", () => ({
  loadRuntimeInfo: vi.fn(async () => ({ runtime: "cef" })),
  tauriRuntimeBridge: {},
}));

import { App } from "./App";
import type { LocalStorageAdapter } from "./persistence/storage";
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
    ...overrides,
  };
}

describe("DevHud application surfaces", () => {
  it("focuses the desktop search field and presents the exact empty state", async () => {
    render(<App />);
    const search = screen.getByRole("searchbox", { name: "Search tools" });
    expect(search).toHaveFocus();
    expect(screen.getByText("No tools are available in this foundation preview.")).toBeVisible();
  });

  it("traps focus in settings, closes with Escape, and restores focus", async () => {
    const user = userEvent.setup();
    render(<App />);
    const settings = screen.getAllByRole("button", { name: "Settings" })[0];
    if (settings === undefined) throw new Error("Settings trigger is missing");
    await user.click(settings);
    expect(screen.getByRole("dialog", { name: "DevHud settings" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Close settings" })).toHaveFocus();
    await user.keyboard("{Shift>}{Tab}{/Shift}");
    expect(screen.getByRole("button", { name: "Done" })).toHaveFocus();
    await user.keyboard("{Tab}");
    expect(screen.getByRole("button", { name: "Close settings" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(settings).toHaveFocus();
  });

  it("defaults to System and applies an explicit theme choice", async () => {
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    const theme = screen.getByRole("combobox", { name: "Theme preference" });
    expect(theme).toHaveValue("system");
    await user.selectOptions(theme, "dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(theme).toHaveFocus();
  });

  it("keeps launch-at-login disabled until the user explicitly enables it", async () => {
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    const launchAtLogin = screen.getByRole("checkbox", {
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
      write: async () => undefined,
    };

    render(<App storage={storage} />);
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    const theme = screen.getByRole("combobox", { name: "Theme preference" });
    expect(theme).toBeDisabled();
    completeRead?.(null);
    await waitFor(() => expect(theme).toBeEnabled());
  });

  it("hides the application shell from assistive technology while settings is open", async () => {
    const user = userEvent.setup();
    render(<App />);
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
      write: async () => undefined,
    };

    render(<App storage={storage} />);
    const alerts = await screen.findAllByRole("alert");
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    for (const alert of alerts) {
      expect(alert.closest("div[aria-hidden]")).toHaveAttribute("aria-hidden", "true");
      expect(alert.closest("div[inert]")).toHaveAttribute("inert");
    }
  });

  it("has no automated accessibility violations", async () => {
    const { container } = render(<App />);
    const results = await axe.run(container, {
      rules: {
        "color-contrast": { enabled: false },
      },
    });
    expect(results.violations).toEqual([]);
  });

  it("hides the native HUD on Escape and focus loss", async () => {
    const bridge = desktopBridge();
    render(<App desktopBridge={bridge} />);
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.blur(window);
    expect(bridge.hideHud).toHaveBeenCalledTimes(2);
  });

  it("preserves the previous shortcut after conflict and cancellation", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      runtime: "cef",
      sandboxEnabled: true,
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
    render(<App desktopBridge={bridge} />);
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

  it("offers a skippable first run while preserving native tray access", async () => {
    vi.mocked(loadRuntimeInfo).mockResolvedValueOnce({
      applicationId: "dev.deli.devhud",
      bundledOrigin: "http://tauri.localhost",
      runtime: "cef",
      sandboxEnabled: true,
      surface: "settings",
      firstRun: true,
    });
    const bridge = desktopBridge();
    const user = userEvent.setup();
    render(<App desktopBridge={bridge} />);
    expect(await screen.findByRole("heading", { name: "Set up DevHud" })).toBeVisible();
    expect(
      screen.getByText(/remains available from the tray either way/u),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Skip for now" }));
    expect(bridge.completeFirstRun).toHaveBeenCalledOnce();
    await waitFor(() => expect(bridge.hideSettings).toHaveBeenCalledOnce());
  });

  it("provides explicit mobile content states without visible widgets", async () => {
    const user = userEvent.setup();
    render(<App platform="mobile" />);
    expect(screen.getByRole("heading", { name: "No tools yet" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Open settings" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Widgets" }));
    expect(screen.getByRole("heading", { name: "No widgets available" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Diagnostics" }));
    expect(screen.getByRole("heading", { name: "Diagnostics are unavailable" })).toBeVisible();
  });

  it("shows a runtime startup failure on the mobile Home screen", async () => {
    vi.mocked(loadRuntimeInfo).mockRejectedValueOnce(new Error("runtime unavailable"));
    render(<App platform="mobile" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("DevHud could not initialize its local runtime.");
  });
});
