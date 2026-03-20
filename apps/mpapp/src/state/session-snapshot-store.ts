import {
  MpappConnectionEvent,
  MpappDisconnectReason,
  MpappErrorCode,
} from "../contracts/enums";
import type { MpappSessionSnapshot } from "../contracts/types";

export const MPAPP_SESSION_SNAPSHOT_STORAGE_KEY = "mpapp.session-snapshot.v1";

export interface SessionSnapshotStore {
  load(): Promise<MpappSessionSnapshot | null>;
  save(snapshot: MpappSessionSnapshot): Promise<void>;
  clear(): Promise<void>;
}

type SessionSnapshotStorage = {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
};

const inMemoryFallbackData: Record<string, string> = {};

const inMemoryFallbackStorage: SessionSnapshotStorage = {
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

const MPAPP_CONNECTION_EVENT_VALUES = new Set(Object.values(MpappConnectionEvent));
const MPAPP_DISCONNECT_REASON_VALUES = new Set(Object.values(MpappDisconnectReason));
const MPAPP_ERROR_CODE_VALUES = new Set(Object.values(MpappErrorCode));

function resolveDefaultStorage(): SessionSnapshotStorage {
  try {
    const moduleRef = require("@react-native-async-storage/async-storage");
    const candidate = (moduleRef?.default ?? moduleRef) as
      | SessionSnapshotStorage
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

function normalizeConnectionEvent(value: unknown): MpappConnectionEvent | null {
  if (typeof value !== "string") {
    return null;
  }

  if (!MPAPP_CONNECTION_EVENT_VALUES.has(value as MpappConnectionEvent)) {
    return null;
  }

  return value as MpappConnectionEvent;
}

function normalizeDisconnectReason(value: unknown): MpappDisconnectReason | null {
  if (typeof value !== "string") {
    return null;
  }

  if (!MPAPP_DISCONNECT_REASON_VALUES.has(value as MpappDisconnectReason)) {
    return null;
  }

  return value as MpappDisconnectReason;
}

function normalizeErrorCode(value: unknown): MpappErrorCode | null {
  if (typeof value !== "string") {
    return null;
  }

  if (!MPAPP_ERROR_CODE_VALUES.has(value as MpappErrorCode)) {
    return null;
  }

  return value as MpappErrorCode;
}

function normalizeErrorMessage(value: unknown): string | null {
  if (typeof value !== "string") {
    return null;
  }

  const trimmedValue = value.trim();
  if (!trimmedValue) {
    return null;
  }

  return trimmedValue;
}

function normalizeUpdatedAt(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return Date.now();
  }

  return Math.floor(value);
}

function normalizeSessionSnapshot(value: unknown): MpappSessionSnapshot | null {
  if (!value || typeof value !== "object") {
    return null;
  }

  const candidate = value as Partial<Record<keyof MpappSessionSnapshot, unknown>>;

  return {
    lastConnectionEvent: normalizeConnectionEvent(candidate.lastConnectionEvent),
    lastDisconnectReason: normalizeDisconnectReason(candidate.lastDisconnectReason),
    errorCode: normalizeErrorCode(candidate.errorCode),
    errorMessage: normalizeErrorMessage(candidate.errorMessage),
    updatedAt: normalizeUpdatedAt(candidate.updatedAt),
  };
}

function parseStoredSnapshot(rawValue: string | null): MpappSessionSnapshot | null {
  if (!rawValue) {
    return null;
  }

  try {
    const parsedValue = JSON.parse(rawValue);
    return normalizeSessionSnapshot(parsedValue);
  } catch {
    return null;
  }
}

export function buildSessionSnapshot(params: {
  lastConnectionEvent: MpappConnectionEvent | null;
  lastDisconnectReason: MpappDisconnectReason | null;
  errorCode: MpappErrorCode | null;
  errorMessage: string | null;
}): MpappSessionSnapshot {
  return {
    lastConnectionEvent: params.lastConnectionEvent,
    lastDisconnectReason: params.lastDisconnectReason,
    errorCode: params.errorCode,
    errorMessage: normalizeErrorMessage(params.errorMessage),
    updatedAt: Date.now(),
  };
}

export class AsyncStorageSessionSnapshotStore implements SessionSnapshotStore {
  private storage: SessionSnapshotStorage;

  constructor(storage: SessionSnapshotStorage = resolveDefaultStorage()) {
    this.storage = storage;
  }

  private switchToFallbackStorage(params: {
    operation: "load" | "save" | "clear";
    error: unknown;
  }): void {
    if (this.storage === inMemoryFallbackStorage) {
      return;
    }

    console.warn("[mpapp][session-snapshot-store] switching to in-memory fallback", {
      operation: params.operation,
      error: params.error,
    });
    this.storage = inMemoryFallbackStorage;
  }

  public async load(): Promise<MpappSessionSnapshot | null> {
    let storedValue: string | null;
    try {
      storedValue = await this.storage.getItem(MPAPP_SESSION_SNAPSHOT_STORAGE_KEY);
    } catch (error: unknown) {
      this.switchToFallbackStorage({
        operation: "load",
        error,
      });
      storedValue = await this.storage.getItem(MPAPP_SESSION_SNAPSHOT_STORAGE_KEY);
    }

    return parseStoredSnapshot(storedValue);
  }

  public async save(snapshot: MpappSessionSnapshot): Promise<void> {
    const normalizedSnapshot = normalizeSessionSnapshot(snapshot);
    if (!normalizedSnapshot) {
      return;
    }

    try {
      await this.storage.setItem(
        MPAPP_SESSION_SNAPSHOT_STORAGE_KEY,
        JSON.stringify(normalizedSnapshot),
      );
    } catch (error: unknown) {
      this.switchToFallbackStorage({
        operation: "save",
        error,
      });
      await this.storage.setItem(
        MPAPP_SESSION_SNAPSHOT_STORAGE_KEY,
        JSON.stringify(normalizedSnapshot),
      );
    }
  }

  public async clear(): Promise<void> {
    try {
      await this.storage.removeItem(MPAPP_SESSION_SNAPSHOT_STORAGE_KEY);
    } catch (error: unknown) {
      this.switchToFallbackStorage({
        operation: "clear",
        error,
      });
      await this.storage.removeItem(MPAPP_SESSION_SNAPSHOT_STORAGE_KEY);
    }
  }
}
