import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SessionQuery } from "@delinoio/delidev-api-client";
import { encode, object, text } from "./documents";
import { Problem } from "./ui";

enum FileOperation { Roots = "roots", Directory = "directory", File = "file" }
enum EntryKind { Directory = "directory", File = "file", Link = "link", Other = "other" }
type Root = { repository_id: string; name: string; primary: boolean };
type Entry = { name: string; kind: EntryKind; size: string };
type Observation = { roots: Root[]; entries: Entry[]; next: string; text: string; size: string; binary: boolean; truncated: boolean };

function observation(raw: Uint8Array): Observation {
  const invalid = () => { throw new Error("The workspace observation is malformed. Refresh the view."); };
  if (raw.byteLength > 512 * 1024) return invalid();
  let value;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const roots = value.roots ?? [], entries = value.entries ?? [];
  if (!Array.isArray(roots) || roots.length > 100 || !Array.isArray(entries) || entries.length > 100 || typeof value.binary !== "boolean" || typeof value.truncated !== "boolean" || typeof value.size !== "string" || !/^\d{1,19}$/.test(value.size) || (value.text !== undefined && typeof value.text !== "string") || (value.next_page_token !== undefined && typeof value.next_page_token !== "string")) return invalid();
  return {
    roots: roots.map((row) => { const item = object(row); if (typeof item.name !== "string" || typeof item.primary !== "boolean" || (item.repository_id !== undefined && typeof item.repository_id !== "string")) return invalid(); return { repository_id: text(item.repository_id), name: item.name, primary: item.primary }; }),
    entries: entries.map((row) => { const item = object(row); if (typeof item.name !== "string" || !Object.values(EntryKind).includes(item.kind as EntryKind) || typeof item.size !== "string" || !/^\d{1,19}$/.test(item.size)) return invalid(); return { name: item.name, kind: item.kind as EntryKind, size: item.size }; }),
    next: text(value.next_page_token), text: text(value.text), size: value.size, binary: value.binary, truncated: value.truncated,
  };
}

// Each view observes one path. Inactive observations have no persistent cache;
// changing path or closing the panel cancels the prior read through Connect.
export const workspaceReadOptions = { retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false, select: (response: { documentJson: Uint8Array }) => observation(response.documentJson) };
const readOptions = workspaceReadOptions;

export function SessionFiles({ sessionId, close }: { sessionId: string; close: () => void }) {
  const heading = useRef<HTMLHeadingElement>(null);
  const roots = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: FileOperation.Roots }) }, readOptions);
  const [repository, setRepository] = useState<string>();
  const selected = repository ?? roots.data?.roots.find((root) => root.primary)?.repository_id ?? roots.data?.roots[0]?.repository_id;
  useEffect(() => { heading.current?.focus(); }, []);
  return <aside className="session-files" aria-label="Session files" onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
    <header><h2 ref={heading} tabIndex={-1}>Files</h2><button onClick={close} aria-label="Close session files">Close</button></header>
    <p>Files on this session's execution machine.</p>
    <Problem error={roots.error} />
    {roots.isPending ? <p role="status">Loading workspace roots…</p> : null}
    {roots.error ? <button disabled={roots.isFetching} onClick={() => void roots.refetch()}>Retry workspace roots</button> : null}
    {roots.data ? <label>Workspace repository<select value={selected} onChange={(event) => setRepository(event.target.value)}>{roots.data.roots.map((root) => <option key={root.repository_id} value={root.repository_id}>{root.name}{root.primary ? " · Primary" : ""}</option>)}</select></label> : null}
    {selected !== undefined ? <WorkspaceFiles key={selected} sessionId={sessionId} repository={selected} /> : null}
  </aside>;
}

function WorkspaceFiles({ sessionId, repository }: { sessionId: string; repository: string }) {
  const [location, setLocation] = useState({ path: ".", operation: FileOperation.Directory, page: "" });
  const [pathDraft, setPathDraft] = useState(".");
  const pathLabel = useRef<HTMLParagraphElement>(null);
  useEffect(() => { pathLabel.current?.focus(); }, [location.path, location.operation, location.page]);
  const result = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: location.operation, repository_id: repository, path: location.path, page_token: location.page }) }, readOptions);
  const directory = location.operation === FileOperation.Directory;
  const navigate = (path: string, operation: FileOperation = FileOperation.Directory) => { setLocation({ path, operation, page: "" }); setPathDraft(operation === FileOperation.File ? path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "." : path); };
  const parent = location.path.includes("/") ? location.path.slice(0, location.path.lastIndexOf("/")) : ".";
  const join = (name: string) => location.path === "." ? name : `${location.path}/${name}`;
  return <>
    <nav className="actions" aria-label="Workspace navigation"><button disabled={location.path === "."} onClick={() => navigate(".")}>Root</button><button disabled={location.path === "."} onClick={() => navigate(parent)}>Up</button><button disabled={result.isFetching} onClick={() => { if (location.page) setLocation({ ...location, page: "" }); else void result.refetch(); }}>Refresh files</button></nav>
    <form onSubmit={(event) => { event.preventDefault(); navigate(pathDraft); }}><label>Relative directory<input value={pathDraft} onChange={(event) => setPathDraft(event.target.value)} autoComplete="off" spellCheck={false} /></label><button>Open directory</button></form>
    <p className="file-path" ref={pathLabel} tabIndex={-1}>{location.path}</p>
    <Problem error={result.error} />
    {result.isFetching ? <p role="status">Reading {directory ? "directory" : "file"}…</p> : null}
    {result.error && result.data ? <p role="alert">Refresh failed. The last observation is shown below.</p> : null}
    {result.data ? directory ? <>
      <ul className="file-entries" aria-label="Workspace entries">{result.data.entries.map((entry) => <li key={entry.name}>{entry.kind === EntryKind.Directory || entry.kind === EntryKind.File ? <button onClick={() => navigate(join(entry.name), entry.kind === EntryKind.Directory ? FileOperation.Directory : FileOperation.File)}><span>{entry.name}</span><small>{entry.kind === EntryKind.Directory ? "Folder" : `${entry.size} bytes`}</small></button> : <span>{entry.name}<small>{entry.kind === EntryKind.Link ? "Symbolic link · Preview unavailable" : "Special file · Preview unavailable"}</small></span>}</li>)}</ul>
      {!result.data.entries.length ? <p>This directory is empty.</p> : null}
      <nav className="actions" aria-label="Directory pages"><button disabled={!location.page || result.isFetching} onClick={() => setLocation({ ...location, page: "" })}>First directory page</button><button disabled={!result.data.next || result.isFetching} onClick={() => setLocation({ ...location, page: result.data!.next })}>Next directory page</button></nav>
    </> : <section aria-label="File preview"><p>{result.data.size} bytes{result.data.truncated ? " · Preview limited to 64 KiB" : ""}</p>{result.data.binary ? <p>This file has no UTF-8 text preview.</p> : <pre tabIndex={0}>{result.data.text || "(Empty file)"}</pre>}</section> : null}
  </>;
}
