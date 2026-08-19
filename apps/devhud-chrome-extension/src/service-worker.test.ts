import { afterEach, describe, expect, it, vi } from "vitest";

interface FakePort {
  readonly messageListeners: Array<(message: unknown) => void>;
  readonly disconnectListeners: Array<() => void>;
}

function fakePort(): FakePort & chrome.runtime.Port {
  const messageListeners: Array<(message: unknown) => void> = [];
  const disconnectListeners: Array<() => void> = [];
  return {
    messageListeners,
    disconnectListeners,
    name: "test",
    sender: undefined,
    onMessage: {
      addListener: (listener: unknown) => messageListeners.push(listener as (message: unknown) => void),
    } as unknown as chrome.runtime.Port["onMessage"],
    onDisconnect: {
      addListener: (listener: unknown) => disconnectListeners.push(listener as () => void),
    } as unknown as chrome.runtime.Port["onDisconnect"],
    disconnect: vi.fn(),
    postMessage: vi.fn(),
  };
}

describe("native host reconnect backoff", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("resets only after a valid host response", async () => {
    vi.useFakeTimers();
    const ports: Array<FakePort & chrome.runtime.Port> = [];
    const connectNative = vi.fn(() => {
      const port = fakePort();
      ports.push(port);
      return port;
    });
    vi.stubGlobal("chrome", {
      runtime: {
        connectNative,
        onMessage: { addListener: vi.fn() },
      },
    });

    await import("./service-worker.js");
    expect(connectNative).toHaveBeenCalledTimes(1);

    ports[0]!.disconnectListeners[0]!();
    await vi.advanceTimersByTimeAsync(250);
    expect(connectNative).toHaveBeenCalledTimes(2);

    ports[1]!.disconnectListeners[0]!();
    await vi.advanceTimersByTimeAsync(499);
    expect(connectNative).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(connectNative).toHaveBeenCalledTimes(3);

    ports[2]!.messageListeners[0]!({
      version: 1,
      schema_version: 1,
      request_id: "01900000-0000-7000-8000-000000000000",
      ok: false,
      state: "disconnected",
      payload: null,
    });
    ports[2]!.disconnectListeners[0]!();
    await vi.advanceTimersByTimeAsync(249);
    expect(connectNative).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(1);
    expect(connectNative).toHaveBeenCalledTimes(4);
  });
});
