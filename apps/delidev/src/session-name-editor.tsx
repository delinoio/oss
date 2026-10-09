// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId } from "@delinoio/delidev-api-client";
import { resourceName } from "./documents";
import { useRetainedMutation } from "./mutation";
import { copy, useLocale } from "./localization";
import { DialogSurface, Problem } from "./ui";
const Editor = createContext<((id: string, opener: HTMLElement) => void) | undefined>(undefined);
export const useSessionNameEditor = () => useContext(Editor);
export function validSessionName(value: string) { return Boolean(value.trim()) && !value.includes("\0") && !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(value) && new TextEncoder().encode(value).byteLength <= 256; }
export function SessionNameEditorProvider({ children }: { children: ReactNode }) {
  const [records, setRecords] = useState<Map<string, HTMLElement>>(() => new Map());
  const [selected, setSelected] = useState<string>();
  const alive = useRef(true);
  useLayoutEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  return <Editor.Provider value={(id, opener) => { setRecords(values => new Map(values).set(id, opener)); setSelected(id); }}>{children}{[...records].map(([id, opener]) => <SessionNameController key={id} id={id} visible={selected === id} opener={opener} alive={alive} close={() => setSelected(current => current === id ? undefined : current)} />)}</Editor.Provider>;
}
function SessionNameController({ id, visible, opener, alive, close }: { id: string; visible: boolean; opener: HTMLElement; alive: { current: boolean }; close: () => void }) {
  useLocale();
  const client = useQueryClient(), dialog = useRef<HTMLDialogElement>(null), input = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState<{ value: string; revision: bigint }>();
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.SESSION, id }, { enabled: visible, retry: false });
  const current = result.data?.resource, readable = current?.id === id && current.kind === EntityKind.SESSION && current.schemaVersion === 1 && current.revision > 0n;
  const rename = useRetainedMutation(`session-name:${id}`, SessionQuery.renameSession, reply => { if (reply.change?.session?.id !== id) return; setDraft(undefined); void client.invalidateQueries({ refetchType: "active" }); close(); }, (reply, request) => reply.change?.requestId === request.mutation?.requestId && reply.change?.session?.id === id && reply.change.session.kind === EntityKind.SESSION && reply.change.session.schemaVersion === 1 && reply.change.session.revision > (request.mutation?.expectedRevision ?? 0n));
  useEffect(() => { if (visible && readable && !draft && !rename.busy && !rename.uncertain) setDraft({ value: resourceName(current), revision: current.revision }); }, [visible, readable, current, draft, rename.busy, rename.uncertain]);
  useLayoutEffect(() => { if (!visible) return; dialog.current?.showModal(); input.current?.focus(); input.current?.select(); return () => { dialog.current?.close(); if (alive.current) { const target = opener.isConnected && !opener.closest('[hidden],[inert]') ? opener : document.querySelector<HTMLElement>('#main'); target?.focus({ preventScroll: true }); } }; }, [visible, opener]);
  useLayoutEffect(() => { if (visible && draft && document.activeElement !== input.current && !rename.busy && !rename.uncertain) { input.current?.focus(); input.current?.select(); } }, [visible, Boolean(draft)]);
  const conflict = readable && draft && draft.revision !== current.revision;
  const valid = draft && validSessionName(draft.value);
  const blocked = !readable || Boolean(result.error) || !draft || !valid || conflict || rename.busy || rename.uncertain;
  return <DialogSurface ref={dialog} hidden={!visible} className="session-name-dialog" aria-label={copy("session-name.edit")} onCancel={() => close()} onKeyDown={event => {
    if (event.key !== "Tab") return;
    // Keep keyboard focus in the editor even when a desktop host includes chrome in its modal Tab cycle.
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>("button,input")].filter(node => !node.matches(":disabled") && !node.closest("[hidden],[inert]"));
    const first = controls[0], last = controls.at(-1);
    if (first && last && (!event.currentTarget.contains(document.activeElement) || (event.shiftKey ? document.activeElement === first : document.activeElement === last))) { event.preventDefault(); (event.shiftKey ? last : first).focus(); }
  }}>
    <header><h2>{copy("session-name.edit")}</h2><button type="button" onClick={close}>{copy("ui.close_7d9eb7")}</button></header>
    <form onSubmit={event => { event.preventDefault(); if (blocked || !draft) return; void rename.send({ mutation: { id, expectedRevision: draft.revision, requestId: newRequestId() }, name: draft.value }); }}>
      <label>{copy("session-tools.sessionName_136a71")}<input ref={input} value={draft?.value ?? ""} disabled={!readable || rename.busy || rename.uncertain} aria-invalid={draft && !valid ? true : undefined} onChange={event => draft && setDraft({ ...draft, value: event.target.value })} onKeyDown={event => { if (event.key === "Enter" && event.nativeEvent.isComposing) event.preventDefault(); }} /></label>
      {draft && !valid ? <p role="alert">{copy("session-name.invalid")}</p> : null}
      {conflict ? <><p role="alert">{copy("session-tools.thisSessionChangedWhileEditingThe_e72d40")}</p><button type="button" disabled={rename.busy || rename.uncertain} onClick={() => setDraft(undefined)}>{copy("session-name.discard")}</button></> : null}
      <Problem error={result.error} actions={<button type="button" onClick={() => void result.refetch()}>{copy("session-name.retryRead")}</button>} /><Problem error={rename.error} />
      {rename.uncertain ? <button type="button" disabled={rename.busy} onClick={rename.retry}>{copy("session-name.retry")}</button> : null}
      <button className="primary" disabled={Boolean(blocked)}>{copy("session-name.save")}</button>
    </form>
  </DialogSurface>;
}
