import { describe, expect, it, vi } from "vitest";

import {
  loadRuntimeInfo,
  type RuntimeBridge,
  type RuntimeInfo,
} from "./startup";

const runtimeInfo: RuntimeInfo = {
  applicationId: "dev.deli.devhud",
  bundledOrigin: "http://tauri.localhost",
  runtime: "cef",
  sandboxEnabled: true,
};

describe("runtime startup", () => {
  it("loads runtime information through the only native command", async () => {
    const invoke = vi.fn(async () => runtimeInfo);
    const bridge = { invoke } as RuntimeBridge;

    await expect(loadRuntimeInfo(bridge)).resolves.toEqual(runtimeInfo);
    expect(invoke).toHaveBeenCalledOnce();
    expect(invoke).toHaveBeenCalledWith("get_runtime_info");
  });

  it("surfaces runtime initialization failures", async () => {
    const error = new Error("runtime unavailable");
    const bridge = {
      invoke: vi.fn(async () => {
        throw error;
      }),
    } as RuntimeBridge;

    await expect(loadRuntimeInfo(bridge)).rejects.toBe(error);
  });
});
