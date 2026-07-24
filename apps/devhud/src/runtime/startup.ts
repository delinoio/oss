import { invoke } from "@tauri-apps/api/core";

export interface RuntimeInfo {
  applicationId: "dev.deli.devhud";
  bundledOrigin: string;
  runtime: "cef" | "system-webview";
  sandboxEnabled: boolean;
}

interface NativeCommandResults {
  get_runtime_info: RuntimeInfo;
}

export interface RuntimeBridge {
  invoke<K extends keyof NativeCommandResults>(
    command: K,
  ): Promise<NativeCommandResults[K]>;
}

export const tauriRuntimeBridge: RuntimeBridge = {
  invoke: <K extends keyof NativeCommandResults>(command: K) =>
    invoke<NativeCommandResults[K]>(command),
};

export function loadRuntimeInfo(
  bridge: RuntimeBridge,
): Promise<RuntimeInfo> {
  return bridge.invoke("get_runtime_info");
}
