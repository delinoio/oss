import { useTerminalTabShortcuts } from "./shortcut-provider";
// SPDX-License-Identifier: Apache-2.0
import "./terminal-dock.css";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, TerminalAction, TerminalQuery, TerminalService, SystemQuery, SystemCapability, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import type { TerminalScreen } from "./terminal-emulator";
import { TerminalInputQueue } from "./terminal-input-queue";
import { terminalDockHeight } from "./terminal-dock-size";
import { ServiceProblem, Failure, Problem  } from "./ui";

export enum TerminalDockPresentation { Docked = "docked", CompactRestored = "compact-restored", Maximized = "maximized" }

enum OutputState { Connecting = "connecting", Attached = "attached", Detached = "detached", Exited = "exited" }

export function SessionTerminals({ session, close, active = true, presentationChanged, selectedId, openTerminal, tabbed = false }: { session: Resource; close: () => void; active?: boolean; presentationChanged?: (value: TerminalDockPresentation) => void; selectedId?: string; openTerminal?: (id: string) => void; tabbed?: boolean }) {
  useLocale();
  const dock = useRef<HTMLElement>(null);
  const [details, setDetails] = useState(false), [maximized, setMaximized] = useState(false), [height, setHeight] = useState<number>();
  const [geometry, setGeometry] = useState({ width: 0, height: 0 }), [discarded, setDiscarded] = useState(false);
  useLayoutEffect(() => {
    const content = dock.current?.closest<HTMLElement>(".session-content"); if (!content) return;
    const measure = () => setGeometry({ width: content.clientWidth, height: content.clientHeight });
    measure(); if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure); observer.observe(content); return () => observer.disconnect();
  }, []);
  const actualHeight = terminalDockHeight(geometry.width, geometry.height, maximized, height);
  const presentation = !active || geometry.height <= 0 ? TerminalDockPresentation.Docked : actualHeight >= geometry.height ? TerminalDockPresentation.Maximized : geometry.height - actualHeight < 200 ? TerminalDockPresentation.CompactRestored : TerminalDockPresentation.Docked;
  useLayoutEffect(() => { presentationChanged?.(presentation); }, [presentation, presentationChanged]);
  useLayoutEffect(() => () => { presentationChanged?.(TerminalDockPresentation.Docked); }, [presentationChanged]);
  useLayoutEffect(() => { dock.current?.closest<HTMLElement>(".session-content")?.style.setProperty("--terminal-dock-height", `${actualHeight}px`); }, [actualHeight]);
  const [shell, setShell] = useState("");
  const [internalSelected, setSelected] = useState("");
  const selected = selectedId ?? internalSelected;
  const [createdTerminal, setCreatedTerminal] = useState<Resource>();
  const [selectedTerminal, setSelectedTerminal] = useState<Resource>();
  const listRoot = useRef<HTMLDivElement>(null);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.SESSION_TERMINALS_V1) ?? false;
  const list = useConversationPages(EntityKind.TERMINAL, session.id, active && supported, 50, undefined, 1000);
  const create = useRetainedMutation(`terminal-create:${session.id}`, TerminalQuery.createTerminal, (value) => { if (value.terminal) { setCreatedTerminal(value.terminal); setSelectedTerminal(undefined); setSelected(value.terminal.id); openTerminal?.(value.terminal.id); } if (supported) void list.refresh(); });
  const blocked = !active || !supported || create.busy || create.uncertain || text(document(session).archive) !== "active";
  // The accepted resource can be beyond the first history page. Retain just
  // that one explicit selection so history eviction never detaches its shell.
  const reachedSelection = list.data?.resources.find(value => value.id === selected);
  useEffect(() => {
    // Keep the newest observation of the explicit selection when its history
    // page leaves the payload window; eviction must not roll its revision back.
    if (reachedSelection && (!selectedTerminal || reachedSelection.revision > selectedTerminal.revision)) { setSelectedTerminal(reachedSelection); setCreatedTerminal(undefined); }
  }, [reachedSelection, selectedTerminal]);
  const tabs = [...list.rows]; if (createdTerminal && !tabs.some(row => row.id === createdTerminal.id)) tabs.push({ id: createdTerminal.id, revision: createdTerminal.revision });
  const retainedResource = supported ? reachedSelection ?? (createdTerminal?.id === selected ? createdTerminal : selectedTerminal?.id === selected ? selectedTerminal : undefined) : undefined;
  const selectedRead=useQuery(ResourceQuery.getResource,{kind:EntityKind.TERMINAL,id:selected},{enabled:active&&supported&&Boolean(selected)&&!retainedResource,retry:false});
  const readResource=selectedRead.data?.resource;
  const resource=retainedResource??(readResource?.kind===EntityKind.TERMINAL&&readResource.id===selected&&readResource.sessionId===session.id&&readResource.schemaVersion===1?readResource:undefined);
  return <aside ref={dock} hidden={!active} className="terminal-dock" aria-label={copy("session-terminals.sessionTerminals_db991c")}>
    {!tabbed ? <div role="separator" tabIndex={0} aria-orientation="horizontal" aria-label={copy("session-terminals.resizeDock")} aria-valuemin={Math.min(200, geometry.height * .7)} aria-valuemax={Math.floor(geometry.height)} aria-valuenow={Math.round(actualHeight)} className="terminal-dock-separator" onPointerDown={event => { const target = event.currentTarget, start = event.clientY, initial = actualHeight; target.setPointerCapture(event.pointerId); const move = (next: PointerEvent) => { setMaximized(false); setHeight(initial + start - next.clientY); }; const stop = () => { target.removeEventListener("pointermove", move); target.removeEventListener("pointerup", stop); target.removeEventListener("pointercancel", stop); }; target.addEventListener("pointermove", move); target.addEventListener("pointerup", stop, { once: true }); target.addEventListener("pointercancel", stop, { once: true }); }} onKeyDown={event => { if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return; event.preventDefault(); setMaximized(false); setHeight(event.key === "Home" ? 200 : event.key === "End" ? geometry.height * .7 : actualHeight + (event.key === "ArrowUp" ? 20 : -20)); }} /> : null}
    <header className="terminal-dock-header"><h3>{copy("session-terminals.terminals_7482c4")}</h3><button type="button" aria-label={copy("session-terminals.createTerminal_747b98")} disabled={blocked} onClick={() => void create.send({ mutation: { requestId: newRequestId(), id: session.id, expectedRevision: session.revision }, shellOverride: shell, rows: 24, columns: 80 })}>+</button><button type="button" aria-expanded={details} onClick={() => setDetails(value => !value)}>{copy("session-terminals.details")}</button>{!tabbed ? <button type="button" data-terminal-restore onClick={() => { if (actualHeight === geometry.height) { setMaximized(false); setHeight(geometry.height * .4); } else setMaximized(true); }}>{copy(actualHeight === geometry.height ? "session-terminals.restoreDock" : "session-terminals.maximizeDock")}</button> : null}<button type="button" onClick={close}>{copy("session-terminals.hideTerminals_522e2b")}</button></header>
    {discarded ? <p role="status">{copy("session-terminals.unsentDiscarded")}</p> : null}
    <div className="terminal-dock-details" hidden={!details}>
    <p>{copy("session-terminals.terminalsRunOnThisSessionS_0699b6")}</p>
    <form onSubmit={(event) => { event.preventDefault(); void create.send({ mutation: { requestId: newRequestId(), id: session.id, expectedRevision: session.revision }, shellOverride: shell, rows: 24, columns: 80 }); }}>
      <label>{copy("session-terminals.workerShellOverride_b27d25")}<input value={shell} onChange={(event) => setShell(event.target.value)} placeholder={copy("session-terminals.workerAccountDefaultShell_2fd88a")} disabled={blocked} autoComplete="off" spellCheck={false} /></label>
      <button disabled={blocked}>{copy("session-terminals.createTerminal_747b98")}</button>
    </form>
    <button disabled={!active || !supported || list.isFetching} onClick={() => { if (active && supported) void list.refetch(); }}>{copy("session-terminals.refreshTerminals_6e87f2")}</button>
    </div>
    <div ref={listRoot} role={tabbed ? "group" : "tablist"} aria-label={copy("session-terminals.terminals_7482c4")} className="terminal-tabs">{tabs.map((row, index) => <button key={row.id} id={`terminal-tab-${row.id}`} role={tabbed ? undefined : "tab"} aria-selected={tabbed ? undefined : selected === row.id} aria-controls={selected === row.id ? `terminal-panel-${row.id}` : undefined} tabIndex={tabbed || selected === row.id || !selected && index === 0 ? 0 : -1} onKeyDown={event => { if (tabbed || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return; event.preventDefault(); const elements = [...(listRoot.current?.querySelectorAll<HTMLButtonElement>("[role=tab]") ?? [])], at = elements.indexOf(event.currentTarget), next = event.key === "Home" ? 0 : event.key === "End" ? elements.length - 1 : (at + (event.key === "ArrowRight" ? 1 : -1) + elements.length) % elements.length; elements[next]?.focus(); elements[next]?.click(); }} onClick={() => { openTerminal?.(row.id); setSelected(row.id); const value = list.data?.resources.find(value => value.id === row.id) ?? (createdTerminal?.id === row.id ? createdTerminal : selectedTerminal?.id === row.id ? selectedTerminal : undefined); if (value) { setSelectedTerminal(value); setCreatedTerminal(undefined); } else { const page = list.pages.find(page => page.rows.some(value => value.id === row.id)); if (page) list.restore(page.token); } }}><LocalizedText id="session-terminals.terminal_8058ce" components={{ s0: <>{index + 1}</>, s1: <>{statusLabel(text(document(list.data?.resources.find(value => value.id === row.id) ?? (resource?.id === row.id ? resource : undefined)).state))}</> }} /></button>)}</div>
    <ScrollContinuation query={list} root={listRoot} active={active && supported} label={copy("session-terminals.terminalHistoryPages_2ace98")} />
    {supported && list.loaded && !list.rows.length && !createdTerminal && !list.error ? <p role="status">{copy("session-terminals.emptyInventory")}</p> : null}
    {!supported && !status.error ? <p role="status">{copy("session-terminals.waitingForAServerThatSupports_e0becc")}</p> : null}
    <Problem error={selectedRead.error} />
    <Problem error={status.error} actions={<button disabled={!active || status.isFetching} onClick={() => void status.refetch()}>{copy("session-terminals.retryCapability")}</button>} /><Problem error={create.error} />
    <Failure failure={supported ? list.error?.failure : undefined} />
    {create.uncertain ? <button disabled={create.busy} onClick={create.retry}>{copy("session-terminals.retryTheSameTerminalCreation_bc3946")}</button> : null}
    {active && resource ? <TerminalView key={resource.id} resource={resource} details={details} discarded={() => setDiscarded(true)} refresh={() => { if (supported) void list.refresh(); }} /> : null}
  </aside>;
}

function TerminalView({ resource, details, discarded, refresh }: { resource: Resource; details: boolean; discarded: () => void; refresh: () => void }) {
  useLocale();
  const tabShortcut = useTerminalTabShortcuts();
  const transport = useTransport(), host = useRef<HTMLDivElement>(null), screen = useRef<TerminalScreen | undefined>(undefined);
  const [observed, setObserved] = useState(resource), [state, setState] = useState(OutputState.Connecting), [error, setError] = useState<unknown>();
  const [ready, setReady] = useState(false);
  const [rendererUnavailable, setRendererUnavailable] = useState(false), [gap, setGap] = useState(false), [overflow, setOverflow] = useState(false), [restart, setRestart] = useState(0), [rendererRestart, setRendererRestart] = useState(0), [deliveryError, setDeliveryError] = useState(false), [wake, setWake] = useState(0);
  const cursor = useRef({ epoch: "", afterSequence: 0n }), queue = useRef(new TerminalInputQueue()), dispatching = useRef(false), seenError = useRef<unknown>(undefined), alive = useRef(false);
  const terminal = resource.revision > observed.revision ? resource : observed, current = useRef(terminal); current.current = terminal;
  const data = document(terminal);
  const control = useRetainedMutation(`terminal-control:${resource.id}`, TerminalQuery.controlTerminal, (value, request) => {
    if (value.terminal) { const accepted = value.terminal; if (accepted.revision >= current.current.revision) current.current = accepted; setObserved(previous => accepted.revision >= previous.revision ? accepted : previous); }
    queue.current.acknowledge(request.action === TerminalAction.RESIZE ? { rows: request.rows, columns: request.columns } : undefined);
    dispatching.current = false; setWake(value => value + 1); refresh();
  }, (value, request) => !!value.terminal && value.terminal.id === request.mutation?.id && value.terminal.kind === EntityKind.TERMINAL && value.terminal.sessionId === resource.sessionId && value.terminal.revision >= (request.mutation?.expectedRevision ?? 1n));
  const pending = Object.keys(object(data.pending)).length > 0;
  const blocked = rendererUnavailable || deliveryError || control.busy || control.uncertain || pending || text(data.state) !== "running" || !!data.close_request_id || state !== OutputState.Attached;
  const canType = !rendererUnavailable && !deliveryError && !control.uncertain && text(data.state) === "running" && !data.close_request_id && state === OutputState.Attached;
  const focusPending = useRef(true), controlRef = useRef(control); controlRef.current = control;
  useLayoutEffect(() => {
    alive.current = true; let disposed = false;
    const unavailable = () => { if (!disposed && alive.current) { setRendererUnavailable(true); if (queue.current.discard()) discarded(); } };
    setReady(false);
    void import("./terminal-emulator").then(({ openTerminalScreen }) => {
    if (disposed) return;
    try {
      screen.current = openTerminalScreen(host.current!, bytes => { if (!queue.current.enqueue(bytes)) setOverflow(true); else { setOverflow(false); setWake(value => value + 1); } }, (rows, columns) => { queue.current.resize(rows, columns); setWake(value => value + 1); }, unavailable, tabShortcut);
      focusPending.current = true; setReady(true);
    } catch { unavailable(); }
    }, unavailable);
    return () => { disposed = true; alive.current = false; if (queue.current.discard()) discarded(); screen.current?.dispose(); screen.current = undefined; };
  }, [resource.id, rendererRestart]);
  useEffect(() => { screen.current?.enabled(canType); if (canType && focusPending.current) { focusPending.current = false; screen.current?.focus(); } }, [canType, restart, ready]);
  useEffect(() => {
    if (control.error && control.error !== seenError.current && !control.busy && !control.uncertain) { seenError.current = control.error; setDeliveryError(true); dispatching.current = false; if (queue.current.discard()) discarded(); return; }
    if (blocked || dispatching.current) return;
    const next = queue.current.next(); if (!next) return;
    dispatching.current = true;
    const record = current.current;
    void control.send({ mutation: { id: record.id, expectedRevision: record.revision, requestId: newRequestId() }, action: "input" in next ? TerminalAction.INPUT : TerminalAction.RESIZE, input: "input" in next ? next.input : new Uint8Array(), rows: "rows" in next ? next.rows : 0, columns: "columns" in next ? next.columns : 0 });
  }, [wake, blocked, terminal.revision, control.busy, control.uncertain, control.error]);
  useEffect(() => {
    const abort = new AbortController(), client = createClient(TerminalService, transport);
    const run = async () => {
      setState(OutputState.Connecting); setError(undefined);
      try {
        let cleanup = false;
        for await (const frame of client.watchTerminalOutput({ terminalId: resource.id, ...cursor.current }, { signal: abort.signal })) {
          if (abort.signal.aborted) return;
          const previous = cursor.current;
          if (!frame.epoch || frame.sequence < 0n || !frame.gap && previous.epoch === frame.epoch && frame.sequence < previous.afterSequence) throw new Error("Terminal output cursor regressed.");
          if (frame.terminal && (frame.terminal.id !== resource.id || frame.terminal.kind !== EntityKind.TERMINAL || frame.terminal.sessionId !== resource.sessionId)) throw new Error("The terminal observation identity changed.");
          if (!frame.gap && previous.epoch && previous.epoch !== frame.epoch || !frame.gap && previous.epoch === frame.epoch && frame.data.length && frame.sequence !== previous.afterSequence + 1n) throw new Error("Terminal output order changed. Reattach to inspect the gap.");
          if (frame.gap) setGap(true);
          if (frame.gap || frame.data.length) await screen.current?.write(frame.data, frame.gap);
          if (abort.signal.aborted) return;
          cursor.current = { epoch: frame.epoch, afterSequence: frame.sequence };
          if (frame.terminal) { setObserved(value => frame.terminal!.revision >= value.revision ? frame.terminal! : value); cleanup = !!document(frame.terminal).cleanup_verified; }
          setState(OutputState.Attached);
        }
        if (!abort.signal.aborted) { setState(cleanup ? OutputState.Exited : OutputState.Detached); if (queue.current.discard()) discarded(); }
      } catch (failure) { if (!abort.signal.aborted) { setError(failure); setState(OutputState.Detached); if (queue.current.discard()) discarded(); } }
    };
    if (ready && !rendererUnavailable) void run();
    return () => abort.abort();
  }, [resource.id, transport, restart, rendererUnavailable, ready]);
  const reattach = () => { if (queue.current.discard()) discarded(); if (rendererUnavailable) { cursor.current = { epoch: "", afterSequence: 0n }; setGap(true); setRendererRestart(value => value + 1); } setDeliveryError(false); setRendererUnavailable(false); setRestart(value => value + 1); };
  return <section id={`terminal-panel-${resource.id}`} aria-labelledby={`terminal-tab-${resource.id}`} className="terminal-view" role="tabpanel" aria-label={copy("session-terminals.attachedTerminal_19246e")}>
    {gap ? <p role="status">{copy("session-terminals.outputGap")}</p> : null}
    {overflow ? <p role="alert">{copy("session-terminals.inputOverflow")}</p> : null}
    {rendererUnavailable ? <p role="alert">{copy("session-terminals.rendererUnavailable")}</p> : null}
    <div ref={host} className="terminal-screen" data-shortcuts="passthrough" aria-label={copy("session-terminals.attribute.34ad7d49708a")} onKeyDown={event => event.stopPropagation()} />
    <footer role="status">{copy(`session-terminals.${state}`)} · {statusLabel(text(data.state))}</footer>
    <div className="terminal-resource-details" hidden={!details}>
      <p><LocalizedText id="session-terminals.worker_5a5ff9" components={{ s0: <>{text(data.machine_id)}</>, s1: <br />, s2: <>{text(data.shell)}</>, s3: <br />, s4: <>{text(data.cwd)}</> }} /></p>
      <p>{data.cleanup_verified ? copy("session-terminals.processCleanupVerified_024000") : copy("session-terminals.processCleanupPending_efa78d")}</p>
    </div>
    <Problem error={error || control.error} />
    {state === OutputState.Detached || rendererUnavailable || deliveryError ? <button type="button" onClick={reattach}>{copy("session-terminals.reattachOriginalTerminal_b22f08")}</button> : null}
    {control.uncertain ? <><p role="status">{copy("session-terminals.uncertainInput")}</p><button type="button" disabled={control.busy} onClick={control.retry}>{copy("session-terminals.retryTheSameTerminalOperation_92eff7")}</button></> : null}
    {details || rendererUnavailable ? <button type="button" disabled={control.busy || control.uncertain || !!data.cleanup_verified || !!data.close_request_id} onClick={() => { if (queue.current.discard()) discarded(); void control.send({ mutation: { id: terminal.id, expectedRevision: terminal.revision, requestId: newRequestId() }, action: TerminalAction.CLOSE }); }}>{copy("session-terminals.closeTerminal_7d02fb")}</button> : null}
    {object(data.problem).message ? <ServiceProblem code={text(object(data.problem).code) || text(object(data.problem).problem_code)}><p role="alert">{text(object(data.problem).message)} {text(object(data.problem).guidance)}</p></ServiceProblem> : null}
  </section>;
}
