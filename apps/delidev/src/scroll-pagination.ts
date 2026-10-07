import { clientFailure, FailureCode, type ClientFailure } from "@delinoio/delidev-api-client";

export interface PaginationRow { id: string; revision: bigint }
export interface PaginationBatch<Row extends PaginationRow, Payload = never> { rows: Row[]; nextPageToken: string; payload?: Payload[] }
export interface PaginationPage<Row extends PaginationRow> { rows: Row[]; nextPageToken: string; token: string; height?: number }
export enum ReadStage { Initial = "initial", Additional = "additional", Refresh = "refresh", Reload = "reload", Restore = "restore" }
export interface PaginationFailure { stage: ReadStage; token: string; failure: ClientFailure; stalled?: boolean }
export interface PaginationPayloadPage<Payload> { token: string; payload: Payload[] }
export interface PaginationSnapshot<Row extends PaginationRow, Payload = never> {
  payloadPages: PaginationPayloadPage<Payload>[];
  rows: Row[];
  pages: PaginationPage<Row>[];
  loaded: boolean;
  nextPageToken: string;
  loading?: ReadStage;
  error?: PaginationFailure;
}
export type PaginationReader<Row extends PaginationRow, Payload = never> = (token: string, signal: AbortSignal) => Promise<PaginationBatch<Row, Payload>>;

function uniqueRows<Row extends PaginationRow>(pages: PaginationPage<Row>[]): Row[] {
  const rows = new Map<string, Row>();
  for (const page of pages) for (const row of page.rows) {
    if (!row.id) continue;
    const previous = rows.get(row.id);
    if (!previous || row.revision >= previous.revision) rows.set(row.id, row);
  }
  return [...rows.values()];
}
const emptySnapshot = <Row extends PaginationRow, Payload>(): PaginationSnapshot<Row, Payload> => ({ payloadPages: [], rows: [], pages: [], loaded: false, nextPageToken: "" });

// A scope has one serial reader and a generation independent of every other
// scope. Cancellation is also a publication guard for transports ignoring abort.
export class PaginationChain<Row extends PaginationRow, Payload = never> {
  private snapshot = emptySnapshot<Row, Payload>();
  private listeners = new Set<() => void>();
  private generation = 0;
  private controller?: AbortController;
  private active = false;
  private protectedToken?: string;
  private retainedPayloadTokens: string[] = [];
  protect(token?: string) { this.protectedToken = token; }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = () => this.snapshot;
  private publish(next: PaginationSnapshot<Row, Payload>) { this.snapshot = next; for (const listener of this.listeners) listener(); }
  activate() { this.active = true; }
  suspend() {
    if (this.snapshot.payloadPages.length) this.retainedPayloadTokens = this.snapshot.payloadPages.map(page => page.token);
    this.active = false;
    this.protectedToken = undefined;
    this.generation++;
    this.controller?.abort();
    this.controller = undefined;
    if (this.snapshot.loading || this.snapshot.payloadPages.length) this.publish({ ...this.snapshot, payloadPages: [], loading: undefined });
  }
  reset() { this.suspend(); this.retainedPayloadTokens = []; this.publish(emptySnapshot<Row, Payload>()); }
  refresh(reader: PaginationReader<Row, Payload>) {
    if (this.snapshot.error) return Promise.resolve();
    return this.run(this.snapshot.loaded ? ReadStage.Refresh : ReadStage.Initial, "", reader);
  }
  append(reader: PaginationReader<Row, Payload>) {
    if (!this.snapshot.loaded || !this.snapshot.nextPageToken || this.snapshot.error) return Promise.resolve();
    return this.run(ReadStage.Additional, this.snapshot.nextPageToken, reader);
  }
  retry(reader: PaginationReader<Row, Payload>) {
    const error = this.snapshot.error;
    if (!error || error.failure.code === FailureCode.CursorExpired || error.stalled) return Promise.resolve();
    return this.run(error.stage, error.token, reader, error.stage === ReadStage.Refresh || error.stage === ReadStage.Restore);
  }
  reload(reader: PaginationReader<Row, Payload>) { return this.run(ReadStage.Reload, "", reader); }
  // Heights are presentation measurements, never authority for a domain read.
  measure(token: string, height: number) {
    if (!Number.isFinite(height) || height < 0) return;
    const index = this.snapshot.pages.findIndex(page => page.token === token);
    if (index < 0 || this.snapshot.pages[index].height === height) return;
    const pages = [...this.snapshot.pages];
    pages[index] = { ...pages[index], height };
    this.publish({ ...this.snapshot, pages });
  }
  restore(token: string, reader: PaginationReader<Row, Payload>) {
    if (this.snapshot.error || !this.snapshot.pages.some(page => page.token === token)
      || this.snapshot.payloadPages.some(page => page.token === token)) return Promise.resolve();
    return this.run(ReadStage.Restore, token, reader, true);
  }
  private async run(stage: ReadStage, token: string, reader: PaginationReader<Row, Payload>, retryOnly = false) {
    if (!this.active || this.controller) return;
    const controller = new AbortController();
    const generation = this.generation;
    const previous = this.snapshot;
    this.controller = controller;
    this.publish({ ...previous, loading: stage, error: undefined });
    let currentToken = token;
    try {
      const pages: PaginationPage<Row>[] = stage === ReadStage.Additional || retryOnly ? [...previous.pages] : [];
      const payloads = new Map(previous.payloadPages.map(page => [page.token, page.payload]));
      const refreshWindow = new Set(previous.payloadPages.length ? previous.payloadPages.map(page => page.token) : this.retainedPayloadTokens.length ? this.retainedPayloadTokens : previous.pages.slice(-3).map(page => page.token));
      if (stage === ReadStage.Reload || stage === ReadStage.Initial) payloads.clear();
      const retryIndex = retryOnly ? pages.findIndex((page) => page.token === token) : -1;
      const seen = new Set((retryOnly ? pages.slice(0, Math.max(0, retryIndex)) : pages).map((page) => page.token));
      // Refresh the accepted request tokens verbatim. Changed boundaries,
      // including a formerly exhausted tail, require explicit Reload list.
      const refreshing = stage === ReadStage.Refresh && !retryOnly;
      const count = refreshing ? previous.pages.length : 1;
      for (let index = 0; index < count; index++) {
        const acceptedPage = refreshing ? previous.pages[index] : retryOnly ? pages[retryIndex] : undefined;
        if (refreshing) currentToken = acceptedPage!.token;
        const batch = await reader(currentToken, controller.signal);
        if (!this.active || generation !== this.generation || controller.signal.aborted) return;
        // Cursors are opaque and the server renews their expiry on each read.
        // Compare the visible range end and continuation presence, then retain
        // the original tokens so refresh cannot discover a different range.
        const restoredRangeChanged = stage === ReadStage.Restore && acceptedPage && (batch.rows.length !== acceptedPage.rows.length || batch.rows.some((row, index) => row.id !== acceptedPage.rows[index].id));
        const boundaryChanged = restoredRangeChanged || acceptedPage && (Boolean(batch.nextPageToken) !== Boolean(acceptedPage.nextPageToken)
          || Boolean(acceptedPage.nextPageToken) && batch.rows.at(-1)?.id !== acceptedPage.rows.at(-1)?.id);
        if ((refreshing || retryOnly) && (!acceptedPage || boundaryChanged)) {
          console.warn("delidev.pagination.read_failed", { stage, classification: FailureCode.CursorExpired });
          this.publish({ ...previous, loading: undefined, error: { stage, token: currentToken, failure: { code: FailureCode.CursorExpired, message: "The loaded list boundaries changed.", guidance: "Reload this list to accept a new chain." } } });
          return;
        }
        seen.add(currentToken);
        if (batch.nextPageToken && seen.has(batch.nextPageToken)) {
          console.warn("delidev.pagination.read_failed", { stage, classification: "non-advancing-continuation" });
          this.publish({ ...previous, loading: undefined, error: { stage, token: currentToken, stalled: true, failure: { code: FailureCode.Internal, message: "The list continuation did not advance.", guidance: "Reload this list to resume navigation." } } });
          return;
        }
        const acceptedBatch = { rows: batch.rows, nextPageToken: acceptedPage?.nextPageToken ?? batch.nextPageToken, token: currentToken, height: acceptedPage?.height };
        // Full domain payloads never enter retained page boundaries. Keep at
        // most three reached pages around the latest requested visible page.
        if (batch.payload && (!refreshing || refreshWindow.has(currentToken))) payloads.set(currentToken, batch.payload);
        if (!refreshing) {
        const center = retryOnly ? retryIndex : refreshing ? previous.pages.findIndex(page => page.token === currentToken) : pages.length;
        const closest = pages.map((page, index) => ({ token: page.token, distance: Math.abs(index - center) }))
          .filter(page => page.token !== currentToken && payloads.has(page.token)).sort((a, b) => a.distance - b.distance).slice(0, this.protectedToken !== undefined && this.protectedToken !== currentToken && payloads.has(this.protectedToken) ? 1 : 2).map(page => page.token);
        const keep = new Set([currentToken, ...closest, ...(this.protectedToken !== undefined ? [this.protectedToken] : [])]);
        for (const key of payloads.keys()) if (!keep.has(key)) payloads.delete(key);
        }
        if (retryOnly) pages[retryIndex] = acceptedBatch;
        else pages.push(acceptedBatch);
        if (!batch.nextPageToken) break;
        currentToken = batch.nextPageToken;
      }
      this.retainedPayloadTokens = [...payloads.keys()];
      for (const page of pages) page.height = this.snapshot.pages.find(previousPage => previousPage.token === page.token)?.height ?? page.height;
      this.publish({ rows: uniqueRows(pages), pages, payloadPages: [...payloads].map(([token, payload]) => ({ token, payload })), loaded: true, nextPageToken: pages.at(-1)?.nextPageToken ?? "" });
    } catch (error) {
      if (!this.active || generation !== this.generation || controller.signal.aborted) return;
      // Only safe typed failure fields are retained; arbitrary exception text,
      // endpoint values and opaque continuation tokens never enter diagnostics.
      const failure = clientFailure(error);
      console.warn("delidev.pagination.read_failed", { stage, classification: failure.code });
      this.publish({ ...previous, loading: undefined, error: { stage, token: currentToken, failure } });
    } finally {
      if (this.controller === controller) this.controller = undefined;
    }
  }
}

