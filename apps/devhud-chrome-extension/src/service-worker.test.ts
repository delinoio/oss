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

describe("capture configuration freshness", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("refreshes configuration after interactive selection", async () => {
    const port = fakePort();
    const requestTypes: string[] = [];
    const configurations = [
      {
        origins: [{
          origin: "https://example.com",
          mappings: [{
            mappingId: "01900000-0000-7000-8000-000000000001",
            matcher: { scheme: "https", host: ["example", "com"], hostIsIpLiteral: false, port: "", path: ["before", "**"] },
          }],
        }],
        language: "en",
      },
      {
        origins: [{
          origin: "https://example.com",
          mappings: [{
            mappingId: "01900000-0000-7000-8000-000000000001",
            matcher: { scheme: "https", host: ["example", "com"], hostIsIpLiteral: false, port: "", path: ["after", "**"] },
          }],
        }],
        language: "en",
      },
    ];
    let configurationIndex = 0;
    port.postMessage = vi.fn((request: { request_id: string; type: string }) => {
      requestTypes.push(request.type);
      const payload = request.type === "configure" ? configurations[configurationIndex++] : null;
      queueMicrotask(() => port.messageListeners[0]!({
        version: 1,
        schema_version: 1,
        request_id: request.request_id,
        ok: true,
        state: "accepted",
        payload,
      }));
    });
    let runtimeListener: ((message: unknown, sender: chrome.runtime.MessageSender, sendResponse: (response: unknown) => void) => boolean) | undefined;
    vi.stubGlobal("chrome", {
      runtime: {
        connectNative: vi.fn(() => port),
        onMessage: { addListener: vi.fn((listener) => { runtimeListener = listener; }) },
      },
      tabs: { query: vi.fn(async () => [{ id: 7, url: "https://example.com/before/page", incognito: false }]) },
      permissions: { contains: vi.fn(async () => true) },
      scripting: { executeScript: vi.fn(async () => [{ result: {
        liveUrl: "https://example.com/before/page",
        url: "https://example.com/%3Credacted%3E/%3Credacted%3E",
        title: "Example",
        viewport: { width: 1280, height: 720 },
        userAgent: "test",
        selectedBounds: null,
        accessibility: {},
        outerHtml: "",
      } }]) },
    });

    await import("./service-worker.js");
    const response = await new Promise<{ state: string }>((resolve) => {
      runtimeListener!({ type: "capture", selectElement: true }, {} as chrome.runtime.MessageSender, (value) => resolve(value as { state: string }));
    });

    expect(requestTypes).toEqual(["configure", "configure"]);
    expect(chrome.scripting.executeScript).toHaveBeenCalledWith(expect.objectContaining({ args: [true, "en"] }));
    expect(response.state).toBe("denied");
  });
});
