import {
  decodeSettings,
  decodeWidgetConfiguration,
  defaultSettings,
  defaultWidgetConfiguration,
  encodeSettings,
  encodeWidgetConfiguration,
  SETTINGS_STORAGE_KEY,
  WIDGET_CONFIGURATION_STORAGE_KEY,
  type DecodeFailure,
  type DevHudSettings,
  type PersistenceKey,
  type WidgetConfiguration,
} from "./contracts";

export interface LocalStorageAdapter {
  read(key: PersistenceKey): Promise<string | null>;
  write(key: PersistenceKey, value: string): Promise<void>;
}

export interface TauriPersistenceBridge {
  readSettings(): Promise<string | null>;
  writeSettings(record: string): Promise<void>;
  readWidgetConfiguration(): Promise<string | null>;
  writeWidgetConfiguration(record: string): Promise<void>;
}

/**
 * This adapter deliberately maps only the two versioned DevHud records. It is
 * usable by both Tauri CEF and the mobile system-webview runtimes without
 * granting the frontend a filesystem or default-store API.
 */
export function createTauriPersistenceAdapter(
  bridge: TauriPersistenceBridge,
): LocalStorageAdapter {
  return {
    read(key) {
      return key === SETTINGS_STORAGE_KEY
        ? bridge.readSettings()
        : bridge.readWidgetConfiguration();
    },
    write(key, value) {
      return key === SETTINGS_STORAGE_KEY
        ? bridge.writeSettings(value)
        : bridge.writeWidgetConfiguration(value);
    },
  };
}

export class MemoryStorageAdapter implements LocalStorageAdapter {
  readonly values = new Map<PersistenceKey, string>();

  async read(key: PersistenceKey): Promise<string | null> {
    return this.values.get(key) ?? null;
  }

  async write(key: PersistenceKey, value: string): Promise<void> {
    this.values.set(key, value);
  }
}

export type PersistenceIssue = DecodeFailure | { readonly kind: "storage"; readonly guidance: string };

export interface LoadedPersistence {
  readonly settings: DevHudSettings;
  readonly widgetConfiguration: WidgetConfiguration;
  readonly issues: readonly PersistenceIssue[];
}

export class FutureVersionWriteBlockedError extends Error {
  readonly guidance =
    "This DevHud data was created by a newer version. Update DevHud before changing it.";

  constructor() {
    super("DevHud refused to overwrite a future local data record.");
    this.name = "FutureVersionWriteBlockedError";
  }
}

const storageGuidance =
  "DevHud could not access local storage. Check device storage, then restart DevHud.";

function storageIssue(): PersistenceIssue {
  return { kind: "storage", guidance: storageGuidance };
}

/** Serializes each record so call order, rather than completion timing, defines last-successful-write-wins. */
export class DevHudPersistence {
  private readonly writeTails = new Map<PersistenceKey, Promise<void>>();
  private readonly protectedKeys = new Set<PersistenceKey>();
  private loadPromise: Promise<LoadedPersistence> | undefined;

  constructor(private readonly storage: LocalStorageAdapter) {}

  load(): Promise<LoadedPersistence> {
    this.loadPromise ??= this.loadRecords();
    return this.loadPromise;
  }

  private async loadRecords(): Promise<LoadedPersistence> {
    const [settings, widgetConfiguration] = await Promise.all([
      this.loadSettings(),
      this.loadWidgetConfiguration(),
    ]);
    return {
      settings: settings.value,
      widgetConfiguration: widgetConfiguration.value,
      issues: [...settings.issues, ...widgetConfiguration.issues],
    };
  }

  saveSettings(settings: DevHudSettings): Promise<void> {
    return this.enqueue(SETTINGS_STORAGE_KEY, encodeSettings(settings));
  }

  saveWidgetConfiguration(configuration: WidgetConfiguration): Promise<void> {
    return this.enqueue(
      WIDGET_CONFIGURATION_STORAGE_KEY,
      encodeWidgetConfiguration(configuration),
    );
  }

  private async loadSettings(): Promise<{ value: DevHudSettings; issues: readonly PersistenceIssue[] }> {
    try {
      const raw = await this.storage.read(SETTINGS_STORAGE_KEY);
      if (raw === null) return { value: defaultSettings, issues: [] };
      const decoded = decodeSettings(raw);
      if (decoded.ok) return { value: decoded.value, issues: [] };
      if (decoded.failure.kind === "future-version") this.protectedKeys.add(SETTINGS_STORAGE_KEY);
      return { value: defaultSettings, issues: [decoded.failure] };
    } catch {
      return { value: defaultSettings, issues: [storageIssue()] };
    }
  }

  private async loadWidgetConfiguration(): Promise<{
    value: WidgetConfiguration;
    issues: readonly PersistenceIssue[];
  }> {
    try {
      const raw = await this.storage.read(WIDGET_CONFIGURATION_STORAGE_KEY);
      if (raw === null) return { value: defaultWidgetConfiguration, issues: [] };
      const decoded = decodeWidgetConfiguration(raw);
      if (decoded.ok) return { value: decoded.value, issues: [] };
      if (decoded.failure.kind === "future-version") {
        this.protectedKeys.add(WIDGET_CONFIGURATION_STORAGE_KEY);
      }
      return { value: defaultWidgetConfiguration, issues: [decoded.failure] };
    } catch {
      return { value: defaultWidgetConfiguration, issues: [storageIssue()] };
    }
  }

  private enqueue(key: PersistenceKey, value: string): Promise<void> {
    const previous = this.writeTails.get(key) ?? Promise.resolve();
    const initialization = this.loadPromise ?? Promise.resolve();
    const write = previous
      .catch(() => undefined)
      .then(() => initialization.then(() => undefined))
      .then(() => {
        if (this.protectedKeys.has(key)) throw new FutureVersionWriteBlockedError();
        return this.storage.write(key, value).then(() => {
          this.loadPromise = undefined;
        });
      });
    this.writeTails.set(key, write);
    void write.finally(() => {
      if (this.writeTails.get(key) === write) this.writeTails.delete(key);
    }).catch(() => undefined);
    return write;
  }
}
