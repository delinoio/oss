// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, useState } from "react";
import { useTransport } from "@connectrpc/connect-query";
import { copy, useLocale } from "./localization";
import { useSettingsOpening } from "./settings-lifetime";
import "./project-repository-order.css";

type Movement = { id: string; original: string[]; preview: string[]; pointer?: number; authority: object };
export function RepositorySecondaryID({ id }: { id: string }) { return <code className="project-repository-secondary-id">{id}</code>; }
/** Preview order belongs to this view; only an explicit commit changes the draft. */
export function ProjectRepositoryOrder({ ids, names, active, change, editable = true }: { ids: string[]; names: ReadonlyMap<string, string>; active: boolean; change: (ids: string[]) => void; editable?: boolean }) {
  useLocale();
  const transport = useTransport(), opening = useSettingsOpening();
  const root = useRef<HTMLOListElement>(null), handles = useRef(new Map<string, HTMLButtonElement>());
  const current = useRef({ ids, active, editable, transport, change });
  current.current = { ids, active, editable, transport, change };
  const movement = useRef<Movement | undefined>(undefined);
  const [preview, setPreview] = useState<string[]>(), [status, announce] = useState("");
  const allowed = () => { const node = root.current; return current.current.active && current.current.editable && !opening?.disposed && !!node?.isConnected && !node.closest('[hidden], [inert], fieldset:disabled, [aria-disabled="true"]'); };
  const focus = (id: string) => handles.current.get(id)?.focus({ preventScroll: true });
  const finish = (commit: boolean) => {
    const value = movement.current; if (!value) return;
    const valid = allowed() && value.authority === current.current.transport && JSON.stringify(value.original) === JSON.stringify(current.current.ids);
    movement.current = undefined; setPreview(undefined);
    if (commit && valid && value.preview.some((id, i) => id !== value.original[i])) current.current.change(value.preview);
    announce(copy(commit && valid ? "project-order.completed" : "project-order.canceled", { position: (commit && valid ? value.preview : value.original).indexOf(value.id) + 1 }));
    if (valid) focus(value.id);
  };
  const begin = (id: string, pointer?: number) => {
    if (!allowed() || current.current.ids.length < 2 || movement.current) return;
    const original = [...current.current.ids];
    movement.current = { id, original, preview: original, pointer, authority: current.current.transport }; setPreview(original);
    announce(copy("project-order.picked", { position: original.indexOf(id) + 1 })); focus(id);
  };
  const move = (index: number) => {
    const value = movement.current; if (!value) return;
    if (!allowed() || value.authority !== current.current.transport || JSON.stringify(value.original) !== JSON.stringify(current.current.ids)) { finish(false); return; }
    const next = value.preview.filter(id => id !== value.id); next.splice(Math.max(0, Math.min(next.length, index)), 0, value.id);
    value.preview = next; setPreview(next); announce(copy("project-order.position", { position: next.indexOf(value.id) + 1, count: next.length }));
  };
  useLayoutEffect(() => { if (movement.current && (!active || !editable || movement.current.authority !== transport || JSON.stringify(movement.current.original) !== JSON.stringify(ids))) finish(false); }, [active, editable, transport, ids]);
  useLayoutEffect(() => {
    const cancel = () => finish(false);
    const abort = opening?.controller.signal;
    abort?.addEventListener("abort", cancel);
    const hide = () => { if (document.visibilityState === "hidden") cancel(); };
    document.addEventListener("visibilitychange", hide);
    // Ancestor locks can change outside React props (native fieldset/modal ownership).
    const observer = new MutationObserver(() => { if (movement.current && !allowed()) cancel(); });
    const ancestors: Element[] = []; for (let node: Element | null = root.current; node; node = node.parentElement) ancestors.push(node);
    ancestors.forEach(node => observer.observe(node, { attributes: true, attributeFilter: ["disabled", "hidden", "inert", "aria-disabled"] }));
    return () => { movement.current = undefined; observer.disconnect(); abort?.removeEventListener("abort", cancel); document.removeEventListener("visibilitychange", hide); };
  }, [opening, transport]);
  const shown = preview ?? ids;
  return <><ol ref={root} className="project-selected-repositories project-repository-order" onKeyDownCapture={event => {
    if (movement.current && event.key === "Escape") { event.preventDefault(); event.stopPropagation(); finish(false); }
  }}>{shown.map((id, index) => {
    const name = names.get(id), distinct = !name || ids.some(other => other !== id && names.get(other) === name);
    return <li key={id} data-repository-id={id} data-moving={movement.current?.id === id || undefined}>
      {editable ? <button type="button" className="project-repository-grip" ref={node => { if (node) handles.current.set(id, node); else handles.current.delete(id); }} disabled={!active || ids.length < 2} aria-label={copy("project-order.handle", { position: index + 1, name: name || copy("project-creation.nameUnavailable") })} aria-pressed={movement.current?.id === id} onKeyDown={event => {
        if (event.repeat || event.nativeEvent.isComposing || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
        if (event.key === " " || event.key === "Enter") { event.preventDefault(); event.stopPropagation(); if (movement.current?.id === id && movement.current.pointer === undefined) finish(true); else begin(id); }
        if (movement.current?.id === id && movement.current.pointer === undefined && ["ArrowUp", "ArrowDown"].includes(event.key)) { event.preventDefault(); event.stopPropagation(); move(index + (event.key === "ArrowUp" ? -1 : 1)); handles.current.get(id)?.scrollIntoView?.({ block: "nearest" }); }
      }} onPointerDown={event => { if (event.button !== 0) return; event.preventDefault(); begin(id, event.pointerId); if (movement.current?.pointer === event.pointerId) event.currentTarget.setPointerCapture?.(event.pointerId); }} onPointerMove={event => {
        const value = movement.current; if (!value || value.pointer !== event.pointerId) return;
        if (!allowed()) { finish(false); return; }
        const rows = Array.from(root.current?.children ?? []) as HTMLElement[];
        const target = rows.findIndex(row => event.clientY < row.getBoundingClientRect().bottom); move(target < 0 ? rows.length - 1 : target);
        // Scroll the existing dialog body rather than creating a second scroll owner.
        for (let node = root.current?.parentElement; node; node = node.parentElement) { if (node.scrollHeight > node.clientHeight && /auto|scroll/.test(getComputedStyle(node).overflowY)) { const bounds = node.getBoundingClientRect(); if (event.clientY < bounds.top + 40) node.scrollTop -= 24; else if (event.clientY > bounds.bottom - 40) node.scrollTop += 24; break; } }
      }} onPointerUp={event => { if (movement.current?.pointer === event.pointerId) finish(true); }} onPointerCancel={event => { if (movement.current?.pointer === event.pointerId) finish(false); }} onLostPointerCapture={event => { if (movement.current?.pointer === event.pointerId) finish(false); }}><span aria-hidden="true">⠿</span></button> : null}
      <span className="project-repository-position" aria-hidden="true">{index + 1}</span><span className="project-repository-name">{name || copy("project-creation.nameUnavailable")}{distinct ? <RepositorySecondaryID id={id} /> : null}</span>
      {editable ? <div className="actions"><button type="button" disabled={!active || !!movement.current} aria-label={copy("configuration-fields.removeEntry_8a2d73", { v0: index + 1 })} onClick={() => { if (allowed()) change(ids.filter(value => value !== id)); }}>{copy("configuration-fields.remove_c3812f")}</button></div> : null}
    </li>;
  })}</ol><p role="status" aria-live="polite" className="project-repository-order-status">{status}</p></>;
}
