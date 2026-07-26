import { invoke } from "@tauri-apps/api/core";

import type {
  AutostartOutcome,
  ShortcutFailure,
} from "./desktop";

export interface RuntimeInfo {
  applicationId: "dev.deli.devhud";
  bundledOrigin: string;
  operatingSystem: "android" | "ios" | "linux" | "macos" | "windows";
  runtime: "cef" | "system-webview";
  sandboxEnabled: boolean;
  surface?: "hud" | "settings" | "mobile";
  firstRun?: boolean;
  shortcutStartupFailure?: ShortcutFailure | null;
  autostartStartupOutcome?: AutostartOutcome | null;
  updatePolicy: "Unsupported" | "Desktop updater unavailable";
}

export type DiagnosticsExportOutcome =
  | { status: "exported" }
  | { status: "cancelled" };

interface NativeCommandResults {
  get_runtime_info: RuntimeInfo;
  export_diagnostics: DiagnosticsExportOutcome;
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

export function exportDiagnostics(
  bridge: RuntimeBridge,
): Promise<DiagnosticsExportOutcome> {
  return bridge.invoke("export_diagnostics");
}
