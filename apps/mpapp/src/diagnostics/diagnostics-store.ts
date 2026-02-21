import {
  MpappActionType,
  MpappLogEventFamily,
  MpappMode,
} from "../contracts/enums";
import type { MpappLogEvent } from "../contracts/types";

export const DIAGNOSTICS_STORAGE_KEY = "mpapp.diagnostics.v1";
export const DIAGNOSTICS_RING_BUFFER_LIMIT = 300;

export interface DiagnosticsStore {
  append(event: MpappLogEvent): Promise<void>;
  listRecent(limit: number): Promise<MpappLogEvent[]>;
  clear(): Promise<void>;
}

type DiagnosticsStorage = {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
};

const inMemoryFallbackStorage: DiagnosticsStorage = {
  async getItem() {
    return null;
  },
  async setItem() {
    return;
  },
  async removeItem() {
    return;
  },
};

function resolveDefaultStorage(): DiagnosticsStorage {
  try {
    const moduleRef = require("@react-native-async-storage/async-storage");
    const candidate = (moduleRef?.default ?? moduleRef) as
      | DiagnosticsStorage
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

function createEventId(): string {
  const entropy = Math.random().toString(36).slice(2, 8);
  return `log-${Date.now()}-${entropy}`;
}

function parseStoredEvents(rawValue: string | null): MpappLogEvent[] {
  if (!rawValue) {
    return [];
  }

  try {
    const parsedValue = JSON.parse(rawValue);
    if (!Array.isArray(parsedValue)) {
      return [];
    }

    return parsedValue as MpappLogEvent[];
  } catch {
    return [];
  }
}

export function buildLogEvent(params: {
  eventFamily: MpappLogEventFamily;
  sessionId: string;
  connectionState: MpappMode;
  actionType: MpappActionType;
  latencyMs: number;
  failureReason?: string | null;
  platform: string;
  osVersion: string;
  payload?: Record<string, unknown>;
}): MpappLogEvent {
  return {
    eventId: createEventId(),
    eventFamily: params.eventFamily,
    sessionId: params.sessionId,
    connectionState: params.connectionState,
    actionType: params.actionType,
    latencyMs: params.latencyMs,
    failureReason: params.failureReason ?? null,
    platform: params.platform,
    osVersion: params.osVersion,
    timestampMs: Date.now(),
    payload: params.payload ?? {},
  };
}

export class AsyncStorageDiagnosticsStore implements DiagnosticsStore {
  private readonly storage: DiagnosticsStorage;

  constructor(storage: DiagnosticsStorage = resolveDefaultStorage()) {
    this.storage = storage;
  }

  public async append(event: MpappLogEvent): Promise<void> {
    const existing = await this.storage.getItem(DIAGNOSTICS_STORAGE_KEY);
    const events = parseStoredEvents(existing);
    events.push(event);

    const boundedEvents =
      events.length > DIAGNOSTICS_RING_BUFFER_LIMIT
        ? events.slice(events.length - DIAGNOSTICS_RING_BUFFER_LIMIT)
        : events;

    await this.storage.setItem(
      DIAGNOSTICS_STORAGE_KEY,
      JSON.stringify(boundedEvents),
    );
  }

  public async listRecent(limit: number): Promise<MpappLogEvent[]> {
    const existing = await this.storage.getItem(DIAGNOSTICS_STORAGE_KEY);
    const events = parseStoredEvents(existing);
    const safeLimit = Math.max(1, Math.floor(limit));
    const startIndex = Math.max(0, events.length - safeLimit);

    return events.slice(startIndex).reverse();
  }

  public async clear(): Promise<void> {
    await this.storage.removeItem(DIAGNOSTICS_STORAGE_KEY);
  }
}
