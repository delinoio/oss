import { describe, expect, it, vi } from "vitest";

import {
  defaultSettings,
  defaultWidgetConfiguration,
  encodeSettings,
  encodeWidgetConfiguration,
  parseStableToolId,
  ShortcutKey,
  ShortcutModifier,
  ThemePreference,
  WidgetSlot,
  SETTINGS_STORAGE_KEY,
  WIDGET_CONFIGURATION_STORAGE_KEY,
} from "./contracts";
import {
  createTauriPersistenceAdapter,
  DevHudPersistence,
  MemoryStorageAdapter,
  type LocalStorageAdapter,
} from "./storage";

describe("DevHud local persistence", () => {
  it("uses the default state until a record exists", async () => {
    const persistence = new DevHudPersistence(new MemoryStorageAdapter());

    await expect(persistence.load()).resolves.toEqual({
      settings: defaultSettings,
      widgetConfiguration: defaultWidgetConfiguration,
      issues: [],
    });
  });

  it("round-trips every theme and a validated structured shortcut", async () => {
    const storage = new MemoryStorageAdapter();
    const persistence = new DevHudPersistence(storage);
    const shortcut = { modifiers: [ShortcutModifier.Control, ShortcutModifier.Shift], key: ShortcutKey.K };

    for (const theme of Object.values(ThemePreference)) {
      await persistence.saveSettings({ theme, launchAtLogin: true, shortcut });
      await expect(persistence.load()).resolves.toMatchObject({
        settings: { theme, launchAtLogin: true, shortcut },
      });
    }
  });

  it("round-trips widget slot references with stable tool IDs", async () => {
    const toolId = parseStableToolId("fixture-diagnostics");
    if (toolId === null) throw new Error("fixture tool ID is invalid");
    const persistence = new DevHudPersistence(new MemoryStorageAdapter());
    const configuration = { slots: [{ slot: WidgetSlot.Primary, toolId }] };

    await persistence.saveWidgetConfiguration(configuration);
    await expect(persistence.load()).resolves.toMatchObject({ widgetConfiguration: configuration });
  });

  it("rejects unchecked shortcuts and duplicate widget slots before writing", () => {
    expect(() =>
      encodeSettings({
        ...defaultSettings,
        shortcut: { modifiers: [ShortcutModifier.Control, ShortcutModifier.Control], key: ShortcutKey.K },
      }),
    ).toThrow("local persistence contract");
    const toolId = parseStableToolId("fixture-diagnostics");
    if (toolId === null) throw new Error("fixture tool ID is invalid");
    expect(() =>
      encodeWidgetConfiguration({
        slots: [
          { slot: WidgetSlot.Primary, toolId },
          { slot: WidgetSlot.Primary, toolId },
        ],
      }),
    ).toThrow("local persistence contract");
  });

  it("serializes concurrent writes so the latest successful value remains stored", async () => {
    const values = new Map<string, string>();
    let releaseFirstWrite: (() => void) | undefined;
    const firstWrite = new Promise<void>((resolve) => {
      releaseFirstWrite = resolve;
    });
    const write = vi.fn(async (key: string, value: string) => {
      if (write.mock.calls.length === 1) await firstWrite;
      values.set(key, value);
    });
    const storage: LocalStorageAdapter = {
      read: async (key) => values.get(key) ?? null,
      write,
    };
    const persistence = new DevHudPersistence(storage);

    const first = persistence.saveSettings({ ...defaultSettings, theme: ThemePreference.Dark });
    const second = persistence.saveSettings({ ...defaultSettings, theme: ThemePreference.Light });
    for (let microtask = 0; microtask < 5; microtask += 1) await Promise.resolve();
    expect(write).toHaveBeenCalledTimes(1);
    releaseFirstWrite?.();
    await Promise.all([first, second]);

    expect(write).toHaveBeenCalledTimes(2);
    await expect(persistence.load()).resolves.toMatchObject({
      settings: { theme: ThemePreference.Light },
    });
  });

  it("keeps the last valid value when an injected write fails", async () => {
    const values = new Map<string, string>();
    let failNextWrite = false;
    const storage: LocalStorageAdapter = {
      read: async (key) => values.get(key) ?? null,
      write: async (key, value) => {
        if (failNextWrite) {
          failNextWrite = false;
          throw new Error("injected storage failure");
        }
        values.set(key, value);
      },
    };
    const persistence = new DevHudPersistence(storage);
    await persistence.saveSettings({ ...defaultSettings, theme: ThemePreference.Dark });
    failNextWrite = true;

    await expect(
      persistence.saveSettings({ ...defaultSettings, theme: ThemePreference.Light }),
    ).rejects.toThrow("injected storage failure");
    await expect(persistence.load()).resolves.toMatchObject({
      settings: { theme: ThemePreference.Dark },
    });
  });

  it("preserves future records and provides safe update guidance", async () => {
    const storage = new MemoryStorageAdapter();
    const futureRecord = JSON.stringify({ version: 2, settings: { secret: "not exposed" } });
    storage.values.set(SETTINGS_STORAGE_KEY, futureRecord);
    const persistence = new DevHudPersistence(storage);

    const loaded = await persistence.load();
    expect(loaded.settings).toEqual(defaultSettings);
    expect(loaded.issues).toEqual([
      expect.objectContaining({ kind: "future-version", guidance: expect.stringContaining("Update DevHud") }),
    ]);
    expect(storage.values.get(SETTINGS_STORAGE_KEY)).toBe(futureRecord);
    await expect(
      persistence.saveSettings({ ...defaultSettings, theme: ThemePreference.Dark }),
    ).rejects.toMatchObject({ name: "FutureVersionWriteBlockedError" });
    expect(storage.values.get(SETTINGS_STORAGE_KEY)).toBe(futureRecord);
  });

  it("handles corrupt data without exposing its raw contents or overwriting it", async () => {
    const storage = new MemoryStorageAdapter();
    const corruptRecord = "{not-json-with-sensitive-content}";
    storage.values.set(WIDGET_CONFIGURATION_STORAGE_KEY, corruptRecord);
    const loaded = await new DevHudPersistence(storage).load();

    expect(loaded.widgetConfiguration).toEqual(defaultWidgetConfiguration);
    expect(loaded.issues).toEqual([
      expect.objectContaining({ kind: "corrupt", guidance: expect.stringContaining("Reset DevHud") }),
    ]);
    expect(loaded.issues[0]?.guidance).not.toContain("sensitive-content");
    expect(storage.values.get(WIDGET_CONFIGURATION_STORAGE_KEY)).toBe(corruptRecord);
  });

  it("restores records through a new persistence instance after restart", async () => {
    const storage = new MemoryStorageAdapter();
    await new DevHudPersistence(storage).saveSettings({
      ...defaultSettings,
      theme: ThemePreference.Dark,
      launchAtLogin: true,
    });

    await expect(new DevHudPersistence(storage).load()).resolves.toMatchObject({
      settings: { theme: ThemePreference.Dark, launchAtLogin: true },
    });
  });

  it("maps only the four narrow native record operations", async () => {
    const bridge = {
      readSettings: vi.fn(async () => null),
      writeSettings: vi.fn(async () => undefined),
      readWidgetConfiguration: vi.fn(async () => null),
      writeWidgetConfiguration: vi.fn(async () => undefined),
    };
    const storage = createTauriPersistenceAdapter(bridge);

    await storage.read(SETTINGS_STORAGE_KEY);
    await storage.write(WIDGET_CONFIGURATION_STORAGE_KEY, "{}");
    expect(bridge.readSettings).toHaveBeenCalledOnce();
    expect(bridge.writeWidgetConfiguration).toHaveBeenCalledWith("{}");
    expect(bridge.readWidgetConfiguration).not.toHaveBeenCalled();
    expect(bridge.writeSettings).not.toHaveBeenCalled();
  });
});
