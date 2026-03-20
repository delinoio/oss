import {
  MpappConnectionEvent,
  MpappDisconnectReason,
  MpappErrorCode,
} from "../contracts/enums";
import {
  AsyncStorageSessionSnapshotStore,
  buildSessionSnapshot,
  MPAPP_SESSION_SNAPSHOT_STORAGE_KEY,
} from "../state/session-snapshot-store";

describe("session snapshot store", () => {
  it("persists and reloads snapshot values", async () => {
    const memoryStore: Record<string, string> = {};
    const storage = {
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
      removeItem: async (key: string) => {
        delete memoryStore[key];
      },
    };

    const store = new AsyncStorageSessionSnapshotStore(storage);
    await store.clear();
    await store.save({
      lastConnectionEvent: MpappConnectionEvent.DisconnectFailure,
      lastDisconnectReason: MpappDisconnectReason.TransportLost,
      errorCode: MpappErrorCode.TransportFailure,
      errorMessage: "disconnect failed",
      updatedAt: 1700000000000,
    });

    const loadedSnapshot = await store.load();
    expect(loadedSnapshot).toEqual({
      lastConnectionEvent: MpappConnectionEvent.DisconnectFailure,
      lastDisconnectReason: MpappDisconnectReason.TransportLost,
      errorCode: MpappErrorCode.TransportFailure,
      errorMessage: "disconnect failed",
      updatedAt: 1700000000000,
    });
  });

  it("returns null when stored snapshot payload is corrupted", async () => {
    const store = new AsyncStorageSessionSnapshotStore({
      getItem: async () => "{broken-json",
      setItem: async () => {},
      removeItem: async () => {},
    });

    await expect(store.load()).resolves.toBeNull();
  });

  it("sanitizes unknown enum values in stored snapshot payload", async () => {
    const memoryStore: Record<string, string> = {
      [MPAPP_SESSION_SNAPSHOT_STORAGE_KEY]: JSON.stringify({
        lastConnectionEvent: "unknown-event",
        lastDisconnectReason: "unexpected-reason",
        errorCode: "invalid-error",
        errorMessage: "  keep me  ",
        updatedAt: "not-a-number",
      }),
    };
    const store = new AsyncStorageSessionSnapshotStore({
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
      removeItem: async (key: string) => {
        delete memoryStore[key];
      },
    });

    const loadedSnapshot = await store.load();
    expect(loadedSnapshot).not.toBeNull();
    expect(loadedSnapshot).toMatchObject({
      lastConnectionEvent: null,
      lastDisconnectReason: null,
      errorCode: null,
      errorMessage: "keep me",
    });
    expect(typeof loadedSnapshot?.updatedAt).toBe("number");
  });

  it("builds snapshot with a fresh timestamp", () => {
    const snapshot = buildSessionSnapshot({
      lastConnectionEvent: MpappConnectionEvent.Disconnect,
      lastDisconnectReason: MpappDisconnectReason.UserAction,
      errorCode: null,
      errorMessage: null,
    });

    expect(snapshot).toMatchObject({
      lastConnectionEvent: MpappConnectionEvent.Disconnect,
      lastDisconnectReason: MpappDisconnectReason.UserAction,
      errorCode: null,
      errorMessage: null,
    });
    expect(typeof snapshot.updatedAt).toBe("number");
    expect(snapshot.updatedAt).toBeGreaterThan(0);
  });
});
