import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

let listener;
const contains = vi.fn();
const remove = vi.fn();
const executeScript = vi.fn();
const captureVisibleTab = vi.fn();
const query = vi.fn();
const sendNativeMessage = vi.fn();
const storageGet = vi.fn();
const storageRemove = vi.fn();
const storageSet = vi.fn();
const sessionStorage = new Map();

beforeAll(async () => {
  globalThis.chrome = {
    extension: { inIncognitoContext: false },
    permissions: { contains, remove },
    runtime: {
      onMessage: {
        addListener(candidate) {
          listener = candidate;
        },
      },
      sendNativeMessage,
    },
    scripting: { executeScript },
    storage: {
      session: {
        get: storageGet,
        remove: storageRemove,
        set: storageSet,
      },
    },
    tabs: { captureVisibleTab, query },
  };
  await import("./service-worker.js");
});

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  storageGet.mockImplementation(async (key) =>
    sessionStorage.has(key)
      ? { [key]: structuredClone(sessionStorage.get(key)) }
      : {},
  );
  storageRemove.mockImplementation(async (key) => {
    sessionStorage.delete(key);
  });
  storageSet.mockImplementation(async (values) => {
    for (const [key, value] of Object.entries(values)) {
      sessionStorage.set(key, structuredClone(value));
    }
  });
  query.mockResolvedValue([
    {
      id: 7,
      windowId: 3,
      url: "https://example.com/private?token=value",
      title: "Example",
      incognito: false,
    },
  ]);
  captureVisibleTab.mockResolvedValue("data:image/png;base64,iVBORw==");
  contains.mockResolvedValue(true);
  remove.mockResolvedValue(true);
  sendNativeMessage.mockImplementation((_host, request) =>
    Promise.resolve({
      version: 1,
      requestId: request.requestId,
      status: "accepted",
    }),
  );
  executeScript.mockResolvedValue([
    {
      result: {
        selector: "body > button.primary",
        tag: "button",
        textContent: "must not escape",
      },
    },
  ]);
});

function dispatch(message) {
  return new Promise((resolve) => {
    expect(listener(message, {}, resolve)).toBe(true);
  });
}

describe("RealQA extension service worker", () => {
  it("captures only the active visible viewport and reads URL/title from tabs", async () => {
    await expect(dispatch({ kind: "begin-capture" })).resolves.toMatchObject({
      ok: true,
      value: {
        captureMode: "visible-viewport",
        capturedTabId: 7,
        capturedWindowId: 3,
        capturedUrl: "https://example.com/private?token=value",
        title: "Example",
      },
    });
    expect(query).toHaveBeenCalledWith({
      active: true,
      lastFocusedWindow: true,
    });
    expect(captureVisibleTab).toHaveBeenCalledWith(3, { format: "png" });
  });

  it("rejects a capture when the active tab changes while Chrome captures", async () => {
    query
      .mockResolvedValueOnce([
        {
          id: 7,
          windowId: 3,
          url: "https://example.com/private?token=value",
          title: "Example",
          incognito: false,
        },
      ])
      .mockResolvedValueOnce([
        {
          id: 8,
          windowId: 3,
          url: "https://other.example/",
          title: "Other",
          incognito: false,
        },
      ]);

    await expect(dispatch({ kind: "begin-capture" })).resolves.toEqual({
      ok: false,
      error: "captured-tab-changed",
    });
    await expect(dispatch({ kind: "get-draft" })).resolves.toEqual({
      ok: true,
      value: undefined,
    });
  });

  it("falls back to OS capture on restricted pages and excludes Incognito", async () => {
    query.mockResolvedValueOnce([
      { id: 7, windowId: 3, url: "chrome://settings", incognito: false },
    ]);
    const restricted = await dispatch({ kind: "begin-capture" });
    expect(restricted).toMatchObject({
      ok: true,
      value: { captureMode: "os-capture", restricted: true },
    });
    expect(restricted.value).not.toHaveProperty("url");
    expect(captureVisibleTab).not.toHaveBeenCalled();

    query.mockResolvedValueOnce([
      { id: 8, windowId: 3, url: "https://example.com", incognito: true },
    ]);
    await expect(dispatch({ kind: "begin-capture" })).resolves.toEqual({
      ok: false,
      error: "incognito-excluded",
    });
  });

  it("honors optional permission grant and denial before DOM selection", async () => {
    const captured = await dispatch({ kind: "begin-capture" });
    const captureId = captured.value.captureId;
    await expect(
      dispatch({
        kind: "select-boundary",
        capture: {
          captureId,
          capturedTabId: 7,
          capturedWindowId: 3,
          capturedUrl: "https://example.com/private?token=value",
          origin: "https://example.com/*",
        },
      }),
    ).resolves.toMatchObject({
      ok: true,
      value: { selector: "body > button.primary", tag: "button" },
    });
    await expect(dispatch({ kind: "get-draft" })).resolves.toMatchObject({
      ok: true,
      value: {
        captureId,
        selection: { selector: "body > button.primary", tag: "button" },
      },
    });
    expect(executeScript).toHaveBeenCalledOnce();
    expect(remove).toHaveBeenCalledWith({
      origins: ["https://example.com/*"],
    });

    contains.mockResolvedValueOnce(false);
    await expect(
      dispatch({
        kind: "select-boundary",
        capture: {
          captureId,
          capturedTabId: 7,
          capturedWindowId: 3,
          capturedUrl: "https://example.com/private?token=value",
          origin: "https://example.com/*",
        },
      }),
    ).resolves.toEqual({ ok: false, error: "permission-denied" });
    expect(executeScript).toHaveBeenCalledOnce();
    expect(remove).toHaveBeenCalledTimes(2);
  });

  it("rejects an origin switch before injection", async () => {
    const captured = await dispatch({ kind: "begin-capture" });
    await expect(
      dispatch({
        kind: "select-boundary",
        capture: {
          captureId: captured.value.captureId,
          capturedTabId: 7,
          capturedWindowId: 3,
          capturedUrl: "https://example.com/private?token=value",
          origin: "https://attacker.example/*",
        },
      }),
    ).resolves.toEqual({ ok: false, error: "origin-changed" });
    expect(contains).not.toHaveBeenCalled();
  });

  it("rejects a same-origin tab or navigation change before injection", async () => {
    const captured = await dispatch({ kind: "begin-capture" });
    query.mockResolvedValueOnce([
      {
        id: 8,
        windowId: 3,
        url: "https://example.com/private?token=value",
        incognito: false,
      },
    ]);
    const capture = {
      captureId: captured.value.captureId,
      capturedTabId: 7,
      capturedWindowId: 3,
      capturedUrl: "https://example.com/private?token=value",
      origin: "https://example.com/*",
    };
    await expect(
      dispatch({ kind: "select-boundary", capture }),
    ).resolves.toEqual({ ok: false, error: "captured-tab-changed" });

    query.mockResolvedValueOnce([
      {
        id: 7,
        windowId: 3,
        url: "https://example.com/other",
        incognito: false,
      },
    ]);
    await expect(
      dispatch({ kind: "select-boundary", capture }),
    ).resolves.toEqual({ ok: false, error: "captured-tab-changed" });
    expect(contains).not.toHaveBeenCalled();
    expect(executeScript).not.toHaveBeenCalled();
  });

  it("rejects DOM metadata when the captured page changes during selection", async () => {
    const captured = await dispatch({ kind: "begin-capture" });
    query
      .mockResolvedValueOnce([
        {
          id: 7,
          windowId: 3,
          url: "https://example.com/private?token=value",
          incognito: false,
        },
      ])
      .mockResolvedValueOnce([
        {
          id: 7,
          windowId: 3,
          url: "https://example.com/after-selection",
          incognito: false,
        },
      ]);

    await expect(
      dispatch({
        kind: "select-boundary",
        capture: {
          captureId: captured.value.captureId,
          capturedTabId: 7,
          capturedWindowId: 3,
          capturedUrl: "https://example.com/private?token=value",
          origin: "https://example.com/*",
        },
      }),
    ).resolves.toEqual({ ok: false, error: "captured-tab-changed" });
    expect(remove).toHaveBeenCalledWith({
      origins: ["https://example.com/*"],
    });
    const restored = await dispatch({ kind: "get-draft" });
    expect(restored.ok).toBe(true);
    expect(restored.value).not.toHaveProperty("selection");
  });

  it("submits only the active draft and clears it after acceptance", async () => {
    const captured = await dispatch({ kind: "begin-capture" });
    await expect(
      dispatch({
        kind: "send-to-devhud",
        draft: {
          ...captured.value,
          url: "https://example.com/edited?private=yes",
          title: "Edited title",
        },
      }),
    ).resolves.toMatchObject({
      ok: true,
      value: { status: "accepted" },
    });
    expect(sendNativeMessage).toHaveBeenCalledOnce();
    expect(sendNativeMessage.mock.calls[0][1]).toMatchObject({
      page: { url: "https://example.com/edited", title: "Edited title" },
    });
    await expect(dispatch({ kind: "get-draft" })).resolves.toEqual({
      ok: true,
      value: undefined,
    });
  });

  it("restores the active draft after the service worker restarts", async () => {
    const captured = await dispatch({ kind: "begin-capture" });
    expect(captured.ok).toBe(true);

    vi.resetModules();
    await import("./service-worker.js");

    await expect(dispatch({ kind: "get-draft" })).resolves.toMatchObject({
      ok: true,
      value: {
        captureId: captured.value.captureId,
        captureMode: "visible-viewport",
        capturedTabId: 7,
      },
    });
  });
});
