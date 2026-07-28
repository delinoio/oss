import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  approvedImagePayload,
  ImageMediaType,
  type ApprovedComposerImage,
  type ComposerImage,
  type RealQaComposerBridge,
} from "../capture";
import {
  ScreenshotEditor,
  blurPreviewTiles,
  boxBlurPreviewPixels,
  pixelatePreviewPixels,
  pixelatePreviewTiles,
} from "./ScreenshotEditor";

const source: ComposerImage = {
  imageId: "image-1",
  sourceRevision: 1,
  contentType: "image/png",
  width: 100,
  height: 80,
  encodedBytes: 8,
  sessionEncodedBytes: 8,
  image: {
    mediaType: ImageMediaType.Png,
    bytes: [137, 80, 78, 71, 13, 10, 26, 10],
  },
};

const defaultCanvasRect: DOMRect = {
  x: 0,
  y: 0,
  left: 0,
  top: 0,
  right: 500,
  bottom: 400,
  width: 500,
  height: 400,
  toJSON: () => ({}),
};

function fixture({
  canvasRect = defaultCanvasRect,
  initialSource = source,
}: {
  readonly canvasRect?: DOMRect;
  readonly initialSource?: ComposerImage;
} = {}) {
  const flattened = {
    ...source,
    encodedBytes: 4,
    sessionEncodedBytes: 4,
    image: { mediaType: ImageMediaType.Png, bytes: [1, 2, 3, 4] },
  } as unknown as ApprovedComposerImage;
  const bridge: RealQaComposerBridge = {
    acceptImage: vi.fn(),
    flattenImage: vi.fn(async () => flattened),
    removeImage: vi.fn(),
    resetSession: vi.fn(),
  };
  const onApprove = vi.fn();
  const renderEditor = ({
    sourceValue = source,
    imageId = sourceValue.imageId,
    sessionId = "session-1",
  }: {
    readonly imageId?: string;
    readonly sessionId?: string;
    readonly sourceValue?: ComposerImage;
  } = {}) => (
    <ScreenshotEditor.Provider
      bridge={bridge}
      imageId={imageId}
      onApprove={onApprove}
      sessionId={sessionId}
      source={sourceValue}
    >
      <ScreenshotEditor.Frame>
        <ScreenshotEditor.Toolbar />
        <ScreenshotEditor.Canvas />
        <ScreenshotEditor.Inspector />
        <ScreenshotEditor.Actions />
      </ScreenshotEditor.Frame>
    </ScreenshotEditor.Provider>
  );
  const result = render(renderEditor({ sourceValue: initialSource }));
  const canvas = screen.getByRole("application", {
    name: /Screenshot editor canvas/u,
  });
  vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue(canvasRect);
  return { ...result, bridge, flattened, onApprove, canvas, renderEditor };
}

afterEach(cleanup);

describe("ScreenshotEditor", () => {
  it("supports pointer edits, undo, redo, and native-only approval", async () => {
    const user = userEvent.setup();
    const { bridge, flattened, onApprove, canvas } = fixture();

    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    fireEvent.pointerMove(canvas, { clientX: 350, clientY: 240, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 350, clientY: 240, pointerId: 1 });
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "Arrow",
    );

    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(screen.getByRole("list", { name: "Applied edits" })).toBeEmptyDOMElement();
    await user.click(screen.getByRole("button", { name: "Redo" }));
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "Arrow",
    );

    await user.click(screen.getByRole("button", { name: "Approve 1 edits" }));
    expect(bridge.flattenImage).toHaveBeenCalledWith({
      sessionId: "session-1",
      imageId: "image-1",
      sourceRevision: 1,
      operations: [
        {
          kind: "arrow",
          start: { x: 10, y: 8 },
          end: { x: 69, y: 47 },
          color: "#e5484d",
          lineWidth: 4,
        },
      ],
      outputMediaType: ImageMediaType.Png,
    });
    expect(onApprove).toHaveBeenCalledWith(flattened);
    expect(approvedImagePayload(flattened)).toEqual({
      contentType: "image/png",
      bytes: [1, 2, 3, 4],
      width: 100,
      height: 80,
    });
    expect(approvedImagePayload(flattened).bytes).not.toEqual(source.image.bytes);
  });

  it("freezes editing while native approval is in flight", async () => {
    const user = userEvent.setup();
    const { bridge, canvas, flattened, onApprove } = fixture();
    let resolveApproval: ((image: ApprovedComposerImage) => void) | undefined;
    vi.mocked(bridge.flattenImage).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveApproval = resolve;
        }),
    );

    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 350, clientY: 240, pointerId: 1 });
    await user.click(screen.getByRole("button", { name: "Approve 1 edits" }));

    expect(canvas).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("button", { name: "Arrow" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Undo" })).toBeDisabled();
    expect(screen.getByRole("slider", { name: /Line width/u })).toBeDisabled();
    fireEvent.pointerDown(canvas, { clientX: 10, clientY: 10, pointerId: 2 });
    fireEvent.pointerUp(canvas, { clientX: 90, clientY: 70, pointerId: 2 });
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "1. Arrow",
    );
    expect(screen.getByRole("list", { name: "Applied edits" })).not.toHaveTextContent(
      "2. Arrow",
    );

    resolveApproval?.(flattened);
    await waitFor(() => expect(onApprove).toHaveBeenCalledWith(flattened));
  });

  it("supports keyboard drawing, tool selection, and shortcuts", async () => {
    const user = userEvent.setup();
    const { canvas } = fixture();
    await user.click(screen.getByRole("button", { name: "Rectangle" }));
    canvas.focus();
    await user.keyboard("{Enter}{Shift>}{ArrowRight}{ArrowDown}{/Shift}{Enter}");
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "Rectangle",
    );
    await user.keyboard("{Control>}z{/Control}");
    expect(screen.getByRole("list", { name: "Applied edits" })).toBeEmptyDOMElement();
    await user.keyboard("{Control>}{Shift>}z{/Shift}{/Control}");
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "Rectangle",
    );
    await user.keyboard("{Escape}");
    expect(screen.getByText("Pending edit cancelled.")).toBeVisible();
  });

  it("maps pointer input through letterboxed screenshot bounds", async () => {
    const user = userEvent.setup();
    const squareSource = { ...source, width: 100, height: 100 };
    const { bridge, canvas } = fixture({
      initialSource: squareSource,
      canvasRect: {
        ...defaultCanvasRect,
        height: 400,
        width: 500,
      },
    });

    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 200, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 450, clientY: 200, pointerId: 1 });
    await user.click(screen.getByRole("button", { name: "Approve 1 edits" }));

    expect(bridge.flattenImage).toHaveBeenCalledWith(
      expect.objectContaining({
        operations: [
          expect.objectContaining({
            kind: "arrow",
            start: { x: 0, y: 50 },
            end: { x: 99, y: 50 },
          }),
        ],
      }),
    );
  });

  it("uses the active crop as the preview and input viewport", async () => {
    const user = userEvent.setup();
    const { bridge, canvas } = fixture();

    await user.click(screen.getByRole("button", { name: "Crop" }));
    fireEvent.pointerDown(canvas, { clientX: 100, clientY: 80, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 400, clientY: 320, pointerId: 1 });
    expect(canvas).toHaveAttribute("viewBox", "20 16 60 48");

    await user.click(screen.getByRole("button", { name: "Arrow" }));
    fireEvent.pointerDown(canvas, { clientX: 0, clientY: 0, pointerId: 2 });
    fireEvent.pointerUp(canvas, { clientX: 500, clientY: 400, pointerId: 2 });
    await user.click(screen.getByRole("button", { name: "Approve 2 edits" }));

    expect(bridge.flattenImage).toHaveBeenCalledWith(
      expect.objectContaining({
        operations: [
          { kind: "crop", rect: { x: 20, y: 16, width: 60, height: 48 } },
          expect.objectContaining({
            kind: "arrow",
            start: { x: 20, y: 16 },
            end: { x: 79, y: 63 },
          }),
        ],
      }),
    );
  });

  it("clamps keyboard drawing to the active crop viewport", async () => {
    const user = userEvent.setup();
    const { bridge, canvas } = fixture();

    await user.click(screen.getByRole("button", { name: "Crop" }));
    fireEvent.pointerDown(canvas, { clientX: 100, clientY: 80, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 400, clientY: 320, pointerId: 1 });

    await user.click(screen.getByRole("button", { name: "Arrow" }));
    canvas.focus();
    await user.keyboard(
      "{Enter}{Shift>}{ArrowRight}{ArrowRight}{ArrowRight}{ArrowRight}{ArrowRight}{/Shift}{Enter}",
    );
    await user.click(screen.getByRole("button", { name: "Approve 2 edits" }));

    expect(bridge.flattenImage).toHaveBeenCalledWith(
      expect.objectContaining({
        operations: [
          { kind: "crop", rect: { x: 20, y: 16, width: 60, height: 48 } },
          expect.objectContaining({
            kind: "arrow",
            start: { x: 50, y: 40 },
            end: { x: 79, y: 40 },
          }),
        ],
      }),
    );
  });

  it("resets image-specific state when the same image receives a new source revision", async () => {
    const user = userEvent.setup();
    const { bridge, canvas, renderEditor, rerender } = fixture();
    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    fireEvent.pointerMove(canvas, { clientX: 350, clientY: 240, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 350, clientY: 240, pointerId: 1 });
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "Arrow",
    );

    const nextSource = {
      ...source,
      sourceRevision: 2,
      width: 40,
      height: 20,
    };
    rerender(renderEditor({ sourceValue: nextSource }));

    expect(screen.getByRole("list", { name: "Applied edits" }))
      .toBeEmptyDOMElement();
    expect(
      screen.getByRole("application", { name: /Cursor at 20, 10/u }),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Approve 0 edits" }));
    expect(bridge.flattenImage).toHaveBeenCalledWith({
      sessionId: "session-1",
      imageId: "image-1",
      sourceRevision: 2,
      operations: [],
      outputMediaType: ImageMediaType.Png,
    });
  });

  it("previews native box blur and source-derived pixelation", async () => {
    const user = userEvent.setup();
    const { canvas, container } = fixture();
    const lineWidth = screen.getByRole("slider", { name: /Line width/u });

    await user.click(screen.getByRole("button", { name: "Blur" }));
    fireEvent.change(lineWidth, { target: { value: "5" } });
    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    fireEvent.pointerMove(canvas, { clientX: 250, clientY: 200, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 250, clientY: 200, pointerId: 1 });
    const blurPreview = container.querySelector("canvas.editor-blur");
    expect(blurPreview).toHaveAttribute("height", "33");
    expect(blurPreview).toHaveAttribute("width", "41");
    expect(container.querySelector("feGaussianBlur")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Undo" }));
    await user.click(screen.getByRole("button", { name: "Pixelate" }));
    fireEvent.change(lineWidth, { target: { value: "7" } });
    fireEvent.pointerDown(canvas, { clientX: 250, clientY: 200, pointerId: 2 });
    fireEvent.pointerMove(canvas, { clientX: 450, clientY: 360, pointerId: 2 });
    fireEvent.pointerUp(canvas, { clientX: 450, clientY: 360, pointerId: 2 });
    const pixelPreview = container.querySelector("canvas.editor-pixelate");
    expect(pixelPreview).toHaveAttribute("height", "3");
    expect(pixelPreview).toHaveAttribute("width", "3");
    expect(pixelPreview?.parentElement).toHaveAttribute("height", "42");
    expect(pixelPreview?.parentElement).toHaveAttribute("width", "42");
    expect(container.querySelector("pattern")).not.toBeInTheDocument();
  });

  it("matches the native separable box average and bounds blur previews", () => {
    const pixels = new Uint8ClampedArray(8 * 8 * 4);
    for (let y = 0; y < 8; y += 1) {
      for (let x = 0; x < 8; x += 1) {
        pixels.set(
          [x * 20, y * 20, x * 10 + y, 255],
          (y * 8 + x) * 4,
        );
      }
    }

    const blurred = boxBlurPreviewPixels(
      pixels,
      8,
      8,
      1,
      { x: 0, y: 0, width: 8, height: 8 },
    );
    expect(Array.from(blurred.slice((4 * 8 + 4) * 4, (4 * 8 + 5) * 4))).toEqual([
      80, 80, 44, 255,
    ]);

    const tiles = blurPreviewTiles(10_000, 10_000, 128);
    expect(tiles).toHaveLength(1);
    expect(
      tiles.reduce(
        (pixels, tile) =>
          pixels + tile.previewWidth * tile.previewHeight,
        0,
      ),
    ).toBeLessThanOrEqual(4 * 1_024 * 1_024);
    expect(tiles.some((tile) => tile.previewWidth < tile.width)).toBe(true);

    for (const skinnyTiles of [
      blurPreviewTiles(25_000_000, 1, 128),
      blurPreviewTiles(1, 25_000_000, 128),
    ]) {
      expect(skinnyTiles).toHaveLength(1);
      expect(
        skinnyTiles.reduce(
          (pixels, tile) =>
            pixels + tile.previewWidth * tile.previewHeight,
          0,
        ),
      ).toBeLessThanOrEqual(4 * 1_024 * 1_024);
      expect(
        skinnyTiles.every(
          (tile) =>
            tile.previewWidth <= 2_048 && tile.previewHeight <= 2_048,
        ),
      ).toBe(true);
    }
  });

  it("averages partial pixelation edge blocks independently", () => {
    const pixels = new Uint8ClampedArray(6 * 5 * 4);
    for (let y = 0; y < 5; y += 1) {
      for (let x = 0; x < 6; x += 1) {
        const offset = (y * 6 + x) * 4;
        const value = y * 10 + x;
        pixels.set([value, value, value, 255], offset);
      }
    }

    const preview = pixelatePreviewPixels(pixels, 6, 5, 4);

    expect(preview.columns).toBe(2);
    expect(preview.rows).toBe(2);
    expect(Array.from(preview.data)).toEqual([
      16, 16, 16, 255,
      19, 19, 19, 255,
      41, 41, 41, 255,
      44, 44, 44, 255,
    ]);
  });

  it("bounds pixelation scratch canvases for maximum-size images", () => {
    const tiles = pixelatePreviewTiles(10_000, 10_000, 4);

    expect(
      tiles.every((tile) => tile.width * tile.height <= 1_024 * 1_024),
    ).toBe(true);
    expect(
      tiles.reduce((pixels, tile) => pixels + tile.width * tile.height, 0),
    ).toBe(100_000_000);
    expect(
      tiles.reduce((pixels, tile) => pixels + tile.columns * tile.rows, 0),
    ).toBe(2_500 * 2_500);
  });

  it("keeps source-derived effects before annotations", async () => {
    const user = userEvent.setup();
    const { bridge, canvas } = fixture();

    await user.click(screen.getByRole("button", { name: "Blur" }));
    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 250, clientY: 200, pointerId: 1 });

    expect(screen.getByRole("button", { name: "Arrow" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Blur" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Pixelate" })).toBeDisabled();

    fireEvent.pointerDown(canvas, { clientX: 250, clientY: 200, pointerId: 2 });
    fireEvent.pointerUp(canvas, { clientX: 450, clientY: 360, pointerId: 2 });
    await user.click(screen.getByRole("button", { name: "Approve 2 edits" }));

    expect(bridge.flattenImage).toHaveBeenCalledWith(
      expect.objectContaining({
        operations: [
          expect.objectContaining({ kind: "blur" }),
          expect.objectContaining({ kind: "arrow" }),
        ],
      }),
    );
  });

  it("normalizes unsupported text before previewing the operation", async () => {
    const user = userEvent.setup();
    const { canvas } = fixture();
    await user.click(screen.getByRole("button", { name: "Text" }));
    const input = screen.getByRole("textbox", { name: "Text" });
    await user.clear(input);
    await user.type(input, "café");
    expect(input).toHaveValue("caf?");
    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    expect(screen.getByRole("list", { name: "Applied edits" })).toHaveTextContent(
      "Text: caf?",
    );
    expect(
      canvas.querySelector("path.editor-bitmap-text"),
    ).toHaveAttribute("transform", "translate(10 8) scale(2)");
    expect(canvas.querySelector("text")).not.toBeInTheDocument();
  });

  it("previews arrowheads with the native pixel sizing formula", () => {
    const { canvas } = fixture();
    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 200, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 450, clientY: 200, pointerId: 1 });

    const arrowWings = canvas.querySelectorAll("line.editor-arrow-head");
    expect(arrowWings).toHaveLength(2);
    expect(arrowWings[0]).toHaveAttribute("x1", "89");
    expect(arrowWings[0]).toHaveAttribute("x2", "76");
    expect(arrowWings[0]).toHaveAttribute("y1", "40");
    expect(arrowWings[0]).toHaveAttribute("y2", "49");
    expect(arrowWings.item(0).parentElement).toHaveAttribute(
      "stroke-linecap",
      "round",
    );
    expect(arrowWings.item(0).parentElement).toHaveAttribute(
      "stroke-linejoin",
      "round",
    );
    expect(canvas.querySelector("marker")).not.toBeInTheDocument();
  });

  it("previews marker numbers with native bitmap metrics", async () => {
    const user = userEvent.setup();
    const { canvas } = fixture();
    await user.click(screen.getByRole("button", { name: "Numbered marker" }));
    for (let number = 1; number <= 10; number += 1) {
      fireEvent.pointerDown(canvas, {
        clientX: 250,
        clientY: 200,
        pointerId: number,
      });
    }

    const labels = canvas.querySelectorAll("path.editor-marker-label");
    expect(labels).toHaveLength(10);
    expect(labels[9]).toHaveAttribute("transform", "translate(44 34) scale(1)");
    expect(canvas.querySelector("text")).not.toBeInTheDocument();
  });

  it("has no automated WCAG violations at mobile width", async () => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 360,
    });
    const { container } = fixture();
    const accessibility = await axe.run(container, {
      rules: {
        "color-contrast": { enabled: false },
      },
    });
    expect(accessibility.violations).toEqual([]);
  });
});
