import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { takeBrowserCapture } from "./browserCapture";
import { BrowserCaptureComposer } from "./BrowserCaptureComposer";
import {
  publishPersistenceReset,
  publishSessionInvalidation,
} from "../runtime/theme";
import {
  ImageMediaType,
  type ApprovedComposerImage,
  CaptureMode,
  type ComposerImage,
  PointerInclusion,
  type RealQaBrowserComposerBridge,
} from "./capture";

vi.mock("./browserCapture", () => ({
  takeBrowserCapture: vi.fn(),
}));

const takeBrowserCaptureMock = vi.mocked(takeBrowserCapture);
const broadcastListeners = new Map<
  string,
  Set<(event: MessageEvent<unknown>) => void>
>();

class TestBroadcastChannel {
  readonly #listeners = new Set<(event: MessageEvent<unknown>) => void>();

  constructor(readonly name: string) {}

  addEventListener(
    _type: "message",
    listener: (event: MessageEvent<unknown>) => void,
  ) {
    this.#listeners.add(listener);
    const listeners = broadcastListeners.get(this.name) ?? new Set();
    listeners.add(listener);
    broadcastListeners.set(this.name, listeners);
  }

  postMessage(data: unknown) {
    for (const listener of broadcastListeners.get(this.name) ?? []) {
      listener({ data } as MessageEvent<unknown>);
    }
  }

  close() {
    const listeners = broadcastListeners.get(this.name);
    if (listeners === undefined) return;
    for (const listener of this.#listeners) listeners.delete(listener);
  }
}

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
const composerBridge: RealQaBrowserComposerBridge = {
  captureBrowserFallback: vi.fn(async () => ({
    mode: CaptureMode.Display,
    pointer: PointerInclusion.Exclude,
    logicalBounds: { x: 0, y: 0, width: 100, height: 80 },
    pixelRegions: [
      {
        displayId: "display-1",
        pixels: { x: 0, y: 0, width: 100, height: 80 },
      },
    ],
    image: source.image,
  })),
  acceptImage: vi.fn(async () => source),
  flattenImage: vi.fn(
    async () => source as unknown as ApprovedComposerImage,
  ),
  removeImage: vi.fn(async () => undefined),
  resetSession: vi.fn(async () => undefined),
};

afterEach(() => {
  cleanup();
  broadcastListeners.clear();
  vi.unstubAllGlobals();
});

describe("BrowserCaptureComposer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    takeBrowserCaptureMock.mockReset();
    vi.stubGlobal("BroadcastChannel", TestBroadcastChannel);
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
      selection: {
        selector: "main > button#submit",
        tag: "button",
        role: "button",
        accessibleName: "Submit report",
        boundary: { x: 10, y: 20, width: 30, height: 40 },
      },
    });

    render(<BrowserCaptureComposer composerBridge={composerBridge} />);

    expect(await screen.findByRole("heading", { name: "Captured page" })).toBeVisible();
    expect(
      screen.getByRole("application", { name: /Screenshot editor canvas/u }),
    ).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "Submit report" }),
    ).toBeVisible();
    expect(screen.getByText("main > button#submit")).toBeVisible();
    expect(screen.getByText("30 × 40 at 10, 20")).toBeVisible();
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

  it("starts native capture for a restricted browser page", async () => {
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
      await screen.findByRole("button", { name: "Capture primary display" }),
    ).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", { name: "Capture primary display" }),
    );

    expect(
      await screen.findByRole("application", {
        name: /Screenshot editor canvas/u,
      }),
    ).toBeVisible();
    expect(composerBridge.captureBrowserFallback).toHaveBeenCalledWith(
      "realqa-browser-capture",
    );
    expect(composerBridge.acceptImage).toHaveBeenCalledWith({
      sessionId: "realqa-browser-capture",
      imageId: "019a97f3-cb9d-7c44-a7b2-2514486e42b2",
      image: source.image,
      outputMediaType: ImageMediaType.Png,
    });
    expect(takeBrowserCaptureMock.mock.calls.length).toBeGreaterThan(
      initialCalls,
    );
  });

  it("serializes browser capture drains without clearing an accepted capture", async () => {
    let resolveFirstCapture:
      | ((capture: Awaited<ReturnType<typeof takeBrowserCapture>>) => void)
      | undefined;
    const firstCapture = new Promise<
      Awaited<ReturnType<typeof takeBrowserCapture>>
    >((resolve) => {
      resolveFirstCapture = resolve;
    });
    let activeDrains = 0;
    let maximumActiveDrains = 0;
    takeBrowserCaptureMock
      .mockImplementationOnce(async () => {
        activeDrains += 1;
        maximumActiveDrains = Math.max(maximumActiveDrains, activeDrains);
        const capture = await firstCapture;
        activeDrains -= 1;
        return capture;
      })
      .mockImplementationOnce(async () => {
        activeDrains += 1;
        maximumActiveDrains = Math.max(maximumActiveDrains, activeDrains);
        activeDrains -= 1;
        return null;
      });

    render(<BrowserCaptureComposer composerBridge={composerBridge} />);
    await act(async () => {
      window.dispatchEvent(
        new Event("devhud:realqa-browser-capture-available"),
      );
      resolveFirstCapture?.({
        kind: "submit-capture",
        version: 1,
        requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b3",
        captureMode: "visible-viewport",
        page: { title: "Serialized capture" },
        image: {
          mediaType: "png",
          base64: "iVBORw0KGgo=",
          encodedBytes: 8,
        },
      });
    });

    expect(
      await screen.findByRole("heading", { name: "Serialized capture" }),
    ).toBeVisible();
    await vi.waitFor(() => expect(takeBrowserCaptureMock).toHaveBeenCalledTimes(2));
    expect(maximumActiveDrains).toBe(1);
    expect(
      screen.queryByRole("heading", { name: "Waiting for a capture" }),
    ).not.toBeInTheDocument();
  });

  it("removes an accepted preview when Reset DevHud is published", async () => {
    takeBrowserCaptureMock.mockResolvedValue({
      kind: "submit-capture",
      version: 1,
      requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b4",
      captureMode: "visible-viewport",
      page: { title: "Reset-sensitive capture" },
      image: {
        mediaType: "png",
        base64: "iVBORw0KGgo=",
        encodedBytes: 8,
      },
    });

    render(<BrowserCaptureComposer composerBridge={composerBridge} />);
    expect(
      await screen.findByRole("application", {
        name: /Screenshot editor canvas/u,
      }),
    ).toBeVisible();

    act(() => publishPersistenceReset({ status: "complete" }));

    expect(
      screen.getByRole("heading", { name: "Capture locked" }),
    ).toBeVisible();
    expect(
      screen.queryByRole("application", {
        name: /Screenshot editor canvas/u,
      }),
    ).not.toBeInTheDocument();
  });

  it("does not restore an in-flight preview after logout invalidation", async () => {
    let resolveAcceptance: ((image: ComposerImage) => void) | undefined;
    const pendingAcceptance = new Promise<ComposerImage>((resolve) => {
      resolveAcceptance = resolve;
    });
    const pendingBridge: RealQaBrowserComposerBridge = {
      ...composerBridge,
      acceptImage: vi.fn(() => pendingAcceptance),
    };
    takeBrowserCaptureMock.mockResolvedValue({
      kind: "submit-capture",
      version: 1,
      requestId: "019a97f3-cb9d-7c44-a7b2-2514486e42b5",
      captureMode: "visible-viewport",
      image: {
        mediaType: "png",
        base64: "iVBORw0KGgo=",
        encodedBytes: 8,
      },
    });

    render(<BrowserCaptureComposer composerBridge={pendingBridge} />);
    await vi.waitFor(() => expect(pendingBridge.acceptImage).toHaveBeenCalled());

    act(() => publishSessionInvalidation());
    expect(
      screen.getByRole("heading", { name: "Capture locked" }),
    ).toBeVisible();

    await act(async () => resolveAcceptance?.(source));

    expect(
      screen.getByRole("heading", { name: "Capture locked" }),
    ).toBeVisible();
    expect(
      screen.queryByRole("application", {
        name: /Screenshot editor canvas/u,
      }),
    ).not.toBeInTheDocument();
  });
});
