import {
  MpappActionType,
  MpappLogEventFamily,
  MpappMode,
} from "../contracts/enums";
import {
  AsyncStorageDiagnosticsStore,
  DIAGNOSTICS_RING_BUFFER_LIMIT,
  buildLogEvent,
} from "../diagnostics/diagnostics-store";

describe("diagnostics store", () => {
  it("enforces ring buffer limit and returns newest first", async () => {
    const memoryStore: Record<string, string> = {};
    const store = new AsyncStorageDiagnosticsStore({
      getItem: async (key: string) => memoryStore[key] ?? null,
      setItem: async (key: string, value: string) => {
        memoryStore[key] = value;
      },
      removeItem: async (key: string) => {
        delete memoryStore[key];
      },
    });

    await store.clear();

    for (let i = 0; i < DIAGNOSTICS_RING_BUFFER_LIMIT + 5; i += 1) {
      await store.append(
        buildLogEvent({
          eventFamily: MpappLogEventFamily.InputMove,
          actionType: MpappActionType.Move,
          sessionId: "session-a",
          connectionState: MpappMode.Connected,
          latencyMs: i,
          platform: "android",
          osVersion: "34",
          payload: { index: i },
        }),
      );
    }

    const recent = await store.listRecent(400);
    expect(recent).toHaveLength(DIAGNOSTICS_RING_BUFFER_LIMIT);
    expect(recent[0]?.payload).toMatchObject({ index: DIAGNOSTICS_RING_BUFFER_LIMIT + 4 });
  });
});
