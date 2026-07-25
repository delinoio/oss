import {
  decodeFailureFromKind,
  decodeSettings,
  decodeWidgetConfiguration,
  defaultSettings,
  defaultWidgetConfiguration,
  encodeSettings,
  encodeWidgetConfiguration,
  SETTINGS_STORAGE_KEY,
  WIDGET_CONFIGURATION_STORAGE_KEY,
  type DecodeFailure,
  type DecodeFailureKind,
  type DevHudSettings,
  type PersistenceKey,
  type WidgetConfiguration,
} from "./contracts";

export interface LocalStorageAdapter {
  read(key: PersistenceKey): Promise<string | null>;
  reset(): Promise<void>;
  write(key: PersistenceKey, value: string): Promise<void>;
}

export interface TauriPersistenceBridge {
  readSettings(): Promise<string | null>;
  resetDevHud(): Promise<void>;
  writeSettings(record: string): Promise<void>;
  readWidgetConfiguration(): Promise<string | null>;
  writeWidgetConfiguration(record: string): Promise<void>;
}

class NativeRecordReadError extends Error {
  constructor(readonly failure: DecodeFailure) {
    super("The native adapter rejected a DevHud persistence record.");
    this.name = "NativeRecordReadError";
  }
}

const nativeRecordFailureKinds = new Set<DecodeFailureKind>([
  "corrupt",
  "future-version",
  "incompatible",
]);

function nativeRecordReadError(error: unknown): NativeRecordReadError | undefined {
  if (typeof error !== "string" || !nativeRecordFailureKinds.has(error as DecodeFailureKind)) {
    return undefined;
  }
  return new NativeRecordReadError(decodeFailureFromKind(error as DecodeFailureKind));
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
        : bridge.readWidgetConfiguration().catch((error: unknown) => {
            throw nativeRecordReadError(error) ?? error;
          });
    },
    reset() {
      return bridge.resetDevHud();
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

  async reset(): Promise<void> {
    this.values.clear();
  }

  async write(key: PersistenceKey, value: string): Promise<void> {
    this.values.set(key, value);
  }
}

export type PersistenceIssue =
  | (DecodeFailure & { readonly key: PersistenceKey })
  | { readonly key: PersistenceKey; readonly kind: "storage"; readonly guidance: string };

type RecordWriteBlockFailure = DecodeFailure | { readonly kind: "storage"; readonly guidance: string };

export interface LoadedPersistence {
  readonly settings: DevHudSettings;
  readonly widgetConfiguration: WidgetConfiguration;
  readonly issues: readonly PersistenceIssue[];
}

export class PersistenceResetError extends Error {
  constructor(
    readonly loaded: LoadedPersistence,
    options: ErrorOptions,
  ) {
    super("DevHud could not fully reset local data.", options);
    this.name = "PersistenceResetError";
  }
}

export class RejectedRecordWriteBlockedError extends Error {
  constructor(
    readonly key: PersistenceKey,
    readonly failure: RecordWriteBlockFailure,
  ) {
    super("DevHud refused to overwrite a rejected local data record.");
    this.name = "RejectedRecordWriteBlockedError";
  }
}

const storageGuidance =
  "DevHud could not access local storage. Check device storage, then restart DevHud.";

function storageIssue(key: PersistenceKey): PersistenceIssue {
  return { key, kind: "storage", guidance: storageGuidance };
}

/** Serializes each record so call order, rather than completion timing, defines last-successful-write-wins. */
export class DevHudPersistence {
  private readonly writeTails = new Map<PersistenceKey, Promise<void>>();
  private readonly blockedRecords = new Map<PersistenceKey, RecordWriteBlockFailure>();
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

  async reset(): Promise<LoadedPersistence> {
    await Promise.allSettled(this.writeTails.values());
    let resetFailure: { readonly cause: unknown } | undefined;
    try {
      await this.storage.reset();
    } catch (cause: unknown) {
      resetFailure = { cause };
    }
    this.blockedRecords.clear();
    this.loadPromise = undefined;
    const loaded = await this.load();
    if (resetFailure) {
      throw new PersistenceResetError(loaded, resetFailure);
    }
    return loaded;
  }

  private async loadSettings(): Promise<{ value: DevHudSettings; issues: readonly PersistenceIssue[] }> {
    try {
      const raw = await this.storage.read(SETTINGS_STORAGE_KEY);
      if (raw === null) return { value: defaultSettings, issues: [] };
      const decoded = decodeSettings(raw);
      if (decoded.ok) return { value: decoded.value, issues: [] };
      this.blockedRecords.set(SETTINGS_STORAGE_KEY, decoded.failure);
      return { value: defaultSettings, issues: [{ ...decoded.failure, key: SETTINGS_STORAGE_KEY }] };
    } catch {
      this.blockedRecords.set(SETTINGS_STORAGE_KEY, storageIssue(SETTINGS_STORAGE_KEY));
      return { value: defaultSettings, issues: [storageIssue(SETTINGS_STORAGE_KEY)] };
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
      this.blockedRecords.set(WIDGET_CONFIGURATION_STORAGE_KEY, decoded.failure);
      return {
        value: defaultWidgetConfiguration,
        issues: [{ ...decoded.failure, key: WIDGET_CONFIGURATION_STORAGE_KEY }],
      };
    } catch (error) {
      const failure =
        error instanceof NativeRecordReadError
          ? error.failure
          : storageIssue(WIDGET_CONFIGURATION_STORAGE_KEY);
      this.blockedRecords.set(
        WIDGET_CONFIGURATION_STORAGE_KEY,
        failure,
      );
      return {
        value: defaultWidgetConfiguration,
        issues: [{ ...failure, key: WIDGET_CONFIGURATION_STORAGE_KEY }],
      };
    }
  }

  private enqueue(key: PersistenceKey, value: string): Promise<void> {
    const previous = this.writeTails.get(key) ?? Promise.resolve();
    const initialization = this.loadPromise ?? Promise.resolve();
    const write = previous
      .catch(() => undefined)
      .then(() => initialization.then(() => undefined))
      .then(() => {
        const blockedRecord = this.blockedRecords.get(key);
        if (blockedRecord !== undefined) {
          throw new RejectedRecordWriteBlockedError(key, blockedRecord);
        }
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
