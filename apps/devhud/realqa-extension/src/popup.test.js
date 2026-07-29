import { beforeEach, describe, expect, it, vi } from "vitest";

const permissionRequest = vi.fn();
const sendMessage = vi.fn();

function popupMarkup() {
  return `
    <button id="capture" type="button"></button>
    <input id="page-url" disabled />
    <button id="remove-url" disabled type="button"></button>
    <input id="page-title" disabled />
    <button id="remove-title" disabled type="button"></button>
    <button id="select" disabled type="button"></button>
    <ul id="fields"></ul>
    <button id="send" disabled type="button"></button>
    <p id="status"></p>
  `;
}

async function loadPopup() {
  vi.resetModules();
  document.body.innerHTML = popupMarkup();
  globalThis.chrome = {
    permissions: { request: permissionRequest },
    runtime: { sendMessage },
  };
  await import("./popup.js");
  await vi.waitFor(() =>
    expect(sendMessage).toHaveBeenCalledWith({ kind: "get-draft" }),
  );
}

async function clickCapture() {
  await vi.waitFor(() =>
    expect(document.querySelector("#capture").disabled).toBe(false),
  );
  document.querySelector("#capture").click();
  await vi.waitFor(() =>
    expect(sendMessage).toHaveBeenCalledWith({ kind: "begin-capture" }),
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("RealQA extension popup", () => {
  it("requests only the active origin and preserves capture when permission is denied", async () => {
    sendMessage
      .mockResolvedValueOnce({ ok: true, value: undefined })
      .mockResolvedValueOnce({
        ok: true,
        value: {
          captureId: "capture",
          captureMode: "visible-viewport",
          capturedTabId: 7,
          capturedWindowId: 3,
          capturedUrl: "https://example.com/private",
          url: "https://example.com/private",
          title: "Private title",
          image: { mediaType: "png", base64: "iVBORw==", encodedBytes: 4 },
          restricted: false,
        },
      });
    permissionRequest.mockResolvedValueOnce(false);
    await loadPopup();
    await clickCapture();
    document.querySelector("#select").click();
    await vi.waitFor(() => expect(permissionRequest).toHaveBeenCalledOnce());

    expect(permissionRequest).toHaveBeenCalledWith({
      origins: ["https://example.com/*"],
    });
    expect(sendMessage).toHaveBeenCalledTimes(2);
    expect(document.querySelector("#send").disabled).toBe(false);
    expect(document.querySelector("#status").textContent).toContain("denied");
  });

  it("removes URL, title, and every granted DOM metadata field before sending", async () => {
    sendMessage
      .mockResolvedValueOnce({ ok: true, value: undefined })
      .mockResolvedValueOnce({
        ok: true,
        value: {
          captureId: "capture",
          captureMode: "visible-viewport",
          capturedTabId: 7,
          capturedWindowId: 3,
          capturedUrl: "https://example.com/private",
          url: "https://example.com/private",
          title: "Private title",
          image: { mediaType: "png", base64: "iVBORw==", encodedBytes: 4 },
          restricted: false,
        },
      })
      .mockResolvedValueOnce({
        ok: true,
        value: {
          boundary: { x: 1, y: 2, width: 3, height: 4 },
          selector: "body > button.primary",
          tag: "button",
          role: "button",
          accessibleName: "Private label",
          viewport: { width: 800, height: 600, devicePixelRatio: 2 },
        },
      })
      .mockResolvedValueOnce({
        ok: true,
        value: { version: 1, requestId: "request", status: "accepted" },
      });
    permissionRequest.mockResolvedValueOnce(true);
    await loadPopup();
    await clickCapture();
    document.querySelector("#select").click();
    await vi.waitFor(() =>
      expect(document.querySelectorAll("#fields li")).toHaveLength(6),
    );
    expect(sendMessage.mock.calls[2][0]).toEqual({
      kind: "select-boundary",
      capture: {
        captureId: "capture",
        capturedTabId: 7,
        capturedWindowId: 3,
        capturedUrl: "https://example.com/private",
        origin: "https://example.com/*",
      },
    });

    document.querySelector("#remove-url").click();
    document.querySelector("#remove-title").click();
    for (const button of [...document.querySelectorAll("#fields button")]) {
      button.click();
    }
    expect(document.querySelectorAll("#fields li")).toHaveLength(0);

    document.querySelector("#send").click();
    await vi.waitFor(() => expect(sendMessage).toHaveBeenCalledTimes(4));
    expect(sendMessage.mock.calls[3][0]).toMatchObject({
      kind: "send-to-devhud",
      draft: {
        captureId: "capture",
        url: "",
        title: "",
        selection: {},
      },
    });
    expect(document.querySelector("#send").disabled).toBe(true);
  });

  it("restores a selected draft after Chrome closes and reopens the popup", async () => {
    sendMessage.mockResolvedValueOnce({
      ok: true,
      value: {
        captureId: "capture",
        captureMode: "visible-viewport",
        capturedTabId: 7,
        capturedWindowId: 3,
        capturedUrl: "https://example.com/private",
        url: "https://example.com/private",
        title: "Private title",
        image: { mediaType: "png", base64: "iVBORw==", encodedBytes: 4 },
        restricted: false,
        selection: { tag: "button" },
      },
    });

    await loadPopup();

    expect(document.querySelector("#page-url").value).toBe(
      "https://example.com/private",
    );
    expect(document.querySelectorAll("#fields li")).toHaveLength(1);
    expect(document.querySelector("#send").disabled).toBe(false);
    expect(document.querySelector("#status").textContent).toContain(
      "Boundary selected",
    );
  });

  it("clears a stale draft before a failed recapture", async () => {
    sendMessage
      .mockResolvedValueOnce({
        ok: true,
        value: {
          captureId: "old-capture",
          captureMode: "os-capture",
          url: "https://old.example/",
          title: "Old capture",
          restricted: true,
        },
      })
      .mockResolvedValueOnce({ ok: false, error: "capture-unavailable" });
    await loadPopup();
    expect(document.querySelector("#send").disabled).toBe(false);

    await clickCapture();
    await vi.waitFor(() =>
      expect(document.querySelector("#status").textContent).toBe(
        "capture-unavailable",
      ),
    );

    expect(document.querySelector("#page-url").value).toBe("");
    expect(document.querySelector("#page-title").value).toBe("");
    expect(document.querySelector("#send").disabled).toBe(true);
  });

  it("allows only one native handoff while Send is pending", async () => {
    let settleHandoff;
    const handoff = new Promise((resolve) => {
      settleHandoff = resolve;
    });
    sendMessage
      .mockResolvedValueOnce({
        ok: true,
        value: {
          captureId: "capture",
          captureMode: "os-capture",
          restricted: true,
        },
      })
      .mockReturnValueOnce(handoff);
    await loadPopup();

    const send = document.querySelector("#send");
    send.click();
    send.click();
    await vi.waitFor(() => expect(sendMessage).toHaveBeenCalledTimes(2));
    expect(send.disabled).toBe(true);
    expect(
      sendMessage.mock.calls.filter(
        ([message]) => message.kind === "send-to-devhud",
      ),
    ).toHaveLength(1);

    settleHandoff({ ok: false, error: "native-host-unavailable" });
    await vi.waitFor(() => expect(send.disabled).toBe(false));
  });
});
