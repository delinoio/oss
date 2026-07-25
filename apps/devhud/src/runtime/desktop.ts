import { invoke, isTauri } from "@tauri-apps/api/core";

import type {
  StructuredShortcut,
  ThemePreference,
} from "../persistence/contracts";
import {
  publishThemePreference,
  subscribeToThemePreference,
} from "./theme";

export type ShortcutFailure =
  | "malformed"
  | "conflict"
  | "permission-denied"
  | "registration-failed"
  | "unsupported-display"
  | "storage-failed";

export type ShortcutReplacementOutcome =
  | { readonly status: "replaced"; readonly shortcut: StructuredShortcut }
  | { readonly status: "unchanged"; readonly reason: ShortcutFailure }
  | { readonly status: "cancelled" };

export type AutostartFailure = "permission-denied" | "operation-failed";

export type AutostartOutcome =
  | { readonly status: "applied"; readonly enabled: boolean }
  | {
      readonly status: "unchanged";
      readonly enabled: boolean;
      readonly reason: AutostartFailure;
    };

export type HudActionOutcome =
  | { readonly status: "shown" }
  | { readonly status: "hidden" }
  | {
      readonly status: "unchanged";
      readonly reason:
        | "unsupported-display"
        | "window-unavailable"
        | "position-failed";
    };

export type FirstRunOutcome =
  | { readonly status: "completed" }
  | {
      readonly status: "unchanged";
      readonly reason:
        | "storage-unavailable"
        | "invalid-record"
        | "write-failed";
    };

export type UpdateActionOutcome = {
  readonly status: "unavailable";
  readonly reason: "scoped-updater-unavailable";
};

export interface DesktopBridge {
  showHud(): Promise<HudActionOutcome>;
  hideHud(): Promise<HudActionOutcome>;
  showSettings(): Promise<void>;
  hideSettings(): Promise<void>;
  replaceGlobalShortcut(
    candidate: StructuredShortcut | null,
  ): Promise<ShortcutReplacementOutcome>;
  setLaunchAtLogin(enabled: boolean): Promise<AutostartOutcome>;
  completeFirstRun(): Promise<FirstRunOutcome>;
  requestUpdateAction(): Promise<UpdateActionOutcome>;
  publishTheme(theme: ThemePreference): void;
  subscribeTheme(listener: (theme: ThemePreference) => void): () => void;
}

export const tauriDesktopBridge: DesktopBridge = {
  showHud: () => invoke<HudActionOutcome>("show_hud"),
  hideHud: () => invoke<HudActionOutcome>("hide_hud"),
  showSettings: () => invoke<void>("show_settings"),
  hideSettings: () => invoke<void>("hide_settings"),
  replaceGlobalShortcut: (candidate) =>
    invoke<ShortcutReplacementOutcome>("replace_global_shortcut", { candidate }),
  setLaunchAtLogin: (enabled) =>
    invoke<AutostartOutcome>("set_launch_at_login", { enabled }),
  completeFirstRun: () => invoke<FirstRunOutcome>("complete_first_run"),
  requestUpdateAction: () =>
    invoke<UpdateActionOutcome>("request_update_action"),
  publishTheme: publishThemePreference,
  subscribeTheme: subscribeToThemePreference,
};

export function nativeDesktopBridge(
  runtime: "cef" | "system-webview",
): DesktopBridge | null {
  return desktopBridgeForRuntime(
    runtime,
    isTauri() ? tauriDesktopBridge : null,
  );
}

export function desktopBridgeForRuntime(
  runtime: "cef" | "system-webview",
  bridge: DesktopBridge | null,
): DesktopBridge | null {
  return runtime === "cef" ? bridge : null;
}
