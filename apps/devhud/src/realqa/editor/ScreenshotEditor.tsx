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
  const { imageId, sessionId } = props;
  return (
    <ScreenshotEditorStateProvider
      key={JSON.stringify([sessionId, imageId])}
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
  const [keyboardCursor, setKeyboardCursor] = useState<EditorPoint>({
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
        setStatus("That edit is outside the image or has no drawable size.");
        return;
      }
      dispatch({ type: "commit", operation });
      setStatus(`${tool} edit added.`);
    },
    [bounds, color, lineWidth, markerNumber, source, text, tool],
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
    setApproving(true);
    setStatus("Creating approved image.");
    try {
      const approved = await bridge.flattenImage({
        sessionId,
        imageId,
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
    bridge,
    history.present,
    imageId,
    onApprove,
    outputMediaType,
    sessionId,
  ]);
  const moveKeyboardCursor = useCallback(
    (deltaX: number, deltaY: number) => {
      setKeyboardCursor((current) => ({
        x: Math.max(0, Math.min(source.width - 1, current.x + deltaX)),
        y: Math.max(0, Math.min(source.height - 1, current.y + deltaY)),
      }));
    },
    [source.height, source.width],
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
    canRedo,
    canUndo,
    redo,
    setTool,
    tool,
    undo,
  } = useScreenshotEditor();
  return (
    <div aria-label="Screenshot editing tools" className="editor-toolbar" role="toolbar">
      {Object.values(EditorTool).map((candidate) => (
        <button
          aria-pressed={tool === candidate}
          className="editor-tool"
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
        disabled={!canUndo}
        onClick={undo}
        type="button"
      >
        Undo
      </button>
      <button
        aria-keyshortcuts="Control+Shift+Z Meta+Shift+Z Control+Y"
        disabled={!canRedo}
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
  source: ComposerImage,
): EditorPoint {
  const screenTransform = event.currentTarget.getScreenCTM?.();
  if (screenTransform !== undefined && screenTransform !== null) {
    const point = new DOMPoint(event.clientX, event.clientY).matrixTransform(
      screenTransform.inverse(),
    );
    return {
      x: Math.max(0, Math.min(source.width - 1, Math.round(point.x))),
      y: Math.max(0, Math.min(source.height - 1, Math.round(point.y))),
    };
  }

  const rect = event.currentTarget.getBoundingClientRect();
  const scale = Math.min(
    rect.width / source.width,
    rect.height / source.height,
  );
  const renderedWidth = source.width * scale;
  const renderedHeight = source.height * scale;
  const left = rect.left + (rect.width - renderedWidth) / 2;
  const top = rect.top + (rect.height - renderedHeight) / 2;
  const x = renderedWidth > 0 ? (event.clientX - left) / renderedWidth : 0;
  const y = renderedHeight > 0 ? (event.clientY - top) / renderedHeight : 0;
  return {
    x: Math.max(0, Math.min(source.width - 1, Math.round(x * (source.width - 1)))),
    y: Math.max(
      0,
      Math.min(source.height - 1, Math.round(y * (source.height - 1))),
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

function renderPixelatedPreview(
  preview: HTMLImageElement,
  canvas: HTMLCanvasElement,
  operation: Extract<EditorOperation, { readonly kind: "pixelate" }>,
) {
  const context = canvas.getContext("2d");
  if (context === null) return;
  context.imageSmoothingEnabled = true;
  context.imageSmoothingQuality = "high";
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.drawImage(
    preview,
    operation.rect.x,
    operation.rect.y,
    operation.rect.width,
    operation.rect.height,
    0,
    0,
    canvas.width,
    canvas.height,
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
  const columns = Math.ceil(operation.rect.width / operation.blockSize);
  const rows = Math.ceil(operation.rect.height / operation.blockSize);
  const clipId = `${effectPrefix}-pixel-clip-${index}`;

  useEffect(() => {
    const preview = new Image();
    preview.onload = () => {
      const canvas = canvasRef.current;
      if (canvas !== null) renderPixelatedPreview(preview, canvas, operation);
    };
    preview.src = sourceUrl;
    return () => {
      preview.onload = null;
    };
  }, [operation, sourceUrl]);

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
        height={rows * operation.blockSize}
        width={columns * operation.blockSize}
        x={operation.rect.x}
        y={operation.rect.y}
      >
        <canvas
          aria-hidden="true"
          className="editor-pixelate"
          height={rows}
          ref={canvasRef}
          width={columns}
        />
      </foreignObject>
    </>
  );
}

function OperationOverlay({
  effectPrefix,
  index,
  operation,
  sourceUrl,
}: {
  readonly effectPrefix: string;
  readonly index: number;
  readonly operation: EditorOperation;
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
    case "arrow":
      return (
        <line
          markerEnd={`url(#${effectPrefix}-arrow-head)`}
          stroke={operation.color}
          strokeWidth={operation.lineWidth}
          x1={operation.start.x}
          x2={operation.end.x}
          y1={operation.start.y}
          y2={operation.end.y}
        />
      );
    case "rectangle":
      return (
        <rect
          fill="none"
          height={operation.rect.height}
          stroke={operation.color}
          strokeWidth={operation.lineWidth}
          width={operation.rect.width}
          x={operation.rect.x}
          y={operation.rect.y}
        />
      );
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
        <text
          fill={operation.color}
          fontSize={operation.fontSize}
          x={operation.origin.x}
          y={operation.origin.y + operation.fontSize}
        >
          {operation.text}
        </text>
      );
    case "marker":
      return (
        <g>
          <circle
            cx={operation.center.x}
            cy={operation.center.y}
            fill={operation.color}
            r={operation.size / 2}
          />
          <text
            fill="#ffffff"
            fontSize={operation.size / 2}
            textAnchor="middle"
            x={operation.center.x}
            y={operation.center.y + operation.size / 6}
          >
            {operation.number}
          </text>
        </g>
      );
    case "blur":
      return (
        <g>
          <defs>
            <clipPath id={`${effectPrefix}-clip-${index}`}>
              <rect
                height={operation.rect.height}
                width={operation.rect.width}
                x={operation.rect.x}
                y={operation.rect.y}
              />
            </clipPath>
            <filter id={`${effectPrefix}-blur-${index}`}>
              <feGaussianBlur stdDeviation={operation.radius} />
            </filter>
          </defs>
          <image
            clipPath={`url(#${effectPrefix}-clip-${index})`}
            filter={`url(#${effectPrefix}-blur-${index})`}
            height="100%"
            href={sourceUrl}
            width="100%"
            x="0"
            y="0"
          />
        </g>
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
  } = editor;
  const pointerActive = useRef(false);
  const effectPrefix = useId().replaceAll(":", "");
  const titleId = `${effectPrefix}-title`;
  const instructionsId = `${effectPrefix}-instructions`;

  const onKeyDown = (event: KeyboardEvent<SVGSVGElement>) => {
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
      if (pending !== null) move({
        x: Math.max(0, Math.min(source.width - 1, keyboardCursor.x + delta[0])),
        y: Math.max(0, Math.min(source.height - 1, keyboardCursor.y + delta[1])),
      });
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
        aria-label={`Screenshot editor canvas. ${toolLabels[tool]} selected. Cursor at ${keyboardCursor.x}, ${keyboardCursor.y}.`}
        className="editor-canvas"
        onKeyDown={onKeyDown}
        onPointerCancel={() => {
          pointerActive.current = false;
          cancelGesture();
        }}
        onPointerDown={(event) => {
          pointerActive.current = true;
          event.currentTarget.setPointerCapture?.(event.pointerId);
          begin(pointFromPointer(event, source));
        }}
        onPointerMove={(event) => {
          if (pointerActive.current) move(pointFromPointer(event, source));
        }}
        onPointerUp={(event) => {
          if (!pointerActive.current) return;
          pointerActive.current = false;
          event.currentTarget.releasePointerCapture?.(event.pointerId);
          finish(pointFromPointer(event, source));
        }}
        role="application"
        tabIndex={0}
        viewBox={`0 0 ${source.width} ${source.height}`}
      >
        <defs>
          <marker
            id={`${effectPrefix}-arrow-head`}
            markerHeight="8"
            markerWidth="8"
            orient="auto"
            refX="6"
            refY="3"
          >
            <path d="M0,0 L0,6 L7,3 z" fill="context-stroke" />
          </marker>
        </defs>
        <image height="100%" href={sourceUrl} width="100%" x="0" y="0" />
        {operations.map((operation, index) => (
          <OperationOverlay
            key={`${index}-${operation.kind}`}
            effectPrefix={effectPrefix}
            index={index}
            operation={operation}
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
    <fieldset className="editor-inspector">
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
  return (
    <div className="editor-actions">
      <p aria-atomic="true" className="editor-status" role="status">
        {status}
      </p>
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
