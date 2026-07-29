import { describe, expect, it } from "vitest";

import {
  captureRequest,
  dataUrlImage,
  isRestrictedPage,
  MAX_ENCODED_IMAGE_BYTES,
  originPatternForUrl,
  sanitizePageUrl,
  sanitizeSelection,
  sanitizeSelector,
} from "./protocol.js";

describe("RealQA extension protocol", () => {
  it("redacts URL credentials, queries, fragments, controls, and bounds titles", () => {
    expect(
      sanitizePageUrl("https://user:secret@example.com/path?token=secret#private"),
    ).toBe("https://example.com/path");
    expect(
      captureRequest({
        captureMode: "os-capture",
        url: "chrome://settings/?secret=yes",
        title: "Settings\u0000 private",
      }).page,
    ).toEqual({
      url: "chrome://settings/",
      title: "Settings private",
    });
  });

  it("allows only exact HTTP(S) origins for optional DOM permission", () => {
    expect(originPatternForUrl("https://example.com/a?secret=1")).toBe(
      "https://example.com/*",
    );
    expect(originPatternForUrl("http://localhost:3000/a")).toBe(
      "http://localhost:3000/*",
    );
    expect(isRestrictedPage("chrome://settings")).toBe(true);
    expect(isRestrictedPage("file:///tmp/private")).toBe(true);
  });

  it("sanitizes selectors and collects no HTML or arbitrary page text", () => {
    expect(sanitizeSelector("body > main#content > button.primary")).toBe(
      "body > main#content > button.primary",
    );
    expect(sanitizeSelector('div[data-secret="page text"]')).toBeUndefined();
    expect(
      sanitizeSelection({
        boundary: { x: 1, y: 2, width: 30, height: 40 },
        selector: "body > button.primary",
        tag: "BUTTON",
        role: "button",
        accessibleName: "Save\u0000 draft",
        viewport: { width: 1280, height: 720, devicePixelRatio: 2 },
        innerHTML: "<strong>secret</strong>",
        textContent: "secret page text",
      }),
    ).toEqual({
      boundary: { x: 1, y: 2, width: 30, height: 40 },
      selector: "body > button.primary",
      tag: "button",
      role: "button",
      accessibleName: "Save draft",
      viewport: { width: 1280, height: 720, devicePixelRatio: 2 },
    });
  });

  it("keeps encoded images at or below 25 MiB and never creates full-page input", () => {
    expect(dataUrlImage("data:image/png;base64,iVBORw==")).toEqual({
      mediaType: "png",
      base64: "iVBORw==",
      encodedBytes: 4,
    });
    const oversizedBase64 = "A".repeat(
      Math.ceil(((MAX_ENCODED_IMAGE_BYTES + 1) * 4) / 3),
    );
    expect(() =>
      dataUrlImage(`data:image/png;base64,${oversizedBase64}`),
    ).toThrow("image-too-large");
    expect(() =>
      captureRequest({ captureMode: "full-page", url: "https://example.com" }),
    ).toThrow("invalid-capture-mode");
  });

  it("allows every DOM metadata field to be omitted", () => {
    expect(
      captureRequest({
        captureMode: "visible-viewport",
        image: { mediaType: "png", base64: "iVBORw==", encodedBytes: 4 },
        url: "https://example.com/",
        selection: {},
      }),
    ).not.toHaveProperty("selection");
  });

  it("drops a visual boundary that exceeds its visible viewport", () => {
    expect(
      sanitizeSelection({
        boundary: { x: 700, y: 500, width: 200, height: 200 },
        viewport: { width: 800, height: 600, devicePixelRatio: 1 },
      }),
    ).toEqual({
      viewport: { width: 800, height: 600, devicePixelRatio: 1 },
    });
  });
});
