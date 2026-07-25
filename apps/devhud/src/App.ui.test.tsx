import { render, screen, waitFor } from "@testing-library/react";
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
import { loadRuntimeInfo } from "./runtime/startup";

afterEach(cleanup);

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
    expect(screen.getByRole("combobox", { name: "Theme preference" })).toHaveFocus();
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

  it("does not expose launch-at-login without the native startup integration", async () => {
    const user = userEvent.setup();
    render(<App />);
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
