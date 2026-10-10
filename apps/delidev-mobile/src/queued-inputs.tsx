// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { EntityKind, SessionQuery, SteerQueuedInputRequestSchema, requireEntityId, supportsResourceSchema, type Resource, type SteerQueuedInputRequest } from "@delinoio/delidev-api-client";
import { documentOf, uuid } from "./state";
import type { Labels } from "./localization";
import { acceptQueuePage, emptyQueue, reachedQueue, queueContinuation, queuePageSize } from "./queue-pagination";

interface Props {
  id: string; enabled: boolean; available: boolean; execution: string; turn: string; labels: Labels;
  steer: (request: SteerQueuedInputRequest) => void;
}
function validQueuedInput(row: Resource, sessionId: string): boolean {
  try {
    requireEntityId(row.id);
    const doc = documentOf(row.documentJson);
    return row.kind === EntityKind.QUEUE && row.sessionId === sessionId && row.revision > 0n && supportsResourceSchema(row) && doc.delivery === "queued" && typeof doc.prompt === "string";
  } catch { return false; }
}
export function QueuedInputs(props: Props) {
  const transport = useTransport();
  const owner = useMemo(() => uuid(), [transport, props.id, props.enabled]);
  const [reload, setReload] = useState(0);
  // A different authenticated read owner or foreground lifetime cannot inherit
  // pages or callbacks. Reload starts a fresh first-page read, not a mutation.
  return <QueueRead key={`${owner}/${reload}`} {...props} reload={() => setReload(value => value + 1)} />;
}
function QueueRead({ id, enabled, available, execution, turn, labels: c, steer, reload }: Props & { reload: () => void }) {
  const [token, setToken] = useState("");
  const [pages, setPages] = useState(emptyQueue);
  const live = useRef(true);
  useEffect(() => { live.current = true; return () => { live.current = false; }; }, []);
  const query = useQuery(SessionQuery.listQueue, { sessionId: id, pageSize: queuePageSize, pageToken: token }, { enabled, retry: false, staleTime: 0, refetchOnMount: "always" });
  const [proof, setProof] = useState<{ data: typeof query.data; updatedAt: number; token: string }>();
  useEffect(() => {
    if (!enabled || query.isFetching || query.isError || !query.data) return;
    setPages(previous => acceptQueuePage(previous, token, query.data!.inputs, query.data!.nextPageToken, row => validQueuedInput(row, id)));
    setProof({ data: query.data, updatedAt: query.dataUpdatedAt, token });
  }, [id, enabled, token, query.data, query.dataUpdatedAt, query.isFetching, query.isError]);
  const rows = reachedQueue(pages), next = queueContinuation(pages);
  const failed = pages.invalid || query.isError;
  useEffect(() => {
    if (failed) console.warn("mobile_queue_read", { operation: "list-queue", outcome: "incomplete" });
  }, [failed]);
  const ready = live.current && enabled && !query.isFetching && !failed && !!proof && proof.data === query.data && proof.updatedAt === query.dataUpdatedAt && proof.token === token && pages.pages.some(page => page.token === token);
  return <section aria-label={c.queue}>
    <h3>{c.queue}</h3>
    {enabled && query.isFetching ? <p role="status">{c.loading}</p> : null}
    {failed ? <p role="alert">{c.queueIncomplete}</p> : null}
    {rows.length === 0 && ready && !next ? <p>{c.empty}</p> : null}
    {rows.map(input => <article key={input.id}>
      <p>{String(documentOf(input.documentJson).prompt)}</p>
      <button disabled={!available || !ready || !execution || !turn} onClick={() => {
        if (!live.current || !available || !ready || !execution || !turn) return;
        steer(create(SteerQueuedInputRequestSchema, { mutation: { id: input.id, expectedRevision: input.revision, requestId: uuid() }, sessionId: id, expectedExecutionId: execution, expectedTurnId: turn }));
      }}>{c.steer}</button>
    </article>)}
    {next && !pages.invalid ? <button disabled={!enabled || query.isFetching || query.isError} onClick={() => setToken(next)}>{c.more}</button> : null}
    {query.isError ? <button disabled={!enabled || query.isFetching} onClick={() => { void query.refetch(); }}>{c.refresh}</button> : null}
    <button disabled={!enabled || query.isFetching} onClick={reload}>{c.queueReload}</button>
  </section>;
}
