import { describe, expect, it, vi } from "vitest";

import {
  exportDiagnostics,
  loadRuntimeInfo,
  type RuntimeBridge,
  type RuntimeInfo,
} from "./startup";

const runtimeInfo: RuntimeInfo = {
  applicationId: "dev.deli.devhud",
  bundledOrigin: "http://tauri.localhost",
  operatingSystem: "linux",
  runtime: "cef",
  sandboxEnabled: true,
  updatePolicy: "Desktop updater unavailable",
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

  it("exports only through the explicit scoped native command", async () => {
    const invoke = vi.fn(async () => ({ status: "cancelled" as const }));
    const bridge = { invoke } as RuntimeBridge;

    await expect(exportDiagnostics(bridge)).resolves.toEqual({
      status: "cancelled",
    });
    expect(invoke).toHaveBeenCalledOnce();
    expect(invoke).toHaveBeenCalledWith("export_diagnostics");
  });
});
