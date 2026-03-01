import {
  AsyncStorageInputPreferencesStore,
  DEFAULT_MPAPP_INPUT_PREFERENCES,
  INPUT_PREFERENCES_STORAGE_KEY,
} from "../preferences/input-preferences-store";

function createMemoryStorage() {
  const memoryStore: Record<string, string> = {};

  return {
    memoryStore,
    storage: {
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
      removeItem: async (key: string) => {
        delete memoryStore[key];
      },
    },
  };
}

describe("input preferences store", () => {
  it("loads defaults when storage is empty", async () => {
    const { storage } = createMemoryStorage();
    const store = new AsyncStorageInputPreferencesStore(storage);

    const loaded = await store.load();

    expect(loaded).toEqual(DEFAULT_MPAPP_INPUT_PREFERENCES);
  });

  it("persists and reloads preferences across store instances", async () => {
    const { storage } = createMemoryStorage();
    const firstStore = new AsyncStorageInputPreferencesStore(storage);
    const secondStore = new AsyncStorageInputPreferencesStore(storage);

    await firstStore.save({
      sensitivity: 1.4,
      invertX: true,
      invertY: false,
    });

    const loaded = await secondStore.load();

    expect(loaded).toEqual({
      sensitivity: 1.4,
      invertX: true,
      invertY: false,
    });
  });

  it("sanitizes malformed and out-of-range values", async () => {
    const { memoryStore, storage } = createMemoryStorage();
    const store = new AsyncStorageInputPreferencesStore(storage);

    memoryStore[INPUT_PREFERENCES_STORAGE_KEY] = "{not valid json}";
    const malformed = await store.load();
    expect(malformed).toEqual(DEFAULT_MPAPP_INPUT_PREFERENCES);

    memoryStore[INPUT_PREFERENCES_STORAGE_KEY] = JSON.stringify({
      sensitivity: 50,
      invertX: "yes",
      invertY: true,
    });
    const sanitized = await store.load();

    expect(sanitized).toEqual({
      sensitivity: 2,
      invertX: false,
      invertY: true,
    });
  });

  it("retains data with default in-memory fallback storage", async () => {
    const firstStore = new AsyncStorageInputPreferencesStore();
    const secondStore = new AsyncStorageInputPreferencesStore();

    await firstStore.clear();
    await firstStore.save({
      sensitivity: 1.1,
      invertX: true,
      invertY: true,
    });

    const loaded = await secondStore.load();

    expect(loaded).toEqual({
      sensitivity: 1.1,
      invertX: true,
      invertY: true,
    });
  });
});
