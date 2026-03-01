import type { MpappInputPreferences } from "../contracts/types";
import {
  AsyncStorageInputPreferencesStore,
  DEFAULT_MPAPP_INPUT_PREFERENCES,
  MPAPP_INPUT_PREFERENCES_STORAGE_KEY,
} from "../preferences/input-preferences-store";

describe("input preferences store", () => {
  it("loads default preferences when storage is empty", async () => {
    const store = new AsyncStorageInputPreferencesStore({
      getItem: async () => null,
      setItem: async () => {},
    });

    const loadedPreferences = await store.load();
    expect(loadedPreferences).toEqual(DEFAULT_MPAPP_INPUT_PREFERENCES);
  });

  it("persists and reloads preferences across store instances", async () => {
    const memoryStore: Record<string, string> = {};
    const storage = {
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
    };

    const initialStore = new AsyncStorageInputPreferencesStore(storage);
    const savedPreferences: MpappInputPreferences = {
      sensitivity: 1.4,
      invertX: true,
      invertY: false,
    };

    await initialStore.save(savedPreferences);

    const restartStore = new AsyncStorageInputPreferencesStore(storage);
    const loadedPreferences = await restartStore.load();
    expect(loadedPreferences).toEqual(savedPreferences);
  });

  it("falls back to defaults when the stored payload is corrupted", async () => {
    const memoryStore: Record<string, string> = {
      [MPAPP_INPUT_PREFERENCES_STORAGE_KEY]: "{bad json",
    };
    const store = new AsyncStorageInputPreferencesStore({
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
    });

    const loadedPreferences = await store.load();
    expect(loadedPreferences).toEqual(DEFAULT_MPAPP_INPUT_PREFERENCES);
  });

  it("clamps and normalizes invalid persisted sensitivity values", async () => {
    const memoryStore: Record<string, string> = {
      [MPAPP_INPUT_PREFERENCES_STORAGE_KEY]: JSON.stringify({
        sensitivity: 999,
        invertX: "true",
        invertY: true,
      }),
    };
    const store = new AsyncStorageInputPreferencesStore({
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
    });

    const loadedPreferences = await store.load();
    expect(loadedPreferences).toEqual({
      sensitivity: 2,
      invertX: false,
      invertY: true,
    });
  });
});
