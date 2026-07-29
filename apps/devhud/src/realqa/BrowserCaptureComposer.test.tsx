import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { takeBrowserCapture } from "./browserCapture";
import { BrowserCaptureComposer } from "./BrowserCaptureComposer";
import {
  ImageMediaType,
  type ApprovedComposerImage,
  type ComposerImage,
  type RealQaComposerBridge,
} from "./capture";

vi.mock("./browserCapture", () => ({
  takeBrowserCapture: vi.fn(),
}));

const takeBrowserCaptureMock = vi.mocked(takeBrowserCapture);
const source: ComposerImage = {
  imageId: "019a97f3-cb9d-7c44-a7b2-2514486e42b1",
  sourceRevision: 1,
  contentType: "image/png",
  width: 100,
  height: 80,
  previewWidth: 100,
  previewHeight: 80,
  encodedBytes: 8,
  sessionEncodedBytes: 8,
  image: {
    mediaType: ImageMediaType.Png,
    bytes: [137, 80, 78, 71, 13, 10, 26, 10],
  },
};
const composerBridge: RealQaComposerBridge = {
  acceptImage: vi.fn(async () => source),
  flattenImage: vi.fn(
    async () => source as unknown as ApprovedComposerImage,
  ),
  removeImage: vi.fn(async () => undefined),
  resetSession: vi.fn(async () => undefined),
};

describe("BrowserCaptureComposer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    takeBrowserCaptureMock.mockReset();
  });

  it("drains and sanitizes the in-memory browser capture before editing", async () => {
    takeBrowserCaptureMock.mockResolvedValue({
      kind: "submit-capture",
      version: 1,
      requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b1",
      captureMode: "visible-viewport",
      page: {
        title: "Captured page",
        url: "https://example.com/private",
      },
      image: {
        mediaType: "png",
        base64: "iVBORw0KGgo=",
        encodedBytes: 8,
      },
    });

    render(<BrowserCaptureComposer composerBridge={composerBridge} />);

    expect(await screen.findByRole("heading", { name: "Captured page" })).toBeVisible();
    expect(
      screen.getByRole("application", { name: /Screenshot editor canvas/u }),
    ).toBeVisible();
    expect(composerBridge.acceptImage).toHaveBeenCalledWith({
      sessionId: "realqa-browser-capture",
      imageId: "019a97f3-cb9d-7c44-a7b2-2514486e42b1",
      image: {
        mediaType: ImageMediaType.Png,
        bytes: [137, 80, 78, 71, 13, 10, 26, 10],
      },
      outputMediaType: ImageMediaType.Png,
    });
  });

  it("drains a later capture when the native listener signals availability", async () => {
    takeBrowserCaptureMock.mockResolvedValue(null);

    render(<BrowserCaptureComposer composerBridge={composerBridge} />);
    expect(
      await screen.findByRole("heading", { name: "Waiting for a capture" }),
    ).toBeVisible();

    const initialCalls = takeBrowserCaptureMock.mock.calls.length;
    takeBrowserCaptureMock.mockResolvedValueOnce({
      kind: "submit-capture",
      version: 1,
      requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b2",
      captureMode: "os-capture",
    });
    await act(async () => {
      window.dispatchEvent(
        new Event("devhud:realqa-browser-capture-available"),
      );
    });

    expect(
      await screen.findByText(
        "Chrome requested the native OS capture flow for this page.",
      ),
    ).toBeVisible();
    expect(takeBrowserCaptureMock.mock.calls.length).toBeGreaterThan(
      initialCalls,
    );
  });
});
