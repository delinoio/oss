// SPDX-License-Identifier: Apache-2.0
import {
  clientFailure,
  FailureCode,
  type ClientFailure,
} from "@delinoio/delidev-api-client";

export interface PaginationRow {
  id: string;
  revision: bigint;
}
export const paginationIdentity = (row: PaginationRow) => row.id;
export const paginationRevision = (row: PaginationRow) => row.revision;
export interface PaginationBatch<Row extends PaginationRow, Payload = never> {
  rows: Row[];
  nextPageToken: string;
  payload?: Payload[];
  /** Optional bounded scalar describing this raw page. It is retained in page
   * metadata for every reached page, so it survives payload eviction and is
   * replaced together with the pages by Initial, Reload, Refresh and Restore. */
  rawCount?: number;
  /** Runs once after generation, abort, boundary and continuation checks
   * accept this read and before any payload is evicted. Rejected, canceled and
   * superseded reads never call it. A throwing callback never fails the read. */
  accepted?: () => void;
}
export interface PaginationPage<Row extends PaginationRow> {
  rows: Row[];
  nextPageToken: string;
  token: string;
  height?: number;
  /** Raw page scalar copied from the accepted batch; see PaginationBatch. */
  rawCount?: number;
}
export enum ReadStage {
  Initial = "initial",
  Additional = "additional",
  Refresh = "refresh",
  Reload = "reload",
  Restore = "restore",
}
export interface PaginationFailure {
  stage: ReadStage;
  token: string;
  failure: ClientFailure;
  stalled?: boolean;
}
export interface PaginationPayloadPage<Payload> {
  token: string;
  payload: Payload[];
}
export interface PaginationSnapshot<
  Row extends PaginationRow,
  Payload = never,
> {
  payloadPages: PaginationPayloadPage<Payload>[];
  rows: Row[];
  pages: PaginationPage<Row>[];
  loaded: boolean;
  nextPageToken: string;
  loading?: ReadStage;
  error?: PaginationFailure;
}
export type PaginationReader<Row extends PaginationRow, Payload = never> = (
  token: string,
  signal: AbortSignal,
) => Promise<PaginationBatch<Row, Payload>>;

/**
 * True when every retained page token still has its payload resident. A
 * terminal continuation token proves traversal only: the chain keeps page
 * metadata for every reached page but at most three payload pages.
 */
export function pagesResident(query: {
  pages: readonly { token: string }[];
  payloadPages: readonly { token: string }[];
}): boolean {
  return query.pages.every((page) =>
    query.payloadPages.some((payload) => payload.token === page.token),
  );
}

function uniqueRows<Row extends PaginationRow>(
  pages: PaginationPage<Row>[],
): Row[] {
  const rows = new Map<string, Row>();
  for (const page of pages)
    for (const row of page.rows) {
      if (!row.id) continue;
      const previous = rows.get(row.id);
      if (!previous || row.revision >= previous.revision) rows.set(row.id, row);
    }
  return [...rows.values()];
}
const emptySnapshot = <
  Row extends PaginationRow,
  Payload,
>(): PaginationSnapshot<Row, Payload> => ({
  payloadPages: [],
  rows: [],
  pages: [],
  loaded: false,
  nextPageToken: "",
});

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
  // Failed refresh retains only its staged display projections. Full staged
  // responses are discarded; earlier refreshed payloads restore by exact token.
  private pendingRefresh?: { pages: PaginationPage<Row>[]; window: string[] };
  // An invalidation that arrives while another read owns the controller (or an
  // error is held) is never dropped. The latest reader is remembered and one
  // coalesced reached-range refresh runs once a read settles successfully. Only
  // suspend/reset (scope, owner or generation change) clear it; a failed read
  // retains it for the next explicit recovery. There is no automatic retry.
  private queuedRefresh?: PaginationReader<Row, Payload>;
  protect(token?: string) {
    this.protectedToken = token;
  }
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  getSnapshot = () => this.snapshot;
  private publish(next: PaginationSnapshot<Row, Payload>) {
    this.snapshot = next;
    for (const listener of this.listeners) listener();
  }
  activate() {
    this.active = true;
  }
  suspend(retainPayload = false) {
    if (this.snapshot.payloadPages.length)
      this.retainedPayloadTokens = this.snapshot.payloadPages.map(
        (page) => page.token,
      );
    this.active = false;
    this.protectedToken = undefined;
    this.queuedRefresh = undefined;
    this.generation++;
    this.controller?.abort();
    this.controller = undefined;
    if (
      this.snapshot.loading ||
      (!retainPayload && this.snapshot.payloadPages.length)
    )
      this.publish({
        ...this.snapshot,
        payloadPages: retainPayload ? this.snapshot.payloadPages : [],
        loading: undefined,
      });
  }
  reset() {
    this.suspend();
    this.pendingRefresh = undefined;
    this.retainedPayloadTokens = [];
    this.publish(emptySnapshot<Row, Payload>());
  }
  refresh(reader: PaginationReader<Row, Payload>) {
    if (this.active && (this.controller || this.snapshot.error)) {
      // Record the invalidation instead of dropping it. Repeated calls coalesce
      // into one queued refresh that uses the latest reader.
      this.queuedRefresh = reader;
      console.debug("delidev.pagination.refresh_queued", {
        busy: this.snapshot.loading ?? null,
        failed: Boolean(this.snapshot.error),
      });
      return Promise.resolve();
    }
    return this.run(
      this.snapshot.loaded ? ReadStage.Refresh : ReadStage.Initial,
      "",
      reader,
    );
  }
  append(reader: PaginationReader<Row, Payload>) {
    if (
      !this.snapshot.loaded ||
      !this.snapshot.nextPageToken ||
      this.snapshot.error
    )
      return Promise.resolve();
    return this.run(ReadStage.Additional, this.snapshot.nextPageToken, reader);
  }
  retry(reader: PaginationReader<Row, Payload>) {
    const error = this.snapshot.error;
    if (
      !error ||
      error.failure.code === FailureCode.CursorExpired ||
      error.stalled
    )
      return Promise.resolve();
    return this.run(
      error.stage,
      error.token,
      reader,
      error.stage === ReadStage.Refresh || error.stage === ReadStage.Restore,
    );
  }
  reload(reader: PaginationReader<Row, Payload>) {
    return this.run(ReadStage.Reload, "", reader);
  }
  // Heights are presentation measurements, never authority for a domain read.
  measure(token: string, height: number) {
    if (!Number.isFinite(height) || height < 0) return;
    const index = this.snapshot.pages.findIndex((page) => page.token === token);
    if (index < 0 || this.snapshot.pages[index].height === height) return;
    const pages = [...this.snapshot.pages];
    pages[index] = { ...pages[index], height };
    this.publish({ ...this.snapshot, pages });
  }
  restore(token: string, reader: PaginationReader<Row, Payload>) {
    if (
      this.snapshot.error ||
      !this.snapshot.pages.some((page) => page.token === token) ||
      this.snapshot.payloadPages.some((page) => page.token === token)
    )
      return Promise.resolve();
    return this.run(ReadStage.Restore, token, reader, true);
  }
  private async run(
    stage: ReadStage,
    token: string,
    reader: PaginationReader<Row, Payload>,
    retryOnly = false,
  ) {
    if (!this.active || this.controller) return;
    const controller = new AbortController();
    const generation = this.generation;
    const previous = this.snapshot;
    let completed = false;
    this.controller = controller;
    this.publish({ ...previous, loading: stage, error: undefined });
    let currentToken = token;
    try {
      const refreshing = stage === ReadStage.Refresh;
      const resumed = refreshing && retryOnly ? this.pendingRefresh : undefined;
      const start = resumed?.pages.length ?? 0;
      const pages: PaginationPage<Row>[] = resumed
        ? [...resumed.pages]
        : stage === ReadStage.Additional || (retryOnly && !refreshing)
          ? [...previous.pages]
          : [];
      if (stage === ReadStage.Reload || stage === ReadStage.Initial)
        this.pendingRefresh = undefined;
      const payloads = new Map(
        previous.payloadPages.map((page) => [page.token, page.payload]),
      );
      const refreshWindow = new Set(
        resumed?.window ??
          (previous.payloadPages.length
            ? previous.payloadPages.map((page) => page.token)
            : this.retainedPayloadTokens.length
              ? this.retainedPayloadTokens
              : previous.pages.slice(-3).map((page) => page.token)),
      );
      if (stage === ReadStage.Reload || stage === ReadStage.Initial)
        payloads.clear();
      if (resumed)
        for (const page of resumed.pages) payloads.delete(page.token);
      if (refreshing)
        this.pendingRefresh = { pages: [...pages], window: [...refreshWindow] };
      const retryIndex = retryOnly
        ? pages.findIndex((page) => page.token === token)
        : -1;
      const seen = new Set(
        (refreshing
          ? pages
          : retryOnly
            ? pages.slice(0, Math.max(0, retryIndex))
            : pages
        ).map((page) => page.token),
      );
      // Refresh the accepted request tokens verbatim. Changed boundaries,
      // including a formerly exhausted tail, require explicit Reload list.
      const count = refreshing ? previous.pages.length - start : 1;
      for (let index = 0; index < count; index++) {
        const acceptedPage = refreshing
          ? previous.pages[start + index]
          : retryOnly
            ? pages[retryIndex]
            : undefined;
        if (refreshing) currentToken = acceptedPage!.token;
        const batch = await reader(currentToken, controller.signal);
        if (
          !this.active ||
          generation !== this.generation ||
          controller.signal.aborted
        )
          return;
        // Cursors are opaque and the server renews their expiry on each read.
        // Compare the visible range end and continuation presence, then retain
        // the original tokens so refresh cannot discover a different range.
        const restoredRangeChanged =
          stage === ReadStage.Restore &&
          acceptedPage &&
          (batch.rows.length !== acceptedPage.rows.length ||
            batch.rows.some(
              (row, index) => row.id !== acceptedPage.rows[index].id,
            ));
        const boundaryChanged =
          restoredRangeChanged ||
          (acceptedPage &&
            (Boolean(batch.nextPageToken) !==
              Boolean(acceptedPage.nextPageToken) ||
              (Boolean(acceptedPage.nextPageToken) &&
                batch.rows.at(-1)?.id !== acceptedPage.rows.at(-1)?.id)));
        if ((refreshing || retryOnly) && (!acceptedPage || boundaryChanged)) {
          this.pendingRefresh = undefined;
          console.warn("delidev.pagination.read_failed", {
            stage,
            classification: FailureCode.CursorExpired,
          });
          this.publish({
            ...previous,
            loading: undefined,
            error: {
              stage,
              token: currentToken,
              failure: {
                code: FailureCode.CursorExpired,
                message: "The loaded list boundaries changed.",
                guidance: "Reload this list to accept a new chain.",
              },
            },
          });
          return;
        }
        seen.add(currentToken);
        if (batch.nextPageToken && seen.has(batch.nextPageToken)) {
          console.warn("delidev.pagination.read_failed", {
            stage,
            classification: "non-advancing-continuation",
          });
          this.publish({
            ...previous,
            loading: undefined,
            error: {
              stage,
              token: currentToken,
              stalled: true,
              failure: {
                code: FailureCode.Internal,
                message: "The list continuation did not advance.",
                guidance: "Reload this list to resume navigation.",
              },
            },
          });
          return;
        }
        // The accepted read is the only point where an evictable page's
        // documents are still available to domain projections.
        try {
          batch.accepted?.();
        } catch {
          console.warn("delidev.pagination.accepted_observer_failed", {
            stage,
          });
        }
        const acceptedBatch = {
          rows: batch.rows,
          nextPageToken: acceptedPage?.nextPageToken ?? batch.nextPageToken,
          token: currentToken,
          height: acceptedPage?.height,
          rawCount: batch.rawCount,
        };
        // Full domain payloads never enter retained page boundaries. Keep at
        // most three reached pages around the latest requested visible page.
        if (batch.payload && (!refreshing || refreshWindow.has(currentToken)))
          payloads.set(currentToken, batch.payload);
        if (!refreshing) {
          const center = retryOnly
            ? retryIndex
            : refreshing
              ? previous.pages.findIndex((page) => page.token === currentToken)
              : pages.length;
          const closest = pages
            .map((page, index) => ({
              token: page.token,
              distance: Math.abs(index - center),
            }))
            .filter(
              (page) => page.token !== currentToken && payloads.has(page.token),
            )
            .sort((a, b) => a.distance - b.distance)
            .slice(
              0,
              this.protectedToken !== undefined &&
                this.protectedToken !== currentToken &&
                payloads.has(this.protectedToken)
                ? 1
                : 2,
            )
            .map((page) => page.token);
          const keep = new Set([
            currentToken,
            ...closest,
            ...(this.protectedToken !== undefined ? [this.protectedToken] : []),
          ]);
          for (const key of payloads.keys())
            if (!keep.has(key)) payloads.delete(key);
        }
        if (retryOnly && !refreshing) pages[retryIndex] = acceptedBatch;
        else pages.push(acceptedBatch);
        if (refreshing)
          this.pendingRefresh = {
            pages: [...pages],
            window: [...refreshWindow],
          };
        if (!batch.nextPageToken) break;
        currentToken = batch.nextPageToken;
      }
      this.pendingRefresh = undefined;
      this.retainedPayloadTokens = [...payloads.keys()];
      for (const page of pages)
        page.height =
          this.snapshot.pages.find(
            (previousPage) => previousPage.token === page.token,
          )?.height ?? page.height;
      this.publish({
        rows: uniqueRows(pages),
        pages,
        payloadPages: [...payloads].map(([token, payload]) => ({
          token,
          payload,
        })),
        loaded: true,
        nextPageToken: pages.at(-1)?.nextPageToken ?? "",
      });
      completed = true;
    } catch (error) {
      if (
        !this.active ||
        generation !== this.generation ||
        controller.signal.aborted
      )
        return;
      // Only safe typed failure fields are retained; arbitrary exception text,
      // endpoint values and opaque continuation tokens never enter diagnostics.
      const failure = clientFailure(error);
      console.warn("delidev.pagination.read_failed", {
        stage,
        classification: failure.code,
      });
      this.publish({
        ...previous,
        loading: undefined,
        error: { stage, token: currentToken, failure },
      });
    } finally {
      if (this.controller === controller) this.controller = undefined;
    }
    // Only a successfully published read honors the queued invalidation, and
    // only for the same live owner generation.
    if (completed && this.active && generation === this.generation) {
      const queued = this.queuedRefresh;
      this.queuedRefresh = undefined;
      if (queued) await this.refresh(queued);
    }
  }
}
