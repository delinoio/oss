import { describe, expect, it, vi } from "vitest";

import {
  isCapabilityDenial,
  runBundledStartupHandshake,
  runPlatformGateIfEnabled,
  type ProbeBridge,
  type StartupReceipt,
} from "./startup";

const receipt: StartupReceipt = {
  applicationId: "dev.deli.devhud",
  bundledOrigin: "http://tauri.localhost",
  runtime: "cef",
  sandboxEnabled: true,
};

function bridgeWith(
  invoke: (
    command: Parameters<ProbeBridge["invoke"]>[0],
  ) => Promise<StartupReceipt | null>,
): ProbeBridge {
  return { invoke: invoke as ProbeBridge["invoke"] };
}

describe("bundled startup handshake", () => {
  it("requires an allowed startup command and an observed capability denial", async () => {
    const calls: string[] = [];
    const bridge = bridgeWith(async (command) => {
      calls.push(command);
      if (command === "probe_bundled_asset_ready") {
        return receipt;
      }
      if (command === "probe_forbidden") {
        throw new Error("Command probe_forbidden not allowed by capability");
      }
      return null;
    });

    await expect(runBundledStartupHandshake(bridge)).resolves.toEqual({
      receipt,
      capabilityDenied: true,
    });
    expect(calls).toEqual([
      "probe_bundled_asset_ready",
      "probe_forbidden",
      "probe_denial_observed",
    ]);
  });

  it("fails if the forbidden command becomes callable", async () => {
    const bridge = bridgeWith(async (command) => {
      if (command === "probe_bundled_asset_ready") {
        return receipt;
      }
      return null;
    });

    await expect(runBundledStartupHandshake(bridge)).rejects.toThrow(
      "unexpectedly passed",
    );
  });

  it("does not misclassify unrelated invocation failures as a denial", async () => {
    const observed = vi.fn();
    const bridge = bridgeWith(async (command) => {
      if (command === "probe_bundled_asset_ready") {
        return receipt;
      }
      if (command === "probe_forbidden") {
        throw new Error("renderer disconnected");
      }
      observed();
      return null;
    });

    await expect(runBundledStartupHandshake(bridge)).rejects.toThrow(
      "renderer disconnected",
    );
    expect(observed).not.toHaveBeenCalled();
  });
});

describe("capability denial classification", () => {
  it.each([
    "not allowed by ACL",
    "permission denied",
    "missing capability",
  ])("accepts %s", (message) => {
    expect(isCapabilityDenial(message)).toBe(true);
  });

  it("rejects unrelated failures", () => {
    expect(isCapabilityDenial(new Error("renderer disconnected"))).toBe(false);
  });
});

describe("platform gate routing", () => {
  it("does not invoke macOS-only commands when the gate is disabled", async () => {
    const calls: string[] = [];
    const bridge = bridgeWith(async (command) => {
      calls.push(command);
      return "disabled" as never;
    });

    await expect(runPlatformGateIfEnabled(bridge)).resolves.toBe("disabled");
    expect(calls).toEqual(["probe_gate_mode"]);
  });

  it("completes the normal macOS gate explicitly", async () => {
    const calls: string[] = [];
    const bridge = bridgeWith(async (command) => {
      calls.push(command);
      return (command === "probe_gate_mode" ? "normal" : null) as never;
    });

    await expect(runPlatformGateIfEnabled(bridge)).resolves.toBe("normal");
    expect(calls).toEqual([
      "probe_gate_mode",
      "probe_macos_gate_run",
      "probe_macos_gate_complete",
    ]);
  });

  it("waits for external renderer termination without normal shutdown", async () => {
    const calls: string[] = [];
    const bridge = bridgeWith(async (command) => {
      calls.push(command);
      return (command === "probe_gate_mode"
        ? "renderer-termination"
        : null) as never;
    });

    await expect(runPlatformGateIfEnabled(bridge)).resolves.toBe(
      "renderer-termination",
    );
    expect(calls).toEqual([
      "probe_gate_mode",
      "probe_macos_gate_run",
      "probe_macos_gate_renderer_ready",
    ]);
  });

  it("reports a gate command failure before rejecting", async () => {
    const calls: string[] = [];
    const bridge = bridgeWith(async (command) => {
      calls.push(command);
      if (command === "probe_gate_mode") {
        return "normal" as never;
      }
      if (command === "probe_macos_gate_run") {
        throw new Error("autostart probe failed");
      }
      return null;
    });

    await expect(runPlatformGateIfEnabled(bridge)).rejects.toThrow(
      "autostart probe failed",
    );
    expect(calls).toEqual([
      "probe_gate_mode",
      "probe_macos_gate_run",
      "probe_gate_failure",
    ]);
  });
});
