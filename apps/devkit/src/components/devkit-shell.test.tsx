import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, vi } from "vitest";

import { AuthGuardDecision, AuthGuardResult } from "@/lib/auth-guard";
import {
  DevkitMiniAppId,
  DevkitRoute,
  MINI_APP_REGISTRATIONS,
} from "@/lib/mini-app-registry";
import * as logger from "@/lib/logger";

import { DevkitShell } from "./devkit-shell";

function mockMatchMedia(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
}

describe("DevkitShell", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders one navigation link per registered mini app", () => {
    render(
      <DevkitShell title="Test" currentRoute={DevkitRoute.Home}>
        <p>body</p>
      </DevkitShell>,
    );

    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(MINI_APP_REGISTRATIONS.length + 1);
  });

  it("marks the home route with aria-current", () => {
    render(
      <DevkitShell title="Home" currentRoute={DevkitRoute.Home}>
        <p>home content</p>
      </DevkitShell>,
    );

    const activeLink = screen.getByRole("link", { name: "Home" });
    expect(activeLink).toHaveAttribute("aria-current", "page");
  });

  it("marks the active route with aria-current", () => {
    render(
      <DevkitShell
        title="Remote"
        currentRoute={DevkitRoute.RemoteFilePicker}
        miniAppId={DevkitMiniAppId.RemoteFilePicker}
      >
        <p>remote content</p>
      </DevkitShell>,
    );

    const activeLink = screen.getByRole("link", { name: "Remote File Picker" });
    expect(activeLink).toHaveAttribute("aria-current", "page");
  });

  it("toggles drawer open and closed through the mobile menu button", async () => {
    mockMatchMedia(true);
    const user = userEvent.setup();

    render(
      <DevkitShell title="Home" currentRoute={DevkitRoute.Home}>
        <p>content</p>
      </DevkitShell>,
    );

    const menuButton = screen.getByRole("button", {
      name: "Toggle mini app navigation menu",
    });

    expect(menuButton).toHaveAttribute("aria-expanded", "false");

    await user.click(menuButton);
    expect(menuButton).toHaveAttribute("aria-expanded", "true");

    await user.click(menuButton);
    expect(menuButton).toHaveAttribute("aria-expanded", "false");
  });

  it("closes the drawer when clicking overlay or navigation links", async () => {
    mockMatchMedia(true);
    const user = userEvent.setup();

    render(
      <DevkitShell
        title="Remote"
        currentRoute={DevkitRoute.RemoteFilePicker}
        miniAppId={DevkitMiniAppId.RemoteFilePicker}
      >
        <p>content</p>
      </DevkitShell>,
    );

    const menuButton = screen.getByRole("button", {
      name: "Toggle mini app navigation menu",
    });

    await user.click(menuButton);
    expect(menuButton).toHaveAttribute("aria-expanded", "true");

    const closeButton = screen.getByRole("button", {
      name: "Close mini app navigation menu",
    });
    await user.click(closeButton);
    expect(menuButton).toHaveAttribute("aria-expanded", "false");

    await user.click(menuButton);
    expect(menuButton).toHaveAttribute("aria-expanded", "true");

    await user.click(screen.getByRole("link", { name: "Home" }));
    expect(menuButton).toHaveAttribute("aria-expanded", "false");
  });

  it("removes hidden mobile drawer links from tab order when closed", async () => {
    mockMatchMedia(true);
    const user = userEvent.setup();

    render(
      <DevkitShell title="Home" currentRoute={DevkitRoute.Home}>
        <p>content</p>
      </DevkitShell>,
    );

    const menuButton = screen.getByRole("button", {
      name: "Toggle mini app navigation menu",
    });
    const sidebarElement = document.getElementById("dk-shell-navigation");
    const homeLink = sidebarElement?.querySelector('a[href="/"]');

    expect(homeLink).toBeTruthy();

    await waitFor(() => {
      expect(homeLink).toHaveAttribute("tabindex", "-1");
      expect(sidebarElement).toHaveAttribute("aria-hidden", "true");
    });

    await user.click(menuButton);

    expect(homeLink).not.toHaveAttribute("tabindex");
    expect(sidebarElement).not.toHaveAttribute("aria-hidden");
  });

  it("logs route render with required baseline fields", () => {
    const logSpy = vi.spyOn(logger, "logInfo").mockImplementation(() => undefined);

    render(
      <DevkitShell title="Home" currentRoute={DevkitRoute.Home}>
        <p>content</p>
      </DevkitShell>,
    );

    expect(logSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: logger.LogEvent.RouteRender,
        route: DevkitRoute.Home,
      }),
    );
  });

  it("falls back to allow when guard throws", () => {
    const throwingGuard = () => {
      throw new Error("guard failed");
    };

    render(
      <DevkitShell title="Home" currentRoute={DevkitRoute.Home} guard={throwingGuard}>
        <p>content remains visible</p>
      </DevkitShell>,
    );

    expect(screen.getByText("content remains visible")).toBeInTheDocument();
  });

  it("blocks rendering when guard explicitly denies access", () => {
    const denyGuard = (): AuthGuardResult => ({
      decision: AuthGuardDecision.Deny,
      reason: "test deny",
    });

    render(
      <DevkitShell
        title="Denied"
        currentRoute={DevkitRoute.CommitTracker}
        miniAppId={DevkitMiniAppId.CommitTracker}
        guard={denyGuard}
      >
        <p>should not appear</p>
      </DevkitShell>,
    );

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText("should not appear")).not.toBeInTheDocument();
  });
});
