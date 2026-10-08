// SPDX-License-Identifier: Apache-2.0
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useCallback, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type KeyboardEvent, type RefObject } from "react";
import { SessionQuery, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { copy, LocalizedText, useLocale } from "./localization";
import { FilesController, childPath, parentPath, type FilesSnapshot, type WorkspaceReader } from "./session-files-model";
import { EntryKind, observation, type Entry } from "./session-files-observation";
import { ScrollContinuation } from "./scroll-continuation";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { Failure, Problem } from "./ui";

// Other read-only workspace consumers retain their original disposable query
// contract. Files itself additionally serializes every observation in one owner.
export const workspaceReadOptions = { retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false, select: (response: { documentJson: Uint8Array }) => observation(response.documentJson) };
// A reopened panel or replaced session shares the connection's pending-read
// barrier. The weak entry retains no observation bytes and expires with its client.
const filesReadBarriers = new WeakMap<QueryClient, Promise<void>>();
function useWorkspaceReader(sessionId: string): WorkspaceReader {
  const transport = useTransport(), client = useQueryClient();
  return useCallback((query, signal) => {
    const pending = (filesReadBarriers.get(client) ?? Promise.resolve()).then(async () => {
    const options = createQueryOptions(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode(query) }, { transport });
    const queryKey = [...options.queryKey, { filesObservation: newRequestId() }];
    const abort = () => { void client.cancelQueries({ queryKey, exact: true }); };
    signal.addEventListener("abort", abort, { once: true });
    let original: Promise<{ documentJson: Uint8Array }> | undefined;
    try {
      if (signal.aborted) throw new DOMException("Workspace observation canceled", "AbortError");
      return await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
        original = Promise.resolve(options.queryFn({ ...context, queryKey: options.queryKey }));
        return observation((await original).documentJson);
      } });
    } finally {
      // TanStack cancellation can reject before its underlying read settles.
      // Retain the scheduler slot until that original promise finishes.
      if (original) await original.catch(() => undefined);
      signal.removeEventListener("abort", abort); client.removeQueries({ queryKey, exact: true });
    }
    });
    filesReadBarriers.set(client, pending.then(() => undefined, () => undefined));
    return pending;
  }, [client, transport, sessionId]);
}
function Icon({ kind }: { kind: "refresh" | "close" | "folder" | "file" | "chevron" }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{kind === "refresh" ? <path d="M20 7v5h-5M19 12a7 7 0 1 0-2 5M20 12l-3-5" /> : kind === "close" ? <path d="m6 6 12 12M18 6 6 18" /> : kind === "folder" ? <path d="M3 6h7l2 3h9v11H3z" /> : kind === "file" ? <path d="M6 3h8l4 4v14H6zM14 3v5h4" /> : <path d="m9 5 7 7-7 7" />}</svg>;
}
export function SessionFiles({ sessionId, close }: { sessionId: string; close: () => void }) {
  useLocale();
  const read = useWorkspaceReader(sessionId), owner = useMemo(() => new FilesController(read), [read]);
  const state = useSyncExternalStore(owner.subscribe, owner.getSnapshot);
  const panel = useRef<HTMLElement>(null), heading = useRef<HTMLHeadingElement>(null);
  const shortcuts = useShortcuts([{ id: ShortcutId.FilesClose, scope: Surface.Sessions, label: "shortcuts.closeFiles", bindings: [{ key: "Escape" }], target: panel, input: ShortcutInput.Target, run: close }]);
  useLayoutEffect(() => { owner.start(); heading.current?.focus(); return () => owner.dispose(); }, [owner]);
  return <aside ref={panel} aria-keyshortcuts={shortcuts.aria(ShortcutId.FilesClose)} className="session-files" aria-label={copy("session-files.sessionFiles_206907")} onKeyDown={shortcuts.onKeyDown}>
    <header><h2 ref={heading} tabIndex={-1}>{copy("session-files.files_abc7e9")}</h2><div className="file-header-actions"><button type="button" className="file-icon-button" disabled={state.refreshing || state.rootsLoading || state.preview?.loading} onClick={() => void owner.refresh()} aria-label={copy("session-files.refreshFiles_e2b488")}><Icon kind="refresh" /></button><button type="button" className="file-icon-button" onClick={close} aria-label={copy("session-files.closeSessionFiles_e86cdc")} aria-keyshortcuts={shortcuts.aria(ShortcutId.FilesClose)}><Icon kind="close" /></button></div></header>
    <p className="file-context">{copy("session-files.readOnlyContext")}</p>
    <Problem error={state.rootsError} />
    {state.rootsLoading ? <p role="status">{copy("session-files.loadingWorkspaceRoots_0d8c0f")}</p> : null}
    {state.rootsError ? <button type="button" disabled={state.rootsLoading} onClick={() => void owner.readRoots()}>{copy("session-files.retryWorkspaceRoots_6b5165")}</button> : null}
    {state.roots ? <label className="file-root-selector">{copy("session-files.workspaceRepository_0dbfbb")}<select value={state.repository ?? ""} onChange={event => owner.selectRepository(event.target.value)}>{state.roots.map(root => <option key={root.repository_id} value={root.repository_id}>{root.name}{root.primary ? copy("session-files.primary_b88564") : ""}</option>)}</select></label> : null}
    {state.roots?.length === 0 ? <p role="status">{copy("session-files.noRoots")}</p> : null}
    {state.repository !== undefined ? <Explorer key={state.repository} owner={owner} state={state} /> : null}
  </aside>;
}
function Explorer({ owner, state }: { owner: FilesController; state: FilesSnapshot }) {
  const scroll = useRef<HTMLDivElement>(null), tree = useRef<HTMLDivElement>(null), previewHeading = useRef<HTMLHeadingElement>(null);
  const scrollPosition = useRef(0), returning = useRef(false), focusOwned = useRef(false);
  const [focused, setFocused] = useState<string>();
  const visible: { path: string; parent: string; entry: Entry }[] = [];
  const visit = (parent: string) => { for (const entry of state.directories.get(parent)?.rows ?? []) { const path = childPath(parent, entry.name); visible.push({ path, parent, entry }); if (entry.kind === EntryKind.Directory && state.expanded.has(path)) visit(path); } };
  visit(".");
  const findVisible = (path?: string) => {
    for (let candidate = path; candidate && candidate !== "."; candidate = parentPath(candidate)) if (visible.some(row => row.path === candidate)) return candidate;
    return undefined;
  };
  const focusPath = findVisible(focused) ?? findVisible(state.selected) ?? visible[0]?.path;
  const focusRow = (path?: string) => {
    setFocused(path); const row = [...(tree.current?.querySelectorAll<HTMLElement>("[role=treeitem]") ?? [])].find(element => element.dataset.path === path);
    (row ?? tree.current)?.focus();
  };
  useLayoutEffect(() => {
    if (state.preview) { previewHeading.current?.focus(); return; }
    if (returning.current) { returning.current = false; if (scroll.current) scroll.current.scrollTop = scrollPosition.current; focusRow(findVisible(state.selected)); }
    // Refresh can remove the focused row; only repair focus if the tree owned it.
  }, [Boolean(state.preview)]);
  useLayoutEffect(() => {
    if (!state.preview && focusOwned.current && focused && !visible.some(row => row.path === focused)) focusRow(focusPath);
  }, [state.directories, state.expanded, state.preview, focused]);
  const open = (path: string) => { scrollPosition.current = scroll.current?.scrollTop ?? 0; owner.open(path); };
  const activate = (path: string, entry: Entry) => { if (entry.kind !== EntryKind.Directory && entry.kind !== EntryKind.File) return; owner.select(path); setFocused(path); if (entry.kind === EntryKind.Directory) owner.toggle(path); else if (entry.kind === EntryKind.File) open(path); };
  const keyboard = (event: KeyboardEvent<HTMLElement>, path: string, parent: string, entry: Entry) => {
    if (event.nativeEvent.isComposing) return;
    const index = visible.findIndex(row => row.path === path);
    switch (event.key) {
      case "ArrowDown": focusRow(visible[Math.min(index + 1, visible.length - 1)]?.path); break;
      case "ArrowUp": focusRow(visible[Math.max(0, index - 1)]?.path); break;
      case "Home": focusRow(visible[0]?.path); break;
      case "End": focusRow(visible.at(-1)?.path); break;
      case "ArrowRight": if (entry.kind === EntryKind.Directory) { if (!state.expanded.has(path)) owner.toggle(path); else if (visible[index + 1]?.parent === path) focusRow(visible[index + 1].path); } break;
      case "ArrowLeft": if (entry.kind === EntryKind.Directory && state.expanded.has(path)) owner.toggle(path); else if (parent !== ".") focusRow(parent); break;
      case "Enter": activate(path, entry); break;
      default: return;
    }
    event.preventDefault(); event.stopPropagation();
  };
  const directory = (path: string, level: number) => <><ul role={path === "." ? "none" : "group"} className="file-tree-entries">{(state.directories.get(path)?.rows ?? []).map(entry => {
    const child = childPath(path, entry.name), folder = entry.kind === EntryKind.Directory, inert = !folder && entry.kind !== EntryKind.File;
    return <li key={entry.name} role="treeitem" data-path={child} tabIndex={focusPath === child ? 0 : -1} aria-level={level} aria-selected={state.selected === child} aria-expanded={folder ? state.expanded.has(child) : undefined} aria-disabled={inert || undefined} aria-label={`${entry.name} ${folder ? copy("session-files.folder_74ccd4") : inert ? copy(entry.kind === EntryKind.Link ? "session-files.symbolicLinkPreviewUnavailable_c8f825" : "session-files.specialFilePreviewUnavailable_768988") : copy("session-files.bytes_4c5914", { v0: entry.size })}`} className="file-tree-node" onFocus={event => { if (event.target === event.currentTarget) setFocused(child); }} onClick={event => { if (event.target instanceof Element && event.target.closest("[role=treeitem]") === event.currentTarget && !event.target.closest("button")) { event.stopPropagation(); event.currentTarget.focus(); activate(child, entry); } }} onKeyDown={event => { if (event.target === event.currentTarget) keyboard(event, child, path, entry); }}><div className="file-tree-row" title={entry.name}>
      <span className={`file-tree-chevron ${folder && state.expanded.has(child) ? "expanded" : ""}`}>{folder ? <Icon kind="chevron" /> : null}</span><Icon kind={folder ? "folder" : "file"} /><span className="file-tree-name">{entry.name}</span><small>{folder ? copy("session-files.folder_74ccd4") : inert ? copy(entry.kind === EntryKind.Link ? "session-files.symbolicLinkPreviewUnavailable_c8f825" : "session-files.specialFilePreviewUnavailable_768988") : copy("session-files.bytes_4c5914", { v0: entry.size })}</small>
    </div>{folder && state.expanded.has(child) ? directory(child, level + 1) : null}</li>;
  })}</ul><DirectoryStatus owner={owner} state={state} path={path} root={scroll} /></>;
  const preview = state.preview;
  return preview ? <section className="file-preview" aria-label={copy("session-files.filePreview_71d50a")}><button type="button" className="file-back" onClick={() => { returning.current = true; owner.back(); }}>{copy("session-files.back")}</button><h3 ref={previewHeading} tabIndex={-1}>{preview.path.split("/").at(-1)}</h3><p className="file-path">{parentPath(preview.path)}</p><p className="file-context">{copy("session-files.readOnly")}</p><Problem error={preview.error} />{preview.loading ? <p role="status">{copy("session-files.previewLoading")}</p> : null}{preview.error && preview.data ? <p role="alert">{copy("session-files.refreshFailedTheLastObservationIs_876810")}</p> : null}
    {preview.data ? <><p><LocalizedText id="session-files.bytes_d4ed53" components={{ s0: <>{preview.data.size}</>, s1: <>{preview.data.truncated ? copy("session-files.previewLimitedTo64Kib_9fd380") : ""}</> }} /></p>{preview.data.binary ? <p>{copy("session-files.thisFileHasNoUtf8_c06ed8")}</p> : preview.data.text ? <pre tabIndex={0}>{preview.data.text}</pre> : <p>{copy("session-files.emptyFile")}</p>}</> : preview.error ? <p>{copy("session-files.previewUnavailable")}</p> : null}</section>
    : <><div ref={scroll} className="conversation-page-scroll file-tree-scroll"><div ref={tree} onFocus={() => { focusOwned.current = true; }} onBlur={event => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node)) focusOwned.current = false; }} role="tree" aria-label={copy("session-files.workspaceEntries_5b2690")} tabIndex={focusPath ? -1 : 0}>{directory(".", 1)}</div></div><p className="file-context file-tree-hint">{copy("session-files.selectFile")}</p></>;
}
function DirectoryStatus({ owner, state, path, root }: { owner: FilesController; state: FilesSnapshot; path: string; root: RefObject<HTMLDivElement | null> }) {
  const page = state.directories.get(path);
  if (!page) return null;
  return <div className="file-directory-status"><Failure failure={page.error?.failure} />{page.error && page.loaded ? <p role="alert">{copy("session-files.refreshFailedTheLastObservationIs_876810")}</p> : null}{page.loaded && !page.loading && !page.error && !page.rows.length ? <p>{copy("session-files.thisDirectoryIsEmpty_c450d5")}</p> : null}{!page.loaded && !page.loading && !page.error ? <button type="button" onClick={() => owner.retry(path)}>{copy("session-files.loadDirectory")}</button> : null}<ScrollContinuation query={{ ...page, append: () => owner.append(path), retry: () => owner.retry(path), reload: () => owner.reload(path) }} label={copy("session-files.directoryPages_cd4c9e")} root={root} active={!state.preview && !state.refreshing && owner.visible(path)} /></div>;
}
