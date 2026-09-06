// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode, createRef, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { messages, type Copy } from "./localization";
import { NativeBridgeError, NativeBridgeErrorCode, type CaptureDraft, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1 } from "./native-bridge";
import { RealqaSurface, type RealqaController } from "./realqa-ui";
import { ShortcutActionId } from "./shortcuts";

vi.mock("./realqa-submission-ui.tsx", () => ({
  RealqaSubmissionModal: ({ draft, onConfirmed }: { readonly draft: CaptureDraft; readonly onConfirmed: (expectedRevision: number) => Promise<void> }) => <button onClick={() => void onConfirmed(draft.revision)}>confirm fixture issue</button>,
}));

const draft: CaptureDraft = {
  id: "019b0000-0000-7000-8000-000000000001",
  revision: 3,
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_100,
  expiresAt: 1_702_592_100,
  hasBrowserContext: false,
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

function bridgeWith(handler?: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>, openDraft?: (request: Extract<NativeBridgeRequestV1, { operation: "capture.open-draft" }>) => Promise<NativeBridgeResponseV1>) {
  const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
    if (value.operation === "capture.open-draft") {
      if (openDraft) return openDraft(value);
      return { kind: "capture-draft", draft: value.draftId === secondDraft.id ? secondDraft : draft };
    }
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

async function openEditor(copy: Copy = messages.en, index = 0) {
  const buttons = await screen.findAllByRole("button", { name: copy.realqaOpenEditor });
  fireEvent.click(buttons[index]);
  await screen.findByRole("heading", { name: copy.editorTitle });
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("RealQA capture and editor", () => {
  it("orders the five existing capture actions before fixed encrypted-draft policy and drafts", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const flow = document.querySelector(".realqa-flow") as HTMLElement;
    expect(Array.from(flow.children, (element) => element.className)).toEqual(expect.arrayContaining(["ui-card realqa-capture-card", "ui-card realqa-policy"]));
    const captureCard = document.querySelector(".realqa-capture-card") as HTMLElement;
    expect(within(captureCard).getByRole("heading", { level: 3, name: messages.en.realqaCapture })).toBeTruthy();
    for (const label of [messages.en.captureDisplay, messages.en.captureWindow, messages.en.captureAll, messages.en.captureSelection, messages.en.captureToolbar]) {
      expect(screen.getByRole("button", { name: label })).toBeTruthy();
    }
    expect(screen.getByText(messages.en.realqaPolicySummary)).toBeTruthy();
    expect(screen.getByText(messages.en.realqaPolicyQuota)).toBeTruthy();
    expect(messages.en.realqaPolicyQuota).toMatch(/log out/iu);
    expect(messages.en.realqaPolicyQuota).toMatch(/confirmed issue creation/iu);
    expect(messages.ko.realqaPolicyQuota).toContain("로그아웃");
    expect(messages.ko.realqaPolicyQuota).toContain("확인된 이슈 생성");
    expect(flow.textContent).not.toMatch(/used|%/iu);
    expect(document.querySelector(".realqa-flow .progress")).toBeNull();
    expect(screen.getByRole("status").textContent).toContain(messages.en.realqaNoDrafts);
  });

  it("returns palette focus to an open editor sheet", async () => {
    const { bridge } = bridgeWith();
    const view = render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} paletteOpen />);
    screen.getByRole("button", { name: messages.en.captureDisplay }).focus();
    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} paletteOpen={false} />);

    const editor = screen.getByRole("dialog", { name: messages.en.editorTitle });
    const image = within(editor).getByRole("button", { name: `${messages.en.editorImage} 1` });
    await waitFor(() => expect(document.activeElement).toBe(image));
    const captureDisplay = within(editor).getByRole("button", { name: messages.en.captureDisplay });
    captureDisplay.focus();
    fireEvent.keyDown(captureDisplay, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(within(editor).getByRole("button", { name: messages.en.close }));
  });

  it("returns palette focus to an already-open capture picker", async () => {
    const { bridge } = bridgeWith();
    const view = render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    const picker = await screen.findByRole("dialog", { name: messages.en.captureSelection });

    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} paletteOpen />);
    screen.getByRole("button", { name: messages.en.captureDisplay }).focus();
    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} paletteOpen={false} />);

    const captureNow = within(picker).getByRole("button", { name: messages.en.captureNow });
    await waitFor(() => expect(document.activeElement).toBe(captureNow));
    fireEvent.keyDown(captureNow, { key: "Tab" });
    expect(picker.contains(document.activeElement)).toBe(true);
  });

  it("starts append captures from the editor sheet", async () => {
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft: { ...draft, revision: 4 } };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    fireEvent.click(within(screen.getByRole("dialog", { name: messages.en.editorTitle })).getByRole("button", { name: messages.en.captureDisplay }));

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.start",
      options: expect.objectContaining({ appendToDraftId: draft.id }),
    })));
  });

  it.each([
    [NativeBridgeErrorCode.QuotaExhausted, "realqaQuotaTitle", "captureQuotaFull"],
    [NativeBridgeErrorCode.PermissionDenied, "realqaPermissionTitle", "capturePermission"],
    [NativeBridgeErrorCode.ProtectedContent, "realqaProtectedTitle", "captureProtected"],
    [NativeBridgeErrorCode.TopologyChanged, "realqaTopologyTitle", "captureTopologyChanged"],
    [NativeBridgeErrorCode.StorageFailure, "realqaSaveTitle", "realqaSaveFailed"],
  ] as const)("presents %s with localized text and an icon-backed state panel", async (code, titleKey, summaryKey) => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") throw new NativeBridgeError(code);
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en[titleKey]);
    expect(alert.textContent).toContain(messages.en[summaryKey]);
    expect(alert.querySelector("svg")).toBeTruthy();
  });

  it.each([
    [NativeBridgeErrorCode.QuotaExhausted, "realqaQuotaTitle", "captureQuotaFull"],
    [NativeBridgeErrorCode.PermissionDenied, "realqaPermissionTitle", "capturePermission"],
    [NativeBridgeErrorCode.ProtectedContent, "realqaProtectedTitle", "captureProtected"],
    [NativeBridgeErrorCode.TopologyChanged, "realqaTopologyTitle", "captureTopologyChanged"],
    [NativeBridgeErrorCode.StorageFailure, "realqaSaveTitle", "realqaSaveFailed"],
  ] as const)("dismisses the selection picker and presents %s capture feedback", async (code, titleKey, summaryKey) => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") throw new NativeBridgeError(code);
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    await screen.findByRole("dialog", { name: messages.en.captureSelection });
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureNow }));

    const alert = await screen.findByRole("alert");
    expect(screen.queryByRole("dialog", { name: messages.en.captureSelection })).toBeNull();
    expect(alert.textContent).toContain(messages.en[titleKey]);
    expect(alert.textContent).toContain(messages.en[summaryKey]);
  });

  it("presents an initial draft storage failure as a save failure", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en.realqaSaveTitle);
    expect(alert.textContent).toContain(messages.en.realqaSaveFailed);
  });

  it("keeps nested RealQA state-panel headings below their containing sections", async () => {
    const unreadableId = "019b0000-0000-7000-8000-000000000099";
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [unreadableId] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    expect((await screen.findByRole("heading", { name: messages.en.realqaUnreadableTitle })).tagName).toBe("H4");
    cleanup();

    const empty = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={empty.bridge} copy={messages.en} />);
    expect((await screen.findByRole("heading", { name: messages.en.realqaEmptyTitle })).tagName).toBe("H4");
  });

  it("keeps editor save feedback below the sheet heading", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") throw new Error("save failed");
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));

    expect((await screen.findByRole("heading", { name: messages.en.realqaSaveTitle })).tagName).toBe("H3");
  });

  it.each([["en", messages.en], ["ko", messages.ko]] as const)("exposes the accessible %s editor and ordered layer controls", async (_language, copy) => {
    const { bridge, request } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={copy} />);

    await openEditor(copy);
    expect(screen.getByRole("heading", { name: copy.editorTitle })).toBeTruthy();
    expect(screen.getByRole("heading", { name: copy.editorLayers }).tagName).toBe("H3");
    for (const name of [copy.editorCrop, copy.editorArrow, copy.editorRectangle, copy.editorDrawing, copy.editorText, copy.editorBlur, copy.editorRedaction]) {
      expect(screen.getByRole("button", { name })).toBeTruthy();
    }
    expect(screen.getByRole("img", { name: copy.editorCanvas })).toBeTruthy();
    const arrowLines = document.querySelectorAll(".annotation-overlay > g > line");
    expect(arrowLines).toHaveLength(3);
    expect(Number(arrowLines[1].getAttribute("x2"))).toBeCloseTo(37.26, 2);
    expect(Number(arrowLines[1].getAttribute("y2"))).toBeCloseTo(29.68, 2);
    expect(Number(arrowLines[2].getAttribute("x2"))).toBeCloseTo(37.26, 2);
    expect(Number(arrowLines[2].getAttribute("y2"))).toBeCloseTo(10.32, 2);
    const textPreview = document.querySelector(".annotation-overlay text");
    expect(textPreview?.classList.contains("annotation-text")).toBe(true);
    expect(textPreview?.getAttribute("style")).toBeNull();
    const blurPreview = document.querySelector(".annotation-overlay .blur-preview");
    const blurSourceHref = blurPreview?.getAttribute("href");
    expect(blurSourceHref).toBe(`#realqa-blur-source-${draft.images[0].layers[3].id}`);
    const blurSource = document.getElementById(blurSourceHref?.slice(1) ?? "");
    expect(blurSource?.getAttribute("clip-path")).toContain("realqa-blur-clip-");
    const accumulatedSourceHref = blurSource?.querySelector("use")?.getAttribute("href");
    expect(accumulatedSourceHref).toBe(`#realqa-layer-state-${draft.images[0].id}-3`);
    expect(document.getElementById(accumulatedSourceHref?.slice(1) ?? "")?.querySelector("text")?.textContent).toBe("한글");
    expect(blurPreview?.getAttribute("clip-path")).toBeNull();
    expect(blurPreview?.getAttribute("filter")).toContain("realqa-blur-filter-");
    expect(document.querySelector(".annotation-overlay feGaussianBlur")?.getAttribute("stdDeviation")).toBe("12");
    const remove = screen.getAllByRole("button", { name: copy.editorRemove }).at(-1)!;
    remove.focus();
    fireEvent.click(remove);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.editor.apply",
      expectedRevision: 3,
      command: expect.objectContaining({ kind: "remove-layer" }),
    })));
    const imageSelector = screen.getByRole("button", { name: `${copy.editorImage} 1` });
    expect(imageSelector).toBe(document.activeElement);
    expect(screen.getByRole("dialog", { name: copy.editorTitle }).contains(document.activeElement)).toBe(true);
  });

  it("revalidates a listed draft before opening the editor", async () => {
    const openedDraft = { ...draft, revision: 4, images: draft.images.map((image) => ({ ...image, previewUrl: image.previewUrl.replace(/\/3$/, "/4") })) };
    const { bridge, request } = bridgeWith(undefined, async () => ({ kind: "capture-draft", draft: openedDraft }));
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    await openEditor();

    expect(await screen.findByRole("heading", { name: messages.en.editorTitle })).toBeTruthy();
    expect(request).toHaveBeenCalledWith({ operation: "capture.open-draft", draftId: draft.id });
    expect(screen.getByRole("img", { name: messages.en.editorCanvas }).querySelector("img")?.getAttribute("src")).toBe(openedDraft.images[0].previewUrl);
  });

  it("restores the draft opener recorded before a delayed draft load", async () => {
    let resolveOpen: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge, request } = bridgeWith(undefined, async () => new Promise<NativeBridgeResponseV1>((resolve) => { resolveOpen = resolve; }));
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const opener = await screen.findByRole("button", { name: messages.en.realqaOpenEditor });
    opener.focus();
    fireEvent.click(opener);
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.open-draft", draftId: draft.id }));
    screen.getByRole("button", { name: messages.en.captureDisplay }).focus();
    await act(async () => { resolveOpen?.({ kind: "capture-draft", draft }); });
    await screen.findByRole("dialog", { name: messages.en.editorTitle });

    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await waitFor(() => expect(opener).toBe(document.activeElement));
  });

  it("keeps an expired draft out of the editor and reconciles the cached list", async () => {
    let listRequests = 0;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") {
        listRequests += 1;
        return { kind: "capture-drafts", drafts: listRequests === 1 ? [draft] : [], unreadableDraftIds: [] };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    }, async () => { throw new NativeBridgeError(NativeBridgeErrorCode.NotFound); });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(await screen.findByRole("button", { name: messages.en.realqaOpenEditor }));

    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.realqaOpenFailed);
    await waitFor(() => expect(screen.queryByRole("button", { name: messages.en.realqaOpenEditor })).toBeNull());
    expect(screen.queryByRole("heading", { name: messages.en.editorTitle })).toBeNull();
  });

  it("lazy-loads full-resolution draft card previews", async () => {
    const { bridge } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    await screen.findByRole("button", { name: messages.en.realqaOpenEditor });
    const preview = document.querySelector<HTMLImageElement>(".draft-preview img");
    expect(preview?.getAttribute("loading")).toBe("lazy");
    expect(preview?.getAttribute("decoding")).toBe("async");
  });

  it("keeps editor operations active through the Strict Mode setup-cleanup cycle", async () => {
    const { bridge, request } = bridgeWith();
    render(<StrictMode><RealqaSurface bridge={bridge} copy={messages.en} /></StrictMode>);

    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.editor.apply",
      expectedRevision: draft.revision,
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

  it("validates capture regions inline and omits them from window capture", async () => {
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [{ id: "main", name: "Main", logicalBounds: { x: -100, y: 0, width: 1920, height: 1080 }, pixelWidth: 3840, pixelHeight: 2160, scale: 2, primary: true }] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    await screen.findByRole("dialog", { name: messages.en.captureSelection });
    const width = screen.getByRole("spinbutton", { name: messages.en.captureWidth });
    const captureNow = screen.getByRole("button", { name: messages.en.captureNow });
    fireEvent.change(width, { target: { value: "" } });

    expect(captureNow).toHaveProperty("disabled", true);
    expect(width.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByRole("alert").textContent).toBe(messages.en.captureRegionInvalid);
    fireEvent.click(captureNow);
    expect(request.mock.calls.some(([value]) => value.operation === "capture.start")).toBe(false);

    fireEvent.click(screen.getByRole("radio", { name: messages.en.captureWindowMode }));
    expect(screen.queryByText(messages.en.captureRegionInvalid)).toBeNull();
    expect(captureNow).toHaveProperty("disabled", false);
    fireEvent.click(captureNow);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.start",
      options: expect.not.objectContaining({ selection: expect.anything() }),
    })));
  });

  it("attaches, inspects, and removes Chrome context after a successful capture", async () => {
    const attachedDraft: CaptureDraft = {
      ...draft,
      revision: 4,
      hasBrowserContext: true,
      browserContext: {
        mappingId: "01900000-0000-7000-8000-000000000001",
        context: {
          url: "https://example.com/%3Credacted%3E",
          title: "Captured page",
          viewport: { width: 1280, height: 720 },
          userAgent: "DevHUD test",
          selectedBounds: { x: 10, y: 20, width: 300, height: 200 },
          accessibility: { "aria-label": "Captured content" },
          outerHtml: "<main>Captured page</main>",
        },
      },
    };
    const removedDraft: CaptureDraft = {
      ...attachedDraft,
      revision: 5,
      hasBrowserContext: false,
      browserContext: undefined,
    };
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const takeBrowserContext = vi.fn(async () => attachedDraft);
    let captured = false;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: captured ? [attachedDraft] : [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") { captured = true; return { kind: "capture-draft", draft }; }
      if (value.operation === "capture.remove-browser-context") return { kind: "capture-draft", draft: removedDraft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} takeBrowserContext={takeBrowserContext} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    await waitFor(() => expect(takeBrowserContext).toHaveBeenCalledWith(draft.id, draft.revision));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.floatingPreviewOpen }));
    expect(screen.getByRole("heading", { name: messages.en.browserContextAttached })).toBeTruthy();
    expect(screen.getByText("Captured page")).toBeTruthy();
    expect(screen.getByText("https://example.com/%3Credacted%3E")).toBeTruthy();
    fireEvent.click(screen.getByText(messages.en.browserContextDetails));
    expect(screen.getByText("DevHUD test")).toBeTruthy();
    expect(screen.getByText("Captured content")).toBeTruthy();
    expect(screen.getByText("<main>Captured page</main>")).toBeTruthy();

    const firstImage = screen.getByRole("button", { name: `${messages.en.editorImage} 1` });
    const removeContext = screen.getByRole("button", { name: messages.en.browserContextRemove });
    removeContext.focus();
    fireEvent.click(removeContext);

    await waitFor(() => expect(request).toHaveBeenCalledWith({
      operation: "capture.remove-browser-context",
      draftId: attachedDraft.id,
      expectedRevision: attachedDraft.revision,
    }));
    expect(screen.queryByRole("heading", { name: messages.en.browserContextAttached })).toBeNull();
    expect(screen.getByText(messages.en.browserContextRemoved).closest("[role='status']")).toBeTruthy();
    expect(firstImage).toBe(document.activeElement);
    expect(screen.getByRole("dialog", { name: messages.en.editorTitle }).contains(document.activeElement)).toBe(true);
  });

  it("keeps attached context visible when revision-checked removal fails", async () => {
    const attachedDraft: CaptureDraft = {
      ...draft,
      revision: 4,
      hasBrowserContext: true,
      browserContext: {
        mappingId: "01900000-0000-7000-8000-000000000001",
        context: {
          url: "https://example.com/%3Credacted%3E",
          title: "Captured page",
          viewport: { width: 1280, height: 720 },
          userAgent: "DevHUD test",
          selectedBounds: null,
          accessibility: {},
          outerHtml: "<main>Captured page</main>",
        },
      },
    };
    const listedDraft = { ...attachedDraft, browserContext: undefined };
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [listedDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.remove-browser-context") throw new NativeBridgeError(NativeBridgeErrorCode.RevisionConflict);
      throw new Error(`unexpected operation ${value.operation}`);
    }, async () => ({ kind: "capture-draft", draft: attachedDraft }));
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(await screen.findByRole("button", { name: messages.en.realqaOpenEditor }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.browserContextRemove }));

    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.browserContextRemoveFailed);
    expect(screen.getByRole("heading", { name: messages.en.browserContextAttached })).toBeTruthy();
  });

  it("keeps a saved capture when Chrome context attachment fails", async () => {
    const takeBrowserContext = vi.fn(async () => { throw new Error("unavailable"); });
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} takeBrowserContext={takeBrowserContext} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    expect(await screen.findByText(messages.en.captureSaved)).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain(messages.en.browserContextAttachmentTitle);
    expect(screen.getByRole("alert").textContent).toContain(messages.en.browserContextAttachmentFailed);
    expect(screen.getByRole("alert").textContent).not.toContain(messages.en.realqaCaptureFailedTitle);
    expect(screen.getByRole("complementary", { name: messages.en.floatingPreview })).toBeTruthy();
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

  it("restores the opener recorded before a delayed topology refresh", async () => {
    let statusRequests = 0;
    let resolveDialogStatus: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") {
        statusRequests += 1;
        if (statusRequests === 1) return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
        return new Promise((resolve) => { resolveDialogStatus = resolve; });
      }
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await waitFor(() => expect(statusRequests).toBe(1));

    const opener = screen.getByRole("button", { name: messages.en.captureSelection });
    opener.focus();
    fireEvent.click(opener);
    screen.getByRole("button", { name: messages.en.captureDisplay }).focus();
    await act(async () => { resolveDialogStatus?.({ kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] }); });
    await screen.findByRole("dialog", { name: messages.en.captureSelection });

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureCancel }));
    await waitFor(() => expect(opener).toBe(document.activeElement));
  });

  it.each([ShortcutActionId.CaptureSelection, ShortcutActionId.CaptureToolbar])("retargets a delayed %s picker after its editor closes", async (action) => {
    let statusRequests = 0;
    let resolveDialogStatus: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") {
        statusRequests += 1;
        if (statusRequests === 1) return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
        return new Promise((resolve) => { resolveDialogStatus = resolve; });
      }
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const view = render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} requestedAction={{ action, sequence: 1 }} />);
    await waitFor(() => expect(resolveDialogStatus).toBeTypeOf("function"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    const fallback = screen.getByRole("button", { name: messages.en.captureDisplay });
    expect(fallback).toHaveProperty("disabled", false);

    await act(async () => { resolveDialogStatus?.({ kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] }); });
    await screen.findByRole("dialog", { name: action === ShortcutActionId.CaptureToolbar ? messages.en.captureToolbar : messages.en.captureSelection });
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureCancel }));

    await waitFor(() => expect(document.activeElement).toBe(fallback));
  });

  it("does not reopen a draft when its pending open completes after leaving RealQA", async () => {
    let resolveOpen: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    }, async () => new Promise((resolve) => { resolveOpen = resolve; }));
    const view = render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(await screen.findByRole("button", { name: messages.en.realqaOpenEditor }));
    await waitFor(() => expect(resolveOpen).toBeTypeOf("function"));
    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} active={false} />);
    await act(async () => { resolveOpen?.({ kind: "capture-draft", draft }); });
    view.rerender(<RealqaSurface bridge={bridge} copy={messages.en} />);

    expect(screen.queryByRole("dialog", { name: messages.en.editorTitle })).toBeNull();
  });

  it.each([ShortcutActionId.CaptureSelection, ShortcutActionId.CaptureToolbar])("restores an editor %s picker to its triggering capture action", async (action) => {
    const { bridge } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    const editor = screen.getByRole("dialog", { name: messages.en.editorTitle });
    const label = action === ShortcutActionId.CaptureToolbar ? messages.en.captureToolbar : messages.en.captureSelection;
    const opener = within(editor).getByRole("button", { name: label });
    opener.focus();
    fireEvent.click(opener);
    await screen.findByRole("dialog", { name: label });
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureCancel }));

    await waitFor(() => expect(document.activeElement).toBe(opener));
  });

  it("derives deferred capture feedback from the current locale", async () => {
    let rejectCapture: ((reason: unknown) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((_resolve, reject) => { rejectCapture = reject; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const view = render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await waitFor(() => expect(rejectCapture).toBeTypeOf("function"));
    view.rerender(<RealqaSurface bridge={bridge} copy={messages.ko} />);
    await act(async () => { rejectCapture?.(new NativeBridgeError(NativeBridgeErrorCode.QuotaExhausted)); });

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.ko.realqaQuotaTitle);
    expect(alert.textContent).toContain(messages.ko.captureQuotaFull);
  });

  it.each([
    ["success", undefined, "status", "captureSaved"],
    ["quota failure", NativeBridgeErrorCode.QuotaExhausted, "alert", "captureQuotaFull"],
  ] as const)("keeps append capture %s feedback visible in the editor sheet", async (_case, failure, role, copyKey) => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") {
        if (failure) throw new NativeBridgeError(failure);
        return { kind: "capture-draft", draft: { ...draft, revision: 4, imageCount: 2, images: [...draft.images, { ...draft.images[0], id: "019b0000-0000-7000-8000-000000000021" }] } };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    const sheet = screen.getByRole("dialog", { name: messages.en.editorTitle });
    expect((await within(sheet).findByRole(role)).textContent).toContain(messages.en[copyKey]);
  });

  it("uses a pending badge until capture persistence completes", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const view = render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    const saving = await screen.findByText(messages.en.captureSaving);
    expect(saving.closest(".status-badge")?.classList.contains("status-badge-info")).toBe(true);
    expect(saving.closest(".status-badge")?.classList.contains("status-badge-success")).toBe(false);

    view.rerender(<RealqaSurface bridge={bridge} copy={messages.ko} />);
    const localizedSaving = await screen.findByText(messages.ko.captureSaving);
    expect(localizedSaving.closest(".status-badge")?.classList.contains("status-badge-info")).toBe(true);
    expect(localizedSaving.closest(".status-badge")?.classList.contains("status-badge-success")).toBe(false);

    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });
    const saved = await screen.findByText(messages.ko.captureSaved);
    expect(saved.closest(".status-badge")?.classList.contains("status-badge-success")).toBe(true);
  });

  it("shows pending capture feedback inside an active selection dialog", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureSelection }));
    const dialog = await screen.findByRole("dialog", { name: messages.en.captureSelection });
    fireEvent.change(within(dialog).getByRole("combobox", { name: messages.en.captureTimer }), { target: { value: "5" } });
    fireEvent.click(within(dialog).getByRole("button", { name: messages.en.captureNow }));

    const saving = await within(dialog).findByRole("status");
    expect(saving.textContent).toContain(messages.en.captureSaving);
    expect(saving.querySelector(".status-badge-info")).toBeTruthy();

    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: messages.en.captureSelection })).toBeNull());
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

  it("keeps a shortcut capture dialog above an open editor sheet", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const rendered = render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    rendered.rerender(<RealqaSurface bridge={bridge} copy={messages.en} requestedAction={{ action: ShortcutActionId.CaptureSelection, sequence: 1 }} />);
    const captureDialog = await screen.findByRole("dialog", { name: messages.en.captureSelection });
    const overlays = document.querySelectorAll(".ui-overlay");

    expect(overlays[overlays.length - 1]?.contains(captureDialog)).toBe(true);
  });

  it("cancels an in-flight append when Escape closes an editor sheet", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      if (value.operation === "capture.cancel") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.start",
      options: expect.objectContaining({ appendToDraftId: draft.id }),
    })));
    fireEvent.keyDown(screen.getByRole("dialog", { name: messages.en.editorTitle }), { key: "Escape" });

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.cancel" }));
    expect(screen.queryByRole("dialog", { name: messages.en.editorTitle })).toBeNull();
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });
  });

  it("does not cancel a standalone capture when closing an independently opened editor", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      if (value.operation === "capture.cancel") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.start",
      options: expect.objectContaining({ appendToDraftId: undefined }),
    })));
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));

    expect(request.mock.calls.some(([value]) => value.operation === "capture.cancel")).toBe(false);
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
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await openEditor(messages.en, 1);

    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } }); });
    expect(screen.getByRole("button", { name: `${messages.en.editorImage} 2` })).toBeTruthy();
  });

  it("shows a standalone capture preview in an unrelated editor sheet", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const captureTrigger = screen.getByRole("button", { name: messages.en.captureDisplay });
    fireEvent.click(captureTrigger);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.start" })));
    await openEditor();
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });

    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    expect(screen.getByRole("dialog", { name: messages.en.editorTitle }).contains(preview)).toBe(true);
    expect(preview.querySelector("img")?.getAttribute("src")).toBe(draft.images[0].previewUrl);
    fireEvent.click(within(preview).getByRole("button", { name: messages.en.floatingPreviewOpen }));
    await waitFor(() => expect(screen.getByRole("img", { name: messages.en.editorCanvas }).querySelector("img")?.getAttribute("src")).toBe(draft.images[0].previewUrl));
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await waitFor(() => expect(document.activeElement).toBe(captureTrigger));
  });

  it("clears a stale draft opener before opening a floating preview", async () => {
    const capturedDraft: CaptureDraft = {
      ...draft,
      id: "019b0000-0000-7000-8000-000000000020",
      revision: 4,
      images: [{ ...draft.images[0], id: "019b0000-0000-7000-8000-000000000021", previewUrl: "realqa://asset/draft/image/captured/4" }],
    };
    let captured = false;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: captured ? [capturedDraft, draft, secondDraft] : [draft, secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") { captured = true; return { kind: "capture-draft", draft: capturedDraft }; }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const [firstOpener] = await screen.findAllByRole("button", { name: messages.en.realqaOpenEditor });
    firstOpener.focus();
    fireEvent.click(firstOpener);
    await screen.findByRole("dialog", { name: messages.en.editorTitle });
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await waitFor(() => expect(firstOpener).toBe(document.activeElement));

    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    fireEvent.click(within(preview).getByRole("button", { name: messages.en.floatingPreviewOpen }));
    await screen.findByRole("dialog", { name: messages.en.editorTitle });
    const close = screen.getByRole("button", { name: messages.en.close });
    close.focus();
    fireEvent.click(close);
    await waitFor(() => expect(firstOpener).not.toBe(document.activeElement));
  });

  it("replaces a stale draft opener when a standalone capture auto-opens its editor", async () => {
    const capturedDraft: CaptureDraft = {
      ...draft,
      id: "019b0000-0000-7000-8000-000000000020",
      revision: 4,
      images: [{ ...draft.images[0], id: "019b0000-0000-7000-8000-000000000021", previewUrl: "realqa://asset/draft/image/captured/4" }],
    };
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft, secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft: capturedDraft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const [firstOpener] = await screen.findAllByRole("button", { name: messages.en.realqaOpenEditor });
    firstOpener.focus();
    fireEvent.click(firstOpener);
    await screen.findByRole("dialog", { name: messages.en.editorTitle });
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await waitFor(() => expect(firstOpener).toBe(document.activeElement));

    const captureTrigger = screen.getByRole("button", { name: messages.en.captureDisplay });
    captureTrigger.focus();
    fireEvent.click(captureTrigger);
    await screen.findByRole("dialog", { name: messages.en.editorTitle });
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));

    await waitFor(() => expect(captureTrigger).toBe(document.activeElement));
  });

  it("starts another capture while the previous reconciliation refresh is pending", async () => {
    let listRequests = 0;
    let captureStarts = 0;
    let resolveRefresh: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") {
        listRequests += 1;
        if (listRequests === 1) return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
        if (listRequests === 2) return new Promise((resolve) => { resolveRefresh = resolve; });
        return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      }
      if (value.operation === "capture.start") {
        captureStarts += 1;
        return { kind: "capture-draft", draft };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await waitFor(() => expect(listRequests).toBe(1));

    const opener = screen.getByRole("button", { name: messages.en.captureDisplay });
    opener.focus();
    fireEvent.click(opener);
    await screen.findByRole("dialog", { name: messages.en.editorTitle });
    await waitFor(() => expect(captureStarts).toBe(1));
    expect(opener).toHaveProperty("disabled", false);
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));

    await waitFor(() => expect(document.activeElement).toBe(opener));
    fireEvent.click(opener);
    await waitFor(() => expect(captureStarts).toBe(2));
    await act(async () => { resolveRefresh?.({ kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] }); });
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

  it("keeps confirmed draft deletion authoritative when refresh fails", async () => {
    let listCalls = 0;
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") {
        listCalls += 1;
        if (listCalls > 1) throw new Error("transient refresh failure");
        return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      }
      if (value.operation === "capture.confirm-issue-created") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.issueSubmit }));
    fireEvent.click(await screen.findByRole("button", { name: "confirm fixture issue" }));

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.confirm-issue-created", draftId: draft.id, expectedRevision: draft.revision }));
    await waitFor(() => expect(screen.queryByRole("heading", { name: messages.en.editorTitle })).toBeNull());
    expect(screen.queryByRole("button", { name: messages.en.realqaOpenEditor })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: messages.en.captureDisplay })));
  });

  it("shows a standalone preview when a capture completes after leaving RealQA", async () => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const rendered = render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await waitFor(() => expect(resolveCapture).toBeTypeOf("function"));

    rendered.rerender(<RealqaSurface bridge={bridge} copy={messages.en} active={false} />);
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });

    expect(await screen.findByRole("complementary", { name: messages.en.floatingPreview })).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: messages.en.editorTitle })).toBeNull();
  });

  it.each(["click", "escape"] as const)("returns focus to Capture after activating an off-surface preview with %s", async (dismissal) => {
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    function Harness() {
      const [active, setActive] = useState(true);
      return <><button onClick={() => setActive(false)}>leave RealQA</button><RealqaSurface bridge={bridge} copy={messages.en} active={active} onActivate={() => setActive(true)} /></>;
    }
    render(<Harness />);

    const staleOpener = screen.getByRole("button", { name: messages.en.captureDisplay });
    fireEvent.click(staleOpener);
    await waitFor(() => expect(resolveCapture).toBeTypeOf("function"));
    fireEvent.click(screen.getByRole("button", { name: "leave RealQA" }));
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });

    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    expect(staleOpener.isConnected).toBe(false);
    fireEvent.click(within(preview).getByRole("button", { name: messages.en.floatingPreviewOpen }));
    await screen.findByRole("dialog", { name: messages.en.editorTitle });
    if (dismissal === "escape") fireEvent.keyDown(screen.getByRole("dialog", { name: messages.en.editorTitle }), { key: "Escape" });
    else fireEvent.click(screen.getByRole("button", { name: messages.en.close }));

    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: messages.en.captureDisplay })));
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
    expect(picker.tagName).toBe("svg");
    expect(picker.getAttribute("viewBox")).toBe("-100 0 1920 1080");
    expect(picker.querySelector("[style]")).toBeNull();
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
    await openEditor();
    const add = screen.getByRole("button", { name: messages.en.editorAdd });
    fireEvent.click(add);
    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(1));
    expect(applyRequests[0].expectedRevision).toBe(3);
    resolveFirst?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } });
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(4);
  });

  it("ignores a stale draft list after an editor revision is installed", async () => {
    let listRequests = 0;
    let resolveStaleList: ((response: NativeBridgeResponseV1) => void) | undefined;
    const applyRequests: Extract<NativeBridgeRequestV1, { operation: "capture.editor.apply" }>[] = [];
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") {
        listRequests += 1;
        if (listRequests === 1) return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
        return new Promise((resolve) => { resolveStaleList = resolve; });
      }
      if (value.operation === "capture.editor.apply") {
        applyRequests.push(value);
        return { kind: "capture-draft", draft: { ...draft, revision: value.expectedRevision + 1 } };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const rendered = render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    rendered.rerender(<RealqaSurface bridge={bridge} copy={messages.ko} />);
    await waitFor(() => expect(listRequests).toBe(2));
    const add = screen.getByRole("button", { name: messages.ko.editorAdd });
    fireEvent.click(add);
    await waitFor(() => expect(screen.getByText(messages.ko.editorSaved)).toBeTruthy());
    expect(applyRequests[0].expectedRevision).toBe(3);

    await act(async () => {
      resolveStaleList?.({ kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] });
    });
    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(4);
  });

  it("serializes an appended capture with editor revisions", async () => {
    let storedDraft = draft;
    let resolveEditor: ((response: NativeBridgeResponseV1) => void) | undefined;
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const applyRequests: Extract<NativeBridgeRequestV1, { operation: "capture.editor.apply" }>[] = [];
    const captureRequests: Extract<NativeBridgeRequestV1, { operation: "capture.start" }>[] = [];
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [storedDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") {
        applyRequests.push(value);
        if (applyRequests.length === 1) return new Promise((resolve) => { resolveEditor = resolve; });
        storedDraft = { ...storedDraft, revision: 6, images: storedDraft.images.map((image) => ({ ...image, previewUrl: image.previewUrl.replace(/\/\d+$/, "/6") })) };
        return { kind: "capture-draft", draft: storedDraft };
      }
      if (value.operation === "capture.start") {
        captureRequests.push(value);
        return new Promise((resolve) => { resolveCapture = resolve; });
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    const add = screen.getByRole("button", { name: messages.en.editorAdd });

    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    expect(add).toHaveProperty("disabled", true);
    expect(captureRequests).toHaveLength(0);

    storedDraft = { ...draft, revision: 4, images: draft.images.map((image) => ({ ...image, previewUrl: image.previewUrl.replace(/\/\d+$/, "/4") })) };
    await act(async () => { resolveEditor?.({ kind: "capture-draft", draft: storedDraft }); });
    await waitFor(() => expect(captureRequests).toHaveLength(1));
    expect(captureRequests[0]?.options?.appendToDraftId).toBe(draft.id);
    expect(applyRequests).toHaveLength(1);

    storedDraft = { ...storedDraft, revision: 5, images: storedDraft.images.map((image) => ({ ...image, previewUrl: image.previewUrl.replace(/\/\d+$/, "/5") })) };
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft: storedDraft }); });
    await waitFor(() => expect(add).toHaveProperty("disabled", false));
    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(5);
  });

  it("does not start a queued append capture after closing the editor", async () => {
    let resolveEditor: ((response: NativeBridgeResponseV1) => void) | undefined;
    const captureRequests: Extract<NativeBridgeRequestV1, { operation: "capture.start" }>[] = [];
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") return new Promise((resolve) => { resolveEditor = resolve; });
      if (value.operation === "capture.start") { captureRequests.push(value); return { kind: "capture-draft", draft }; }
      if (value.operation === "capture.cancel") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();

    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));
    await waitFor(() => expect(resolveEditor).toBeTypeOf("function"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    expect(captureRequests).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "capture.cancel" }));

    await act(async () => { resolveEditor?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } }); });
    await waitFor(() => expect(captureRequests).toHaveLength(0));
  });

  it("keeps a floating preview on the latest editor revision", async () => {
    let storedDraft = draft;
    const applyRequests: Extract<NativeBridgeRequestV1, { operation: "capture.editor.apply" }>[] = [];
    const atRevision = (revision: number): CaptureDraft => ({
      ...storedDraft,
      revision,
      images: storedDraft.images.map((image) => ({ ...image, previewUrl: image.previewUrl.replace(/\/\d+$/, `/${revision}`) })),
    });
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [storedDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") {
        storedDraft = atRevision(4);
        return { kind: "capture-draft", draft: storedDraft };
      }
      if (value.operation === "capture.editor.apply") {
        applyRequests.push(value);
        storedDraft = atRevision(storedDraft.revision + 1);
        return { kind: "capture-draft", draft: storedDraft };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    await waitFor(() => expect(preview.querySelector("img")?.getAttribute("src")).toContain("/4"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));
    await waitFor(() => expect(preview.querySelector("img")?.getAttribute("src")).toContain("/5"));

    const previewOpen = screen.getByRole("button", { name: messages.en.floatingPreviewOpen });
    const firstImage = screen.getByRole("button", { name: `${messages.en.editorImage} 1` });
    previewOpen.focus();
    fireEvent.click(previewOpen);
    expect(firstImage).toBe(document.activeElement);
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(5);
  });

  it("preserves the draft opener after opening its own appended preview", async () => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft: { ...draft, revision: 4 } };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    const opener = await screen.findByRole("button", { name: messages.en.realqaOpenEditor });
    opener.focus();
    fireEvent.click(opener);
    await screen.findByRole("heading", { name: messages.en.editorTitle });
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    fireEvent.click(within(preview).getByRole("button", { name: messages.en.floatingPreviewOpen }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));

    await waitFor(() => expect(document.activeElement).toBe(opener));
  });

  it("previews the first image added by an appended capture", async () => {
    const appendedImage = {
      ...draft.images[0],
      id: "019b0000-0000-7000-8000-000000000020",
      previewUrl: "realqa://asset/draft/image/appended/4",
      layers: [],
    };
    let storedDraft = draft;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [storedDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") {
        storedDraft = { ...draft, revision: 4, imageCount: 2, images: [{ ...draft.images[0], previewUrl: "realqa://asset/draft/image/source/4" }, appendedImage] };
        return { kind: "capture-draft", draft: storedDraft };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    expect(preview.querySelector("img")?.getAttribute("src")).toBe(appendedImage.previewUrl);
    expect(screen.getByRole("dialog", { name: messages.en.editorTitle }).contains(preview)).toBe(true);
  });

  it("keeps focus in the editor sheet when an in-sheet preview times out", async () => {
    const appendedImage = {
      ...draft.images[0],
      id: "019b0000-0000-7000-8000-000000000020",
      previewUrl: "realqa://asset/draft/image/appended/4",
      layers: [],
    };
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft: { ...draft, revision: 4, imageCount: 2, images: [...draft.images, appendedImage] } };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    const setTimeoutSpy = vi.spyOn(window, "setTimeout");
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));

    const preview = await screen.findByRole("complementary", { name: messages.en.floatingPreview });
    const previewOpen = within(preview).getByRole("button", { name: messages.en.floatingPreviewOpen });
    const firstImage = screen.getByRole("button", { name: `${messages.en.editorImage} 1` });
    previewOpen.focus();
    const dismiss = setTimeoutSpy.mock.calls.find(([, delay]) => delay === 5_000)?.[0];
    if (typeof dismiss !== "function") throw new Error("missing preview timeout");
    await act(async () => { dismiss(); });

    expect(screen.queryByRole("complementary", { name: messages.en.floatingPreview })).toBeNull();
    expect(firstImage).toBe(document.activeElement);
    expect(screen.getByRole("dialog", { name: messages.en.editorTitle }).contains(document.activeElement)).toBe(true);
  });

  it("caps text annotations at the native Unicode character limit", async () => {
    const { bridge, request } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorText }));
    const input = screen.getByRole("textbox", { name: messages.en.editorTextValue });
    const limited = "😀".repeat(2_048);
    fireEvent.change(input, { target: { value: `${limited}😀` } });
    expect(input).toHaveProperty("value", limited);
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({
      operation: "capture.editor.apply",
      command: expect.objectContaining({
        kind: "add-layer",
        layer: expect.objectContaining({ tool: "text", text: limited }),
      }),
    })));
  });

  it("validates coordinate-entered annotations before native submission", async () => {
    const { bridge, request } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    const add = screen.getByRole("button", { name: messages.en.editorAdd });
    const x = screen.getByRole("spinbutton", { name: messages.en.captureX });
    const width = screen.getByRole("spinbutton", { name: messages.en.captureWidth });

    fireEvent.change(width, { target: { value: "" } });
    expect(add).toHaveProperty("disabled", true);
    expect(screen.getByRole("alert").textContent).toBe(messages.en.editorCoordinatesInvalid);

    fireEvent.change(width, { target: { value: "100" } });
    fireEvent.change(x, { target: { value: "-1" } });
    expect(add).toHaveProperty("disabled", true);

    fireEvent.change(x, { target: { value: "750" } });
    expect(add).toHaveProperty("disabled", true);
    expect(request.mock.calls.some(([value]) => value.operation === "capture.editor.apply")).toBe(false);

    fireEvent.change(x, { target: { value: "700" } });
    expect(add).toHaveProperty("disabled", false);
    expect(screen.queryByText(messages.en.editorCoordinatesInvalid)).toBeNull();
    fireEvent.click(add);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.editor.apply" })));
  });

  it.each([
    [draft, 1, true],
    [secondDraft, 2, false],
  ] as const)("prevents image removal only when it would empty a %i-image draft", async (listedDraft, _imageCount, disabled) => {
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [listedDraft], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    const imageOrder = document.querySelector<HTMLElement>(".editor-image-order");
    if (!imageOrder) throw new Error("missing image order controls");

    for (const remove of within(imageOrder).getAllByRole("button", { name: messages.en.editorRemove })) {
      expect(remove).toHaveProperty("disabled", disabled);
    }
  });

  it("selects the fallback image after removing the active image", async () => {
    const remainingDraft: CaptureDraft = {
      ...secondDraft,
      revision: secondDraft.revision + 1,
      imageCount: 1,
      images: [secondDraft.images[0]],
    };
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply" && value.command.kind === "remove-image") {
        expect(value.command.imageId).toBe(secondDraft.images[1].id);
        return { kind: "capture-draft", draft: remainingDraft };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    const secondImage = screen.getByRole("button", { name: `${messages.en.editorImage} 2` });
    fireEvent.click(secondImage);
    expect(secondImage.getAttribute("aria-pressed")).toBe("true");
    const remove = within(secondImage.parentElement!).getByRole("button", { name: messages.en.editorRemove });
    remove.focus();
    fireEvent.click(remove);

    const firstImage = await screen.findByRole("button", { name: `${messages.en.editorImage} 1` });
    await waitFor(() => expect(firstImage.getAttribute("aria-pressed")).toBe("true"));
    expect(screen.getByRole("img", { name: messages.en.editorCanvas }).querySelector("img")?.getAttribute("src")).toBe(remainingDraft.images[0].previewUrl);
    expect(firstImage).toBe(document.activeElement);
    expect(screen.getByRole("dialog", { name: messages.en.editorTitle }).contains(document.activeElement)).toBe(true);
  });

  it("clears mounted draft state through the logout controller reset", async () => {
    const controller = createRef<RealqaController>();
    const unreadableId = "019b0000-0000-7000-8000-000000000099";
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [unreadableId] };
      if (value.operation === "capture.start") return { kind: "capture-draft", draft };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface ref={controller} bridge={bridge} copy={messages.en} />);
    await screen.findByRole("button", { name: messages.en.realqaOpenEditor });
    expect(screen.getByText(messages.en.realqaUnreadableDraft)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await screen.findByRole("complementary", { name: messages.en.floatingPreview });

    act(() => controller.current?.reset());

    expect(screen.queryByRole("button", { name: messages.en.realqaOpenEditor })).toBeNull();
    expect(screen.queryByText(messages.en.realqaUnreadableDraft)).toBeNull();
    expect(screen.queryByRole("complementary", { name: messages.en.floatingPreview })).toBeNull();
    expect(screen.getByText(messages.en.realqaNoDrafts)).toBeTruthy();
  });

  it("ignores a capture response that completes after logout reset", async () => {
    const controller = createRef<RealqaController>();
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface ref={controller} bridge={bridge} copy={messages.en} />);
    await screen.findByText(messages.en.realqaNoDrafts);
    fireEvent.click(screen.getByRole("button", { name: messages.en.captureDisplay }));
    await waitFor(() => expect(resolveCapture).toBeTypeOf("function"));

    act(() => controller.current?.reset());
    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft }); });

    expect(screen.queryByRole("button", { name: messages.en.realqaOpenEditor })).toBeNull();
    expect(screen.queryByRole("complementary", { name: messages.en.floatingPreview })).toBeNull();
    expect(screen.queryByText(messages.en.captureSaved)).toBeNull();
    expect(screen.getByText(messages.en.realqaNoDrafts)).toBeTruthy();
  });

  it("ignores an editor response that completes after logout reset", async () => {
    const controller = createRef<RealqaController>();
    let resolveUndo: ((response: NativeBridgeResponseV1) => void) | undefined;
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.undo") return new Promise((resolve) => { resolveUndo = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface ref={controller} bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorUndo }));
    await waitFor(() => expect(resolveUndo).toBeTypeOf("function"));

    act(() => controller.current?.reset());
    await act(async () => { resolveUndo?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } }); });

    expect(screen.queryByRole("button", { name: messages.en.realqaOpenEditor })).toBeNull();
    expect(screen.queryByRole("heading", { name: messages.en.editorTitle })).toBeNull();
    expect(screen.getByText(messages.en.realqaNoDrafts)).toBeTruthy();
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
    await openEditor();
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
      expect(command.crop).toEqual({ x: 200, y: 0, width: 599, height: 150 });
    } else if (command.kind === "add-layer" && command.layer.tool === "arrow") {
      expect(command.layer.start).toEqual({ x: 200, y: 150 });
      expect(command.layer.end).toEqual({ x: 800, y: 0 });
    } else if (command.kind === "add-layer" && command.layer.tool === "drawing") {
      expect(command.layer.points.every((point) => point.x >= 0 && point.x <= 800 && point.y >= 0 && point.y <= 600)).toBe(true);
      expect(command.layer.points.at(-1)).toEqual({ x: 800, y: 0 });
    } else if (command.kind === "add-layer" && (command.layer.tool === "rectangle" || command.layer.tool === "blur" || command.layer.tool === "redaction")) {
      expect(command.layer.bounds).toEqual({ x: 200, y: 0, width: 599, height: 150 });
    } else {
      throw new Error(`unexpected ${tool} editor command`);
    }
  });

  it.each([
    [messages.en.editorCrop, "crop"],
    [messages.en.editorRectangle, "rectangle"],
    [messages.en.editorBlur, "blur"],
    [messages.en.editorRedaction, "redaction"],
  ] as const)("keeps exact lower-right %s gestures inside the final source pixel", async (toolLabel, tool) => {
    Object.defineProperty(window, "PointerEvent", { configurable: true, value: MouseEvent });
    const { bridge, request } = bridgeWith();
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: toolLabel }));
    const canvas = document.querySelector<HTMLElement>(".editor-canvas")!;
    vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 400, bottom: 300, width: 400, height: 300, toJSON: () => ({}) });
    Object.defineProperty(canvas, "setPointerCapture", { value: vi.fn() });

    fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 400, clientY: 300 });
    fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 400, clientY: 300 });

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "capture.editor.apply" })));
    const applied = request.mock.calls
      .map(([value]) => value)
      .find((value) => value.operation === "capture.editor.apply");
    if (!applied || applied.operation !== "capture.editor.apply") throw new Error("missing editor command");
    const command = applied.command;
    const bounds = command.kind === "set-crop"
      ? command.crop
      : command.kind === "add-layer" && (command.layer.tool === "rectangle" || command.layer.tool === "blur" || command.layer.tool === "redaction")
        ? command.layer.bounds
        : null;
    expect(bounds).toEqual({ x: 799, y: 599, width: 1, height: 1 });
    expect(command.kind === "set-crop" ? "crop" : command.kind === "add-layer" ? command.layer.tool : null).toBe(tool);
  });

  it("reconciles a completed editor mutation after close while skipping queued work", async () => {
    let resolveFirst: ((response: NativeBridgeResponseV1) => void) | undefined;
    let firstDraft = draft;
    const applyRequests: Extract<NativeBridgeRequestV1, { operation: "capture.editor.apply" }>[] = [];
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [draft, secondDraft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") {
        applyRequests.push(value);
        return new Promise((resolve) => { resolveFirst = resolve; });
      }
      throw new Error(`unexpected operation ${value.operation}`);
    }, async (value) => ({ kind: "capture-draft", draft: value.draftId === secondDraft.id ? secondDraft : firstDraft }));
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    await openEditor();
    const add = screen.getByRole("button", { name: messages.en.editorAdd });
    fireEvent.click(add);
    fireEvent.click(add);
    await waitFor(() => expect(applyRequests).toHaveLength(1));

    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await openEditor(messages.en, 1);
    expect(screen.getByRole("button", { name: `${messages.en.editorImage} 2` })).toBeTruthy();

    firstDraft = { ...draft, revision: 4 };
    await act(async () => { resolveFirst?.({ kind: "capture-draft", draft: firstDraft }); });
    expect(applyRequests).toHaveLength(1);
    expect(screen.getByRole("button", { name: `${messages.en.editorImage} 2` })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));
    await waitFor(() => expect(applyRequests).toHaveLength(2));
    expect(applyRequests[1].expectedRevision).toBe(4);
  });

  it("queues readable draft deletion behind an in-flight editor mutation", async () => {
    let resolveApply: ((response: NativeBridgeResponseV1) => void) | undefined;
    let deleted = false;
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge, request } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: true, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: deleted ? [] : [draft], unreadableDraftIds: [] };
      if (value.operation === "capture.editor.apply") return new Promise((resolve) => { resolveApply = resolve; });
      if (value.operation === "capture.delete-draft") { deleted = true; return { kind: "ok" }; }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    await openEditor();
    fireEvent.click(screen.getByRole("button", { name: messages.en.editorAdd }));
    await waitFor(() => expect(request.mock.calls.some(([value]) => value.operation === "capture.editor.apply")).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: messages.en.close }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqaDeleteDraft }));
    await act(async () => { await Promise.resolve(); });
    expect(request.mock.calls.some(([value]) => value.operation === "capture.delete-draft")).toBe(false);

    await act(async () => { resolveApply?.({ kind: "capture-draft", draft: { ...draft, revision: 4 } }); });
    await waitFor(() => expect(request.mock.calls.some(([value]) => value.operation === "capture.delete-draft")).toBe(true));
    await waitFor(() => expect(screen.queryByRole("button", { name: messages.en.realqaDeleteDraft })).toBeNull());
  });

  it.each(["readable", "unreadable"] as const)("reports %s native draft deletion failures", async (draftKind) => {
    const unreadableId = "019b0000-0000-7000-8000-000000000004";
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return draftKind === "readable"
        ? { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] }
        : { kind: "capture-drafts", drafts: [], unreadableDraftIds: [unreadableId] };
      if (value.operation === "capture.delete-draft") throw new Error("delete failed");
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    await screen.findByRole("button", { name: messages.en.realqaDeleteDraft });
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqaDeleteDraft }));
    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.realqaDeleteFailed);
  });

  it.each(["readable", "unreadable"] as const)("retains successful %s deletion when refresh fails", async (draftKind) => {
    const unreadableId = "019b0000-0000-7000-8000-000000000004";
    let deleted = false;
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { bridge } = bridgeWith(async (value) => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "macos", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") {
        if (deleted) throw new Error("refresh failed");
        return draftKind === "readable"
          ? { kind: "capture-drafts", drafts: [draft], unreadableDraftIds: [] }
          : { kind: "capture-drafts", drafts: [], unreadableDraftIds: [unreadableId] };
      }
      if (value.operation === "capture.delete-draft") { deleted = true; return { kind: "ok" }; }
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<RealqaSurface bridge={bridge} copy={messages.en} />);

    fireEvent.click(await screen.findByRole("button", { name: messages.en.realqaDeleteDraft }));
    await waitFor(() => expect(screen.queryByRole("button", { name: messages.en.realqaDeleteDraft })).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
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
