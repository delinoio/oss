import { describe, expect, it } from "vitest";

import type { EditorOperation } from "../capture";
import {
  MAX_FREEHAND_POINTS,
  completeFreehandPoints,
  editorHistoryReducer,
  emptyEditorHistory,
  normalizeEditorText,
  validateEditorOperation,
} from "./model";

const bounds = { width: 100, height: 80 };

const operations: readonly EditorOperation[] = [
  { kind: "crop", rect: { x: 1, y: 2, width: 50, height: 40 } },
  {
    kind: "arrow",
    start: { x: 1, y: 2 },
    end: { x: 30, y: 40 },
    color: "#ff0000",
    lineWidth: 3,
  },
  {
    kind: "rectangle",
    rect: { x: 1, y: 2, width: 50, height: 40 },
    color: "#00ff00",
    lineWidth: 3,
  },
  {
    kind: "freehand",
    points: [
      { x: 1, y: 2 },
      { x: 3, y: 4 },
    ],
    color: "#0000ff",
    lineWidth: 3,
  },
  {
    kind: "text",
    origin: { x: 1, y: 2 },
    text: "Expected result",
    color: "#ffffff",
    fontSize: 16,
  },
  {
    kind: "marker",
    center: { x: 1, y: 2 },
    number: 1,
    color: "#ff0000",
    size: 24,
  },
  {
    kind: "blur",
    rect: { x: 1, y: 2, width: 50, height: 40 },
    radius: 6,
  },
  {
    kind: "pixelate",
    rect: { x: 1, y: 2, width: 50, height: 40 },
    blockSize: 8,
  },
];

describe("screenshot editor model", () => {
  it("caps completed freehand strokes while preserving the release point", () => {
    const points = Array.from({ length: MAX_FREEHAND_POINTS }, (_, index) => ({
      x: index % bounds.width,
      y: 0,
    }));
    const releasePoint = { x: 4, y: 3 };

    const completed = completeFreehandPoints(points, releasePoint);

    expect(completed).toHaveLength(MAX_FREEHAND_POINTS);
    expect(completed.at(-1)).toEqual(releasePoint);
  });

  it("validates every closed operation kind", () => {
    expect(operations.map((operation) => validateEditorOperation(operation, bounds)))
      .toEqual(operations.map(() => true));
  });

  it("rejects invalid crop, color, freehand, text, and effect values", () => {
    const invalid: readonly EditorOperation[] = [
      { kind: "crop", rect: { x: 90, y: 70, width: 11, height: 11 } },
      {
        kind: "arrow",
        start: { x: 1, y: 1 },
        end: { x: 1, y: 1 },
        color: "#ff0000",
        lineWidth: 1,
      },
      {
        kind: "rectangle",
        rect: { x: 0, y: 0, width: 1, height: 1 },
        color: "red",
        lineWidth: 1,
      },
      {
        kind: "freehand",
        points: [{ x: 1, y: 1 }],
        color: "#ff0000",
        lineWidth: 1,
      },
      {
        kind: "text",
        origin: { x: 1, y: 1 },
        text: "\n",
        color: "#ff0000",
        fontSize: 16,
      },
      {
        kind: "blur",
        rect: { x: 0, y: 0, width: 1, height: 1 },
        radius: 0,
      },
      {
        kind: "pixelate",
        rect: { x: 0, y: 0, width: 1, height: 1 },
        blockSize: 1,
      },
    ];
    expect(invalid.every((operation) => !validateEditorOperation(operation, bounds)))
      .toBe(true);
  });

  it("preserves supported text casing and normalizes unsupported glyphs", () => {
    expect(normalizeEditorText("Note 12!")).toBe("Note 12!");
    expect(normalizeEditorText("café 🔒")).toBe("caf? ?");
    expect(
      validateEditorOperation(
        {
          kind: "text",
          origin: { x: 1, y: 1 },
          text: "café",
          color: "#ff0000",
          fontSize: 16,
        },
        bounds,
      ),
    ).toBe(false);
  });

  it("preserves immutable undo/redo snapshots and clears redo on a new edit", () => {
    const first = editorHistoryReducer(emptyEditorHistory, {
      type: "commit",
      operation: operations[1]!,
    });
    const second = editorHistoryReducer(first, {
      type: "commit",
      operation: operations[2]!,
    });
    const undone = editorHistoryReducer(second, { type: "undo" });
    expect(undone.present).toEqual([operations[1]]);
    expect(undone.future).toEqual([[operations[1], operations[2]]]);
    const redone = editorHistoryReducer(undone, { type: "redo" });
    expect(redone.present).toEqual([operations[1], operations[2]]);
    const branched = editorHistoryReducer(undone, {
      type: "commit",
      operation: operations[3]!,
    });
    expect(branched.present).toEqual([operations[1], operations[3]]);
    expect(branched.future).toEqual([]);
    expect(second.present).toEqual([operations[1], operations[2]]);
  });

  it("keeps only one undoable crop while preserving annotations", () => {
    const annotated = editorHistoryReducer(emptyEditorHistory, {
      type: "commit",
      operation: operations[1]!,
    });
    const cropped = editorHistoryReducer(annotated, {
      type: "commit",
      operation: operations[0]!,
    });
    const replaced = editorHistoryReducer(cropped, {
      type: "commit",
      operation: {
        kind: "crop",
        rect: { x: 2, y: 2, width: 20, height: 20 },
      },
    });
    expect(replaced.present.filter((operation) => operation.kind === "crop"))
      .toHaveLength(1);
    expect(editorHistoryReducer(replaced, { type: "undo" }).present).toEqual(
      cropped.present,
    );
  });
});
