// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { messages } from "./localization";
import type { CaptureDraft, NativeBridgeRequestV1, NativeBridgeResponseV1, NativeBridgeV1 } from "./native-bridge";
import { RealqaSurface } from "./realqa-ui";

const draft: CaptureDraft = {
  id: "019b0000-0000-7000-8000-000000000001",
  revision: 3,
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_100,
  expiresAt: 1_702_592_100,
  imageCount: 1,
  images: [{
    id: "019b0000-0000-7000-8000-000000000002",
    width: 800,
    height: 600,
    previewUrl: "realqa://asset/draft/image/source/3",
    crop: null,
    layers: [{ tool: "redaction", id: "019b0000-0000-7000-8000-000000000003", bounds: { x: 10, y: 20, width: 30, height: 40 } }],
  }],
  canUndo: true,
  canRedo: false,
};

function bridgeWith(handler?: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>) {
  const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
    if (handler) return handler(value);
    if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [{ id: "main", name: "Main", logicalBounds: { x: -100, y: 0, width: 1920, height: 1080 }, pixelWidth: 3840, pixelHeight: 2160, scale: 2, primary: true }] };
    if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft] };
    if (value.operation === "capture.editor.apply") return { kind: "capture-draft", draft: { ...draft, revision: 4, images: [{ ...draft.images[0], layers: [] }] } };
    if (value.operation === "capture.flatten") return { kind: "capture-flattened", images: [{ imageId: draft.images[0].id, width: 800, height: 600, bytes: 4_096, sha256: "a".repeat(64), assetUrl: "realqa://asset/draft/image/flattened/3", downscaled: false }] };
    throw new Error(`unexpected operation ${value.operation}`);
  });
  const bridge: NativeBridgeV1 = { request, async listen() { return () => {}; } };
  return { bridge, request };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("RealQA capture and editor", () => {
  it.each([["en", messages.en], ["ko", messages.ko]] as const)("exposes the accessible %s editor and ordered layer controls", async (_language, copy) => {
    const { bridge, request } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={copy} />);

    fireEvent.click(await screen.findByRole("button", { name: copy.realqaOpenEditor }));
    expect(screen.getByRole("heading", { name: copy.editorTitle })).toBeTruthy();
    for (const name of [copy.editorCrop, copy.editorArrow, copy.editorRectangle, copy.editorDrawing, copy.editorText, copy.editorBlur, copy.editorRedaction]) {
      expect(screen.getByRole("button", { name })).toBeTruthy();
    }
    expect(screen.getByRole("img", { name: copy.editorCanvas })).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: copy.editorRemove }).at(-1)!);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.editor.apply",
      expectedRevision: 3,
      command: expect.objectContaining({ kind: "remove-layer" }),
    })));
  });

  it("toggles region/window with Space and cancels an in-flight timer with Escape", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      if (value.operation === "capture.cancel") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    const dialog = screen.getByRole("dialog");
    expect(screen.getByRole("radio", { name: messages.en.captureRegionMode })).toHaveProperty("checked", true);
    fireEvent.keyDown(dialog, { key: " " });
    expect(screen.getByRole("radio", { name: messages.en.captureWindowMode })).toHaveProperty("checked", true);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureNow }));
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.start" })));
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    resolveCapture?.({ kind: "capture-draft", draft });
  });

  it("maps pointer drag selection across signed desktop coordinates", async () => {
    Object.defineProperty(window, "PointerEvent", { configurable: true, value: MouseEvent });
    const { bridge } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    const picker = await screen.findByRole("group", { name: messages.en.captureRegionPicker });
    vi.spyOn(picker, "getBoundingClientRect").mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 200, bottom: 100, width: 200, height: 100, toJSON: () => ({}) });
    Object.defineProperty(picker, "setPointerCapture", { value: vi.fn() });
    fireEvent.pointerDown(picker, { pointerId: 1, clientX: 25, clientY: 20 });
    fireEvent.pointerMove(picker, { pointerId: 1, clientX: 125, clientY: 70 });
    fireEvent.pointerUp(picker, { pointerId: 1, clientX: 125, clientY: 70 });
    expect(screen.getByRole("spinbutton", { name: messages.en.captureX })).toHaveProperty("value", "140");
    expect(screen.getByRole("spinbutton", { name: messages.en.captureWidth })).toHaveProperty("value", "960");
  });
});
