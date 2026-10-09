// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useId, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { AttachmentService, clientFailure, type ImageAttachment } from "@delinoio/delidev-api-client";
import { copy, useLocale, type MessageKey } from "./localization";
import { imageDigest, imageLimits, imageMime, ImageInputError, ImageProblem, retainedImages } from "./image-input";
import type { useImageDraft } from "./image-drafts";
import "./image-attachments.css";

const problemKeys: Record<ImageProblem, MessageKey> = { [ImageProblem.Invalid]: "image-input.invalid", [ImageProblem.Limits]: "image-input.limits", [ImageProblem.Unsupported]: "image-input.unsupported", [ImageProblem.Transfer]: "image-input.transferFailed", [ImageProblem.Cleanup]: "image-input.cleanupFailed" };
export function imageEntryHandlers(draft: ReturnType<typeof useImageDraft>, disabled: boolean) {
  return {
    onPaste: (event: ClipboardEvent<HTMLElement>) => { const files = [...event.clipboardData.files]; if (!files.length) return; event.preventDefault(); if (!disabled) void draft.controller.add(files); },
    onDragOver: (event: DragEvent<HTMLElement>) => { if ([...event.dataTransfer.types].includes("Files")) event.preventDefault(); },
    onDrop: (event: DragEvent<HTMLElement>) => { const files = [...event.dataTransfer.files]; if (!files.length) return; event.preventDefault(); if (!disabled) void draft.controller.add(files); },
  };
}
// The portal escapes the composer's bounded scrollport without moving its contents.
function AttachmentGuidance({ children, id }: { children: ReactNode; id: string }) {
  const trigger = useRef<HTMLSpanElement>(null), tooltip = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [hover, setHover] = useState(false), [focus, setFocus] = useState(false), [dismissed, setDismissed] = useState(false);
  const [position, setPosition] = useState({ left: 8, top: 8, width: 280, maxHeight: 100 });
  const open = (hover || focus) && !dismissed;
  const cancelLeave = () => { clearTimeout(timer.current); };
  const enter = () => { cancelLeave(); setHover(true); };
  const enterTrigger = () => { enter(); setDismissed(false); };
  const leave = () => { cancelLeave(); timer.current = setTimeout(() => setHover(false), 100); };
  useEffect(() => () => clearTimeout(timer.current), []);
  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const rect = trigger.current?.getBoundingClientRect(); if (!rect) return;
      const zoom = Number.parseFloat(getComputedStyle(document.body).zoom) || 1;
      const width = Math.min(320 * zoom, Math.max(0, window.innerWidth - 16));
      const height = tooltip.current?.getBoundingClientRect().height ?? 64;
      const above = Math.max(0, rect.top - 16);
      const below = Math.max(0, window.innerHeight - rect.bottom - 16);
      const useAbove = above >= Math.min(height, 100) || above >= below;
      const maxHeight = useAbove ? above : below;
      setPosition({ width: width / zoom, left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)) / zoom, top: (useAbove ? Math.max(8, rect.top - 8 - Math.min(height, maxHeight)) : rect.bottom + 8) / zoom, maxHeight: maxHeight / zoom });
    };
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(place);
    if (tooltip.current) observer?.observe(tooltip.current);
    place(); window.addEventListener("resize", place); window.addEventListener("scroll", place, true);
    return () => { observer?.disconnect(); window.removeEventListener("resize", place); window.removeEventListener("scroll", place, true); };
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setDismissed(true); } };
    document.addEventListener("keydown", escape, true);
    return () => document.removeEventListener("keydown", escape, true);
  }, [open]);
  return <span ref={trigger} className="composer-attach-trigger" onPointerEnter={enterTrigger} onPointerLeave={leave} onFocus={() => { if (!focus) setDismissed(false); setFocus(true); }} onBlur={() => setFocus(false)}>
    {children}
    {/* Keep the description associated even when its visual presentation is dismissed. */}
    <span id={id} className="attachment-description">{copy("image-input.help")}</span>
    {open ? createPortal(<div ref={tooltip} className="composer-attachment-tooltip" role="tooltip" onPointerEnter={enter} onPointerLeave={leave} style={position}>{copy("image-input.help")}</div>, document.body) : null}
  </span>;
}
export function ImageAttachmentInput({ draft, disabled, available, routeReady, routeLoading, machineId, compact = false, active = true, children, controls, creationToolbar }: { creationToolbar?: (attach: ReactNode) => ReactNode; compact?: boolean; active?: boolean; children?: ReactNode; controls?: ReactNode; draft: ReturnType<typeof useImageDraft>; disabled: boolean; available: boolean; routeReady: boolean; routeLoading: boolean; machineId: string }) {
  useLocale();
  const input = useRef<HTMLInputElement>(null);
  const guidanceId = useId();
  const attach = <button className={creationToolbar ? "new-session-attach" : compact ? "composer-attach" : undefined} type="button" aria-label={copy("image-input.attach")} title={compact ? undefined : copy("image-input.attach")} aria-describedby={compact ? guidanceId : undefined} disabled={disabled || draft.busy || !available} onClick={() => input.current?.click()}>{compact || creationToolbar ? <span aria-hidden="true">+</span> : copy("image-input.attach")}</button>;
  return <section className={compact ? "image-attachments image-attachments-compact" : "image-attachments"} aria-label={copy("image-input.heading")}>
    <input ref={input} className="image-file-input" type="file" accept="image/png,image/jpeg,image/webp" multiple tabIndex={-1} aria-label={copy("image-input.select")} disabled={disabled || draft.busy || !available} onChange={event => { const files = [...(event.target.files ?? [])]; event.target.value = ""; void draft.controller.add(files); }} />
    {!compact && !creationToolbar ? attach : null}
    {draft.images.length ? <ol className="image-preview-list">{draft.images.map((image, index) => <li key={image.key}><img src={image.preview} alt={copy("image-input.image", { number: index + 1 })} /><span>{copy(image.ready && image.reference?.machineId === machineId ? "image-input.staged" : "image-input.pending", { number: index + 1 })}</span><button type="button" aria-label={copy("image-input.remove", { number: index + 1 })} disabled={disabled || draft.busy} onClick={() => void draft.controller.remove(image.key)}>×</button></li>)}</ol> : null}
    {children}
    {creationToolbar ? creationToolbar(attach) : compact ? <div className="composer-toolbar">{active ? <AttachmentGuidance id={guidanceId}>{attach}</AttachmentGuidance> : attach}{controls}</div> : <small>{copy("image-input.help")}</small>}
    {!available ? <p role="status">{copy("image-input.update")}</p> : draft.images.length && !routeReady ? <p role="status">{copy(routeLoading ? "image-input.checkingRoute" : "image-input.unsupported")}</p> : null}
    {draft.busy ? <p role="status">{copy("image-input.processing")}</p> : null}
    {draft.error ? <p role="alert">{copy(problemKeys[draft.error])}</p> : null}
    {draft.cleanupPending ? <div className="image-cleanup"><p role="status">{copy("image-input.cleanupPending")}</p><button type="button" disabled={disabled || draft.busy} onClick={() => void draft.controller.retryCleanup()}>{copy("image-input.retryCleanup")}</button></div> : null}
  </section>;
}
function RetainedImage({ sessionId, reference, number, active }: { sessionId: string; reference: ImageAttachment; number: number; active: boolean }) {
  const transport = useTransport();
  const element = useRef<HTMLLIElement>(null);
  const [nearViewport, setNearViewport] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    if (!active || typeof IntersectionObserver === "undefined" || !element.current) return;
    // Retained history can contain many full-size originals. Only read images
    // near the visible conversation, and release their bytes after they leave it.
    const observer = new IntersectionObserver(entries => setNearViewport(entries.some(entry => entry.isIntersecting)), { rootMargin: "200px" });
    observer.observe(element.current); return () => observer.disconnect();
  }, [active]);
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<{ url?: string; failed?: boolean }>({});
  useEffect(() => {
    if (!active || !nearViewport) { setState({}); return; }
    const client = createClient(AttachmentService, transport), controller = new AbortController();
    let url: string | undefined;
    setState({});
    void (async () => {
      const parts: Uint8Array[] = []; let offset = 0;
      while (offset < Number(reference.byteLength)) {
        const limit = Math.min(imageLimits.chunk, Number(reference.byteLength) - offset);
        const reply = await client.readAttachment({ sessionId, attachmentId: reference.id, offset: BigInt(offset), limit }, { signal: controller.signal });
        if (!reply.data.length || reply.data.length > limit || await imageDigest(reply.data) !== reply.sha256 || reply.complete !== (offset + reply.data.length === Number(reference.byteLength))) throw new ImageInputError(ImageProblem.Transfer);
        parts.push(reply.data); offset += reply.data.length;
      }
      const bytes = new Uint8Array(offset); let copied = 0; for (const part of parts) { bytes.set(part, copied); copied += part.length; }
      if (await imageDigest(bytes) !== reference.sha256) throw new ImageInputError(ImageProblem.Transfer);
      if (controller.signal.aborted) return;
      url = URL.createObjectURL(new Blob([bytes], { type: imageMime[reference.mediaType] }));
      setState({ url });
    })().catch(error => { if (!controller.signal.aborted) { console.warn("delidev.image_input.readback_failed", { phase: "readback", classification: clientFailure(error).code }); setState({ failed: true }); } });
    return () => { controller.abort(); if (url) URL.revokeObjectURL(url); };
  }, [attempt, active, nearViewport, transport, sessionId, reference.id, reference.byteLength, reference.mediaType, reference.sha256]);
  return <li ref={element}>{state.url ? <img src={state.url} alt={copy("image-input.image", { number })} /> : <p role="status">{copy(state.failed ? "image-input.readFailed" : "image-input.loading")}</p>}{state.failed ? <button type="button" disabled={!active} onClick={() => setAttempt(value => value + 1)}>{copy("image-input.retryRead")}</button> : null}</li>;
}
export function RetainedImages({ value, sessionId, active = true }: { value: unknown; sessionId: string; active?: boolean }) {
  useLocale();
  const key = JSON.stringify(value);
  const references = useMemo(() => retainedImages(key === undefined ? undefined : JSON.parse(key)), [key]);
  if (!references) return <p role="status">{copy("image-input.evidenceUnavailable")}</p>;
  if (!references.length) return null;
  return <ol className="image-retained-list" aria-label={copy("image-input.heading")}>{references.map((reference, index) => <RetainedImage key={reference.id} sessionId={sessionId} reference={reference} number={index + 1} active={active} />)}</ol>;
}
