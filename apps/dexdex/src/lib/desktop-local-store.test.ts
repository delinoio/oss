import { beforeEach, describe, expect, it } from "vitest";
import { DexDexPageId } from "../contracts/dexdex-page";
import {
  LocalEnvironmentHealth,
  loadDesktopLocalStoreState,
  saveDesktopLocalStoreState,
  updateDesktopLocalStoreState,
} from "./desktop-local-store";

const localStorageKey = "dexdex.desktop.local-store.v1";

function createMemoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() {
      return values.size;
    },
    clear() {
      values.clear();
    },
    getItem(key: string) {
      return values.has(key) ? values.get(key) ?? null : null;
    },
    key(index: number) {
      return Array.from(values.keys())[index] ?? null;
    },
    removeItem(key: string) {
      values.delete(key);
    },
    setItem(key: string, value: string) {
      values.set(key, value);
    },
  };
}

describe("desktop-local-store", () => {
  beforeEach(() => {
    Object.defineProperty(window, "localStorage", {
      value: createMemoryStorage(),
      configurable: true,
    });
    window.localStorage.clear();
  });

  it("loads default state when local storage is empty", () => {
    const state = loadDesktopLocalStoreState();
    expect(state.settings.defaultPage).toBe(DexDexPageId.Threads);
    expect(state.automations.length).toBeGreaterThan(0);
    expect(state.localEnvironments.length).toBeGreaterThan(0);
  });

  it("saves and restores last selected records", () => {
    const saved = saveDesktopLocalStoreState({
      ...loadDesktopLocalStoreState(),
      lastSelectedAutomationId: "automation-nightly-stream",
      lastSelectedEnvironmentId: "env-staging",
    });

    expect(saved.lastSelectedAutomationId).toBe("automation-nightly-stream");
    expect(saved.lastSelectedEnvironmentId).toBe("env-staging");

    const reloaded = loadDesktopLocalStoreState();
    expect(reloaded.lastSelectedAutomationId).toBe("automation-nightly-stream");
    expect(reloaded.lastSelectedEnvironmentId).toBe("env-staging");
  });

  it("preserves null last selected ids", () => {
    const saved = saveDesktopLocalStoreState({
      ...loadDesktopLocalStoreState(),
      lastSelectedAutomationId: null,
      lastSelectedEnvironmentId: null,
    });

    expect(saved.lastSelectedAutomationId).toBeNull();
    expect(saved.lastSelectedEnvironmentId).toBeNull();

    const reloaded = loadDesktopLocalStoreState();
    expect(reloaded.lastSelectedAutomationId).toBeNull();
    expect(reloaded.lastSelectedEnvironmentId).toBeNull();
  });

  it("updates connection diagnostics and persists the change", () => {
    updateDesktopLocalStoreState((current) => ({
      ...current,
      localEnvironments: current.localEnvironments.map((environment) =>
        environment.id === "env-local-main"
          ? {
              ...environment,
              health: LocalEnvironmentHealth.Healthy,
              lastCheckedAt: "2026-03-07T10:00:00.000Z",
              lastErrorMessage: null,
            }
          : environment,
      ),
    }));

    const persistedRaw = window.localStorage.getItem(localStorageKey);
    expect(persistedRaw).toBeTruthy();

    const state = loadDesktopLocalStoreState();
    const environment = state.localEnvironments.find(
      (item) => item.id === "env-local-main",
    );
    expect(environment?.health).toBe(LocalEnvironmentHealth.Healthy);
    expect(environment?.lastCheckedAt).toBe("2026-03-07T10:00:00.000Z");
  });
});
