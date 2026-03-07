import { DexDexPageId } from "../contracts/dexdex-page";

const localStorageKey = "dexdex.desktop.local-store.v1";

export enum LocalEnvironmentHealth {
  Unknown = "UNKNOWN",
  Healthy = "HEALTHY",
  Unreachable = "UNREACHABLE",
}

export type AutomationItem = {
  id: string;
  name: string;
  schedule: string;
  enabled: boolean;
  lastRunAt: string | null;
};

export type LocalEnvironmentItem = {
  id: string;
  name: string;
  endpointUrl: string;
  health: LocalEnvironmentHealth;
  lastCheckedAt: string | null;
  lastErrorMessage: string | null;
};

export type SettingsState = {
  defaultPage: DexDexPageId;
  compactMode: boolean;
  autoStartStream: boolean;
};

export type DesktopLocalStoreState = {
  automations: AutomationItem[];
  localEnvironments: LocalEnvironmentItem[];
  settings: SettingsState;
  lastSelectedAutomationId: string | null;
  lastSelectedEnvironmentId: string | null;
};

const defaultState: DesktopLocalStoreState = {
  automations: [
    {
      id: "automation-daily-inbox",
      name: "Daily Inbox Digest",
      schedule: "Every weekday 09:00",
      enabled: true,
      lastRunAt: null,
    },
  ],
  localEnvironments: [
    {
      id: "env-local-main",
      name: "Local Main Server",
      endpointUrl: "http://127.0.0.1:7878",
      health: LocalEnvironmentHealth.Unknown,
      lastCheckedAt: null,
      lastErrorMessage: null,
    },
  ],
  settings: {
    defaultPage: DexDexPageId.Threads,
    compactMode: false,
    autoStartStream: false,
  },
  lastSelectedAutomationId: "automation-daily-inbox",
  lastSelectedEnvironmentId: "env-local-main",
};

function safeJsonParse(raw: string): unknown {
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function normalizeState(candidate: unknown): DesktopLocalStoreState {
  if (!candidate || typeof candidate !== "object") {
    return structuredClone(defaultState);
  }

  const source = candidate as Partial<DesktopLocalStoreState>;
  const settings = source.settings ?? defaultState.settings;

  return {
    automations: Array.isArray(source.automations) ? source.automations : defaultState.automations,
    localEnvironments: Array.isArray(source.localEnvironments)
      ? source.localEnvironments
      : defaultState.localEnvironments,
    settings: {
      defaultPage:
        settings.defaultPage && Object.values(DexDexPageId).includes(settings.defaultPage)
          ? settings.defaultPage
          : defaultState.settings.defaultPage,
      compactMode:
        typeof settings.compactMode === "boolean"
          ? settings.compactMode
          : defaultState.settings.compactMode,
      autoStartStream:
        typeof settings.autoStartStream === "boolean"
          ? settings.autoStartStream
          : defaultState.settings.autoStartStream,
    },
    lastSelectedAutomationId:
      typeof source.lastSelectedAutomationId === "string" ||
      source.lastSelectedAutomationId === null
        ? source.lastSelectedAutomationId
        : defaultState.lastSelectedAutomationId,
    lastSelectedEnvironmentId:
      typeof source.lastSelectedEnvironmentId === "string" ||
      source.lastSelectedEnvironmentId === null
        ? source.lastSelectedEnvironmentId
        : defaultState.lastSelectedEnvironmentId,
  };
}

export function loadDesktopLocalStoreState(): DesktopLocalStoreState {
  if (typeof window === "undefined") {
    return structuredClone(defaultState);
  }

  const rawValue = window.localStorage.getItem(localStorageKey);
  if (!rawValue) {
    return structuredClone(defaultState);
  }

  return normalizeState(safeJsonParse(rawValue));
}

export function saveDesktopLocalStoreState(state: DesktopLocalStoreState): DesktopLocalStoreState {
  const normalized = normalizeState(state);
  if (typeof window !== "undefined") {
    window.localStorage.setItem(localStorageKey, JSON.stringify(normalized));
  }
  return normalized;
}

export function updateDesktopLocalStoreState(
  updater: (current: DesktopLocalStoreState) => DesktopLocalStoreState,
): DesktopLocalStoreState {
  const current = loadDesktopLocalStoreState();
  return saveDesktopLocalStoreState(updater(current));
}
