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
}

async function click(selector) {
  document.querySelector(selector).click();
  await vi.waitFor(() => expect(sendMessage).toHaveBeenCalled());
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("RealQA extension popup", () => {
  it("requests only the active origin and preserves capture when permission is denied", async () => {
    sendMessage.mockResolvedValueOnce({
      ok: true,
      value: {
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
    await click("#capture");
    document.querySelector("#select").click();
    await vi.waitFor(() => expect(permissionRequest).toHaveBeenCalledOnce());

    expect(permissionRequest).toHaveBeenCalledWith({
      origins: ["https://example.com/*"],
    });
    expect(sendMessage).toHaveBeenCalledTimes(1);
    expect(document.querySelector("#send").disabled).toBe(false);
    expect(document.querySelector("#status").textContent).toContain("denied");
  });

  it("removes URL, title, and every granted DOM metadata field before sending", async () => {
    sendMessage
      .mockResolvedValueOnce({
        ok: true,
        value: {
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
    await click("#capture");
    document.querySelector("#select").click();
    await vi.waitFor(() =>
      expect(document.querySelectorAll("#fields li")).toHaveLength(6),
    );
    expect(sendMessage.mock.calls[1][0]).toEqual({
      kind: "select-boundary",
      capture: {
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
    await vi.waitFor(() => expect(sendMessage).toHaveBeenCalledTimes(3));
    expect(sendMessage.mock.calls[2][0]).toMatchObject({
      kind: "send-to-devhud",
      draft: { url: "", title: "", selection: {} },
    });
  });
});
