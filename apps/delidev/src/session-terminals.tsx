import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, TerminalAction, TerminalQuery, TerminalService, SystemQuery, SystemCapability, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, Failure, Problem  } from "./ui";

enum OutputState { Connecting = "connecting", Attached = "attached", Detached = "detached", Exited = "exited" }

export class TerminalText {
  private decoder = new TextDecoder();
  private value = "";
  append(bytes: Uint8Array, gap = false) {
    if (gap) { this.decoder = new TextDecoder(); this.value += "\n[Terminal output gap]\n"; }
    this.value += this.decoder.decode(bytes, { stream: true });
    // Bound volatile text independently of the server byte ring. Avoid a
    // retained leading half-surrogate when removing old Unicode content.
    if (this.value.length > 262144) { this.value = this.value.slice(-262144); if (/^[\uDC00-\uDFFF]/u.test(this.value)) this.value = this.value.slice(1); }
    return this.value;
  }
  finish() { this.value += this.decoder.decode(); return this.value; }
}

export function SessionTerminals({ session, close }: { session: Resource; close: () => void }) {
  useLocale();
  const [shell, setShell] = useState("");
  const [selected, setSelected] = useState("");
  const [createdTerminal, setCreatedTerminal] = useState<Resource>();
  const [selectedTerminal, setSelectedTerminal] = useState<Resource>();
  const listRoot = useRef<HTMLDivElement>(null);
  const status = useQuery(SystemQuery.getStatus, {});
  const supported = status.data?.capabilities.includes(SystemCapability.SESSION_TERMINALS_V1) ?? false;
  const list = useConversationPages(EntityKind.TERMINAL, session.id, supported, 50, undefined, 1000);
  const create = useRetainedMutation(`terminal-create:${session.id}`, TerminalQuery.createTerminal, (value) => { if (value.terminal) { setCreatedTerminal(value.terminal); setSelectedTerminal(undefined); setSelected(value.terminal.id); } if (supported) void list.refresh(); });
  const blocked = !supported || create.busy || create.uncertain || text(document(session).archive) !== "active";
  // The accepted resource can be beyond the first history page. Retain just
  // that one explicit selection so history eviction never detaches its shell.
  const reachedSelection = list.data?.resources.find(value => value.id === selected);
  useEffect(() => {
    // Keep the newest observation of the explicit selection when its history
    // page leaves the payload window; eviction must not roll its revision back.
    if (reachedSelection && (!selectedTerminal || reachedSelection.revision > selectedTerminal.revision)) { setSelectedTerminal(reachedSelection); setCreatedTerminal(undefined); }
  }, [reachedSelection, selectedTerminal]);
  const resource = supported ? reachedSelection ?? (createdTerminal?.id === selected ? createdTerminal : selectedTerminal?.id === selected ? selectedTerminal : undefined) : undefined;
  return <aside data-shortcuts="passthrough" className="session-files" aria-label={copy("session-terminals.sessionTerminals_db991c")}>
    <header><h3>{copy("session-terminals.terminals_7482c4")}</h3><button onClick={close}>{copy("session-terminals.hideTerminals_522e2b")}</button></header>
    <p>{copy("session-terminals.terminalsRunOnThisSessionS_0699b6")}</p>
    <form onSubmit={(event) => { event.preventDefault(); void create.send({ mutation: { requestId: newRequestId(), id: session.id, expectedRevision: session.revision }, shellOverride: shell, rows: 24, columns: 80 }); }}>
      <label>{copy("session-terminals.workerShellOverride_b27d25")}<input value={shell} onChange={(event) => setShell(event.target.value)} placeholder={copy("session-terminals.workerAccountDefaultShell_2fd88a")} disabled={blocked} autoComplete="off" spellCheck={false} /></label>
      <button disabled={blocked}>{copy("session-terminals.createTerminal_747b98")}</button>
    </form>
    {!supported && !status.error ? <p role="status">{copy("session-terminals.waitingForAServerThatSupports_e0becc")}</p> : null}
    <Problem error={status.error} actions={<button disabled={status.isFetching} onClick={() => void status.refetch()}>{copy("session-terminals.retryCapability")}</button>} />
    <Problem error={create.error} />{create.uncertain ? <button disabled={create.busy} onClick={create.retry}>{copy("session-terminals.retryTheSameTerminalCreation_bc3946")}</button> : null}
    <Failure failure={supported ? list.error?.failure : undefined} /><button disabled={!supported || list.isFetching} onClick={() => { if (supported) void list.refetch(); }}>{copy("session-terminals.refreshTerminals_6e87f2")}</button>
    <div ref={listRoot} className="conversation-page-scroll"><ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={list} root={listRoot} active={supported}>{payload => <ul>{payload.map((value) => <li key={value.id}><button aria-pressed={selected === value.id} onClick={() => { setSelected(value.id); setSelectedTerminal(value); setCreatedTerminal(undefined); }}><LocalizedText id="session-terminals.terminal_8058ce" components={{ s0: <>{list.rows.findIndex(row => row.id === value.id) + 1}</>, s1: <>{statusLabel(text(document(value).state))}</> }} /></button></li>)}</ul>}</ScrollPayloadWindow><ScrollContinuation query={list} root={listRoot} active={supported} label={copy("session-terminals.terminalHistoryPages_2ace98")} /></div>
    {resource ? <TerminalView key={resource.id} resource={resource} refresh={() => { if (supported) void list.refresh(); }} /> : null}
  </aside>;
}

function TerminalView({ resource, refresh }: { resource: Resource; refresh: () => void }) {
  useLocale();
  const transport = useTransport();
  const [observed, setObserved] = useState(resource);
  const [output, setOutput] = useState("");
  const [state, setState] = useState(OutputState.Connecting);
  const [error, setError] = useState<unknown>();
  const [restart, setRestart] = useState(0);
  const [draft, setDraft] = useState("");
  const [rows, setRows] = useState(24);
  const [columns, setColumns] = useState(80);
  const cursor = useRef({ epoch: "", afterSequence: 0n });
  const buffer = useRef(new TerminalText());
  const input = useRef<HTMLTextAreaElement>(null);
  const terminal = resource.revision > observed.revision ? resource : observed;
  const data = document(terminal);
  const control = useRetainedMutation(`terminal-control:${resource.id}`, TerminalQuery.controlTerminal, (value, request) => { if (value.terminal) setObserved(value.terminal); if (request.action === TerminalAction.INPUT) setDraft(""); refresh(); });
  useEffect(() => {
    const abort = new AbortController();
    const client = createClient(TerminalService, transport);
    const run = async () => {
      setState(OutputState.Connecting); setError(undefined);
      try {
        let cleanup = false;
        for await (const frame of client.watchTerminalOutput({ terminalId: resource.id, ...cursor.current }, { signal: abort.signal })) {
          if (abort.signal.aborted) return;
          const previous = cursor.current;
          // A gap explicitly resets both byte decoding and the server cursor.
          if (!frame.gap && previous.epoch === frame.epoch && frame.data.length && frame.sequence !== previous.afterSequence + 1n) throw new Error("Terminal output order changed. Reattach to inspect the gap.");
          cursor.current = { epoch: frame.epoch, afterSequence: frame.sequence };
          if (frame.gap || frame.data.length) setOutput(buffer.current.append(frame.data, frame.gap));
          if (frame.terminal) { setObserved(frame.terminal); cleanup = !!document(frame.terminal).cleanup_verified; }
          setState(OutputState.Attached);
        }
        if (!abort.signal.aborted) { if (cleanup) setOutput(buffer.current.finish()); setState(cleanup ? OutputState.Exited : OutputState.Detached); }
      } catch (failure) { if (!abort.signal.aborted) { setError(failure); setState(OutputState.Detached); } }
    };
    void run();
    return () => abort.abort();
  }, [resource.id, transport, restart]);
  const pending = Object.keys(object(data.pending)).length > 0;
  const inputBlocked = control.busy || control.uncertain || pending || text(data.state) !== "running" || !!data.close_request_id || state !== OutputState.Attached;
  // Native Enter is carriage return in both Unix PTYs and Windows ConPTY.
  // Keep the draft's original UTF-8 bytes; only append that explicit control.
  const lineInput = new TextEncoder().encode(draft + "\r");
  useEffect(() => {
    if (!inputBlocked) input.current?.focus();
  }, [inputBlocked]);
  const send = (action: TerminalAction, input = new Uint8Array()) => void control.send({ mutation: { id: terminal.id, expectedRevision: terminal.revision, requestId: newRequestId() }, action, input, rows: action === TerminalAction.RESIZE ? rows : 0, columns: action === TerminalAction.RESIZE ? columns : 0 });
  return <section aria-label={copy("session-terminals.attachedTerminal_19246e")}>
    <p role="status">{state} · {statusLabel(text(data.state))}{data.cleanup_verified ? copy("session-terminals.processCleanupVerified_024000") : copy("session-terminals.processCleanupPending_efa78d")}</p>
    <p><LocalizedText id="session-terminals.worker_5a5ff9" components={{ s0: <>{text(data.machine_id)}</>, s1: <br />, s2: <>{text(data.shell)}</>, s3: <br />, s4: <>{text(data.cwd)}</> }} /></p>
    <Problem error={error} />{state === OutputState.Detached ? <button onClick={() => setRestart((value) => value + 1)}>{copy("session-terminals.reattachOriginalTerminal_b22f08")}</button> : null}
    <pre className="terminal-output" tabIndex={0} aria-label={copy("session-terminals.attribute.34ad7d49708a")}>{output || copy("session-terminals.extra.d1bcaf842eff")}</pre>
    <p>{copy("session-terminals.textOutputViewUseTerminalByte_d43051")}</p>
    <form onSubmit={(event) => { event.preventDefault(); send(TerminalAction.INPUT, lineInput); }}>
      <label>{copy("session-terminals.terminalInput_32822e")}<textarea ref={input} value={draft} onChange={(event) => setDraft(event.target.value)} disabled={inputBlocked} rows={2} autoComplete="off" spellCheck={false} /></label>
      <button disabled={inputBlocked || lineInput.length > 32768}>{copy("session-terminals.sendLine_f42bd3")}</button>
      <button type="button" disabled={inputBlocked} onClick={() => send(TerminalAction.INPUT, new Uint8Array([3]))}>{copy("session-terminals.interruptCtrlC_ca181f")}</button>
      <button type="button" disabled={inputBlocked} onClick={() => send(TerminalAction.INPUT, new Uint8Array([4]))}>{copy("session-terminals.endInputCtrlD_3eac1b")}</button>
    </form>
    <form onSubmit={(event) => { event.preventDefault(); send(TerminalAction.RESIZE); }}>
      <label>{copy("session-terminals.terminalRows_93046b")}<input type="number" min={1} max={500} required value={rows} onChange={(event) => setRows(Number(event.target.value))} /></label>
      <label>{copy("session-terminals.terminalColumns_d1fc09")}<input type="number" min={1} max={1000} required value={columns} onChange={(event) => setColumns(Number(event.target.value))} /></label>
      <button disabled={inputBlocked}>{copy("session-terminals.resizeTerminal_4b104d")}</button>
    </form>
    <button disabled={control.busy || control.uncertain || !!data.cleanup_verified || !!data.close_request_id} onClick={() => send(TerminalAction.CLOSE)}>{copy("session-terminals.closeTerminal_7d02fb")}</button>
    <Problem error={control.error} />{control.uncertain ? <button disabled={control.busy} onClick={control.retry}>{copy("session-terminals.retryTheSameTerminalOperation_92eff7")}</button> : null}
    {object(data.problem).message ? <ServiceProblem code={text(object(data.problem).code) || text(object(data.problem).problem_code)}><p role="alert">{text(object(data.problem).message)} {text(object(data.problem).guidance)}</p></ServiceProblem> : null}
  </section>;
}
