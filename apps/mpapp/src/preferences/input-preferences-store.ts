import {
  DEFAULT_MPAPP_INPUT_PREFERENCES,
  type MpappInputPreferences,
  normalizeInputPreferences,
} from "./input-preferences";

export const INPUT_PREFERENCES_STORAGE_KEY = "mpapp.input-preferences.v1";

type InputPreferencesStorage = {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
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
  async removeItem(key: string) {
    delete inMemoryFallbackData[key];
  },
};

function resolveDefaultStorage(): InputPreferencesStorage {
  try {
    const moduleRef = require("@react-native-async-storage/async-storage");
    const candidate = (moduleRef?.default ?? moduleRef) as
      | InputPreferencesStorage
      | undefined;

    if (
      candidate &&
      typeof candidate.getItem === "function" &&
      typeof candidate.setItem === "function" &&
      typeof candidate.removeItem === "function"
    ) {
      return candidate;
    }
  } catch {
    // Jest/node environments may not have an initialized native AsyncStorage bridge.
  }

  return inMemoryFallbackStorage;
}

function parseStoredPreferences(
  rawValue: string | null,
): Partial<MpappInputPreferences> | null {
  if (!rawValue) {
    return null;
  }

  try {
    const parsedValue = JSON.parse(rawValue);
    if (!parsedValue || typeof parsedValue !== "object") {
      return null;
    }

    return parsedValue as Partial<MpappInputPreferences>;
  } catch {
    return null;
  }
}

export interface InputPreferencesStore {
  load(): Promise<MpappInputPreferences>;
  save(preferences: MpappInputPreferences): Promise<void>;
}

export class AsyncStorageInputPreferencesStore implements InputPreferencesStore {
  private readonly storage: InputPreferencesStorage;

  constructor(storage: InputPreferencesStorage = resolveDefaultStorage()) {
    this.storage = storage;
  }

  public async load(): Promise<MpappInputPreferences> {
    try {
      const storedValue = await this.storage.getItem(INPUT_PREFERENCES_STORAGE_KEY);
      const preferences = normalizeInputPreferences(
        parseStoredPreferences(storedValue),
      );

      console.info("[mpapp][preferences]", {
        event: "hydrate-success",
        preferences,
      });

      return preferences;
    } catch (error: unknown) {
      console.warn("[mpapp][preferences]", {
        event: "hydrate-failure",
        fallback: "default-preferences",
        error,
      });

      return DEFAULT_MPAPP_INPUT_PREFERENCES;
    }
  }

  public async save(preferences: MpappInputPreferences): Promise<void> {
    const normalizedPreferences = normalizeInputPreferences(preferences);

    try {
      await this.storage.setItem(
        INPUT_PREFERENCES_STORAGE_KEY,
        JSON.stringify(normalizedPreferences),
      );

      console.info("[mpapp][preferences]", {
        event: "save-success",
        preferences: normalizedPreferences,
      });
    } catch (error: unknown) {
      console.error("[mpapp][preferences]", {
        event: "save-failure",
        preferences: normalizedPreferences,
        error,
      });
    }
  }

  public async clear(): Promise<void> {
    await this.storage.removeItem(INPUT_PREFERENCES_STORAGE_KEY);
  }
}

export {
  DEFAULT_MPAPP_INPUT_PREFERENCES,
  type MpappInputPreferences,
  normalizeInputPreferences,
};
