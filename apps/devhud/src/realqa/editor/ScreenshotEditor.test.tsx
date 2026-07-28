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
  const result = render(
    <ScreenshotEditor.Provider
      bridge={bridge}
      imageId="image-1"
      onApprove={onApprove}
      sessionId="session-1"
      source={source}
    >
      <ScreenshotEditor.Frame>
        <ScreenshotEditor.Toolbar />
        <ScreenshotEditor.Canvas />
        <ScreenshotEditor.Inspector />
        <ScreenshotEditor.Actions />
      </ScreenshotEditor.Frame>
    </ScreenshotEditor.Provider>,
  );
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
  return { ...result, bridge, flattened, onApprove, canvas };
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
