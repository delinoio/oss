// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DesktopUpdaterStatus, NativeBridgeEventV1, NativeBridgeRequestV1, NativeBridgeResponseV1, NativeBridgeV1 } from "./native-bridge";
import { DesktopUpdaterPanel } from "./updater-ui";

const available: DesktopUpdaterStatus = {
  kind: "available",
  installedVersion: "0.1.0",
  target: "linux-x86_64",
  packageKind: "linux-appimage",
  candidate: { version: "0.2.0", releaseNotes: { en: "English signed notes", ko: "한국어 서명 노트" } },
  diagnostic: null,
};

function bridgeWithStatus(initial: DesktopUpdaterStatus) {
  let status = initial;
  const operations: string[] = [];
  const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
    operations.push(value.operation);
    if (value.operation === "updates.status") return { kind: "desktop-update-status", status };
    if (value.operation === "updates.approve-download") status = { ...status, kind: "downloaded" };
    if (value.operation === "updates.approve-installation") status = { ...status, kind: "installation-approved" };
    if (value.operation === "updates.approve-restart") status = { ...status, kind: "restarting" };
    return { kind: "desktop-update-status", status };
  });
  const bridge: NativeBridgeV1 = { request, async listen(_listener: (event: NativeBridgeEventV1) => void) { return () => {}; } };
  return { bridge, operations };
}

afterEach(cleanup);

describe("desktop updater approvals", () => {
  it.each([
    ["en", "English signed notes", "Desktop updates"],
    ["ko", "한국어 서명 노트", "데스크톱 업데이트"],
  ] as const)("renders accessible %s release notes", async (language, notes, title) => {
    const { bridge } = bridgeWithStatus(available);
    render(<DesktopUpdaterPanel bridge={bridge} language={language} />);
    expect(await screen.findByRole("heading", { name: title })).toBeTruthy();
    expect(screen.getByText(notes)).toBeTruthy();
    expect(screen.getByRole("status")).toBeTruthy();
  });

  it("requires separate download, installation, and restart confirmation", async () => {
    const { bridge, operations } = bridgeWithStatus(available);
    render(<DesktopUpdaterPanel bridge={bridge} language="en" />);
    await screen.findByText("English signed notes");
    expect(operations).toEqual(["updates.status"]);

    fireEvent.click(screen.getByRole("button", { name: "Approve download" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(operations).toEqual(["updates.status"]);
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByRole("button", { name: "Approve installation" });

    fireEvent.click(screen.getByRole("button", { name: "Approve installation" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByRole("button", { name: "Install and restart" });

    fireEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    const confirm = screen.getByRole("button", { name: "Confirm" });
    expect(confirm).toBe(document.activeElement);
    fireEvent.click(confirm);
    await waitFor(() => expect(operations).toEqual([
      "updates.status", "updates.approve-download", "updates.approve-installation", "updates.approve-restart",
    ]));
  });

  it("cancels a confirmation with Escape without invoking a native action", async () => {
    const { bridge, operations } = bridgeWithStatus(available);
    render(<DesktopUpdaterPanel bridge={bridge} language="en" />);
    await screen.findByText("English signed notes");
    fireEvent.click(screen.getByRole("button", { name: "Approve download" }));
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(operations).toEqual(["updates.status"]);
  });

  it("retries only restart after a package-manager install has completed", async () => {
    const restartRequired: DesktopUpdaterStatus = {
      ...available,
      kind: "restart-required",
      packageKind: "linux-deb",
      diagnostic: { code: "restart-failed", phase: "restart", target: "linux-x86_64", packageKind: "linux-deb", installedVersion: "0.1.0", candidateVersion: "0.2.0" },
    };
    const { bridge, operations } = bridgeWithStatus(restartRequired);
    render(<DesktopUpdaterPanel bridge={bridge} language="en" />);

    expect((await screen.findByRole("alert")).textContent).toContain("The update is installed");
    expect(screen.getByText("Running version")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry restart" }));
    expect(screen.getByRole("dialog").textContent).toContain("without reinstalling");
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(operations).toEqual(["updates.status", "updates.approve-restart"]));
  });

  it("renders a localized typed diagnostic without transport details", async () => {
    const failed: DesktopUpdaterStatus = {
      ...available,
      kind: "failed",
      diagnostic: { code: "invalid-signature", phase: "verification", target: "linux-x86_64", packageKind: "linux-appimage", installedVersion: "0.1.0", candidateVersion: "0.2.0" },
    };
    const { bridge } = bridgeWithStatus(failed);
    render(<DesktopUpdaterPanel bridge={bridge} language="ko" />);
    expect((await screen.findByRole("alert")).textContent).toContain("업데이트 서명을 신뢰할 수 없습니다");
    expect(screen.queryByText(/https?:/u)).toBeNull();
  });
});
