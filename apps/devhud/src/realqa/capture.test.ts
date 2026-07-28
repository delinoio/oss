import { describe, expect, it } from "vitest";

import {
  CaptureFailure,
  CaptureMode,
  createRealQaCaptureBridge,
  createRealQaComposerBridge,
  ImageMediaType,
  PointerInclusion,
  ResizeHandle,
  type CaptureRequest,
  type InvokeCommand,
} from "./capture";

function invokeFixture(): {
  readonly calls: Array<readonly [string, Record<string, unknown>?]>;
  readonly invokeCommand: InvokeCommand;
} {
  const calls: Array<readonly [string, Record<string, unknown>?]> = [];
  return {
    calls,
    invokeCommand: async <T>(
      command: string,
      arguments_?: Record<string, unknown>,
    ) => {
      calls.push([command, arguments_]);
      return undefined as T;
    },
  };
}

describe("RealQA native boundaries", () => {
  it("keeps Windows source metadata bounded by shape and native failures typed", () => {
    const source = {
      id: "windows-window-0000000000000001",
      displayId: "windows-display-0000000000000001",
      bounds: { x: -320, y: 24, width: 640, height: 480 },
      availability: "available",
      metadata: {
        processName: "fixture.exe",
        title: "Fixture",
      },
    } as const;

    expect(Object.keys(source.metadata).toSorted()).toEqual([
      "processName",
      "title",
    ]);
    expect([
      CaptureFailure.PermissionDenied,
      CaptureFailure.ProtectedContent,
      CaptureFailure.WindowMinimized,
      CaptureFailure.WindowClosed,
      CaptureFailure.DisplayRemoved,
      CaptureFailure.CaptureFailed,
    ]).toEqual([
      "permission-denied",
      "protected-content",
      "window-minimized",
      "window-closed",
      "display-removed",
      "capture-failed",
    ]);
  });

  it("uses only the four closed capture commands and exact argument shapes", async () => {
    const { calls, invokeCommand } = invokeFixture();
    const bridge = createRealQaCaptureBridge(invokeCommand);
    const selection = {
      snapshotId: "snapshot-1",
      bounds: { x: -100, y: 0, width: 200, height: 100 },
    };
    const request: CaptureRequest = {
      sessionId: "session-1",
      snapshotId: "snapshot-1",
      source: {
        mode: CaptureMode.Region,
        selection,
      },
      pointer: PointerInclusion.Include,
      outputMediaType: ImageMediaType.Png,
    };

    await bridge.listSources();
    await bridge.adjustSelection(selection, {
      kind: "resize",
      handle: ResizeHandle.SouthEast,
      deltaX: 10,
      deltaY: 20,
    });
    await bridge.beginCapture(request);
    await bridge.cancelCapture("session-1");

    expect(calls).toEqual([
      ["realqa_list_capture_sources", undefined],
      [
        "realqa_adjust_capture_selection",
        {
          selection,
          adjustment: {
            kind: "resize",
            handle: "south-east",
            deltaX: 10,
            deltaY: 20,
          },
        },
      ],
      ["realqa_begin_capture", { request }],
      ["realqa_cancel_capture", { sessionId: "session-1" }],
    ]);
  });

  it("uses only bounded composer image/session commands", async () => {
    const { calls, invokeCommand } = invokeFixture();
    const bridge = createRealQaComposerBridge(invokeCommand);
    const request = {
      sessionId: "session-1",
      imageId: "image-1",
      image: {
        mediaType: ImageMediaType.Png,
        bytes: [137, 80, 78, 71],
      },
      outputMediaType: ImageMediaType.Webp,
    } as const;

    await bridge.acceptImage(request);
    await bridge.removeImage("session-1", "image-1");
    await bridge.resetSession("session-1");

    expect(calls).toEqual([
      ["realqa_composer_accept_image", { request }],
      [
        "realqa_composer_remove_image",
        { sessionId: "session-1", imageId: "image-1" },
      ],
      ["realqa_composer_reset_session", { sessionId: "session-1" }],
    ]);
  });
});
