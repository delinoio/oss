import { render, screen } from "@testing-library/react";
import { afterEach, vi } from "vitest";

import { AuthGuardDecision, AuthGuardResult } from "@/lib/auth-guard";
import {
  DevkitMiniAppId,
  DevkitRoute,
  MINI_APP_REGISTRATIONS,
} from "@/lib/mini-app-registry";
import * as logger from "@/lib/logger";

import { DevkitShell } from "./devkit-shell";

describe("DevkitShell", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders one navigation link per registered mini app", () => {
    render(
      <DevkitShell title="Test" currentRoute={DevkitRoute.Home}>
        <p>body</p>
      </DevkitShell>,
    );

    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(MINI_APP_REGISTRATIONS.length);
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
