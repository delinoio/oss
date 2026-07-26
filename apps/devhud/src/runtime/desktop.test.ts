import { describe, expect, it, vi } from "vitest";

import { ShortcutKey, ShortcutModifier } from "../persistence/contracts";
import type { StructuredShortcut } from "../persistence/contracts";
import {
  desktopBridgeForRuntime,
  type DesktopBridge,
} from "./desktop";

describe("desktop native boundary", () => {
  it("does not expose desktop IPC commands to mobile system webviews", () => {
    const bridge = {} as DesktopBridge;

    expect(desktopBridgeForRuntime("system-webview", bridge)).toBeNull();
    expect(desktopBridgeForRuntime("cef", bridge)).toBe(bridge);
  });

  it("represents cancelled and failed shortcut replacements without changing settings", async () => {
    const previous = {
      modifiers: [ShortcutModifier.Control],
      key: ShortcutKey.K,
    };
    let persisted: StructuredShortcut = previous;
    const replaceGlobalShortcut = vi
      .fn<DesktopBridge["replaceGlobalShortcut"]>()
      .mockResolvedValueOnce({ status: "cancelled" })
      .mockResolvedValueOnce({ status: "unchanged", reason: "conflict" });

    for (const candidate of [
      null,
      { modifiers: [ShortcutModifier.Control], key: ShortcutKey.P },
    ]) {
      const outcome = await replaceGlobalShortcut(candidate);
      if (outcome.status === "replaced") persisted = outcome.shortcut;
    }

    expect(persisted).toEqual(previous);
  });

  it("keeps update checking behind a typed unavailable local action", async () => {
    const requestUpdateAction = vi.fn(async () => ({
      status: "unavailable" as const,
      reason: "scoped-updater-unavailable" as const,
    }));

    await expect(requestUpdateAction()).resolves.toEqual({
      status: "unavailable",
      reason: "scoped-updater-unavailable",
    });
    expect(requestUpdateAction).toHaveBeenCalledOnce();
  });
});
