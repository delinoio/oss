import { clientFailure, FailureCode, type ClientFailure, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { sessionTitlePresentation } from "./session-title";

// Home deliberately retains only navigation metadata. Full Resource documents
// belong to disposable bounded queries, never to an accumulated inventory.
export interface NavigationRow {
  id: string;
  projectId: string;
  revision: bigint;
  name: string;
  workspace: string;
  outcome: string;
  archive: string;
  title?: ReturnType<typeof sessionTitlePresentation>;
}
export function navigationRow(row: Resource): NavigationRow {
  const data = document(row);
  return { id: row.id, projectId: row.projectId, revision: row.revision, name: resourceName(row), workspace: text(data.workspace), outcome: text(data.outcome), archive: text(data.archive), title: sessionTitlePresentation(data) };
}
export interface NavigationBatch { rows: NavigationRow[]; nextPageToken: string }
export interface NavigationPage extends NavigationBatch { token: string }
export enum ReadStage { Initial = "initial", Additional = "additional", Refresh = "refresh", Reload = "reload" }
export interface NavigationFailure { stage: ReadStage; token: string; failure: ClientFailure; stalled?: boolean }
export interface NavigationSnapshot {
  rows: NavigationRow[];
  pages: NavigationPage[];
  loaded: boolean;
  nextPageToken: string;
  loading?: ReadStage;
  error?: NavigationFailure;
}
export type NavigationReader = (token: string, signal: AbortSignal) => Promise<NavigationBatch>;

function uniqueRows(pages: NavigationPage[]): NavigationRow[] {
  const rows = new Map<string, NavigationRow>();
  for (const page of pages) for (const row of page.rows) {
    if (!row.id) continue;
    const previous = rows.get(row.id);
    if (!previous || row.revision >= previous.revision) rows.set(row.id, row);
  }
  return [...rows.values()];
}
const emptySnapshot = (): NavigationSnapshot => ({ rows: [], pages: [], loaded: false, nextPageToken: "" });

// A scope has one serial reader and a generation independent of every other
// scope. Cancellation is also a publication guard for transports ignoring abort.
export class NavigationChain {
  private snapshot = emptySnapshot();
  private listeners = new Set<() => void>();
  private generation = 0;
  private controller?: AbortController;
  private active = false;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = () => this.snapshot;
  private publish(next: NavigationSnapshot) { this.snapshot = next; for (const listener of this.listeners) listener(); }
  activate() { this.active = true; }
  suspend() {
    this.active = false;
    this.generation++;
    this.controller?.abort();
    this.controller = undefined;
    if (this.snapshot.loading) this.publish({ ...this.snapshot, loading: undefined });
  }
  reset() { this.suspend(); this.publish(emptySnapshot()); }
  refresh(reader: NavigationReader) {
    if (this.snapshot.error) return Promise.resolve();
    return this.run(this.snapshot.loaded ? ReadStage.Refresh : ReadStage.Initial, "", reader);
  }
  append(reader: NavigationReader) {
    if (!this.snapshot.loaded || !this.snapshot.nextPageToken || this.snapshot.error) return Promise.resolve();
    return this.run(ReadStage.Additional, this.snapshot.nextPageToken, reader);
  }
  retry(reader: NavigationReader) {
    const error = this.snapshot.error;
    if (!error || error.failure.code === FailureCode.CursorExpired || error.stalled) return Promise.resolve();
    return this.run(error.stage, error.token, reader, error.stage === ReadStage.Refresh);
  }
  reload(reader: NavigationReader) { return this.run(ReadStage.Reload, "", reader); }
  private async run(stage: ReadStage, token: string, reader: NavigationReader, retryOnly = false) {
    if (!this.active || this.controller) return;
    const controller = new AbortController();
    const generation = this.generation;
    const previous = this.snapshot;
    this.controller = controller;
    this.publish({ ...previous, loading: stage, error: undefined });
    let currentToken = token;
    try {
      const pages: NavigationPage[] = stage === ReadStage.Additional || retryOnly ? [...previous.pages] : [];
      const retryIndex = retryOnly ? pages.findIndex((page) => page.token === token) : -1;
      const seen = new Set((retryOnly ? pages.slice(0, Math.max(0, retryIndex)) : pages).map((page) => page.token));
      // Refresh only the number of already accepted ranges. It must never
      // discover an unseen trailing range, even if the inventory changes.
      const count = stage === ReadStage.Refresh && !retryOnly ? previous.pages.length : 1;
      for (let index = 0; index < count; index++) {
        const batch = await reader(currentToken, controller.signal);
        if (!this.active || generation !== this.generation || controller.signal.aborted) return;
        seen.add(currentToken);
        if (batch.nextPageToken && seen.has(batch.nextPageToken)) {
          console.warn("delidev.home_navigation.read_failed", { stage, classification: "non-advancing-continuation" });
          this.publish({ ...previous, loading: undefined, error: { stage, token: currentToken, stalled: true, failure: { code: FailureCode.Internal, message: "The list continuation did not advance.", guidance: "Reload this list to resume navigation." } } });
          return;
        }
        if (retryOnly) {
          if (retryIndex < 0 || batch.nextPageToken !== pages[retryIndex].nextPageToken) {
            this.publish({ ...previous, loading: undefined, error: { stage, token: currentToken, failure: { code: FailureCode.CursorExpired, message: "The loaded list boundaries changed.", guidance: "Reload this list to accept a new chain." } } });
            return;
          }
          pages[retryIndex] = { ...batch, token: currentToken };
        } else pages.push({ ...batch, token: currentToken });
        if (!batch.nextPageToken) break;
        currentToken = batch.nextPageToken;
      }
      this.publish({ rows: uniqueRows(pages), pages, loaded: true, nextPageToken: pages.at(-1)?.nextPageToken ?? "" });
    } catch (error) {
      if (!this.active || generation !== this.generation || controller.signal.aborted) return;
      // Only safe typed failure fields are retained; arbitrary exception text,
      // endpoint values and opaque continuation tokens never enter diagnostics.
      const failure = clientFailure(error);
      console.warn("delidev.home_navigation.read_failed", { stage, classification: failure.code });
      this.publish({ ...previous, loading: undefined, error: { stage, token: currentToken, failure } });
    } finally {
      if (this.controller === controller) this.controller = undefined;
    }
  }
}

export class HomeNavigation {
  catalog = new NavigationChain();
  global = new NavigationChain();
  projects = new Map<string, NavigationChain>();
  project(id: string) {
    let chain = this.projects.get(id);
    if (!chain) { chain = new NavigationChain(); this.projects.set(id, chain); }
    return chain;
  }
  resetSessions() { this.global.reset(); for (const chain of this.projects.values()) chain.reset(); }
}
