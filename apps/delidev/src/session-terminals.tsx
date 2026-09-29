import { useEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, TerminalAction, TerminalQuery, TerminalService, SystemQuery, SystemCapability, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

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
  const [shell, setShell] = useState("");
  const [selected, setSelected] = useState("");
  const [page, setPage] = useState("");
  const list = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.TERMINAL, sessionId: session.id, pageToken: page, pageSize: 50 } }, { retry: false, refetchInterval: 1000 });
  const create = useRetainedMutation(`terminal-create:${session.id}`, TerminalQuery.createTerminal, (value) => { if (value.terminal) setSelected(value.terminal.id); setPage(""); void list.refetch(); });
  const status = useQuery(SystemQuery.getStatus, {});
  const supported = status.data?.capabilities.includes(SystemCapability.SESSION_TERMINALS_V1) ?? false;
  const blocked = !supported || create.busy || create.uncertain || text(document(session).archive) !== "active";
  const resource = list.data?.resources.find((value) => value.id === selected);
  return <aside className="session-files" aria-label="Session terminals">
    <header><h3>Terminals</h3><button onClick={close}>Hide terminals</button></header>
    <p>Terminals run on this session's Worker in its primary workspace. Agent Stop preserves them. Archive closes their owned processes.</p>
    <form onSubmit={(event) => { event.preventDefault(); void create.send({ mutation: { requestId: newRequestId(), id: session.id, expectedRevision: session.revision }, shellOverride: shell, rows: 24, columns: 80 }); }}>
      <label>Worker shell override<input value={shell} onChange={(event) => setShell(event.target.value)} placeholder="Worker account default shell" disabled={blocked} autoComplete="off" spellCheck={false} /></label>
      <button disabled={blocked}>Create terminal</button>
    </form>
    {!supported ? <p role="status">Waiting for a server that supports session terminals.</p> : null}
    <Problem error={create.error} />{create.uncertain ? <button disabled={create.busy} onClick={create.retry}>Retry the same terminal creation</button> : null}
    <Problem error={list.error} /><button disabled={list.isFetching} onClick={() => void list.refetch()}>Refresh terminals</button>
    <ul>{list.data?.resources.map((value, index) => <li key={value.id}><button aria-pressed={selected === value.id} onClick={() => setSelected(value.id)}>Terminal {index + 1} · {text(document(value).state)}</button></li>)}</ul>
    <nav aria-label="Terminal history pages"><button disabled={!page} onClick={() => { setPage(""); setSelected(""); }}>First page</button><button disabled={!list.data?.nextPageToken} onClick={() => { setPage(list.data!.nextPageToken); setSelected(""); }}>Next page</button></nav>
    {resource ? <TerminalView key={resource.id} resource={resource} refresh={() => void list.refetch()} /> : null}
  </aside>;
}

function TerminalView({ resource, refresh }: { resource: Resource; refresh: () => void }) {
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
  const send = (action: TerminalAction, input = new Uint8Array()) => void control.send({ mutation: { id: terminal.id, expectedRevision: terminal.revision, requestId: newRequestId() }, action, input, rows: action === TerminalAction.RESIZE ? rows : 0, columns: action === TerminalAction.RESIZE ? columns : 0 });
  return <section aria-label="Attached terminal">
    <p role="status">{state} · {text(data.state)}{data.cleanup_verified ? " · Process cleanup verified" : " · Process cleanup pending"}</p>
    <p>Worker {text(data.machine_id)}<br />{text(data.shell)}<br />{text(data.cwd)}</p>
    <Problem error={error} />{state === OutputState.Detached ? <button onClick={() => setRestart((value) => value + 1)}>Reattach original terminal</button> : null}
    <pre className="terminal-output" tabIndex={0} aria-label="Terminal output">{output || "Waiting for terminal output…"}</pre>
    <p>Text output view. Use terminal byte input for interactive controls; full-screen application display is not emulated here.</p>
    <form onSubmit={(event) => { event.preventDefault(); send(TerminalAction.INPUT, new TextEncoder().encode(draft + "\n")); }}>
      <label>Terminal input<textarea value={draft} onChange={(event) => setDraft(event.target.value)} disabled={inputBlocked} rows={2} autoComplete="off" spellCheck={false} /></label>
      <button disabled={inputBlocked || new TextEncoder().encode(draft + "\n").length > 32768}>Send line</button>
      <button type="button" disabled={inputBlocked} onClick={() => send(TerminalAction.INPUT, new Uint8Array([3]))}>Interrupt (Ctrl+C)</button>
      <button type="button" disabled={inputBlocked} onClick={() => send(TerminalAction.INPUT, new Uint8Array([4]))}>End input (Ctrl+D)</button>
    </form>
    <form onSubmit={(event) => { event.preventDefault(); send(TerminalAction.RESIZE); }}>
      <label>Terminal rows<input type="number" min={1} max={500} required value={rows} onChange={(event) => setRows(Number(event.target.value))} /></label>
      <label>Terminal columns<input type="number" min={1} max={1000} required value={columns} onChange={(event) => setColumns(Number(event.target.value))} /></label>
      <button disabled={inputBlocked}>Resize terminal</button>
    </form>
    <button disabled={control.busy || control.uncertain || !!data.cleanup_verified || !!data.close_request_id} onClick={() => send(TerminalAction.CLOSE)}>Close terminal</button>
    <Problem error={control.error} />{control.uncertain ? <button disabled={control.busy} onClick={control.retry}>Retry the same terminal operation</button> : null}
    {object(data.problem).message ? <p role="alert">{text(object(data.problem).message)} {text(object(data.problem).guidance)}</p> : null}
  </section>;
}
