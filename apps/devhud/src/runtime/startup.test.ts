import { describe, expect, it, vi } from "vitest";

import {
  isCapabilityDenial,
  runBundledStartupHandshake,
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
