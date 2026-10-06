import { LocalizedText, copy, useLocale } from "./localization";
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
  useLocale();
  const heading = useRef<HTMLHeadingElement>(null);
  const roots = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: FileOperation.Roots }) }, readOptions);
  const [repository, setRepository] = useState<string>();
  const selected = repository ?? roots.data?.roots.find((root) => root.primary)?.repository_id ?? roots.data?.roots[0]?.repository_id;
  useEffect(() => { heading.current?.focus(); }, []);
  return <aside className="session-files" aria-label={copy("session-files.sessionFiles_206907")} onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
    <header><h2 ref={heading} tabIndex={-1}>{copy("session-files.files_abc7e9")}</h2><button onClick={close} aria-label={copy("session-files.closeSessionFiles_e86cdc")}>{copy("session-files.close_7d9eb7")}</button></header>
    <p>{copy("session-files.filesOnThisSessionSExecution_3a04ee")}</p>
    <Problem error={roots.error} />
    {roots.isPending ? <p role="status">{copy("session-files.loadingWorkspaceRoots_0d8c0f")}</p> : null}
    {roots.error ? <button disabled={roots.isFetching} onClick={() => void roots.refetch()}>{copy("session-files.retryWorkspaceRoots_6b5165")}</button> : null}
    {roots.data ? <label>{copy("session-files.workspaceRepository_0dbfbb")}<select value={selected} onChange={(event) => setRepository(event.target.value)}>{roots.data.roots.map((root) => <option key={root.repository_id} value={root.repository_id}>{root.name}{root.primary ? copy("session-files.primary_b88564") : ""}</option>)}</select></label> : null}
    {selected !== undefined ? <WorkspaceFiles key={selected} sessionId={sessionId} repository={selected} /> : null}
  </aside>;
}

function WorkspaceFiles({ sessionId, repository }: { sessionId: string; repository: string }) {
  useLocale();
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
    <nav className="actions" aria-label={copy("session-files.workspaceNavigation_30242d")}><button disabled={location.path === "."} onClick={() => navigate(".")}>{copy("session-files.root_44cb00")}</button><button disabled={location.path === "."} onClick={() => navigate(parent)}>{copy("session-files.up_55490a")}</button><button disabled={result.isFetching} onClick={() => { if (location.page) setLocation({ ...location, page: "" }); else void result.refetch(); }}>{copy("session-files.refreshFiles_e2b488")}</button></nav>
    <form onSubmit={(event) => { event.preventDefault(); navigate(pathDraft); }}><label>{copy("session-files.relativeDirectory_1ccd46")}<input value={pathDraft} onChange={(event) => setPathDraft(event.target.value)} autoComplete="off" spellCheck={false} /></label><button>{copy("session-files.openDirectory_d62455")}</button></form>
    <p className="file-path" ref={pathLabel} tabIndex={-1}>{location.path}</p>
    <Problem error={result.error} />
    {result.isFetching ? <p role="status"><LocalizedText id="session-files.reading_19d2ee" components={{ s0: <>{directory ? copy("session-files.directory_333178") : copy("session-files.file_3b9c35")}</> }} /></p> : null}
    {result.error && result.data ? <p role="alert">{copy("session-files.refreshFailedTheLastObservationIs_876810")}</p> : null}
    {result.data ? directory ? <>
      <ul className="file-entries" aria-label={copy("session-files.workspaceEntries_5b2690")}>{result.data.entries.map((entry) => <li key={entry.name}>{entry.kind === EntryKind.Directory || entry.kind === EntryKind.File ? <button onClick={() => navigate(join(entry.name), entry.kind === EntryKind.Directory ? FileOperation.Directory : FileOperation.File)}><span>{entry.name}</span><small>{entry.kind === EntryKind.Directory ? copy("session-files.folder_74ccd4") : copy("session-files.bytes_4c5914", { v0: entry.size })}</small></button> : <span>{entry.name}<small>{entry.kind === EntryKind.Link ? copy("session-files.symbolicLinkPreviewUnavailable_c8f825") : copy("session-files.specialFilePreviewUnavailable_768988")}</small></span>}</li>)}</ul>
      {!result.data.entries.length ? <p>{copy("session-files.thisDirectoryIsEmpty_c450d5")}</p> : null}
      <nav className="actions" aria-label={copy("session-files.directoryPages_cd4c9e")}><button disabled={!location.page || result.isFetching} onClick={() => setLocation({ ...location, page: "" })}>{copy("session-files.firstDirectoryPage_925688")}</button><button disabled={!result.data.next || result.isFetching} onClick={() => setLocation({ ...location, page: result.data!.next })}>{copy("session-files.nextDirectoryPage_d6d179")}</button></nav>
    </> : <section aria-label={copy("session-files.filePreview_71d50a")}><p><LocalizedText id="session-files.bytes_d4ed53" components={{ s0: <>{result.data.size}</>, s1: <>{result.data.truncated ? copy("session-files.previewLimitedTo64Kib_9fd380") : ""}</> }} /></p>{result.data.binary ? <p>{copy("session-files.thisFileHasNoUtf8_c06ed8")}</p> : <pre tabIndex={0}>{result.data.text || "(Empty file)"}</pre>}</section> : null}
  </>;
}
