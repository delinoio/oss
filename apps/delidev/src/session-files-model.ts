// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError } from "@connectrpc/connect";
import { ErrorDetailSchema, FailureCode, clientFailure } from "@delinoio/delidev-api-client";
import { PaginationChain, type PaginationSnapshot } from "./scroll-pagination";
import { EntryKind, FileOperation, type Entry, type Observation, type Root } from "./session-files-observation";

export type TreeEntry = Entry & { id: string; revision: bigint };
export type WorkspaceQuery = { operation: FileOperation; repository_id?: string; path?: string; page_token?: string };
export type WorkspaceReader = (query: WorkspaceQuery, signal: AbortSignal) => Promise<Observation>;
type Directory = { path: string; chain: PaginationChain<TreeEntry>; release: () => void };
export type Preview = { path: string; data?: Observation; error?: unknown; loading: boolean };
export type FilesSnapshot = {
  roots?: Root[]; rootsError?: unknown; rootsLoading: boolean; repository?: string;
  directories: ReadonlyMap<string, PaginationSnapshot<TreeEntry>>;
  expanded: ReadonlySet<string>; selected?: string; preview?: Preview; refreshing: boolean;
};
const canceled = () => new DOMException("Workspace observation canceled", "AbortError");
export const childPath = (parent: string, name: string) => parent === "." ? name : `${parent}/${name}`;
export const parentPath = (path: string) => path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".";
const descendant = (path: string, parent: string) => parent === "." || path === parent || path.startsWith(`${parent}/`);

/** The scheduler retains its running slot until the underlying promise settles,
 * including a reader that ignores abort. Roots, every chain page and previews
 * share it; cancellation can never start a competing Files observation. */
class ObservationScheduler {
  private tail: Promise<void> = Promise.resolve();
  constructor(private readonly read: WorkspaceReader) {}
  enqueue(query: WorkspaceQuery, signal: AbortSignal): Promise<Observation> {
    const next = this.tail.then(async () => {
      if (signal.aborted) throw canceled();
      return this.read(query, signal);
    });
    this.tail = next.then(() => undefined, () => undefined);
    return next;
  }
}

/** Safe directory metadata lives only in this open Files scope. Preview bytes
 * never enter a directory chain, and all publication has independent fences. */
export class FilesController {
  private scheduler: ObservationScheduler;
  private active = false;
  private generation = 0;
  private previewGeneration = 0;
  private refreshGeneration = 0;
  private rootsAbort?: AbortController;
  private previewAbort?: AbortController;
  private directories = new Map<string, Directory>();
  private listeners = new Set<() => void>();
  private snapshot: FilesSnapshot = { rootsLoading: false, directories: new Map(), expanded: new Set(), refreshing: false };
  constructor(read: WorkspaceReader) { this.scheduler = new ObservationScheduler(read); }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = () => this.snapshot;
  private publish(update: Partial<FilesSnapshot> = {}) {
    this.snapshot = { ...this.snapshot, ...update, directories: new Map([...this.directories].map(([path, directory]) => [path, directory.chain.getSnapshot()])) };
    for (const listener of this.listeners) listener();
  }
  start() { this.active = true; void this.readRoots(); }
  dispose() {
    this.active = false; this.generation++; this.refreshGeneration++;
    this.rootsAbort?.abort(); this.cancelPreview(); this.clearDirectories();
    this.publish({ roots: undefined, rootsError: undefined, rootsLoading: false, repository: undefined, expanded: new Set(), selected: undefined, preview: undefined, refreshing: false });
  }
  private clearDirectories() { for (const directory of this.directories.values()) { directory.release(); directory.chain.reset(); } this.directories.clear(); }
  private cancelPreview() { this.previewGeneration++; this.previewAbort?.abort(); this.previewAbort = undefined; }
  async readRoots() {
    this.rootsAbort?.abort();
    const abort = this.rootsAbort = new AbortController(), generation = this.generation;
    this.publish({ rootsLoading: true, rootsError: undefined });
    try {
      const result = await this.scheduler.enqueue({ operation: FileOperation.Roots }, abort.signal);
      if (!this.active || abort.signal.aborted || generation !== this.generation) return;
      this.publish({ roots: result.roots, rootsLoading: false });
      const repository = result.roots.find(root => root.primary)?.repository_id ?? result.roots[0]?.repository_id;
      if (repository !== undefined) this.selectRepository(repository);
    } catch (error) {
      if (this.active && !abort.signal.aborted && generation === this.generation) this.publish({ rootsLoading: false, rootsError: error });
    }
  }
  selectRepository(repository: string) {
    if (!this.active || !this.snapshot.roots?.some(root => root.repository_id === repository)) return;
    this.generation++; this.refreshGeneration++; this.cancelPreview(); this.clearDirectories();
    this.publish({ repository, expanded: new Set(), selected: undefined, preview: undefined, refreshing: false });
    void this.directory(".").chain.refresh(this.reader("."));
  }
  private directory(path: string) {
    let directory = this.directories.get(path);
    if (!directory) {
      const chain = new PaginationChain<TreeEntry>(); chain.activate();
      directory = { path, chain, release: chain.subscribe(() => { this.prune(path); this.publish(); }) };
      this.directories.set(path, directory);
    }
    return directory;
  }
  private reader(path: string) {
    const repository = this.snapshot.repository;
    return async (token: string, signal: AbortSignal) => {
      let page: Observation;
      try { page = await this.scheduler.enqueue({ operation: FileOperation.Directory, repository_id: repository, path, page_token: token }, signal); }
      catch (error) {
        if (token && clientFailure(error).code === FailureCode.Conflict) throw new ConnectError("The directory page changed.", Code.OutOfRange, undefined, [{ desc: ErrorDetailSchema, value: { code: FailureCode.CursorExpired, guidance: "Reload this directory to accept its current entries." } }]);
        throw error;
      }
      if (new Set(page.entries.map(entry => entry.name)).size !== page.entries.length) throw new ConnectError("The directory page contains duplicate entry identities.", Code.DataLoss, undefined, [{ desc: ErrorDetailSchema, value: { code: FailureCode.Internal } }]);
      return { rows: page.entries.map(entry => ({ ...entry, id: entry.name, revision: 1n })), nextPageToken: page.next };
    };
  }
  private prune(path: string) {
    const page = this.directories.get(path)?.chain.getSnapshot();
    // A partial range or failed/still-running refresh cannot establish absence.
    if (!page?.loaded || page.loading || page.error || page.nextPageToken) return;
    const expanded = new Set(this.snapshot.expanded);
    for (const [candidate, directory] of this.directories) {
      if (candidate === path || parentPath(candidate) !== path) continue;
      const name = candidate.slice(path === "." ? 0 : path.length + 1);
      if (page.rows.some(entry => entry.name === name && entry.kind === EntryKind.Directory)) continue;
      for (const [nested, child] of this.directories) if (descendant(nested, candidate)) { child.release(); child.chain.reset(); this.directories.delete(nested); expanded.delete(nested); }
    }
    let selected = this.snapshot.selected;
    if (selected && selected !== "." && descendant(selected, path)) {
      const name = selected.slice(path === "." ? 0 : path.length + 1).split("/")[0];
      if (!page.rows.some(entry => entry.name === name)) selected = path;
    }
    this.snapshot = { ...this.snapshot, expanded, selected };
  }
  private entry(path: string) { return this.directories.get(parentPath(path))?.chain.getSnapshot().rows.find(entry => childPath(parentPath(path), entry.name) === path); }
  private canRead(path: string) { return this.active && !this.snapshot.preview && !this.snapshot.refreshing && this.visible(path); }
  select(path: string) { this.publish({ selected: path }); }
  toggle(path: string) {
    if (!this.active || this.snapshot.preview || this.snapshot.refreshing || this.entry(path)?.kind !== EntryKind.Directory) return;
    if (this.snapshot.expanded.has(path)) {
      const expanded = new Set(this.snapshot.expanded); expanded.delete(path);
      this.snapshot = { ...this.snapshot, expanded };
      for (const [nested, directory] of this.directories) if (descendant(nested, path)) directory.chain.suspend();
      this.publish();
    } else {
      const expanded = new Set(this.snapshot.expanded); expanded.add(path); this.publish({ expanded });
      for (const candidate of [path, ...expanded]) {
        if (!this.visible(candidate)) continue;
        const directory = this.directory(candidate); directory.chain.activate();
        if (!directory.chain.getSnapshot().loaded) void directory.chain.refresh(this.reader(candidate));
      }
    }
  }
  visible(path: string): boolean {
    if (path === ".") return true;
    if (!this.snapshot.expanded.has(path)) return false;
    for (let parent = parentPath(path); parent !== "."; parent = parentPath(parent)) if (!this.snapshot.expanded.has(parent)) return false;
    return true;
  }
  append(path: string) { if (!this.canRead(path)) return; void this.directory(path).chain.append(this.reader(path)); }
  retry(path: string) { if (!this.canRead(path)) return; const directory = this.directory(path); if (!directory.chain.getSnapshot().loaded && !directory.chain.getSnapshot().error) void directory.chain.refresh(this.reader(path)); else void directory.chain.retry(this.reader(path)); }
  reload(path: string) { if (!this.canRead(path)) return; void this.directory(path).chain.reload(this.reader(path)); }
  async refresh() {
    if (this.snapshot.preview) { await this.readPreview(this.snapshot.preview.path); return; }
    if (!this.snapshot.repository) { await this.readRoots(); return; }
    if (this.snapshot.refreshing) return;
    const generation = ++this.refreshGeneration;
    const paths = [".", ...this.snapshot.expanded].sort((a,b) => a.split("/").length - b.split("/").length);
    // Supersede every unfinished chain before the sequential refresh. An old
    // ignored-abort read still owns the scheduler slot until it settles.
    for (const directory of this.directories.values()) directory.chain.suspend();
    this.publish({ refreshing: true });
    for (const path of paths) {
      if (!this.active || generation !== this.refreshGeneration || this.snapshot.preview) break;
      if (path !== "." && (!this.directories.has(path) || !this.visible(path))) continue;
      const directory = this.directory(path); directory.chain.activate();
      const error = directory.chain.getSnapshot().error;
      if (error?.stalled || error?.failure.code === FailureCode.CursorExpired) await directory.chain.reload(this.reader(path));
      else if (error) await directory.chain.retry(this.reader(path));
      else await directory.chain.refresh(this.reader(path));
    }
    if (generation === this.refreshGeneration) this.publish({ refreshing: false });
  }
  open(path: string) {
    if (!this.active || this.snapshot.preview || this.entry(path)?.kind !== EntryKind.File) return;
    this.refreshGeneration++;
    for (const directory of this.directories.values()) directory.chain.suspend();
    this.publish({ selected: path, preview: { path, loading: true }, refreshing: false });
    void this.readPreview(path);
  }
  private async readPreview(path: string) {
    this.cancelPreview();
    const generation = this.previewGeneration, abort = this.previewAbort = new AbortController();
    const repository = this.snapshot.repository, previous = this.snapshot.preview?.path === path ? this.snapshot.preview.data : undefined;
    this.publish({ preview: { path, data: previous, loading: true } });
    try {
      const data = await this.scheduler.enqueue({ operation: FileOperation.File, repository_id: repository, path }, abort.signal);
      if (this.active && generation === this.previewGeneration && !abort.signal.aborted) this.publish({ preview: { path, data, loading: false } });
    } catch (error) {
      if (this.active && generation === this.previewGeneration && !abort.signal.aborted) this.publish({ preview: { path, data: previous, loading: false, error } });
    }
  }
  back() {
    this.cancelPreview(); this.publish({ preview: undefined });
    // Back restores metadata only; no implicit read or freshness claim.
    for (const [path, directory] of this.directories) if (this.visible(path)) directory.chain.activate();
  }
}
