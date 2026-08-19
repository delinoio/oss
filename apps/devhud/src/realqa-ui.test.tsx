// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { messages } from "./localization";
import type { CaptureDraft, NativeBridgeRequestV1, NativeBridgeResponseV1, NativeBridgeV1 } from "./native-bridge";
import { RealqaSurface } from "./realqa-ui";
import { ShortcutActionId } from "./shortcuts";

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
    layers: [
      { tool: "arrow", id: "019b0000-0000-7000-8000-000000000003", start: { x: 10, y: 20 }, end: { x: 50, y: 20 }, color: "#ef4444", width: 4 },
      { tool: "redaction", id: "019b0000-0000-7000-8000-000000000004", bounds: { x: 10, y: 20, width: 30, height: 40 } },
      { tool: "text", id: "019b0000-0000-7000-8000-000000000005", origin: { x: 60, y: 80 }, text: "한글", color: "#ffffff", size: 24 },
      { tool: "blur", id: "019b0000-0000-7000-8000-000000000006", bounds: { x: 100, y: 120, width: 80, height: 60 }, radius: 12 },
    ],
  }],
  canUndo: true,
  canRedo: false,
};

const secondDraft: CaptureDraft = {
  ...draft,
  id: "019b0000-0000-7000-8000-000000000010",
  revision: 7,
  imageCount: 2,
  images: [{
    ...draft.images[0],
    id: "019b0000-0000-7000-8000-000000000011",
    previewUrl: "realqa://asset/draft/image/second-source/7",
    layers: [],
  }, {
    ...draft.images[0],
    id: "019b0000-0000-7000-8000-000000000012",
    previewUrl: "realqa://asset/draft/image/second-extra/7",
    layers: [],
  }],
};

function bridgeWith(handler?: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>) {
  const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
    if (handler) return handler(value);
    if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [{ id: "main", name: "Main", logicalBounds: { x: -100, y: 0, width: 1920, height: 1080 }, pixelWidth: 3840, pixelHeight: 2160, scale: 2, primary: true }] };
    if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
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
    const arrowLines = document.querySelectorAll(".annotation-overlay line");
    expect(arrowLines).toHaveLength(3);
    expect(Number(arrowLines[1].getAttribute("x2"))).toBeCloseTo(37.26, 2);
    expect(Number(arrowLines[1].getAttribute("y2"))).toBeCloseTo(29.68, 2);
    expect(Number(arrowLines[2].getAttribute("x2"))).toBeCloseTo(37.26, 2);
    expect(Number(arrowLines[2].getAttribute("y2"))).toBeCloseTo(10.32, 2);
    const textPreview = document.querySelector(".annotation-overlay text");
    expect(textPreview?.getAttribute("style")).toContain("font-family: DevHud RealQA Noto Sans KR");
    expect(textPreview?.getAttribute("style")).toContain("font-kerning: none");
    const blurPreview = document.querySelector(".annotation-overlay .blur-preview");
    expect(blurPreview?.getAttribute("href")).toBe(draft.images[0].previewUrl);
    expect(blurPreview?.getAttribute("clip-path")).toContain("realqa-blur-clip-");
    expect(blurPreview?.getAttribute("filter")).toContain("realqa-blur-filter-");
    expect(document.querySelector(".annotation-overlay feGaussianBlur")?.getAttribute("stdDeviation")).toBe("12");
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
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      if (value.operation === "capture.cancel") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    const dialog = await screen.findByRole("dialog");
    const region = screen.getByRole("radio", { name: messages.en.captureRegionMode });
    expect(region).toHaveProperty("checked", true);
    fireEvent.keyDown(region, { key: " " });
    expect(region).toHaveProperty("checked", true);
    fireEvent.keyDown(screen.getByRole("button", { name: messages.en.captureNow }), { key: " " });
    expect(region).toHaveProperty("checked", true);
    fireEvent.keyDown(dialog, { key: " " });
    expect(screen.getByRole("radio", { name: messages.en.captureWindowMode })).toHaveProperty("checked", true);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureNow }));
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.start" })));
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    resolveCapture?.({ kind: "capture-draft", draft });
  });

  it("resets toolbar-only modes when the capture action changes to selection", async () => {
    const { bridge } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureToolbar }));
    await screen.findByRole("dialog", { name: messages.en.captureToolbar });
    fireEvent.click(screen.getByRole("radio", { name: messages.en.captureDisplay }));
    expect(screen.getByRole("radio", { name: messages.en.captureDisplay })).toHaveProperty("checked", true);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    expect(await screen.findByRole("dialog", { name: messages.en.captureSelection })).toBeTruthy();
    expect(screen.getByRole("radio", { name: messages.en.captureRegionMode })).toHaveProperty("checked", true);
    expect(screen.queryByRole("radio", { name: messages.en.captureDisplay })).toBeNull();
    const radios = screen.getAllByRole("radio");
    expect(new Set(radios.map((radio) => radio.getAttribute("name")))).toEqual(new Set(["capture-mode"]));
  });

  it.each(["cancel", "escape"] as const)("restores the capture opener after %s", async (dismissal) => {
    const { bridge } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const opener = screen.getByRole("button", { name: messages.en.captureSelection });
    opener.focus();
    fireEvent.click(opener);
    const dialog = await screen.findByRole("dialog", { name: messages.en.captureSelection });
    expect(screen.getByRole("button", { name: messages.en.captureNow })).toBe(document.activeElement);

    if (dismissal === "cancel") fireEvent.click(screen.getByRole("button", { name: messages.en.captureCancel }));
    else fireEvent.keyDown(dialog, { key: "Escape" });

    await waitFor(() => expect(opener).toBe(document.activeElement));
  });

  it("uses fresh topology when opening a dialog and ignores an older pending status", async () => {
    let statusRequests = 0;
    let resolveInitialStatus: ((response: NativeBridgeResponseV1) => void) | undefined;
    const freshDisplay = { id: "fresh", name: "Fresh", logicalBounds: { x: 400, y: 200, width: 1000, height: 700 }, pixelWidth: 1000, pixelHeight: 700, scale: 1, primary: true };
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") {
        statusRequests += 1;
        if (statusRequests === 1) return new Promise((resolve) => { resolveInitialStatus = resolve; });
        return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [freshDisplay] };
      }
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await waitFor(() => expect(statusRequests).toBe(1));

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    await screen.findByRole("dialog", { name: messages.en.captureSelection });
    expect(screen.getByRole("spinbutton", { name: messages.en.captureX })).toHaveProperty("value", "400");

    await act(async () => {
      resolveInitialStatus?.({ kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: false, topology: [{ ...freshDisplay, id: "stale", name: "Stale", logicalBounds: { ...freshDisplay.logicalBounds, x: -100 } }] });
    });
    expect(screen.getByRole("spinbutton", { name: messages.en.captureX })).toHaveProperty("value", "400");
  });

  it("ignores repeated shortcut captures while a request is in flight", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const rendered = render(<RealqaSurface bridge={bridge} copy={messages.en} requestedAction={{ action: ShortcutActionId.CaptureDisplay, sequence: 1 }} />);
    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "capture.start")).toHaveLength(1));

    rendered.rerender(<RealqaSurface bridge={bridge} copy={messages.en} requestedAction={{ action: ShortcutActionId.CaptureDisplay, sequence: 2 }} />);
    await act(async () => { await Promise.resolve(); });
    expect(request.mock.calls.filter(([value]) => value.operation === "capture.start")).toHaveLength(1);
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });
  });

  it("preserves later draft navigation when a capture completes", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft, secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click((await screen.findAllByRole("button", { name: messages.en.realqaOpenEditor }))[0]);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    fireEvent.click(screen.getAllByRole("button", { name: messages.en.realqaOpenEditor })[1]);

    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } }); });
    expect(screen.getByRole("button", { name: `${messages.en.editorImage} 2` })).toBeTruthy();
  });

  it("keeps a successful capture when the follow-up refresh fails", async () => {
    let listCalls = 0;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") {
        listCalls += 1;
        if (listCalls > 1) throw new Error("transient refresh failure");
        return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      }
      if (value.operation === "capture.start") return { kind: "capture-draft", draft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await waitFor(() => expect(listCalls).toBe(1));
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    expect(await screen.findByText(messages.en.captureSaved)).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("complementary", { name: messages.en.floatingPreview })).toBeTruthy();
  });

  it("hides the floating preview while a capture dialog is open", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));

    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(screen.queryByRole("complementary", { name: messages.en.floatingPreview })).toBeNull();
  });

  it("clears the floating preview when its draft is deleted", async () => {
    let deleted = false;
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: deleted ? [] : [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft };
      if (value.operation === "capture.delete-draft") {
        deleted = true;
        return { kind: "ok" };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqaDeleteDraft }));

    await waitFor(() => expect(screen.queryByRole("complementary", { name: messages.en.floatingPreview })).toBeNull());
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

  it("loads saved drafts when capture status is temporarily unavailable", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") throw new Error("display adapter unavailable");
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    expect(await screen.findByRole("button", { name: messages.en.realqaOpenEditor })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain(messages.en.captureFailed);
  });

  it("serializes revision-checked editor operations", async () => {
    let resolveFirst: ((response: NativeBridgeResponseV1) => void) | undefined;
    const applyRequests: Extract<NativeBridgeRequestV1, { operation: "capture.editor.apply" }>[] = [];
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") {
        applyRequests.push(value);
        if (applyRequests.length === 1) return new Promise((resolve) => { resolveFirst = resolve; });
        return { kind: "capture-draft", draft: { ...draft, revision: 5 } };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(await screen.findByRole("button", { name: messages.en.realqaOpenEditor }));
    const add = screen.getByRole("button", { name: messages.en.editorAdd });
    fireEvent.click(add);
    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(1));
    expect(applyRequests[0].expectedRevision).toBe(3);
    resolveFirst?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } });
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(4);
  });

  it.each([
    [messages.en.editorCrop, "crop"],
    [messages.en.editorArrow, "arrow"],
    [messages.en.editorRectangle, "rectangle"],
    [messages.en.editorDrawing, "drawing"],
    [messages.en.editorBlur, "blur"],
    [messages.en.editorRedaction, "redaction"],
  ] as const)("clamps boundary-crossing %s gestures to the source image", async (toolLabel, tool) => {
    Object.defineProperty(window, "PointerEvent", { configurable: true, value: MouseEvent });
    const { bridge, request } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(await screen.findByRole("button", { name: messages.en.realqaOpenEditor }));
    fireEvent.click(screen.getByRole("button", { name: toolLabel }));
    const canvas = document.querySelector<HTMLElement>(".editor-canvas")!;
    vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 400, bottom: 300, width: 400, height: 300, toJSON: () => ({}) });
    Object.defineProperty(canvas, "setPointerCapture", { value: vi.fn() });

    fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 75 });
    fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 500, clientY: -50 });
    fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 500, clientY: -50 });

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.editor.apply" })));
    const applied = request.mock.calls
      .map(([value]) => value)
      .find((value) => value.operation === "capture.editor.apply");
    if (!applied || applied.operation !== "capture.editor.apply") throw new Error("missing editor command");
    const command = applied.command;
    if (command.kind === "set-crop") {
      expect(command.crop).toEqual({ x: 200, y: 0, width: 600, height: 150 });
    } else if (command.kind === "add-layer" && command.layer.tool === "arrow") {
      expect(command.layer.start).toEqual({ x: 200, y: 150 });
      expect(command.layer.end).toEqual({ x: 800, y: 0 });
    } else if (command.kind === "add-layer" && command.layer.tool === "drawing") {
      expect(command.layer.points.every((point) => point.x >= 0 && point.x <= 800 && point.y >= 0 && point.y <= 600)).toBe(true);
      expect(command.layer.points.at(-1)).toEqual({ x: 800, y: 0 });
    } else if (command.kind === "add-layer" && (command.layer.tool === "rectangle" || command.layer.tool === "blur" || command.layer.tool === "redaction")) {
      expect(command.layer.bounds).toEqual({ x: 200, y: 0, width: 600, height: 150 });
    } else {
      throw new Error(`unexpected ${tool} editor command`);
    }
  });

  it("reconciles a completed editor mutation after close while skipping queued work", async () => {
    let resolveFirst: ((response: NativeBridgeResponseV1) => void) | undefined;
    const applyRequests: Extract<NativeBridgeRequestV1, { operation: "capture.editor.apply" }>[] = [];
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft, secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") {
        applyRequests.push(value);
        return new Promise((resolve) => { resolveFirst = resolve; });
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click((await screen.findAllByRole("button", { name: messages.en.realqaOpenEditor }))[0]);
    const add = screen.getByRole("button", { name: messages.en.editorAdd });
    fireEvent.click(add);
    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(1));

    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    fireEvent.click(screen.getAllByRole("button", { name: messages.en.realqaOpenEditor })[1]);
    expect(screen.getByRole("button", { name: `${messages.en.editorImage} 2` })).toBeTruthy();

    await act(async () => { resolveFirst?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } }); });
    expect(applyRequests).toHaveLength(1);
    expect(screen.getByRole("button", { name: `${messages.en.editorImage} 2` })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    fireEvent.click(screen.getAllByRole("button", { name: messages.en.realqaOpenEditor })[0]);
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(4);
  });

  it.each([
    ["readable", "delete"],
    ["readable", "refresh"],
    ["unreadable", "delete"],
    ["unreadable", "refresh"],
  ] as const)("reports %s draft %s failures", async (draftKind, failure) => {
    const unreadableId = "019b0000-0000-7000-8000-000000000004";
    let deleted = false;
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") {
        if (deleted && failure === "refresh") throw new Error("refresh failed");
        return draftKind === "readable"
          ? { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] }
          : { kind: "capture-drafts", drafts: [], unreadableDraftIds: [unreadableId] };
      }
      if (value.operation === "capture.delete-draft") {
        if (failure === "delete") throw new Error("delete failed");
        deleted = true;
        return { kind: "ok" };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    await screen.findByRole("button", { name: messages.en.realqaDeleteDraft });
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqaDeleteDraft }));
    expect((await screen.findByRole("alert")).textContent).toBe(messages.en.realqaDeleteFailed);
  });

  it("offers explicit deletion for an unreadable encrypted draft", async () => {
    const unreadableId = "019b0000-0000-7000-8000-000000000004";
    let deleted = false;
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: deleted ? [] : [unreadableId] };
      if (value.operation === "capture.delete-draft") { deleted = true; return { kind: "ok" }; }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    expect(await screen.findByText(messages.en.realqaUnreadableDraft)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqaDeleteDraft }));
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.delete-draft", draftId: unreadableId }));
    await waitFor(() => expect(screen.queryByText(messages.en.realqaUnreadableDraft)).toBeNull());
  });
});
