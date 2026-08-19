import { createContext, use, useCallback, useEffect, useImperativeHandle, useRef, useState, type KeyboardEvent, type PointerEvent, type Ref } from "react";
import type { Copy } from "./localization";
import { NativeBridgeError, NativeBridgeErrorCode, type CaptureDisplay, type CaptureDraft, type CaptureDraftImage, type CaptureEditorCommand, type CaptureEditorLayer, type CaptureOptions, type CapturePoint, type CaptureRect, type FlattenedCaptureImage, type NativeBridgeV1 } from "./native-bridge";
import { ShortcutActionId } from "./shortcuts";

export type CaptureActionId = Exclude<ShortcutActionId, typeof ShortcutActionId.CommandPalette>;
type EditorTool = "crop" | "arrow" | "rectangle" | "drawing" | "text" | "blur" | "redaction";
type CaptureRequest = { readonly action: CaptureActionId; readonly sequence: number };
type FloatingPreviewRequest = { readonly draft: CaptureDraft; readonly imageId?: string; readonly sequence: number };
const ANNOTATION_FONT_FAMILY = "DevHud RealQA Noto Sans KR";
const MAX_ANNOTATION_TEXT_CHARACTERS = 2_048;

export interface RealqaController { execute(action: CaptureActionId): Promise<void> }

interface RealqaContextValue {
  readonly state: {
    readonly drafts: readonly CaptureDraft[];
    readonly unreadableDraftIds: readonly string[];
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
    readonly removeUnreadable: (draftId: string) => Promise<void>;
    readonly updateDraft: (draft: CaptureDraft) => void;
    readonly runDraftOperation: <Result>(draftId: string, operation: (draft: CaptureDraft) => Promise<Result>) => Promise<Result>;
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

export function RealqaSurface({ ref, bridge, copy, requestedAction, onRequestedActionConsumed }: { readonly ref?: Ref<RealqaController>; readonly bridge: NativeBridgeV1; readonly copy: Copy; readonly requestedAction?: CaptureRequest | null; readonly onRequestedActionConsumed?: (sequence: number) => void }) {
  const [drafts, setDrafts] = useState<readonly CaptureDraft[]>([]);
  const [unreadableDraftIds, setUnreadableDraftIds] = useState<readonly string[]>([]);
  const [selected, setSelected] = useState<CaptureDraft | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [previewRequest, setPreviewRequest] = useState<FloatingPreviewRequest | null>(null);
  const [captureDialog, setCaptureDialog] = useState<CaptureActionId | null>(null);
  const [captureStatus, setCaptureStatus] = useState<{ topology: readonly CaptureDisplay[]; shadowRemovalSupported: boolean } | null>(null);
  const [options, setOptions] = useState<CaptureOptions>({ delaySeconds: 0, includePointer: false, removeShadow: false });
  const lastRequested = useRef<number | null>(null);
  const captureInFlight = useRef(false);
  const captureStatusInFlight = useRef(false);
  const captureStatusRequest = useRef(0);
  const captureDialogOpener = useRef<HTMLElement | null>(null);
  const previewSequence = useRef(0);
  const draftsById = useRef(new Map<string, CaptureDraft>());
  const draftOperationQueues = useRef(new Map<string, Promise<void>>());
  const preview = previewRequest?.draft ?? null;
  const previewImage = previewRequest?.draft.images.find((image) => image.id === previewRequest.imageId) ?? previewRequest?.draft.images[0] ?? null;

  const replaceDrafts = useCallback((next: readonly CaptureDraft[]) => {
    draftsById.current = new Map(next.map((draft) => [draft.id, draft]));
    setDrafts(next);
  }, []);

  const installDraft = useCallback((draft: CaptureDraft) => {
    draftsById.current.set(draft.id, draft);
    setDrafts((current) => current.some((item) => item.id === draft.id)
      ? current.map((item) => item.id === draft.id ? draft : item)
      : [draft, ...current]);
    setUnreadableDraftIds((current) => current.filter((id) => id !== draft.id));
    setSelected((current) => current?.id === draft.id ? draft : current);
    setPreviewRequest((current) => current?.draft.id === draft.id ? { ...current, draft } : current);
  }, []);

  const runDraftOperation = useCallback(<Result,>(draftId: string, operation: (draft: CaptureDraft) => Promise<Result>) => {
    const preceding = draftOperationQueues.current.get(draftId) ?? Promise.resolve();
    const result = preceding.then(() => {
      const current = draftsById.current.get(draftId);
      if (!current) throw new Error("RealQA draft is unavailable");
      return operation(current);
    });
    const continuation = result.then(() => undefined, () => undefined);
    draftOperationQueues.current.set(draftId, continuation);
    void continuation.then(() => {
      if (draftOperationQueues.current.get(draftId) === continuation) draftOperationQueues.current.delete(draftId);
    });
    return result;
  }, []);

  const refresh = useCallback(async () => {
    const response = await bridge.request({ operation: "capture.list-drafts" });
    if (response.kind === "capture-drafts") {
      replaceDrafts(response.drafts);
      setUnreadableDraftIds(response.unreadableDraftIds);
    }
  }, [bridge, replaceDrafts]);

  const refreshCaptureStatus = useCallback(async () => {
    const request = ++captureStatusRequest.current;
    const response = await bridge.request({ operation: "capture.status" });
    if (response.kind !== "capture-status") return null;
    const next = { topology: response.topology, shadowRemovalSupported: response.shadowRemovalSupported };
    if (request === captureStatusRequest.current) setCaptureStatus(next);
    return next;
  }, [bridge]);

  useEffect(() => {
    void Promise.allSettled([refreshCaptureStatus(), bridge.request({ operation: "capture.list-drafts" })]).then(([native, stored]) => {
      if (native.status === "rejected") {
        setError(errorCopy(copy, native.reason));
      }
      if (stored.status === "fulfilled" && stored.value.kind === "capture-drafts") {
        replaceDrafts(stored.value.drafts);
        setUnreadableDraftIds(stored.value.unreadableDraftIds);
      } else if (stored.status === "rejected") {
        setError(errorCopy(copy, stored.reason));
      }
    });
  }, [bridge, copy, refreshCaptureStatus, replaceDrafts]);

  const dismissCaptureDialog = useCallback((clearStatus = true) => {
    setCaptureDialog(null);
    if (clearStatus) setStatus("");
    const opener = captureDialogOpener.current;
    captureDialogOpener.current = null;
    if (opener) requestAnimationFrame(() => { if (opener.isConnected) opener.focus(); });
  }, []);

  const completeCapture = useCallback(async (action: CaptureActionId, captureOptions: CaptureOptions = options) => {
    if (captureInFlight.current) return;
    captureInFlight.current = true;
    const originatingDraftId = selected?.id ?? null;
    setBusy(true); setError(null); setStatus(copy.captureSaving);
    try {
      const requestCapture = (appendToDraftId?: string) => bridge.request({ operation: "capture.start", actionId: action, options: { ...captureOptions, appendToDraftId } });
      const captureResult = originatingDraftId
        ? await runDraftOperation(originatingDraftId, async (current) => {
          const appended = await requestCapture(current.id);
          if (appended.kind === "capture-draft") installDraft(appended.draft);
          return {
            response: appended,
            previewImageId: appended.kind === "capture-draft" ? appended.draft.images[current.images.length]?.id : undefined,
          };
        })
        : await requestCapture().then((response) => ({
          response,
          previewImageId: response.kind === "capture-draft" ? response.draft.images[0]?.id : undefined,
        }));
      const { response, previewImageId } = captureResult;
      if (response.kind !== "capture-draft") return;
      if (!originatingDraftId) installDraft(response.draft);
      setSelected((current) => (current?.id ?? null) === originatingDraftId ? response.draft : current);
      setPreviewRequest({ draft: response.draft, imageId: previewImageId, sequence: ++previewSequence.current });
      setStatus(copy.captureSaved);
      dismissCaptureDialog(false);
      try { await refresh(); } catch { /* The capture response is already authoritative. */ }
    } catch (reason) {
      setStatus("");
      setError(errorCopy(copy, reason));
    } finally {
      captureInFlight.current = false;
      setBusy(false);
    }
  }, [bridge, copy, dismissCaptureDialog, installDraft, options, refresh, runDraftOperation, selected?.id]);

  const capture = useCallback(async (action: CaptureActionId) => {
    if (captureInFlight.current || captureStatusInFlight.current) return;
    if (action === ShortcutActionId.CaptureSelection || action === ShortcutActionId.CaptureToolbar) {
      captureStatusInFlight.current = true;
      const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setError(null);
      try {
        const freshStatus = await refreshCaptureStatus();
        if (!freshStatus) return;
        if (!captureDialog) captureDialogOpener.current = opener;
        setCaptureDialog(action);
      } catch (reason) {
        setError(errorCopy(copy, reason));
      } finally {
        captureStatusInFlight.current = false;
      }
      return;
    }
    await completeCapture(action);
  }, [captureDialog, completeCapture, copy, refreshCaptureStatus]);
  const cancelCapture = useCallback(() => {
    dismissCaptureDialog();
    if (busy) void bridge.request({ operation: "capture.cancel" }).catch(() => {});
  }, [bridge, busy, dismissCaptureDialog]);

  useImperativeHandle(ref, () => ({ execute: capture }), [capture]);
  useEffect(() => {
    if (!requestedAction || requestedAction.sequence === lastRequested.current) return;
    lastRequested.current = requestedAction.sequence;
    onRequestedActionConsumed?.(requestedAction.sequence);
    void capture(requestedAction.action);
  }, [capture, onRequestedActionConsumed, requestedAction]);
  useEffect(() => {
    if (!previewRequest) return;
    const timer = window.setTimeout(() => setPreviewRequest(null), 5_000);
    return () => window.clearTimeout(timer);
  }, [previewRequest?.sequence]);
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

  const deleteDraft = async (draftId: string) => {
    setError(null);
    try {
      await bridge.request({ operation: "capture.delete-draft", draftId });
      setDrafts((current) => current.filter((draft) => draft.id !== draftId));
      draftsById.current.delete(draftId);
      setUnreadableDraftIds((current) => current.filter((id) => id !== draftId));
      setSelected((current) => current?.id === draftId ? null : current);
      setPreviewRequest((current) => current?.draft.id === draftId ? null : current);
      await refresh();
    } catch {
      setError(copy.realqaDeleteFailed);
    }
  };
  const remove = async (draft: CaptureDraft) => {
    if (!confirm(copy.realqaDeleteConfirm)) return;
    await deleteDraft(draft.id);
  };
  const removeUnreadable = async (draftId: string) => {
    if (!confirm(copy.realqaDeleteConfirm)) return;
    await deleteDraft(draftId);
  };
  const value: RealqaContextValue = {
    state: { drafts, unreadableDraftIds, selected, busy, status, error, preview },
    actions: { capture, open: setSelected, close: () => setSelected(null), remove, removeUnreadable, updateDraft: installDraft, runDraftOperation, refresh },
    meta: { copy, bridge },
  };

  return <RealqaContext value={value}>
    <p className="eyebrow">{copy.realqa}</p>
    <h2>{copy.realqaTitle}</h2>
    <p>{copy.realqaQuotaHint}</p>
    <CaptureActions />
    {status && <p role="status" aria-live="polite">{status}</p>}
    {error && <p role="alert" className="native-setting-error">{error}</p>}
    {captureDialog && <CaptureDialog key={captureDialog} action={captureDialog} status={captureStatus} options={options} onOptions={setOptions} onCapture={completeCapture} onClose={cancelCapture} />}
    {selected ? <CaptureEditor key={selected.id} draft={selected} /> : <DraftList />}
    {preview && previewImage && !captureDialog && <aside className="floating-capture-preview" aria-label={copy.floatingPreview}>
      <img src={previewImage.previewUrl} alt="" />
      <button onClick={() => { setSelected(preview); setPreviewRequest(null); }}>{copy.floatingPreviewOpen}</button>
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
  const { state: { drafts, unreadableDraftIds }, actions, meta: { copy } } = useRealqa();
  return <section aria-labelledby="realqa-drafts-title"><h3 id="realqa-drafts-title">{copy.realqaDrafts}</h3>
    {drafts.length === 0 && unreadableDraftIds.length === 0 ? <p>{copy.realqaNoDrafts}</p> : <ul className="draft-list">{drafts.map((draft) => <li key={draft.id}>
      <button className="draft-preview" onClick={() => actions.open(draft)}><img src={draft.images[0]?.previewUrl} alt="" /><span>{draft.imageCount} {copy.realqaImages}</span></button>
      <time dateTime={new Date(draft.expiresAt * 1000).toISOString()}>{copy.realqaDraftExpiry}: {new Date(draft.expiresAt * 1000).toLocaleString()}</time>
      <div className="actions"><button onClick={() => actions.open(draft)}>{copy.realqaOpenEditor}</button><button className="danger" onClick={() => void actions.remove(draft)}>{copy.realqaDeleteDraft}</button></div>
    </li>)}{unreadableDraftIds.map((draftId) => <li key={draftId}>
      <p role="status">{copy.realqaUnreadableDraft}</p>
      <div className="actions"><button className="danger" onClick={() => void actions.removeUnreadable(draftId)}>{copy.realqaDeleteDraft}</button></div>
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
    const target = event.target instanceof Element ? event.target : null;
    const interactive = target?.closest("button,input,select,textarea,a,[contenteditable='true']");
    if (event.key === " " && !interactive) { event.preventDefault(); setMode((current) => current === "region" ? "window" : "region"); }
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
    <fieldset><legend>{copy.captureMode}</legend><label className="check"><input name="capture-mode" type="radio" checked={mode === "region"} onChange={() => setMode("region")} />{copy.captureRegionMode}</label><label className="check"><input name="capture-mode" type="radio" checked={mode === "window"} onChange={() => setMode("window")} />{copy.captureWindowMode}</label>{action === ShortcutActionId.CaptureToolbar && <><label className="check"><input name="capture-mode" type="radio" checked={mode === "display"} onChange={() => setMode("display")} />{copy.captureDisplay}</label><label className="check"><input name="capture-mode" type="radio" checked={mode === "active-window"} onChange={() => setMode("active-window")} />{copy.captureWindow}</label><label className="check"><input name="capture-mode" type="radio" checked={mode === "all-displays"} onChange={() => setMode("all-displays")} />{copy.captureAll}</label></>}</fieldset>
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
  const { state: { busy }, actions, meta: { bridge, copy } } = useRealqa();
  const [imageId, setImageId] = useState(draft.images[0]?.id ?? "");
  const [tool, setTool] = useState<EditorTool>("arrow");
  const [color, setColor] = useState("#ef4444");
  const [strokeWidth, setStrokeWidth] = useState(4);
  const [text, setText] = useState("");
  const [drawing, setDrawing] = useState<readonly CapturePoint[]>([]);
  const [coordinates, setCoordinates] = useState<CaptureRect>({ x: 0, y: 0, width: 100, height: 100 });
  const [message, setMessage] = useState("");
  const [failed, setFailed] = useState(false);
  const editorActive = useRef(true);
  const active = draft.images.find((image) => image.id === imageId) ?? draft.images[0];
  useEffect(() => () => { editorActive.current = false; }, []);
  useEffect(() => { if (!active && draft.images[0]) setImageId(draft.images[0].id); }, [active, draft.images]);
  useEffect(() => { if (busy) setDrawing([]); }, [busy]);

  const enqueueRevisionOperation = (operation: (current: CaptureDraft) => Promise<void>) => {
    if (busy) return Promise.resolve();
    return actions.runDraftOperation(draft.id, async (current) => {
      if (!editorActive.current) return;
      await operation(current);
    });
  };
  const installDraft = (next: CaptureDraft) => {
    actions.updateDraft(next);
  };
  const mutate = (command: CaptureEditorCommand) => {
    setFailed(false);
    return enqueueRevisionOperation(async (current) => {
      try {
        const response = await bridge.request({ operation: "capture.editor.apply", draftId: current.id, expectedRevision: current.revision, command });
        if (response.kind === "capture-draft") {
          installDraft(response.draft);
          if (editorActive.current) setMessage(copy.editorSaved);
        }
      } catch { if (editorActive.current) { setFailed(true); setMessage(copy.editorSaveFailed); } }
    });
  };
  const history = (operation: "capture.editor.undo" | "capture.editor.redo") => {
    setFailed(false);
    return enqueueRevisionOperation(async (current) => {
      try {
        const response = await bridge.request({ operation, draftId: current.id, expectedRevision: current.revision });
        if (response.kind === "capture-draft") installDraft(response.draft);
      } catch { if (editorActive.current) { setFailed(true); setMessage(copy.editorSaveFailed); } }
    });
  };
  const flatten = () => {
    setFailed(false);
    return enqueueRevisionOperation(async (current) => {
      try {
        const response = await bridge.request({ operation: "capture.flatten", draftId: current.id, expectedRevision: current.revision });
        if (!editorActive.current) return;
        if (response.kind === "capture-flattened") setMessage(response.images.some((image: FlattenedCaptureImage) => image.downscaled) ? copy.editorDownscaled : copy.editorFlattened);
      } catch { if (editorActive.current) { setFailed(true); setMessage(copy.editorSaveFailed); } }
    });
  };
  if (!active) return null;
  const point = (event: PointerEvent<HTMLElement>): CapturePoint => {
    const bounds = event.currentTarget.getBoundingClientRect();
    const x = (event.clientX - bounds.left) * active.width / bounds.width;
    const y = (event.clientY - bounds.top) * active.height / bounds.height;
    return { x: Math.min(active.width, Math.max(0, x)), y: Math.min(active.height, Math.max(0, y)) };
  };
  const pointerDown = (event: PointerEvent<HTMLElement>) => {
    if (busy) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    setDrawing([point(event)]);
  };
  const pointerMove = (event: PointerEvent<HTMLElement>) => {
    if (busy || !drawing.length) return;
    const nextPoint = point(event);
    setDrawing((current) => current.length >= 8191 ? current : [...current, nextPoint]);
  };
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
    if (busy || !drawing.length) return;
    const points = [...drawing, point(event)]; setDrawing([]);
    commit(points[0], points[points.length - 1], points);
  };
  const addFromCoordinates = () => {
    if (busy) return;
    const start = { x: coordinates.x, y: coordinates.y };
    const end = { x: coordinates.x + coordinates.width, y: coordinates.y + coordinates.height };
    commit(start, end);
  };
  const moveImage = (index: number, offset: number) => {
    if (busy) return;
    const target = index + offset;
    if (target >= 0 && target < draft.images.length) void mutate({ kind: "move-image", imageId: draft.images[index].id, toIndex: target });
  };
  return <section className="capture-editor" aria-labelledby="capture-editor-title">
    <div className="editor-heading"><div><h3 id="capture-editor-title">{copy.editorTitle}</h3><p>{copy.editorCloseHint}</p></div><button onClick={actions.close}>{copy.close}</button></div>
    <div className="editor-image-order" aria-label={copy.realqaImages}>{draft.images.map((image, index) => <div key={image.id}><button aria-pressed={image.id === active.id} onClick={() => setImageId(image.id)}>{copy.editorImage} {index + 1}</button><button disabled={busy || index === 0} aria-label={copy.editorMoveEarlier} onClick={() => moveImage(index, -1)}>←</button><button disabled={busy || index === draft.images.length - 1} aria-label={copy.editorMoveLater} onClick={() => moveImage(index, 1)}>→</button><button disabled={busy || draft.images.length === 1} aria-label={copy.editorRemove} onClick={() => void mutate({ kind: "remove-image", imageId: image.id })}>×</button></div>)}</div>
    <div className="editor-layout"><div className="editor-workspace"><div className="editor-canvas" role="img" aria-label={copy.editorCanvas} onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp}>
      <img draggable={false} src={active.previewUrl} alt="" />
      <AnnotationOverlay image={active} drawing={drawing} tool={tool} />
    </div></div>
    <aside className="editor-controls" aria-label={copy.editorTools}><div className="editor-tools">{(["crop","arrow","rectangle","drawing","text","blur","redaction"] as const).map((candidate) => <button key={candidate} aria-pressed={tool === candidate} onClick={() => setTool(candidate)}>{copy[candidate === "crop" ? "editorCrop" : candidate === "arrow" ? "editorArrow" : candidate === "rectangle" ? "editorRectangle" : candidate === "drawing" ? "editorDrawing" : candidate === "text" ? "editorText" : candidate === "blur" ? "editorBlur" : "editorRedaction"]}</button>)}</div>
      <label>{copy.editorColor}<input type="color" value={color} onChange={(event) => setColor(event.target.value)} /></label><label>{copy.editorStrokeWidth}<input type="range" min="1" max="32" value={strokeWidth} onChange={(event) => setStrokeWidth(Number(event.target.value))} /></label>{tool === "text" && <label>{copy.editorTextValue}<input autoFocus value={text} onChange={(event) => setText(limitAnnotationText(event.target.value))} /></label>}
      <fieldset className="editor-coordinate-fields"><legend>{copy.editorCoordinates}</legend>{(["x", "y", "width", "height"] as const).map((key) => <label key={key}>{copy[key === "x" ? "captureX" : key === "y" ? "captureY" : key === "width" ? "captureWidth" : "captureHeight"]}<input type="number" min={key === "width" || key === "height" ? 1 : undefined} value={coordinates[key]} onChange={(event) => setCoordinates((current) => ({ ...current, [key]: Number(event.target.value) }))} /></label>)}<button disabled={busy || (tool === "text" && !text)} onClick={addFromCoordinates}>{copy.editorAdd}</button></fieldset>
      <div className="actions"><button disabled={busy || !draft.canUndo} onClick={() => void history("capture.editor.undo")}>{copy.editorUndo}</button><button disabled={busy || !draft.canRedo} onClick={() => void history("capture.editor.redo")}>{copy.editorRedo}</button></div>
      <LayerList image={active} mutate={mutate} copy={copy} disabled={busy} />
      <button className="primary" disabled={busy} onClick={() => void flatten()}>{copy.editorFlatten}</button>
      {message && <p role={failed ? "alert" : "status"}>{message}</p>}
    </aside></div>
  </section>;
}

function AnnotationOverlay({ image, drawing, tool }: { readonly image: CaptureDraftImage; readonly drawing: readonly CapturePoint[]; readonly tool: EditorTool }) {
  const stateIds = image.layers.map((_, index) => `realqa-layer-state-${image.id}-${index + 1}`);
  const sourceStateId = `realqa-layer-state-${image.id}-0`;
  const precedingStateId = (index: number) => index === 0 ? sourceStateId : stateIds[index - 1];
  return <svg className="annotation-overlay" viewBox={`0 0 ${image.width} ${image.height}`} aria-hidden="true">
    <defs>
      {image.layers.filter((layer) => layer.tool === "blur").map((layer) => <BlurDefinitions key={layer.id} layer={layer} />)}
      <g id={sourceStateId}><image href={image.previewUrl} x="0" y="0" width={image.width} height={image.height} preserveAspectRatio="none" /></g>
      {image.layers.map((layer, index) => <g key={layer.id} id={stateIds[index]}>
        <use href={`#${precedingStateId(index)}`} />
        <LayerShape layer={layer} blurSourceId={precedingStateId(index)} showBlurOutline={false} />
      </g>)}
    </defs>
    {image.crop && <rect className="crop-outline" {...svgRect(image.crop)} />}
    {image.layers.map((layer, index) => <LayerShape key={layer.id} layer={layer} blurSourceId={precedingStateId(index)} showBlurOutline />)}
    {drawing.length > 1 && <polyline points={drawing.map((point) => `${point.x},${point.y}`).join(" ")} fill="none" stroke={tool === "redaction" ? "#000" : "#ef4444"} strokeWidth="4" />}
  </svg>;
}

function BlurDefinitions({ layer }: { readonly layer: Extract<CaptureEditorLayer, { tool: "blur" }> }) {
  const clipId = `realqa-blur-clip-${layer.id}`;
  const filterId = `realqa-blur-filter-${layer.id}`;
  return <>
    <clipPath id={clipId}><rect {...svgRect(layer.bounds)} /></clipPath>
    <filter id={filterId} x={layer.bounds.x} y={layer.bounds.y} width={layer.bounds.width} height={layer.bounds.height} filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
      <feGaussianBlur in="SourceGraphic" stdDeviation={layer.radius} />
    </filter>
  </>;
}

function LayerShape({ layer, blurSourceId, showBlurOutline }: { readonly layer: CaptureEditorLayer; readonly blurSourceId: string; readonly showBlurOutline: boolean }) {
  if (layer.tool === "arrow") {
    const [firstHead, secondHead] = arrowHeadPoints(layer.start, layer.end, layer.width);
    const stroke = { stroke: layer.color, strokeWidth: layer.width, strokeLinecap: "round" as const };
    return <g>
      <line x1={layer.start.x} y1={layer.start.y} x2={layer.end.x} y2={layer.end.y} {...stroke} />
      <line x1={layer.end.x} y1={layer.end.y} x2={firstHead.x} y2={firstHead.y} {...stroke} />
      <line x1={layer.end.x} y1={layer.end.y} x2={secondHead.x} y2={secondHead.y} {...stroke} />
    </g>;
  }
  if (layer.tool === "rectangle") return <rect {...svgRect(layer.bounds)} fill="none" stroke={layer.color} strokeWidth={layer.width} />;
  if (layer.tool === "drawing") return <polyline points={layer.points.map((point) => `${point.x},${point.y}`).join(" ")} fill="none" stroke={layer.color} strokeWidth={layer.width} />;
  if (layer.tool === "text") return <text x={layer.origin.x} y={layer.origin.y} fill={layer.color} fontSize={layer.size} style={{ fontFamily: ANNOTATION_FONT_FAMILY, fontKerning: "none" }}>{layer.text}</text>;
  if (layer.tool === "redaction") return <rect {...svgRect(layer.bounds)} fill="#000" />;
  const clipId = `realqa-blur-clip-${layer.id}`;
  const filterId = `realqa-blur-filter-${layer.id}`;
  return <>
    <use className={showBlurOutline ? "blur-preview" : undefined} href={`#${blurSourceId}`} clipPath={`url(#${clipId})`} filter={`url(#${filterId})`} />
    {showBlurOutline && <rect {...svgRect(layer.bounds)} className="blur-outline" />}
  </>;
}

function arrowHeadPoints(start: CapturePoint, end: CapturePoint, width: number): readonly [CapturePoint, CapturePoint] {
  const angle = Math.atan2(end.y - start.y, end.x - start.x);
  const length = Math.max(width * 4, 12);
  const point = (offset: number): CapturePoint => ({
    x: end.x - length * Math.cos(angle + offset),
    y: end.y - length * Math.sin(angle + offset),
  });
  return [point(-0.65), point(0.65)];
}

function LayerList({ image, mutate, copy, disabled }: { readonly image: CaptureDraftImage; readonly mutate: (command: CaptureEditorCommand) => Promise<void>; readonly copy: Copy; readonly disabled: boolean }) {
  return <section aria-labelledby="editor-layers-title"><h4 id="editor-layers-title">{copy.editorLayers}</h4>{image.layers.length === 0 ? <p>{copy.editorNoLayers}</p> : <ol className="layer-list">{image.layers.map((layer, index) => <li key={layer.id}><span>{copy[layer.tool === "arrow" ? "editorArrow" : layer.tool === "rectangle" ? "editorRectangle" : layer.tool === "drawing" ? "editorDrawing" : layer.tool === "text" ? "editorText" : layer.tool === "blur" ? "editorBlur" : "editorRedaction"]}</span><button disabled={disabled || index === 0} aria-label={copy.editorMoveEarlier} onClick={() => void mutate({ kind: "move-layer", imageId: image.id, layerId: layer.id, toIndex: index - 1 })}>↑</button><button disabled={disabled || index === image.layers.length - 1} aria-label={copy.editorMoveLater} onClick={() => void mutate({ kind: "move-layer", imageId: image.id, layerId: layer.id, toIndex: index + 1 })}>↓</button><button disabled={disabled} aria-label={copy.editorRemove} onClick={() => void mutate({ kind: "remove-layer", imageId: image.id, layerId: layer.id })}>×</button></li>)}</ol>}</section>;
}

function normalizeBounds(start: CapturePoint, end: CapturePoint): CaptureRect { return { x: Math.min(start.x, end.x), y: Math.min(start.y, end.y), width: Math.max(1, Math.abs(end.x - start.x)), height: Math.max(1, Math.abs(end.y - start.y)) }; }
function svgRect(bounds: CaptureRect) { return { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height }; }
function limitAnnotationText(value: string) { return Array.from(value).slice(0, MAX_ANNOTATION_TEXT_CHARACTERS).join(""); }

function uuidV7() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const timestamp = BigInt(Date.now());
  for (let index = 0; index < 6; index += 1) bytes[5 - index] = Number((timestamp >> BigInt(index * 8)) & 0xffn);
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`;
}
