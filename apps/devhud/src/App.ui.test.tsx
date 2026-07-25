import { cleanup, render, screen, waitFor } from "@testing-library/react";
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
  MemoryStorageAdapter,
  type LocalStorageAdapter,
} from "./persistence/storage";
import { loadRuntimeInfo } from "./runtime/startup";

afterEach(cleanup);

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
    await user.keyboard("{Shift>}{Tab}{/Shift}");
    expect(screen.getByRole("combobox", { name: "Theme preference" })).toHaveFocus();
    await user.keyboard("{Tab}");
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

  it("does not expose launch-at-login without the native startup integration", async () => {
    const user = userEvent.setup();
    renderApp();
    await user.click(screen.getAllByRole("button", { name: "Settings" })[0]!);
    expect(screen.queryByRole("checkbox", { name: "Launch DevHud at login" })).toBeNull();
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

  it("provides explicit mobile content states without visible widgets", async () => {
    const user = userEvent.setup();
    renderApp({ platform: "mobile" });
    expect(
      await screen.findByRole("heading", { name: "No tools yet" }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Open settings" })).toBeVisible();
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
