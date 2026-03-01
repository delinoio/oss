import type { MpappInputPreferences } from "../contracts/types";
import { clampSensitivity } from "../input/translate-gesture";

export const MPAPP_INPUT_PREFERENCES_STORAGE_KEY = "mpapp.input-preferences.v1";

export const DEFAULT_MPAPP_INPUT_PREFERENCES: MpappInputPreferences = {
  sensitivity: 1,
  invertX: false,
  invertY: false,
};

export interface InputPreferencesStore {
  load(): Promise<MpappInputPreferences>;
  save(preferences: MpappInputPreferences): Promise<void>;
}

type InputPreferencesStorage = {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
};

const inMemoryFallbackData: Record<string, string> = {};

const inMemoryFallbackStorage: InputPreferencesStorage = {
  async getItem(key: string) {
    return Object.prototype.hasOwnProperty.call(inMemoryFallbackData, key)
      ? inMemoryFallbackData[key]
      : null;
  },
  async setItem(key: string, value: string) {
    inMemoryFallbackData[key] = value;
  },
};

function createDefaultInputPreferences(): MpappInputPreferences {
  return {
    sensitivity: DEFAULT_MPAPP_INPUT_PREFERENCES.sensitivity,
    invertX: DEFAULT_MPAPP_INPUT_PREFERENCES.invertX,
    invertY: DEFAULT_MPAPP_INPUT_PREFERENCES.invertY,
  };
}

function resolveDefaultStorage(): InputPreferencesStorage {
  try {
    const moduleRef = require("@react-native-async-storage/async-storage");
    const candidate = (moduleRef?.default ?? moduleRef) as
      | InputPreferencesStorage
      | undefined;

    if (
      candidate &&
      typeof candidate.getItem === "function" &&
      typeof candidate.setItem === "function"
    ) {
      return candidate;
    }
  } catch {
    // Jest/node environments may not have an initialized native AsyncStorage bridge.
  }

  return inMemoryFallbackStorage;
}

function normalizeSensitivity(value: unknown): number {
  if (typeof value !== "number" || Number.isNaN(value) || !Number.isFinite(value)) {
    return DEFAULT_MPAPP_INPUT_PREFERENCES.sensitivity;
  }

  return Number.parseFloat(clampSensitivity(value).toFixed(1));
}

function normalizeInputPreferences(value: unknown): MpappInputPreferences {
  if (!value || typeof value !== "object") {
    return createDefaultInputPreferences();
  }

  const candidate = value as Partial<Record<keyof MpappInputPreferences, unknown>>;

  return {
    sensitivity: normalizeSensitivity(candidate.sensitivity),
    invertX:
      typeof candidate.invertX === "boolean"
        ? candidate.invertX
        : DEFAULT_MPAPP_INPUT_PREFERENCES.invertX,
    invertY:
      typeof candidate.invertY === "boolean"
        ? candidate.invertY
        : DEFAULT_MPAPP_INPUT_PREFERENCES.invertY,
  };
}

function parseStoredInputPreferences(rawValue: string | null): MpappInputPreferences {
  if (!rawValue) {
    return createDefaultInputPreferences();
  }

  try {
    const parsedValue = JSON.parse(rawValue);
    return normalizeInputPreferences(parsedValue);
  } catch {
    return createDefaultInputPreferences();
  }
}

export class AsyncStorageInputPreferencesStore implements InputPreferencesStore {
  private readonly storage: InputPreferencesStorage;

  constructor(storage: InputPreferencesStorage = resolveDefaultStorage()) {
    this.storage = storage;
  }

  public async load(): Promise<MpappInputPreferences> {
    const storedValue = await this.storage.getItem(
      MPAPP_INPUT_PREFERENCES_STORAGE_KEY,
    );
    return parseStoredInputPreferences(storedValue);
  }

  public async save(preferences: MpappInputPreferences): Promise<void> {
    const normalizedPreferences = normalizeInputPreferences(preferences);
    await this.storage.setItem(
      MPAPP_INPUT_PREFERENCES_STORAGE_KEY,
      JSON.stringify(normalizedPreferences),
    );
  }
}
