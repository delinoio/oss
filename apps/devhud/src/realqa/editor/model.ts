import type {
  EditorOperation,
  EditorPoint,
  EditorRect,
} from "../capture";

export const MAX_EDITOR_OPERATIONS = 1_000;
export const MAX_FREEHAND_POINTS = 20_000;

export enum EditorTool {
  Crop = "crop",
  Arrow = "arrow",
  Rectangle = "rectangle",
  Freehand = "freehand",
  Text = "text",
  Marker = "marker",
  Blur = "blur",
  Pixelate = "pixelate",
}

export interface ImageBounds {
  readonly width: number;
  readonly height: number;
}

export interface EditorHistory {
  readonly past: readonly (readonly EditorOperation[])[];
  readonly present: readonly EditorOperation[];
  readonly future: readonly (readonly EditorOperation[])[];
}

export type EditorHistoryAction =
  | { readonly type: "commit"; readonly operation: EditorOperation }
  | { readonly type: "undo" }
  | { readonly type: "redo" }
  | { readonly type: "reset" };

export const emptyEditorHistory: EditorHistory = {
  past: [],
  present: [],
  future: [],
};

export function editorHistoryReducer(
  history: EditorHistory,
  action: EditorHistoryAction,
): EditorHistory {
  switch (action.type) {
    case "commit": {
      const withoutCrop =
        action.operation.kind === "crop"
          ? history.present.filter((operation) => operation.kind !== "crop")
          : history.present;
      if (withoutCrop.length >= MAX_EDITOR_OPERATIONS) return history;
      return {
        past: [...history.past, history.present],
        present: [...withoutCrop, action.operation],
        future: [],
      };
    }
    case "undo": {
      const previous = history.past.at(-1);
      if (previous === undefined) return history;
      return {
        past: history.past.slice(0, -1),
        present: previous,
        future: [history.present, ...history.future],
      };
    }
    case "redo": {
      const next = history.future[0];
      if (next === undefined) return history;
      return {
        past: [...history.past, history.present],
        present: next,
        future: history.future.slice(1),
      };
    }
    case "reset":
      return emptyEditorHistory;
  }
}

export function pointInBounds(
  point: EditorPoint,
  bounds: ImageBounds,
): boolean {
  return (
    Number.isInteger(point.x) &&
    Number.isInteger(point.y) &&
    point.x >= 0 &&
    point.y >= 0 &&
    point.x < bounds.width &&
    point.y < bounds.height
  );
}

export function rectFromPoints(
  first: EditorPoint,
  second: EditorPoint,
  bounds: ImageBounds,
): EditorRect | null {
  const left = Math.max(0, Math.min(first.x, second.x));
  const top = Math.max(0, Math.min(first.y, second.y));
  const right = Math.min(bounds.width - 1, Math.max(first.x, second.x));
  const bottom = Math.min(bounds.height - 1, Math.max(first.y, second.y));
  const width = right - left + 1;
  const height = bottom - top + 1;
  return width > 0 && height > 0 ? { x: left, y: top, width, height } : null;
}

export function validateEditorOperation(
  operation: EditorOperation,
  bounds: ImageBounds,
): boolean {
  const validColor = (color: string) => /^#[\da-f]{6}$/iu.test(color);
  const validRect = (rect: EditorRect) =>
    Number.isInteger(rect.x) &&
    Number.isInteger(rect.y) &&
    Number.isInteger(rect.width) &&
    Number.isInteger(rect.height) &&
    rect.width > 0 &&
    rect.height > 0 &&
    rect.x >= 0 &&
    rect.y >= 0 &&
    rect.x + rect.width <= bounds.width &&
    rect.y + rect.height <= bounds.height;
  const validLine = (lineWidth: number) =>
    Number.isInteger(lineWidth) && lineWidth >= 1 && lineWidth <= 128;

  switch (operation.kind) {
    case "crop":
      return validRect(operation.rect);
    case "arrow":
      return (
        pointInBounds(operation.start, bounds) &&
        pointInBounds(operation.end, bounds) &&
        (operation.start.x !== operation.end.x ||
          operation.start.y !== operation.end.y) &&
        validColor(operation.color) &&
        validLine(operation.lineWidth)
      );
    case "rectangle":
      return (
        validRect(operation.rect) &&
        validColor(operation.color) &&
        validLine(operation.lineWidth)
      );
    case "freehand":
      return (
        operation.points.length >= 2 &&
        operation.points.length <= MAX_FREEHAND_POINTS &&
        operation.points.every((point) => pointInBounds(point, bounds)) &&
        validColor(operation.color) &&
        validLine(operation.lineWidth)
      );
    case "text":
      return (
        pointInBounds(operation.origin, bounds) &&
        new TextEncoder().encode(operation.text).length <= 4_096 &&
        operation.text.length > 0 &&
        ![...operation.text].some((character) => {
          const codePoint = character.codePointAt(0);
          return (
            codePoint !== undefined &&
            (codePoint < 0x20 || codePoint === 0x7f)
          );
        }) &&
        validColor(operation.color) &&
        Number.isInteger(operation.fontSize) &&
        operation.fontSize >= 8 &&
        operation.fontSize <= 128
      );
    case "marker":
      return (
        pointInBounds(operation.center, bounds) &&
        Number.isInteger(operation.number) &&
        operation.number >= 1 &&
        operation.number <= 999 &&
        validColor(operation.color) &&
        Number.isInteger(operation.size) &&
        operation.size >= 12 &&
        operation.size <= 128
      );
    case "blur":
      return (
        validRect(operation.rect) &&
        Number.isInteger(operation.radius) &&
        operation.radius >= 1 &&
        operation.radius <= 128
      );
    case "pixelate":
      return (
        validRect(operation.rect) &&
        Number.isInteger(operation.blockSize) &&
        operation.blockSize >= 2 &&
        operation.blockSize <= 128
      );
  }
}
