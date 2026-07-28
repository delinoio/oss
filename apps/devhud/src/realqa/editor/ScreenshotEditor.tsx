import {
  createContext,
  use,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useReducer,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
} from "react";

import {
  ImageMediaType,
  type ApprovedComposerImage,
  type ComposerImage,
  type EditorOperation,
  type EditorPoint,
  type EditorRect,
  type RealQaComposerBridge,
} from "../capture";
import {
  EditorTool,
  MAX_FREEHAND_POINTS,
  completeFreehandPoints,
  editorHistoryReducer,
  emptyEditorHistory,
  normalizeEditorText,
  rectFromPoints,
  validateEditorOperation,
  validateEditorOperationBudgets,
} from "./model";

interface PendingGesture {
  readonly start: EditorPoint;
  readonly points: readonly EditorPoint[];
  readonly current: EditorPoint;
}

interface ScreenshotEditorValue {
  readonly source: ComposerImage;
  readonly operations: readonly EditorOperation[];
  readonly tool: EditorTool;
  readonly color: string;
  readonly lineWidth: number;
  readonly text: string;
  readonly outputMediaType: ImageMediaType;
  readonly pending: PendingGesture | null;
  readonly keyboardCursor: EditorPoint;
  readonly canUndo: boolean;
  readonly canRedo: boolean;
  readonly status: string;
  readonly approving: boolean;
  readonly sourceUrl: string;
  readonly sourceEffectAvailable: boolean;
  readonly viewport: EditorRect;
  readonly setTool: (tool: EditorTool) => void;
  readonly setColor: (color: string) => void;
  readonly setLineWidth: (width: number) => void;
  readonly setText: (text: string) => void;
  readonly setOutputMediaType: (mediaType: ImageMediaType) => void;
  readonly undo: () => void;
  readonly redo: () => void;
  readonly approve: () => Promise<void>;
  readonly begin: (point: EditorPoint) => void;
  readonly move: (point: EditorPoint) => void;
  readonly finish: (point: EditorPoint) => void;
  readonly cancelGesture: () => void;
  readonly moveKeyboardCursor: (deltaX: number, deltaY: number) => void;
  readonly activateKeyboardCursor: () => void;
}

const ScreenshotEditorContext = createContext<ScreenshotEditorValue | null>(null);

function useScreenshotEditor(): ScreenshotEditorValue {
  const value = use(ScreenshotEditorContext);
  if (value === null) {
    throw new Error("Screenshot editor components require ScreenshotEditor.Provider");
  }
  return value;
}

function bytesToDataUrl(image: ComposerImage): string {
  const bytes = image.image.bytes;
  let binary = "";
  const chunkSize = 32_768;
  for (let index = 0; index < bytes.length; index += chunkSize) {
    binary += String.fromCharCode(...bytes.slice(index, index + chunkSize));
  }
  return `data:${image.contentType};base64,${btoa(binary)}`;
}

function isSourceEffect(
  operation: EditorOperation,
): operation is Extract<EditorOperation, { readonly kind: "blur" | "pixelate" }> {
  return operation.kind === "blur" || operation.kind === "pixelate";
}

function clampPointToRect(point: EditorPoint, rect: EditorRect): EditorPoint {
  return {
    x: Math.max(rect.x, Math.min(rect.x + rect.width - 1, point.x)),
    y: Math.max(rect.y, Math.min(rect.y + rect.height - 1, point.y)),
  };
}

function operationForGesture(
  tool: EditorTool,
  gesture: PendingGesture,
  source: ComposerImage,
  color: string,
  lineWidth: number,
  text: string,
  markerNumber: number,
): EditorOperation | null {
  const bounds = { width: source.width, height: source.height };
  const rect = rectFromPoints(gesture.start, gesture.current, bounds);
  switch (tool) {
    case EditorTool.Crop:
      return rect === null ? null : { kind: "crop", rect };
    case EditorTool.Arrow:
      return {
        kind: "arrow",
        start: gesture.start,
        end: gesture.current,
        color,
        lineWidth,
      };
    case EditorTool.Rectangle:
      return rect === null ? null : { kind: "rectangle", rect, color, lineWidth };
    case EditorTool.Freehand:
      return {
        kind: "freehand",
        points: completeFreehandPoints(gesture.points, gesture.current),
        color,
        lineWidth,
      };
    case EditorTool.Text:
      return {
        kind: "text",
        origin: gesture.current,
        text,
        color,
        fontSize: Math.max(12, lineWidth * 4),
      };
    case EditorTool.Marker:
      return {
        kind: "marker",
        center: gesture.current,
        number: markerNumber,
        color,
        size: Math.max(20, lineWidth * 6),
      };
    case EditorTool.Blur:
      return rect === null
        ? null
        : { kind: "blur", rect, radius: Math.max(2, lineWidth * 2) };
    case EditorTool.Pixelate:
      return rect === null
        ? null
        : { kind: "pixelate", rect, blockSize: Math.max(4, lineWidth * 2) };
  }
}

interface ScreenshotEditorProviderProps {
  readonly bridge: RealQaComposerBridge;
  readonly children: ReactNode;
  readonly imageId: string;
  readonly onApprove: (image: ApprovedComposerImage) => void;
  readonly sessionId: string;
  readonly source: ComposerImage;
}

export function ScreenshotEditorProvider(props: ScreenshotEditorProviderProps) {
  const { imageId, sessionId, source } = props;
  return (
    <ScreenshotEditorStateProvider
      key={JSON.stringify([sessionId, imageId, source.sourceRevision])}
      {...props}
    />
  );
}

function ScreenshotEditorStateProvider({
  bridge,
  children,
  imageId,
  onApprove,
  sessionId,
  source,
}: ScreenshotEditorProviderProps) {
  const [history, dispatch] = useReducer(
    editorHistoryReducer,
    emptyEditorHistory,
  );
  const [tool, setTool] = useState(EditorTool.Arrow);
  const [color, setColor] = useState("#e5484d");
  const [lineWidth, setLineWidth] = useState(4);
  const [text, setText] = useState("Note");
  const [outputMediaType, setOutputMediaType] = useState(ImageMediaType.Png);
  const [pending, setPending] = useState<PendingGesture | null>(null);
  const [keyboardCursorState, setKeyboardCursor] = useState<EditorPoint>({
    x: Math.floor(source.width / 2),
    y: Math.floor(source.height / 2),
  });
  const [status, setStatus] = useState("Ready to edit.");
  const [approving, setApproving] = useState(false);
  const sourceUrl = useMemo(() => bytesToDataUrl(source), [source]);
  const bounds = useMemo(
    () => ({ width: source.width, height: source.height }),
    [source.height, source.width],
  );
  const fullViewport = useMemo(
    () => ({ x: 0, y: 0, width: source.width, height: source.height }),
    [source.height, source.width],
  );
  const crop = history.present.find((operation) => operation.kind === "crop");
  const viewport = crop?.rect ?? fullViewport;
  const keyboardCursor = clampPointToRect(keyboardCursorState, viewport);
  const sourceEffectAvailable = history.present.every(
    (operation) => operation.kind === "crop",
  );

  const markerNumber =
    history.present.filter((operation) => operation.kind === "marker").length + 1;

  const commitGesture = useCallback(
    (gesture: PendingGesture) => {
      const operation = operationForGesture(
        tool,
        gesture,
        source,
        color,
        lineWidth,
        text,
        markerNumber,
      );
      if (operation === null || !validateEditorOperation(operation, bounds)) {
        setStatus(
          "That edit is outside the image, has no drawable size, or exceeds the processing limit.",
        );
        return;
      }
      if (isSourceEffect(operation) && !sourceEffectAvailable) {
        setStatus("Blur or pixelate must be the first image edit.");
        return;
      }
      const nextOperations =
        operation.kind === "crop"
          ? [
              ...history.present.filter(
                (presentOperation) => presentOperation.kind !== "crop",
              ),
              operation,
            ]
          : [...history.present, operation];
      if (!validateEditorOperationBudgets(nextOperations)) {
        setStatus("That edit exceeds the combined processing limit.");
        return;
      }
      dispatch({ type: "commit", operation });
      if (isSourceEffect(operation)) setTool(EditorTool.Arrow);
      if (
        operation.kind === "pixelate" &&
        pixelatePreviewSize(
          operation.rect.width,
          operation.rect.height,
          operation.blockSize,
        ).capped
      ) {
        setStatus(
          "Pixelate edit added. This preview is scaled below the native block grid; the approved image may show more detail.",
        );
      } else {
        setStatus(`${tool} edit added.`);
      }
    },
    [
      bounds,
      color,
      history.present,
      lineWidth,
      markerNumber,
      source,
      sourceEffectAvailable,
      text,
      tool,
    ],
  );

  const begin = useCallback(
    (point: EditorPoint) => {
      const gesture = { start: point, current: point, points: [point] };
      if (tool === EditorTool.Text || tool === EditorTool.Marker) {
        commitGesture(gesture);
        return;
      }
      setPending(gesture);
      setStatus(`${tool} start selected.`);
    },
    [commitGesture, tool],
  );
  const move = useCallback((point: EditorPoint) => {
    setPending((current) => {
      if (current === null) return null;
      return {
        ...current,
        current: point,
        points:
          current.points.length < MAX_FREEHAND_POINTS
            ? [...current.points, point]
            : current.points,
      };
    });
  }, []);
  const finish = useCallback(
    (point: EditorPoint) => {
      if (pending === null) return;
      commitGesture({ ...pending, current: point });
      setPending(null);
    },
    [commitGesture, pending],
  );
  const cancelGesture = useCallback(() => {
    setPending(null);
    setStatus("Pending edit cancelled.");
  }, []);
  const undo = useCallback(() => {
    if (history.past.length === 0) return;
    dispatch({ type: "undo" });
    setPending(null);
    setStatus("Last edit undone.");
  }, [history.past.length]);
  const redo = useCallback(() => {
    if (history.future.length === 0) return;
    dispatch({ type: "redo" });
    setPending(null);
    setStatus("Edit restored.");
  }, [history.future.length]);
  const approve = useCallback(async () => {
    if (approving) return;
    setApproving(true);
    setPending(null);
    setStatus("Creating approved image.");
    try {
      const approved = await bridge.flattenImage({
        sessionId,
        imageId,
        sourceRevision: source.sourceRevision,
        operations: history.present,
        outputMediaType,
      });
      onApprove(approved);
      setStatus("Approved image is ready.");
    } catch {
      setStatus("The approved image could not be created. No source pixels left this device.");
    } finally {
      setApproving(false);
    }
  }, [
    approving,
    bridge,
    history.present,
    imageId,
    onApprove,
    outputMediaType,
    sessionId,
    source.sourceRevision,
  ]);
  const moveKeyboardCursor = useCallback(
    (deltaX: number, deltaY: number) => {
      setKeyboardCursor((current) => {
        const visible = clampPointToRect(current, viewport);
        return clampPointToRect(
          { x: visible.x + deltaX, y: visible.y + deltaY },
          viewport,
        );
      });
    },
    [viewport],
  );
  const activateKeyboardCursor = useCallback(() => {
    if (pending === null) {
      begin(keyboardCursor);
    } else {
      finish(keyboardCursor);
    }
  }, [begin, finish, keyboardCursor, pending]);

  const value: ScreenshotEditorValue = {
    source,
    operations: history.present,
    tool,
    color,
    lineWidth,
    text,
    outputMediaType,
    pending,
    keyboardCursor,
    canUndo: history.past.length > 0,
    canRedo: history.future.length > 0,
    status,
    approving,
    sourceUrl,
    sourceEffectAvailable,
    viewport,
    setTool,
    setColor,
    setLineWidth,
    setText,
    setOutputMediaType,
    undo,
    redo,
    approve,
    begin,
    move,
    finish,
    cancelGesture,
    moveKeyboardCursor,
    activateKeyboardCursor,
  };
  return (
    <ScreenshotEditorContext value={value}>{children}</ScreenshotEditorContext>
  );
}

const toolLabels: Record<EditorTool, string> = {
  [EditorTool.Crop]: "Crop",
  [EditorTool.Arrow]: "Arrow",
  [EditorTool.Rectangle]: "Rectangle",
  [EditorTool.Freehand]: "Freehand",
  [EditorTool.Text]: "Text",
  [EditorTool.Marker]: "Numbered marker",
  [EditorTool.Blur]: "Blur",
  [EditorTool.Pixelate]: "Pixelate",
};

export function ScreenshotEditorToolbar() {
  const {
    approving,
    canRedo,
    canUndo,
    redo,
    setTool,
    sourceEffectAvailable,
    tool,
    undo,
  } = useScreenshotEditor();
  return (
    <div aria-label="Screenshot editing tools" className="editor-toolbar" role="toolbar">
      {Object.values(EditorTool).map((candidate) => (
        <button
          aria-pressed={tool === candidate}
          className="editor-tool"
          disabled={
            approving ||
            (!sourceEffectAvailable &&
              (candidate === EditorTool.Blur || candidate === EditorTool.Pixelate))
          }
          key={candidate}
          onClick={() => setTool(candidate)}
          type="button"
        >
          {toolLabels[candidate]}
        </button>
      ))}
      <span aria-hidden="true" className="editor-toolbar-separator" />
      <button
        aria-keyshortcuts="Control+Z Meta+Z"
        disabled={approving || !canUndo}
        onClick={undo}
        type="button"
      >
        Undo
      </button>
      <button
        aria-keyshortcuts="Control+Shift+Z Meta+Shift+Z Control+Y"
        disabled={approving || !canRedo}
        onClick={redo}
        type="button"
      >
        Redo
      </button>
    </div>
  );
}

function pointFromPointer(
  event: PointerEvent<SVGSVGElement>,
  viewport: EditorRect,
): EditorPoint {
  const screenTransform = event.currentTarget.getScreenCTM?.();
  if (screenTransform !== undefined && screenTransform !== null) {
    const point = new DOMPoint(event.clientX, event.clientY).matrixTransform(
      screenTransform.inverse(),
    );
    return clampPointToRect(
      { x: Math.round(point.x), y: Math.round(point.y) },
      viewport,
    );
  }

  const rect = event.currentTarget.getBoundingClientRect();
  const scale = Math.min(
    rect.width / viewport.width,
    rect.height / viewport.height,
  );
  const renderedWidth = viewport.width * scale;
  const renderedHeight = viewport.height * scale;
  const left = rect.left + (rect.width - renderedWidth) / 2;
  const top = rect.top + (rect.height - renderedHeight) / 2;
  const x = renderedWidth > 0 ? (event.clientX - left) / renderedWidth : 0;
  const y = renderedHeight > 0 ? (event.clientY - top) / renderedHeight : 0;
  return {
    x: Math.max(
      viewport.x,
      Math.min(
        viewport.x + viewport.width - 1,
        viewport.x + Math.round(x * (viewport.width - 1)),
      ),
    ),
    y: Math.max(
      viewport.y,
      Math.min(
        viewport.y + viewport.height - 1,
        viewport.y + Math.round(y * (viewport.height - 1)),
      ),
    ),
  };
}

function operationDescription(operation: EditorOperation, index: number): string {
  switch (operation.kind) {
    case "crop":
      return `${index + 1}. Crop to ${operation.rect.width} by ${operation.rect.height}`;
    case "arrow":
      return `${index + 1}. Arrow`;
    case "rectangle":
      return `${index + 1}. Rectangle`;
    case "freehand":
      return `${index + 1}. Freehand line`;
    case "text":
      return `${index + 1}. Text: ${operation.text}`;
    case "marker":
      return `${index + 1}. Numbered marker ${operation.number}`;
    case "blur":
      return `${index + 1}. Blur region`;
    case "pixelate":
      return `${index + 1}. Pixelate region`;
  }
}

const fallbackEditorGlyph = [14, 17, 1, 2, 4, 0, 4] as const;

const editorGlyphs: Readonly<Record<string, readonly number[]>> = {
  "0": [14, 17, 19, 21, 25, 17, 14],
  "1": [4, 12, 4, 4, 4, 4, 14],
  "2": [14, 17, 1, 2, 4, 8, 31],
  "3": [30, 1, 1, 14, 1, 1, 30],
  "4": [2, 6, 10, 18, 31, 2, 2],
  "5": [31, 16, 16, 30, 1, 1, 30],
  "6": [14, 16, 16, 30, 17, 17, 14],
  "7": [31, 1, 2, 4, 8, 8, 8],
  "8": [14, 17, 17, 14, 17, 17, 14],
  "9": [14, 17, 17, 15, 1, 1, 14],
  A: [14, 17, 17, 31, 17, 17, 17],
  B: [30, 17, 17, 30, 17, 17, 30],
  C: [14, 17, 16, 16, 16, 17, 14],
  D: [30, 17, 17, 17, 17, 17, 30],
  E: [31, 16, 16, 30, 16, 16, 31],
  F: [31, 16, 16, 30, 16, 16, 16],
  G: [14, 16, 16, 23, 17, 17, 15],
  H: [17, 17, 17, 31, 17, 17, 17],
  I: [14, 4, 4, 4, 4, 4, 14],
  J: [7, 2, 2, 2, 18, 18, 12],
  K: [17, 18, 20, 24, 20, 18, 17],
  L: [16, 16, 16, 16, 16, 16, 31],
  M: [17, 27, 21, 21, 17, 17, 17],
  N: [17, 25, 21, 19, 17, 17, 17],
  O: [14, 17, 17, 17, 17, 17, 14],
  P: [30, 17, 17, 30, 16, 16, 16],
  Q: [14, 17, 17, 17, 21, 18, 13],
  R: [30, 17, 17, 30, 20, 18, 17],
  S: [15, 16, 16, 14, 1, 1, 30],
  T: [31, 4, 4, 4, 4, 4, 4],
  U: [17, 17, 17, 17, 17, 17, 14],
  V: [17, 17, 17, 17, 17, 10, 4],
  W: [17, 17, 17, 21, 21, 21, 10],
  X: [17, 17, 10, 4, 10, 17, 17],
  Y: [17, 17, 10, 4, 4, 4, 4],
  Z: [31, 1, 2, 4, 8, 16, 31],
  a: [0, 0, 14, 1, 15, 17, 15],
  b: [16, 16, 30, 17, 17, 17, 30],
  c: [0, 0, 14, 16, 16, 17, 14],
  d: [1, 1, 15, 17, 17, 17, 15],
  e: [0, 0, 14, 17, 31, 16, 14],
  f: [6, 8, 30, 8, 8, 8, 8],
  g: [0, 0, 15, 17, 15, 1, 14],
  h: [16, 16, 30, 17, 17, 17, 17],
  i: [4, 0, 12, 4, 4, 4, 14],
  j: [2, 0, 6, 2, 2, 18, 12],
  k: [16, 16, 18, 20, 24, 20, 18],
  l: [12, 4, 4, 4, 4, 4, 14],
  m: [0, 0, 26, 21, 21, 21, 21],
  n: [0, 0, 30, 17, 17, 17, 17],
  o: [0, 0, 14, 17, 17, 17, 14],
  p: [0, 0, 30, 17, 30, 16, 16],
  q: [0, 0, 15, 17, 15, 1, 1],
  r: [0, 0, 22, 25, 16, 16, 16],
  s: [0, 0, 15, 16, 14, 1, 30],
  t: [8, 8, 30, 8, 8, 9, 6],
  u: [0, 0, 17, 17, 17, 19, 13],
  v: [0, 0, 17, 17, 17, 10, 4],
  w: [0, 0, 17, 17, 21, 21, 10],
  x: [0, 0, 17, 10, 4, 10, 17],
  y: [0, 0, 17, 17, 15, 1, 14],
  z: [0, 0, 31, 2, 4, 8, 31],
  " ": [0, 0, 0, 0, 0, 0, 0],
  "-": [0, 0, 0, 31, 0, 0, 0],
  ".": [0, 0, 0, 0, 0, 12, 12],
  ":": [0, 12, 12, 0, 12, 12, 0],
  "!": [4, 4, 4, 4, 4, 0, 4],
  "?": fallbackEditorGlyph,
};

function bitmapTextPath(text: string): string {
  const pixels: string[] = [];
  let cursorX = 0;
  let cursorY = 0;
  for (const character of text) {
    if (character === "\n") {
      cursorX = 0;
      cursorY += 8;
      continue;
    }
    const glyph = editorGlyphs[character] ?? fallbackEditorGlyph;
    for (const [row, bits] of glyph.entries()) {
      for (let column = 0; column < 5; column += 1) {
        if ((bits & (1 << (4 - column))) !== 0) {
          pixels.push(`M${cursorX + column} ${cursorY + row}h1v1h-1z`);
        }
      }
    }
    cursorX += 6;
  }
  return pixels.join("");
}

const MAX_BLUR_PREVIEW_EDGE = 2_048;
const MAX_BLUR_PREVIEW_PIXELS = 4 * 1_024 * 1_024;

interface BlurPreviewTile {
  readonly height: number;
  readonly previewHeight: number;
  readonly previewRadius: number;
  readonly previewWidth: number;
  readonly width: number;
  readonly x: number;
  readonly y: number;
}

export function blurPreviewTiles(
  width: number,
  height: number,
  radius: number,
): readonly [BlurPreviewTile] {
  const previewScale = Math.min(
    1,
    MAX_BLUR_PREVIEW_EDGE / width,
    MAX_BLUR_PREVIEW_EDGE / height,
    Math.sqrt(MAX_BLUR_PREVIEW_PIXELS / (width * height)),
  );
  const previewWidth = Math.max(1, Math.floor(width * previewScale));
  const previewHeight = Math.max(1, Math.floor(height * previewScale));
  return [
    {
      height,
      previewHeight,
      previewRadius: Math.round(
        radius * Math.min(previewWidth / width, previewHeight / height),
      ),
      previewWidth,
      width,
      x: 0,
      y: 0,
    },
  ];
}

export function boxBlurPreviewPixels(
  pixels: Uint8ClampedArray,
  sourceWidth: number,
  sourceHeight: number,
  radius: number,
  output: EditorRect,
): Uint8ClampedArray {
  const horizontal = new Uint8ClampedArray(output.width * sourceHeight * 4);
  for (let y = 0; y < sourceHeight; y += 1) {
    let x = output.x;
    let left = Math.max(0, x - radius);
    let right = Math.min(sourceWidth - 1, x + radius);
    const totals = [0, 0, 0, 0];
    for (let sourceX = left; sourceX <= right; sourceX += 1) {
      const sourceOffset = (y * sourceWidth + sourceX) * 4;
      for (let channel = 0; channel < 4; channel += 1) {
        totals[channel] =
          (totals[channel] ?? 0) + (pixels[sourceOffset + channel] ?? 0);
      }
    }
    for (let outputX = 0; outputX < output.width; outputX += 1) {
      const count = right - left + 1;
      const targetOffset = (y * output.width + outputX) * 4;
      for (let channel = 0; channel < 4; channel += 1) {
        horizontal[targetOffset + channel] = Math.floor(
          (totals[channel] ?? 0) / count,
        );
      }
      x += 1;
      const nextLeft = Math.max(0, x - radius);
      const nextRight = Math.min(sourceWidth - 1, x + radius);
      while (left < nextLeft) {
        const sourceOffset = (y * sourceWidth + left) * 4;
        for (let channel = 0; channel < 4; channel += 1) {
          totals[channel] =
            (totals[channel] ?? 0) - (pixels[sourceOffset + channel] ?? 0);
        }
        left += 1;
      }
      while (right < nextRight) {
        right += 1;
        const sourceOffset = (y * sourceWidth + right) * 4;
        for (let channel = 0; channel < 4; channel += 1) {
          totals[channel] =
            (totals[channel] ?? 0) + (pixels[sourceOffset + channel] ?? 0);
        }
      }
    }
  }

  const blurred = new Uint8ClampedArray(output.width * output.height * 4);
  for (let x = 0; x < output.width; x += 1) {
    let y = output.y;
    let top = Math.max(0, y - radius);
    let bottom = Math.min(sourceHeight - 1, y + radius);
    const totals = [0, 0, 0, 0];
    for (let sourceY = top; sourceY <= bottom; sourceY += 1) {
      const sourceOffset = (sourceY * output.width + x) * 4;
      for (let channel = 0; channel < 4; channel += 1) {
        totals[channel] =
          (totals[channel] ?? 0) + (horizontal[sourceOffset + channel] ?? 0);
      }
    }
    for (let outputY = 0; outputY < output.height; outputY += 1) {
      const count = bottom - top + 1;
      const targetOffset = (outputY * output.width + x) * 4;
      for (let channel = 0; channel < 4; channel += 1) {
        blurred[targetOffset + channel] = Math.floor(
          (totals[channel] ?? 0) / count,
        );
      }
      y += 1;
      const nextTop = Math.max(0, y - radius);
      const nextBottom = Math.min(sourceHeight - 1, y + radius);
      while (top < nextTop) {
        const sourceOffset = (top * output.width + x) * 4;
        for (let channel = 0; channel < 4; channel += 1) {
          totals[channel] =
            (totals[channel] ?? 0) - (horizontal[sourceOffset + channel] ?? 0);
        }
        top += 1;
      }
      while (bottom < nextBottom) {
        bottom += 1;
        const sourceOffset = (bottom * output.width + x) * 4;
        for (let channel = 0; channel < 4; channel += 1) {
          totals[channel] =
            (totals[channel] ?? 0) + (horizontal[sourceOffset + channel] ?? 0);
        }
      }
    }
  }
  return blurred;
}

const MAX_PIXELATE_PREVIEW_TILE_EDGE = 1_024;
const MAX_PIXELATE_PREVIEW_EDGE = 2_048;
const MAX_PIXELATE_PREVIEW_PIXELS = 4 * 1_024 * 1_024;

interface PixelatePreviewTile {
  readonly columns: number;
  readonly height: number;
  readonly outputX: number;
  readonly outputY: number;
  readonly rows: number;
  readonly width: number;
  readonly x: number;
  readonly y: number;
}

interface PixelatePreviewSize {
  readonly capped: boolean;
  readonly columns: number;
  readonly height: number;
  readonly rows: number;
  readonly width: number;
}

export function pixelatePreviewSize(
  width: number,
  height: number,
  blockSize: number,
): PixelatePreviewSize {
  const columns = Math.ceil(width / blockSize);
  const rows = Math.ceil(height / blockSize);
  const previewScale = Math.min(
    1,
    MAX_PIXELATE_PREVIEW_EDGE / columns,
    MAX_PIXELATE_PREVIEW_EDGE / rows,
    Math.sqrt(MAX_PIXELATE_PREVIEW_PIXELS / (columns * rows)),
  );
  const previewWidth = Math.max(1, Math.floor(columns * previewScale));
  const previewHeight = Math.max(1, Math.floor(rows * previewScale));
  return {
    capped: previewWidth < columns || previewHeight < rows,
    columns,
    height: previewHeight,
    rows,
    width: previewWidth,
  };
}

export function pixelatePreviewTiles(
  width: number,
  height: number,
  blockSize: number,
): readonly PixelatePreviewTile[] {
  const tileSpan =
    Math.floor(MAX_PIXELATE_PREVIEW_TILE_EDGE / blockSize) * blockSize;
  const tiles: PixelatePreviewTile[] = [];
  for (let y = 0; y < height; y += tileSpan) {
    const tileHeight = Math.min(tileSpan, height - y);
    for (let x = 0; x < width; x += tileSpan) {
      const tileWidth = Math.min(tileSpan, width - x);
      tiles.push({
        columns: Math.ceil(tileWidth / blockSize),
        height: tileHeight,
        outputX: x / blockSize,
        outputY: y / blockSize,
        rows: Math.ceil(tileHeight / blockSize),
        width: tileWidth,
        x,
        y,
      });
    }
  }
  return tiles;
}

export function pixelatePreviewPixels(
  pixels: Uint8ClampedArray,
  width: number,
  height: number,
  blockSize: number,
) {
  const columns = Math.ceil(width / blockSize);
  const rows = Math.ceil(height / blockSize);
  const data = new Uint8ClampedArray(columns * rows * 4);

  for (let blockY = 0; blockY < height; blockY += blockSize) {
    const blockBottom = Math.min(blockY + blockSize, height);
    for (let blockX = 0; blockX < width; blockX += blockSize) {
      const blockRight = Math.min(blockX + blockSize, width);
      const totals: [number, number, number, number] = [0, 0, 0, 0];
      for (let y = blockY; y < blockBottom; y += 1) {
        for (let x = blockX; x < blockRight; x += 1) {
          const offset = (y * width + x) * 4;
          totals[0] += pixels[offset] ?? 0;
          totals[1] += pixels[offset + 1] ?? 0;
          totals[2] += pixels[offset + 2] ?? 0;
          totals[3] += pixels[offset + 3] ?? 0;
        }
      }
      const count = (blockRight - blockX) * (blockBottom - blockY);
      const outputOffset =
        ((blockY / blockSize) * columns + blockX / blockSize) * 4;
      data[outputOffset] = Math.floor(totals[0] / count);
      data[outputOffset + 1] = Math.floor(totals[1] / count);
      data[outputOffset + 2] = Math.floor(totals[2] / count);
      data[outputOffset + 3] = Math.floor(totals[3] / count);
    }
  }

  return { columns, data, rows };
}

function renderPixelatedPreview(
  preview: HTMLImageElement,
  canvas: HTMLCanvasElement,
  operation: Extract<EditorOperation, { readonly kind: "pixelate" }>,
  previewSize: PixelatePreviewSize,
) {
  const context = canvas.getContext("2d");
  if (context === null) return;
  context.clearRect(0, 0, canvas.width, canvas.height);
  if (previewSize.capped) {
    context.imageSmoothingEnabled = true;
    context.imageSmoothingQuality = "high";
    context.drawImage(
      preview,
      operation.rect.x,
      operation.rect.y,
      operation.rect.width,
      operation.rect.height,
      0,
      0,
      previewSize.width,
      previewSize.height,
    );
    return;
  }
  const sourceCanvas = document.createElement("canvas");
  const sourceContext = sourceCanvas.getContext("2d", { willReadFrequently: true });
  if (sourceContext === null) return;
  for (const tile of pixelatePreviewTiles(
    operation.rect.width,
    operation.rect.height,
    operation.blockSize,
  )) {
    sourceCanvas.width = tile.width;
    sourceCanvas.height = tile.height;
    sourceContext.drawImage(
      preview,
      operation.rect.x + tile.x,
      operation.rect.y + tile.y,
      tile.width,
      tile.height,
      0,
      0,
      tile.width,
      tile.height,
    );
    const sourcePixels = sourceContext.getImageData(
      0,
      0,
      tile.width,
      tile.height,
    );
    const pixelated = pixelatePreviewPixels(
      sourcePixels.data,
      tile.width,
      tile.height,
      operation.blockSize,
    );
    const imageData = context.createImageData(tile.columns, tile.rows);
    imageData.data.set(pixelated.data);
    context.putImageData(imageData, tile.outputX, tile.outputY);
  }
}

function arrowPreviewWings(
  operation: Extract<EditorOperation, { readonly kind: "arrow" }>,
  source: ComposerImage,
): readonly EditorPoint[] {
  const deltaX = operation.end.x - operation.start.x;
  const deltaY = operation.end.y - operation.start.y;
  const length = Math.hypot(deltaX, deltaY);
  const head = Math.min(
    Math.max(8, Math.min(48, operation.lineWidth * 4)),
    length * 0.6,
  );
  const angle = Math.atan2(deltaY, deltaX);
  return [2.55, -2.55].map((offset) => ({
    x: Math.max(
      0,
      Math.min(
        source.width - 1,
        Math.round(operation.end.x + head * Math.cos(angle + offset)),
      ),
    ),
    y: Math.max(
      0,
      Math.min(
        source.height - 1,
        Math.round(operation.end.y + head * Math.sin(angle + offset)),
      ),
    ),
  }));
}

function BlurredOverlay({
  operation,
  sourceUrl,
}: {
  readonly operation: Extract<EditorOperation, { readonly kind: "blur" }>;
  readonly sourceUrl: string;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const tile = useMemo(
    () =>
      blurPreviewTiles(
        operation.rect.width,
        operation.rect.height,
        operation.radius,
      )[0],
    [operation.radius, operation.rect.height, operation.rect.width],
  );

  useEffect(() => {
    const preview = new Image();
    preview.onload = () => {
      const canvas = canvasRef.current;
      if (canvas === null) return;
      const context = canvas.getContext("2d", {
        willReadFrequently: true,
      });
      if (context === null) return;
      context.clearRect(0, 0, canvas.width, canvas.height);
      context.imageSmoothingEnabled = true;
      context.imageSmoothingQuality = "high";
      context.drawImage(
        preview,
        operation.rect.x,
        operation.rect.y,
        operation.rect.width,
        operation.rect.height,
        0,
        0,
        tile.previewWidth,
        tile.previewHeight,
      );
      const sourcePixels = context.getImageData(
        0,
        0,
        tile.previewWidth,
        tile.previewHeight,
      );
      const blurred = boxBlurPreviewPixels(
        sourcePixels.data,
        tile.previewWidth,
        tile.previewHeight,
        tile.previewRadius,
        {
          x: 0,
          y: 0,
          width: tile.previewWidth,
          height: tile.previewHeight,
        },
      );
      const imageData = context.createImageData(
        tile.previewWidth,
        tile.previewHeight,
      );
      imageData.data.set(blurred);
      context.putImageData(imageData, 0, 0);
    };
    preview.src = sourceUrl;
    return () => {
      preview.onload = null;
    };
  }, [operation, sourceUrl, tile]);

  return (
    <foreignObject
      height={tile.height}
      width={tile.width}
      x={operation.rect.x}
      y={operation.rect.y}
    >
      <canvas
        aria-hidden="true"
        className="editor-blur"
        height={tile.previewHeight}
        ref={canvasRef}
        width={tile.previewWidth}
      />
    </foreignObject>
  );
}

function PixelatedOverlay({
  effectPrefix,
  index,
  operation,
  sourceUrl,
}: {
  readonly effectPrefix: string;
  readonly index: number;
  readonly operation: Extract<EditorOperation, { readonly kind: "pixelate" }>;
  readonly sourceUrl: string;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const previewSize = useMemo(
    () =>
      pixelatePreviewSize(
        operation.rect.width,
        operation.rect.height,
        operation.blockSize,
      ),
    [operation.blockSize, operation.rect.height, operation.rect.width],
  );
  const clipId = `${effectPrefix}-pixel-clip-${index}`;

  useEffect(() => {
    const preview = new Image();
    preview.onload = () => {
      const canvas = canvasRef.current;
      if (canvas !== null) {
        renderPixelatedPreview(preview, canvas, operation, previewSize);
      }
    };
    preview.src = sourceUrl;
    return () => {
      preview.onload = null;
    };
  }, [operation, previewSize, sourceUrl]);

  return (
    <>
      <defs>
        <clipPath id={clipId}>
          <rect
            height={operation.rect.height}
            width={operation.rect.width}
            x={operation.rect.x}
            y={operation.rect.y}
          />
        </clipPath>
      </defs>
      <foreignObject
        clipPath={`url(#${clipId})`}
        height={previewSize.rows * operation.blockSize}
        width={previewSize.columns * operation.blockSize}
        x={operation.rect.x}
        y={operation.rect.y}
      >
        <canvas
          aria-hidden="true"
          className="editor-pixelate"
          height={previewSize.height}
          ref={canvasRef}
          width={previewSize.width}
        />
      </foreignObject>
    </>
  );
}

function OperationOverlay({
  effectPrefix,
  index,
  operation,
  source,
  sourceUrl,
}: {
  readonly effectPrefix: string;
  readonly index: number;
  readonly operation: EditorOperation;
  readonly source: ComposerImage;
  readonly sourceUrl: string;
}) {
  switch (operation.kind) {
    case "crop":
      return (
        <rect
          className="editor-crop"
          fill="none"
          height={operation.rect.height}
          width={operation.rect.width}
          x={operation.rect.x}
          y={operation.rect.y}
        />
      );
    case "arrow": {
      const wings = arrowPreviewWings(operation, source);
      return (
        <g strokeLinecap="round" strokeLinejoin="round">
          <line
            stroke={operation.color}
            strokeWidth={operation.lineWidth}
            x1={operation.start.x}
            x2={operation.end.x}
            y1={operation.start.y}
            y2={operation.end.y}
          />
          {wings.map((wing, wingIndex) => (
            <line
              className="editor-arrow-head"
              key={wingIndex}
              stroke={operation.color}
              strokeWidth={operation.lineWidth}
              x1={operation.end.x}
              x2={wing.x}
              y1={operation.end.y}
              y2={wing.y}
            />
          ))}
        </g>
      );
    }
    case "rectangle": {
      const right = operation.rect.x + operation.rect.width - 1;
      const bottom = operation.rect.y + operation.rect.height - 1;
      return (
        <g
          className="editor-rectangle"
          stroke={operation.color}
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={operation.lineWidth}
        >
          <line
            x1={operation.rect.x}
            x2={right}
            y1={operation.rect.y}
            y2={operation.rect.y}
          />
          <line x1={right} x2={right} y1={operation.rect.y} y2={bottom} />
          <line
            x1={right}
            x2={operation.rect.x}
            y1={bottom}
            y2={bottom}
          />
          <line
            x1={operation.rect.x}
            x2={operation.rect.x}
            y1={bottom}
            y2={operation.rect.y}
          />
        </g>
      );
    }
    case "freehand":
      return (
        <polyline
          fill="none"
          points={operation.points.map((point) => `${point.x},${point.y}`).join(" ")}
          stroke={operation.color}
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={operation.lineWidth}
        />
      );
    case "text":
      return (
        <path
          className="editor-bitmap-text"
          d={bitmapTextPath(operation.text)}
          fill={operation.color}
          transform={`translate(${operation.origin.x} ${operation.origin.y}) scale(${Math.max(1, Math.floor(operation.fontSize / 7))})`}
        />
      );
    case "marker":
      {
        const label = operation.number.toString();
        const fontSize = Math.max(8, Math.floor(operation.size / 2));
        const scale = Math.max(1, Math.floor(fontSize / 7));
        const textWidth = label.length * 6 * scale;
        const origin = {
          x: Math.max(0, operation.center.x - Math.floor(textWidth / 2)),
          y: Math.max(0, operation.center.y - Math.floor(fontSize / 2)),
        };
        return (
          <g>
            <circle
              cx={operation.center.x}
              cy={operation.center.y}
              fill={operation.color}
              r={Math.floor(operation.size / 2)}
            />
            <path
              className="editor-marker-label"
              d={bitmapTextPath(label)}
              fill="#ffffff"
              transform={`translate(${origin.x} ${origin.y}) scale(${scale})`}
            />
          </g>
        );
      }
    case "blur":
      return (
        <BlurredOverlay operation={operation} sourceUrl={sourceUrl} />
      );
    case "pixelate":
      return (
        <PixelatedOverlay
          effectPrefix={effectPrefix}
          index={index}
          operation={operation}
          sourceUrl={sourceUrl}
        />
      );
  }
}

export function ScreenshotEditorCanvas() {
  const editor = useScreenshotEditor();
  const {
    begin,
    cancelGesture,
    finish,
    keyboardCursor,
    move,
    moveKeyboardCursor,
    operations,
    pending,
    redo,
    source,
    sourceUrl,
    tool,
    undo,
    activateKeyboardCursor,
    viewport,
    approving,
  } = editor;
  const pointerActive = useRef(false);
  const effectPrefix = useId().replaceAll(":", "");
  const titleId = `${effectPrefix}-title`;
  const instructionsId = `${effectPrefix}-instructions`;

  const onKeyDown = (event: KeyboardEvent<SVGSVGElement>) => {
    if (approving) return;
    const modifier = event.ctrlKey || event.metaKey;
    if (modifier && event.key.toLowerCase() === "z") {
      event.preventDefault();
      if (event.shiftKey) redo();
      else undo();
      return;
    }
    if (event.ctrlKey && event.key.toLowerCase() === "y") {
      event.preventDefault();
      redo();
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      cancelGesture();
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      activateKeyboardCursor();
      return;
    }
    const step = event.shiftKey ? 10 : 1;
    const deltas: Partial<Record<string, readonly [number, number]>> = {
      ArrowUp: [0, -step],
      ArrowRight: [step, 0],
      ArrowDown: [0, step],
      ArrowLeft: [-step, 0],
    };
    const delta = deltas[event.key];
    if (delta !== undefined) {
      event.preventDefault();
      moveKeyboardCursor(delta[0], delta[1]);
      if (pending !== null) {
        move(
          clampPointToRect(
            {
              x: keyboardCursor.x + delta[0],
              y: keyboardCursor.y + delta[1],
            },
            viewport,
          ),
        );
      }
    }
  };

  return (
    <section aria-labelledby={titleId} className="editor-canvas-section">
      <h2 className="visually-hidden" id={titleId}>
        Screenshot canvas
      </h2>
      <p className="editor-instructions" id={instructionsId}>
        Choose a tool, then drag on the image. With the canvas focused, use arrow
        keys to move the cursor, Enter to start or finish, and Escape to cancel.
      </p>
      <svg
        aria-describedby={instructionsId}
        aria-disabled={approving}
        aria-label={`Screenshot editor canvas. ${toolLabels[tool]} selected. Cursor at ${keyboardCursor.x}, ${keyboardCursor.y}.`}
        className="editor-canvas"
        onKeyDown={onKeyDown}
        onPointerCancel={() => {
          pointerActive.current = false;
          if (approving) return;
          cancelGesture();
        }}
        onPointerDown={(event) => {
          if (approving) return;
          pointerActive.current = true;
          event.currentTarget.setPointerCapture?.(event.pointerId);
          begin(pointFromPointer(event, viewport));
        }}
        onPointerMove={(event) => {
          if (!approving && pointerActive.current) {
            move(pointFromPointer(event, viewport));
          }
        }}
        onPointerUp={(event) => {
          if (!pointerActive.current) return;
          pointerActive.current = false;
          event.currentTarget.releasePointerCapture?.(event.pointerId);
          if (approving) return;
          finish(pointFromPointer(event, viewport));
        }}
        role="application"
        tabIndex={approving ? -1 : 0}
        viewBox={`${viewport.x} ${viewport.y} ${viewport.width} ${viewport.height}`}
      >
        <image
          height={source.height}
          href={sourceUrl}
          width={source.width}
          x="0"
          y="0"
        />
        {operations.map((operation, index) => (
          <OperationOverlay
            key={`${index}-${operation.kind}`}
            effectPrefix={effectPrefix}
            index={index}
            operation={operation}
            source={source}
            sourceUrl={sourceUrl}
          />
        ))}
        <circle
          aria-hidden="true"
          className="editor-keyboard-cursor"
          cx={keyboardCursor.x}
          cy={keyboardCursor.y}
          r={Math.max(3, source.width / 150)}
        />
      </svg>
      <ol aria-label="Applied edits" className="visually-hidden">
        {operations.map((operation, index) => (
          <li key={`${index}-${operation.kind}`}>
            {operationDescription(operation, index)}
          </li>
        ))}
      </ol>
    </section>
  );
}

export function ScreenshotEditorInspector() {
  const {
    approving,
    color,
    lineWidth,
    outputMediaType,
    setColor,
    setLineWidth,
    setOutputMediaType,
    setText,
    text,
    tool,
  } = useScreenshotEditor();
  return (
    <fieldset className="editor-inspector" disabled={approving}>
      <legend>Tool options</legend>
      <label>
        Color
        <input
          onChange={(event) => setColor(event.target.value)}
          type="color"
          value={color}
        />
      </label>
      <label>
        Line width
        <input
          max="20"
          min="1"
          onChange={(event) => setLineWidth(event.currentTarget.valueAsNumber)}
          type="range"
          value={lineWidth}
        />
        <output>{lineWidth} px</output>
      </label>
      {tool === EditorTool.Text ? (
        <label>
          Text
          <input
            maxLength={1_000}
            onChange={(event) => setText(normalizeEditorText(event.target.value))}
            type="text"
            value={text}
          />
        </label>
      ) : null}
      <label>
        Approved format
        <select
          onChange={(event) =>
            setOutputMediaType(event.target.value as ImageMediaType)
          }
          value={outputMediaType}
        >
          <option value={ImageMediaType.Png}>PNG</option>
          <option value={ImageMediaType.Webp}>WebP</option>
        </select>
      </label>
    </fieldset>
  );
}

export function ScreenshotEditorActions() {
  const { approve, approving, operations, status } = useScreenshotEditor();
  const cappedPixelatePreview = operations.some(
    (operation) =>
      operation.kind === "pixelate" &&
      pixelatePreviewSize(
        operation.rect.width,
        operation.rect.height,
        operation.blockSize,
      ).capped,
  );
  return (
    <div className="editor-actions">
      <p aria-atomic="true" className="editor-status" role="status">
        {status}
      </p>
      {cappedPixelatePreview ? (
        <p className="editor-preview-warning" role="note">
          This pixelate preview is scaled below the native block grid. The
          approved image may show more detail.
        </p>
      ) : null}
      <p className="muted">
        Source pixels stay in this local draft. Only the flattened image created
        after approval can become an upload payload.
      </p>
      <button
        className="primary-button"
        disabled={approving}
        onClick={() => void approve()}
        type="button"
      >
        {approving
          ? "Creating approved image…"
          : `Approve ${operations.length} edits`}
      </button>
    </div>
  );
}

export function ScreenshotEditorFrame({ children }: { readonly children: ReactNode }) {
  return <div className="screenshot-editor">{children}</div>;
}

export const ScreenshotEditor = {
  Provider: ScreenshotEditorProvider,
  Frame: ScreenshotEditorFrame,
  Toolbar: ScreenshotEditorToolbar,
  Canvas: ScreenshotEditorCanvas,
  Inspector: ScreenshotEditorInspector,
  Actions: ScreenshotEditorActions,
};
