import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export type AppIdentity =
  | { platform: "macos"; bundle_id: string }
  | { platform: "windows-app"; app_user_model_id: string }
  | { platform: "windows-executable"; path: string }
  | { platform: "linux"; class: string; desktop_entry_id: string | null };
export interface ExcludedApp {
  identity: AppIdentity;
  displayName: string;
}
export interface Settings {
  schemaVersion: number;
  paused: boolean;
  koreanEnabled: boolean;
  englishEnabled: boolean;
  loginStartup: boolean;
  uiLanguage: "system" | "en" | "ko";
  theme: "system" | "light" | "dark";
  shortcut: string | null;
  excludedApps: ExcludedApp[];
}
export interface UpdateState {
  phase:
    "idle" | "checking" | "available" | "downloading" | "installing" | "failed";
  version: string | null;
  progress: number | null;
  error: string | null;
}
export interface DesktopState {
  revision: number;
  settings: Settings;
  version: string;
  platform: string;
  osLanguage: "en" | "ko";
  status: string;
  active: boolean;
  settingsError: string | null;
  logsHealthy: boolean;
  diagnosticError?: string | null;
  unresolvedApps: number[];
  update: UpdateState;
}
export interface CandidateState {
  language?: "en" | "ko" | null;
  theme?: "system" | "light" | "dark" | null;
  generation: number;
  revision?: number;
  selected: number;
  items: {
    text: string;
    language: string;
    isOriginal: boolean;
    validation: "recognized" | "not-recognized" | "validator-unavailable";
  }[];
}
export interface DesktopApi {
  state(): Promise<DesktopState>;
  save(settings: Settings, revision: number): Promise<DesktopState>;
  action(
    name:
      | "recheck"
      | "permission-settings"
      | "request-permission"
      | "open-logs"
      | "delete-logs"
      | "support"
      | "restore-backup"
      | "check-update"
      | "install-update",
  ): Promise<void>;
  apps(): Promise<ExcludedApp[]>;
  candidates(callback: (state: CandidateState) => void): Promise<() => void>;
  candidateAction(
    generation: number,
    action: "next" | "previous" | "apply" | "close",
    index?: number,
  ): Promise<void>;
}
export const desktopApi: DesktopApi = {
  state: () => invoke("desktop_state"),
  save: (settings, revision) => invoke("save_settings", { settings, revision }),
  action: (action) => invoke("desktop_action", { action }),
  apps: () => invoke("available_apps"),
  candidates: async (callback) => {
    const stop = await listen<CandidateState>(
      "fixkey-candidates",
      ({ payload }) => callback(payload),
    );
    // Subscription is installed first so a concurrent invalidation cannot be missed.
    try {
      callback(await invoke("candidate_state"));
    } catch {
      stop();
      throw new Error("candidate-unavailable");
    }
    return stop;
  },
  candidateAction: (generation, action, index) =>
    invoke("candidate_action", { generation, action, index: index ?? null }),
};

export function errorCode(error: unknown): string {
  if (typeof error === "string" && /^[a-z][a-z0-9-]{1,80}$/.test(error))
    return error;
  if (
    error &&
    typeof error === "object" &&
    "reason" in error &&
    typeof error.reason === "string" &&
    /^[a-z][a-z0-9-]{1,80}$/.test(error.reason)
  )
    return error.reason;
  return "operation-failed";
}
