import { createContext, use, useCallback, useEffect, useImperativeHandle, useRef, useState, type KeyboardEvent, type PointerEvent, type Ref } from "react";
import type { Copy } from "./localization";
import { NativeBridgeError, NativeBridgeErrorCode, type CaptureDisplay, type CaptureDraft, type CaptureDraftImage, type CaptureEditorCommand, type CaptureEditorLayer, type CaptureOptions, type CapturePoint, type CaptureRect, type FlattenedCaptureImage, type NativeBridgeV1 } from "./native-bridge";
import { ShortcutActionId } from "./shortcuts";

export type CaptureActionId = Exclude<ShortcutActionId, typeof ShortcutActionId.CommandPalette>;
type EditorTool = "crop" | "arrow" | "rectangle" | "drawing" | "text" | "blur" | "redaction";
type CaptureRequest = { readonly action: CaptureActionId; readonly sequence: number };

export interface RealqaController { execute(action: CaptureActionId): Promise<void> }

interface RealqaContextValue {
  readonly state: {
    readonly drafts: readonly CaptureDraft[];
    readonly selected: CaptureDraft | null;
    readonly busy: boolean;
    readonly status: string;
    readonly error: string | null;
    readonly preview: CaptureDraft | null;
  };
  readonly actions: {
    readonly capture: (action: CaptureActionId) => Promise<void>;
    readonly open: (draft: CaptureDraft) => void;
    readonly close: () => void;
    readonly remove: (draft: CaptureDraft) => Promise<void>;
    readonly updateDraft: (draft: CaptureDraft) => void;
    readonly refresh: () => Promise<void>;
  };
  readonly meta: { readonly copy: Copy; readonly bridge: NativeBridgeV1 };
}

const RealqaContext = createContext<RealqaContextValue | null>(null);

function useRealqa() {
  const value = use(RealqaContext);
  if (!value) throw new Error("RealQA context is unavailable");
  return value;
}

function errorCopy(copy: Copy, reason: unknown) {
  if (reason instanceof NativeBridgeError) {
    if (reason.code === NativeBridgeErrorCode.ProtectedContent) return copy.captureProtected;
    if (reason.code === NativeBridgeErrorCode.TopologyChanged) return copy.captureTopologyChanged;
    if (reason.code === NativeBridgeErrorCode.QuotaExhausted) return copy.captureQuotaFull;
    if (reason.code === NativeBridgeErrorCode.ImageLimit) return copy.captureImageLimit;
    if (reason.code === NativeBridgeErrorCode.PermissionDenied) return copy.capturePermission;
    if (reason.code === NativeBridgeErrorCode.Cancelled) return null;
  }
  return copy.captureFailed;
}

export function RealqaSurface({ ref, bridge, copy, requestedAction }: { readonly ref?: Ref<RealqaController>; readonly bridge: NativeBridgeV1; readonly copy: Copy; readonly requestedAction?: CaptureRequest | null }) {
  const [drafts, setDrafts] = useState<readonly CaptureDraft[]>([]);
  const [selected, setSelected] = useState<CaptureDraft | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [preview, setPreview] = useState<CaptureDraft | null>(null);
  const [captureDialog, setCaptureDialog] = useState<CaptureActionId | null>(null);
  const [captureStatus, setCaptureStatus] = useState<{ topology: readonly CaptureDisplay[]; shadowRemovalSupported: boolean } | null>(null);
  const [options, setOptions] = useState<CaptureOptions>({ delaySeconds: 0, includePointer: false, removeShadow: false });
  const lastRequested = useRef<number | null>(null);

  const refresh = useCallback(async () => {
    const response = await bridge.request({ operation: "capture.list-drafts" });
    if (response.kind === "capture-drafts") setDrafts(response.drafts);
  }, [bridge]);

  useEffect(() => {
    void Promise.all([bridge.request({ operation: "capture.status" }), bridge.request({ operation: "capture.list-drafts" })]).then(([native, stored]) => {
      if (native.kind === "capture-status") setCaptureStatus({ topology: native.topology, shadowRemovalSupported: native.shadowRemovalSupported });
      if (stored.kind === "capture-drafts") setDrafts(stored.drafts);
    }).catch((reason) => setError(errorCopy(copy, reason)));
  }, [bridge, copy]);

  const completeCapture = useCallback(async (action: CaptureActionId, captureOptions: CaptureOptions = options) => {
    setBusy(true); setError(null); setStatus(copy.captureSaving);
    try {
      const response = await bridge.request({ operation: "capture.start", actionId: action, options: { ...captureOptions, appendToDraftId: selected?.id } });
      if (response.kind !== "capture-draft") return;
      setSelected(response.draft);
      setPreview(response.draft);
      setStatus(copy.captureSaved);
      setCaptureDialog(null);
      await refresh();
    } catch (reason) {
      setStatus("");
      setError(errorCopy(copy, reason));
    } finally {
      setBusy(false);
    }
  }, [bridge, copy, options, refresh, selected?.id]);

  const capture = useCallback(async (action: CaptureActionId) => {
    if (action === ShortcutActionId.CaptureSelection || action === ShortcutActionId.CaptureToolbar) {
      setCaptureDialog(action);
      return;
    }
    await completeCapture(action);
  }, [completeCapture]);
  const cancelCapture = useCallback(() => {
    setCaptureDialog(null);
    setStatus("");
    if (busy) void bridge.request({ operation: "capture.cancel" }).catch(() => {});
  }, [bridge, busy]);

  useImperativeHandle(ref, () => ({ execute: capture }), [capture]);
  useEffect(() => {
    if (!requestedAction || requestedAction.sequence === lastRequested.current) return;
    lastRequested.current = requestedAction.sequence;
    void capture(requestedAction.action);
  }, [capture, requestedAction]);
  useEffect(() => {
    if (!preview) return;
    const timer = window.setTimeout(() => setPreview(null), 5_000);
    return () => window.clearTimeout(timer);
  }, [preview]);
  useEffect(() => {
    const key = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") {
        if (busy || captureDialog) cancelCapture();
        else if (selected) setSelected(null);
      }
    };
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [busy, cancelCapture, captureDialog, selected]);

  const remove = async (draft: CaptureDraft) => {
    if (!confirm(copy.realqaDeleteConfirm)) return;
    await bridge.request({ operation: "capture.delete-draft", draftId: draft.id });
    if (selected?.id === draft.id) setSelected(null);
    await refresh();
  };
  const updateDraft = (draft: CaptureDraft) => {
    setSelected(draft);
    setDrafts((current) => current.map((item) => item.id === draft.id ? draft : item));
  };
  const value: RealqaContextValue = {
    state: { drafts, selected, busy, status, error, preview },
    actions: { capture, open: setSelected, close: () => setSelected(null), remove, updateDraft, refresh },
    meta: { copy, bridge },
  };

  return <RealqaContext value={value}>
    <p className="eyebrow">{copy.realqa}</p>
    <h2>{copy.realqaTitle}</h2>
    <p>{copy.realqaQuotaHint}</p>
    <CaptureActions />
    {status && <p role="status" aria-live="polite">{status}</p>}
    {error && <p role="alert" className="native-setting-error">{error}</p>}
    {captureDialog && <CaptureDialog action={captureDialog} status={captureStatus} options={options} onOptions={setOptions} onCapture={completeCapture} onClose={cancelCapture} />}
    {selected ? <CaptureEditor draft={selected} /> : <DraftList />}
    {preview && <aside className="floating-capture-preview" aria-label={copy.floatingPreview}>
      <img src={preview.images[0]?.previewUrl} alt="" />
      <button onClick={() => { setSelected(preview); setPreview(null); }}>{copy.floatingPreviewOpen}</button>
    </aside>}
  </RealqaContext>;
}

function CaptureActions() {
  const { state, actions, meta: { copy } } = useRealqa();
  const items: readonly [CaptureActionId, keyof Copy][] = [
    [ShortcutActionId.CaptureDisplay, "captureDisplay"], [ShortcutActionId.CaptureActiveWindow, "captureWindow"],
    [ShortcutActionId.CaptureAllDisplays, "captureAll"], [ShortcutActionId.CaptureSelection, "captureSelection"], [ShortcutActionId.CaptureToolbar, "captureToolbar"],
  ];
  return <div className="capture-actions" aria-label={copy.captureMode}>{items.map(([action, label]) => <button key={action} disabled={state.busy} onClick={() => void actions.capture(action)}>{copy[label]}</button>)}</div>;
}

function DraftList() {
  const { state: { drafts }, actions, meta: { copy } } = useRealqa();
  return <section aria-labelledby="realqa-drafts-title"><h3 id="realqa-drafts-title">{copy.realqaDrafts}</h3>
    {drafts.length === 0 ? <p>{copy.realqaNoDrafts}</p> : <ul className="draft-list">{drafts.map((draft) => <li key={draft.id}>
      <button className="draft-preview" onClick={() => actions.open(draft)}><img src={draft.images[0]?.previewUrl} alt="" /><span>{draft.imageCount} {copy.realqaImages}</span></button>
      <time dateTime={new Date(draft.expiresAt * 1000).toISOString()}>{copy.realqaDraftExpiry}: {new Date(draft.expiresAt * 1000).toLocaleString()}</time>
      <div className="actions"><button onClick={() => actions.open(draft)}>{copy.realqaOpenEditor}</button><button className="danger" onClick={() => void actions.remove(draft)}>{copy.realqaDeleteDraft}</button></div>
    </li>)}</ul>}
  </section>;
}

function CaptureDialog({ action, status, options, onOptions, onCapture, onClose }: { readonly action: CaptureActionId; readonly status: { topology: readonly CaptureDisplay[]; shadowRemovalSupported: boolean } | null; readonly options: CaptureOptions; readonly onOptions: (options: CaptureOptions) => void; readonly onCapture: (action: CaptureActionId, options: CaptureOptions) => Promise<void>; readonly onClose: () => void }) {
  const { meta: { copy }, state: { busy } } = useRealqa();
  const first = status?.topology[0];
  const [mode, setMode] = useState<"region" | "window" | "display" | "active-window" | "all-displays">("region");
  const [optionShadow, setOptionShadow] = useState(false);
  const [rect, setRect] = useState<CaptureRect>(() => ({ x: first?.logicalBounds.x ?? 0, y: first?.logicalBounds.y ?? 0, width: Math.min(640, first?.logicalBounds.width ?? 640), height: Math.min(480, first?.logicalBounds.height ?? 480) }));
  const dialog = useRef<HTMLElement>(null);
  const updateRect = (key: keyof CaptureRect, value: string) => setRect((current) => ({ ...current, [key]: Number(value) }));
  const submitAction = mode === "display" ? ShortcutActionId.CaptureDisplay : mode === "active-window" ? ShortcutActionId.CaptureActiveWindow : mode === "all-displays" ? ShortcutActionId.CaptureAllDisplays : action;
  const submit = () => void onCapture(submitAction, { ...options, removeShadow: options.removeShadow || optionShadow, selection: rect, selectionWindow: mode === "window" });
  const keyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Alt" && status?.shadowRemovalSupported) setOptionShadow(true);
    if (event.key === " ") { event.preventDefault(); setMode((current) => current === "region" ? "window" : "region"); }
    if (event.key === "Escape") { event.stopPropagation(); onClose(); }
    if (event.key !== "Tab") return;
    const focusable = dialog.current?.querySelectorAll<HTMLElement>("button:not([disabled]),input:not([disabled]),select:not([disabled])");
    if (!focusable?.length) return;
    const firstItem = focusable[0], lastItem = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === firstItem) { event.preventDefault(); lastItem.focus(); }
    if (!event.shiftKey && document.activeElement === lastItem) { event.preventDefault(); firstItem.focus(); }
  };
  return <div className="overlay" role="presentation"><section ref={dialog} className="capture-dialog" role="dialog" aria-modal="true" aria-labelledby="capture-dialog-title" onKeyDown={keyDown} onKeyUp={(event) => { if (event.key === "Alt") setOptionShadow(false); }}>
    <h3 id="capture-dialog-title">{action === ShortcutActionId.CaptureToolbar ? copy.captureToolbar : copy.captureSelection}</h3>
    <p>{copy.captureRegionHelp}</p>
    <fieldset><legend>{copy.captureMode}</legend><label className="check"><input type="radio" checked={mode === "region"} onChange={() => setMode("region")} />{copy.captureRegionMode}</label><label className="check"><input type="radio" checked={mode === "window"} onChange={() => setMode("window")} />{copy.captureWindowMode}</label>{action === ShortcutActionId.CaptureToolbar && <><label className="check"><input type="radio" checked={mode === "display"} onChange={() => setMode("display")} />{copy.captureDisplay}</label><label className="check"><input type="radio" checked={mode === "active-window"} onChange={() => setMode("active-window")} />{copy.captureWindow}</label><label className="check"><input type="radio" checked={mode === "all-displays"} onChange={() => setMode("all-displays")} />{copy.captureAll}</label></>}</fieldset>
    {mode === "region" && <><RegionPicker displays={status?.topology ?? []} value={rect} onChange={setRect} label={copy.captureRegionPicker} /><div className="capture-coordinates">{(["x", "y", "width", "height"] as const).map((key) => <label key={key}>{copy[key === "x" ? "captureX" : key === "y" ? "captureY" : key === "width" ? "captureWidth" : "captureHeight"]}<input type="number" value={rect[key]} onChange={(event) => updateRect(key, event.target.value)} /></label>)}</div></>}
    <label>{copy.captureTimer}<select value={options.delaySeconds ?? 0} onChange={(event) => onOptions({ ...options, delaySeconds: Number(event.target.value) as 0 | 5 | 10 })}><option value="0">{copy.captureTimerOff}</option><option value="5">{copy.captureTimerFive}</option><option value="10">{copy.captureTimerTen}</option></select></label>
    <label className="check"><input type="checkbox" checked={options.includePointer ?? false} onChange={(event) => onOptions({ ...options, includePointer: event.target.checked })} />{copy.capturePointer}</label>
    {status?.shadowRemovalSupported && <label className="check"><input type="checkbox" checked={(options.removeShadow ?? false) || optionShadow} onChange={(event) => onOptions({ ...options, removeShadow: event.target.checked })} />{copy.captureShadow}</label>}
    <div className="actions"><button autoFocus className="primary" disabled={busy} onClick={submit}>{copy.captureNow}</button><button onClick={onClose}>{copy.captureCancel}</button></div>
  </section></div>;
}

function RegionPicker({ displays, value, onChange, label }: { readonly displays: readonly CaptureDisplay[]; readonly value: CaptureRect; readonly onChange: (value: CaptureRect) => void; readonly label: string }) {
  const dragStart = useRef<CapturePoint | null>(null);
  if (displays.length === 0) return null;
  const left = Math.min(...displays.map((display) => display.logicalBounds.x));
  const top = Math.min(...displays.map((display) => display.logicalBounds.y));
  const right = Math.max(...displays.map((display) => display.logicalBounds.x + display.logicalBounds.width));
  const bottom = Math.max(...displays.map((display) => display.logicalBounds.y + display.logicalBounds.height));
  const width = Math.max(1, right - left);
  const height = Math.max(1, bottom - top);
  const point = (event: PointerEvent<HTMLElement>): CapturePoint => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return {
      x: left + Math.min(1, Math.max(0, (event.clientX - bounds.left) / bounds.width)) * width,
      y: top + Math.min(1, Math.max(0, (event.clientY - bounds.top) / bounds.height)) * height,
    };
  };
  const update = (event: PointerEvent<HTMLElement>) => {
    if (!dragStart.current) return;
    onChange(normalizeBounds(dragStart.current, point(event)));
  };
  return <div
    className="region-picker"
    role="group"
    aria-label={label}
    onPointerDown={(event) => {
      event.currentTarget.setPointerCapture(event.pointerId);
      const start = point(event);
      dragStart.current = start;
      onChange({ ...start, width: 1, height: 1 });
    }}
    onPointerMove={update}
    onPointerUp={(event) => { update(event); dragStart.current = null; }}
    onPointerCancel={() => { dragStart.current = null; }}
    style={{ aspectRatio: `${width} / ${height}` }}
  >
    {displays.map((display) => <span key={display.id} className="region-display" style={{
      left: `${(display.logicalBounds.x - left) / width * 100}%`,
      top: `${(display.logicalBounds.y - top) / height * 100}%`,
      width: `${display.logicalBounds.width / width * 100}%`,
      height: `${display.logicalBounds.height / height * 100}%`,
    }}>{display.name}</span>)}
    <span className="region-selection" style={{
      left: `${(value.x - left) / width * 100}%`,
      top: `${(value.y - top) / height * 100}%`,
      width: `${value.width / width * 100}%`,
      height: `${value.height / height * 100}%`,
    }} />
  </div>;
}

function CaptureEditor({ draft }: { readonly draft: CaptureDraft }) {
  const { actions, meta: { bridge, copy } } = useRealqa();
  const [imageId, setImageId] = useState(draft.images[0]?.id ?? "");
  const [tool, setTool] = useState<EditorTool>("arrow");
  const [color, setColor] = useState("#ef4444");
  const [strokeWidth, setStrokeWidth] = useState(4);
  const [text, setText] = useState("");
  const [drawing, setDrawing] = useState<readonly CapturePoint[]>([]);
  const [coordinates, setCoordinates] = useState<CaptureRect>({ x: 0, y: 0, width: 100, height: 100 });
  const [message, setMessage] = useState("");
  const [failed, setFailed] = useState(false);
  const active = draft.images.find((image) => image.id === imageId) ?? draft.images[0];
  useEffect(() => { if (!active && draft.images[0]) setImageId(draft.images[0].id); }, [active, draft.images]);

  const mutate = async (command: CaptureEditorCommand) => {
    setFailed(false);
    try {
      const response = await bridge.request({ operation: "capture.editor.apply", draftId: draft.id, expectedRevision: draft.revision, command });
      if (response.kind === "capture-draft") { actions.updateDraft(response.draft); setMessage(copy.editorSaved); }
    } catch { setFailed(true); setMessage(copy.editorSaveFailed); }
  };
  const history = async (operation: "capture.editor.undo" | "capture.editor.redo") => {
    const response = await bridge.request({ operation, draftId: draft.id, expectedRevision: draft.revision });
    if (response.kind === "capture-draft") actions.updateDraft(response.draft);
  };
  const flatten = async () => {
    try {
      const response = await bridge.request({ operation: "capture.flatten", draftId: draft.id, expectedRevision: draft.revision });
      if (response.kind === "capture-flattened") setMessage(response.images.some((image: FlattenedCaptureImage) => image.downscaled) ? copy.editorDownscaled : copy.editorFlattened);
    } catch { setFailed(true); setMessage(copy.editorSaveFailed); }
  };
  if (!active) return null;
  const point = (event: PointerEvent<HTMLElement>): CapturePoint => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return { x: (event.clientX - bounds.left) * active.width / bounds.width, y: (event.clientY - bounds.top) * active.height / bounds.height };
  };
  const pointerDown = (event: PointerEvent<HTMLElement>) => { event.currentTarget.setPointerCapture(event.pointerId); setDrawing([point(event)]); };
  const pointerMove = (event: PointerEvent<HTMLElement>) => { if (drawing.length) setDrawing((current) => current.length >= 8191 ? current : [...current, point(event)]); };
  const commit = (start: CapturePoint, end: CapturePoint, points: readonly CapturePoint[] = [start, end]) => {
    const bounds = normalizeBounds(start, end);
    const id = uuidV7();
    if (tool === "crop") void mutate({ kind: "set-crop", imageId: active.id, crop: bounds });
    else if (tool === "arrow") void mutate({ kind: "add-layer", imageId: active.id, layer: { tool, id, start, end, color, width: strokeWidth } });
    else if (tool === "rectangle") void mutate({ kind: "add-layer", imageId: active.id, layer: { tool, id, bounds, color, width: strokeWidth } });
    else if (tool === "drawing") void mutate({ kind: "add-layer", imageId: active.id, layer: { tool, id, points, color, width: strokeWidth } });
    else if (tool === "text" && text) void mutate({ kind: "add-layer", imageId: active.id, layer: { tool, id, origin: start, text, color, size: Math.max(12, strokeWidth * 5) } });
    else if (tool === "blur") void mutate({ kind: "add-layer", imageId: active.id, layer: { tool, id, bounds, radius: 12 } });
    else if (tool === "redaction") void mutate({ kind: "add-layer", imageId: active.id, layer: { tool, id, bounds } });
  };
  const pointerUp = (event: PointerEvent<HTMLElement>) => {
    if (!drawing.length) return;
    const points = [...drawing, point(event)]; setDrawing([]);
    commit(points[0], points[points.length - 1], points);
  };
  const addFromCoordinates = () => {
    const start = { x: coordinates.x, y: coordinates.y };
    const end = { x: coordinates.x + coordinates.width, y: coordinates.y + coordinates.height };
    commit(start, end);
  };
  const moveImage = (index: number, offset: number) => {
    const target = index + offset;
    if (target >= 0 && target < draft.images.length) void mutate({ kind: "move-image", imageId: draft.images[index].id, toIndex: target });
  };
  return <section className="capture-editor" aria-labelledby="capture-editor-title">
    <div className="editor-heading"><div><h3 id="capture-editor-title">{copy.editorTitle}</h3><p>{copy.editorCloseHint}</p></div><button onClick={actions.close}>{copy.close}</button></div>
    <div className="editor-image-order" aria-label={copy.realqaImages}>{draft.images.map((image, index) => <div key={image.id}><button aria-pressed={image.id === active.id} onClick={() => setImageId(image.id)}>{copy.editorImage} {index + 1}</button><button disabled={index === 0} aria-label={copy.editorMoveEarlier} onClick={() => moveImage(index, -1)}>←</button><button disabled={index === draft.images.length - 1} aria-label={copy.editorMoveLater} onClick={() => moveImage(index, 1)}>→</button><button aria-label={copy.editorRemove} onClick={() => void mutate({ kind: "remove-image", imageId: image.id })}>×</button></div>)}</div>
    <div className="editor-layout"><div className="editor-workspace"><div className="editor-canvas" role="img" aria-label={copy.editorCanvas} onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp}>
      <img draggable={false} src={active.previewUrl} alt="" />
      <AnnotationOverlay image={active} drawing={drawing} tool={tool} />
    </div></div>
    <aside className="editor-controls" aria-label={copy.editorTools}><div className="editor-tools">{(["crop","arrow","rectangle","drawing","text","blur","redaction"] as const).map((candidate) => <button key={candidate} aria-pressed={tool === candidate} onClick={() => setTool(candidate)}>{copy[candidate === "crop" ? "editorCrop" : candidate === "arrow" ? "editorArrow" : candidate === "rectangle" ? "editorRectangle" : candidate === "drawing" ? "editorDrawing" : candidate === "text" ? "editorText" : candidate === "blur" ? "editorBlur" : "editorRedaction"]}</button>)}</div>
      <label>{copy.editorColor}<input type="color" value={color} onChange={(event) => setColor(event.target.value)} /></label><label>{copy.editorStrokeWidth}<input type="range" min="1" max="32" value={strokeWidth} onChange={(event) => setStrokeWidth(Number(event.target.value))} /></label>{tool === "text" && <label>{copy.editorTextValue}<input autoFocus value={text} onChange={(event) => setText(event.target.value)} /></label>}
      <fieldset className="editor-coordinate-fields"><legend>{copy.editorCoordinates}</legend>{(["x", "y", "width", "height"] as const).map((key) => <label key={key}>{copy[key === "x" ? "captureX" : key === "y" ? "captureY" : key === "width" ? "captureWidth" : "captureHeight"]}<input type="number" min={key === "width" || key === "height" ? 1 : undefined} value={coordinates[key]} onChange={(event) => setCoordinates((current) => ({ ...current, [key]: Number(event.target.value) }))} /></label>)}<button disabled={tool === "text" && !text} onClick={addFromCoordinates}>{copy.editorAdd}</button></fieldset>
      <div className="actions"><button disabled={!draft.canUndo} onClick={() => void history("capture.editor.undo")}>{copy.editorUndo}</button><button disabled={!draft.canRedo} onClick={() => void history("capture.editor.redo")}>{copy.editorRedo}</button></div>
      <LayerList image={active} mutate={mutate} copy={copy} />
      <button className="primary" onClick={() => void flatten()}>{copy.editorFlatten}</button>
      {message && <p role={failed ? "alert" : "status"}>{message}</p>}
    </aside></div>
  </section>;
}

function AnnotationOverlay({ image, drawing, tool }: { readonly image: CaptureDraftImage; readonly drawing: readonly CapturePoint[]; readonly tool: EditorTool }) {
  return <svg className="annotation-overlay" viewBox={`0 0 ${image.width} ${image.height}`} aria-hidden="true">{image.crop && <rect className="crop-outline" {...svgRect(image.crop)} />}{image.layers.map((layer) => <LayerShape key={layer.id} layer={layer} />)}{drawing.length > 1 && <polyline points={drawing.map((point) => `${point.x},${point.y}`).join(" ")} fill="none" stroke={tool === "redaction" ? "#000" : "#ef4444"} strokeWidth="4" />}</svg>;
}

function LayerShape({ layer }: { readonly layer: CaptureEditorLayer }) {
  if (layer.tool === "arrow") return <line x1={layer.start.x} y1={layer.start.y} x2={layer.end.x} y2={layer.end.y} stroke={layer.color} strokeWidth={layer.width} />;
  if (layer.tool === "rectangle") return <rect {...svgRect(layer.bounds)} fill="none" stroke={layer.color} strokeWidth={layer.width} />;
  if (layer.tool === "drawing") return <polyline points={layer.points.map((point) => `${point.x},${point.y}`).join(" ")} fill="none" stroke={layer.color} strokeWidth={layer.width} />;
  if (layer.tool === "text") return <text x={layer.origin.x} y={layer.origin.y} fill={layer.color} fontSize={layer.size}>{layer.text}</text>;
  if (layer.tool === "redaction") return <rect {...svgRect(layer.bounds)} fill="#000" />;
  return <rect {...svgRect(layer.bounds)} className="blur-outline" />;
}

function LayerList({ image, mutate, copy }: { readonly image: CaptureDraftImage; readonly mutate: (command: CaptureEditorCommand) => Promise<void>; readonly copy: Copy }) {
  return <section aria-labelledby="editor-layers-title"><h4 id="editor-layers-title">{copy.editorLayers}</h4>{image.layers.length === 0 ? <p>{copy.editorNoLayers}</p> : <ol className="layer-list">{image.layers.map((layer, index) => <li key={layer.id}><span>{copy[layer.tool === "arrow" ? "editorArrow" : layer.tool === "rectangle" ? "editorRectangle" : layer.tool === "drawing" ? "editorDrawing" : layer.tool === "text" ? "editorText" : layer.tool === "blur" ? "editorBlur" : "editorRedaction"]}</span><button disabled={index === 0} aria-label={copy.editorMoveEarlier} onClick={() => void mutate({ kind: "move-layer", imageId: image.id, layerId: layer.id, toIndex: index - 1 })}>↑</button><button disabled={index === image.layers.length - 1} aria-label={copy.editorMoveLater} onClick={() => void mutate({ kind: "move-layer", imageId: image.id, layerId: layer.id, toIndex: index + 1 })}>↓</button><button aria-label={copy.editorRemove} onClick={() => void mutate({ kind: "remove-layer", imageId: image.id, layerId: layer.id })}>×</button></li>)}</ol>}</section>;
}

function normalizeBounds(start: CapturePoint, end: CapturePoint): CaptureRect { return { x: Math.min(start.x, end.x), y: Math.min(start.y, end.y), width: Math.max(1, Math.abs(end.x - start.x)), height: Math.max(1, Math.abs(end.y - start.y)) }; }
function svgRect(bounds: CaptureRect) { return { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height }; }

function uuidV7() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const timestamp = BigInt(Date.now());
  for (let index = 0; index < 6; index += 1) bytes[5 - index] = Number((timestamp >> BigInt(index * 8)) & 0xffn);
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`;
}
