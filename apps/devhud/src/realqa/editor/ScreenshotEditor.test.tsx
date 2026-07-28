import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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
import { ScreenshotEditor } from "./ScreenshotEditor";

const source: ComposerImage = {
  imageId: "image-1",
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

function fixture() {
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
  const result = render(renderEditor());
  const canvas = screen.getByRole("application", {
    name: /Screenshot editor canvas/u,
  });
  vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue({
    x: 0,
    y: 0,
    left: 0,
    top: 0,
    right: 500,
    bottom: 400,
    width: 500,
    height: 400,
    toJSON: () => ({}),
  });
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

  it("resets image-specific state when the source identity changes", async () => {
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
      imageId: "image-2",
      width: 40,
      height: 20,
    };
    rerender(
      renderEditor({
        imageId: "image-2",
        sessionId: "session-2",
        sourceValue: nextSource,
      }),
    );

    expect(screen.getByRole("list", { name: "Applied edits" }))
      .toBeEmptyDOMElement();
    expect(
      screen.getByRole("application", { name: /Cursor at 20, 10/u }),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Approve 0 edits" }));
    expect(bridge.flattenImage).toHaveBeenCalledWith({
      sessionId: "session-2",
      imageId: "image-2",
      operations: [],
      outputMediaType: ImageMediaType.Png,
    });
  });

  it("previews blur and pixelate using each operation's strength", async () => {
    const user = userEvent.setup();
    const { canvas, container } = fixture();
    const lineWidth = screen.getByRole("slider", { name: /Line width/u });

    await user.click(screen.getByRole("button", { name: "Blur" }));
    fireEvent.change(lineWidth, { target: { value: "5" } });
    fireEvent.pointerDown(canvas, { clientX: 50, clientY: 40, pointerId: 1 });
    fireEvent.pointerMove(canvas, { clientX: 250, clientY: 200, pointerId: 1 });
    fireEvent.pointerUp(canvas, { clientX: 250, clientY: 200, pointerId: 1 });
    expect(container.querySelector("feGaussianBlur")).toHaveAttribute(
      "stdDeviation",
      "10",
    );

    await user.click(screen.getByRole("button", { name: "Pixelate" }));
    fireEvent.change(lineWidth, { target: { value: "7" } });
    fireEvent.pointerDown(canvas, { clientX: 250, clientY: 200, pointerId: 2 });
    fireEvent.pointerMove(canvas, { clientX: 450, clientY: 360, pointerId: 2 });
    fireEvent.pointerUp(canvas, { clientX: 450, clientY: 360, pointerId: 2 });
    const pattern = container.querySelector("pattern");
    expect(pattern).toHaveAttribute("height", "28");
    expect(pattern).toHaveAttribute("width", "28");
    expect(pattern?.querySelector("rect")).toHaveAttribute("height", "14");
    expect(pattern?.querySelector("rect")).toHaveAttribute("width", "14");
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
