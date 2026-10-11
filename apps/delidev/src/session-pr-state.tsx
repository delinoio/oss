// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo } from "react";
import { createPortal } from "react-dom";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  IntegrationQuery,
  ResourceQuery,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, encode, object, text, type Document } from "./documents";
import { githubResult } from "./github-items";
import { ItemKind, QueryOperation, uuid } from "./github-query-model";
import { copy, useLocale } from "./localization";
import { useSessionActive } from "./session-activity";
import { Timestamp } from "./timestamp-display";

export enum SessionPRState {
  Neutral = "neutral",
  Open = "open",
  Merged = "merged",
  Closed = "closed",
}

/** One limiter belongs to one mounted original-session association reader.
 * Queued reads leave without consuming a slot when their row is disposed. */
export class PRReadLimiter {
  private running = 0;
  private queue: Array<() => void> = [];
  async read<T>(signal: AbortSignal, read: () => Promise<T>): Promise<T> {
    signal.throwIfAborted();
    await new Promise<void>((resolve, reject) => {
      const start = () => {
        signal.removeEventListener("abort", abort);
        this.running++;
        resolve();
      };
      const abort = () => {
        this.queue = this.queue.filter((entry) => entry !== start);
        reject(signal.reason);
      };
      if (this.running < 4) start();
      else {
        this.queue.push(start);
        signal.addEventListener("abort", abort, { once: true });
      }
    });
    try {
      signal.throwIfAborted();
      return await read();
    } finally {
      this.running--;
      this.queue.shift()?.();
    }
  }
}

export function sessionPRState(
  result: Document,
  association: Document,
): SessionPRState {
  const repository = object(result.repository);
  const item = object(
    Array.isArray(result.items) && result.items.length === 1
      ? result.items[0]
      : undefined,
  );
  if (
    repository.id !== association.remote_repository_id ||
    repository.node_id !== association.repository_node_id ||
    text(repository.owner).toLowerCase() !==
      text(association.owner).toLowerCase() ||
    text(repository.name).toLowerCase() !==
      text(association.name).toLowerCase() ||
    item.id !== association.pull_request_id ||
    item.node_id !== association.pull_request_node_id ||
    item.number !== association.number ||
    typeof item.merged !== "boolean" ||
    !["open", "closed"].includes(text(item.state)) ||
    (item.merged && item.state !== "closed")
  )
    return SessionPRState.Neutral;
  return item.merged
    ? SessionPRState.Merged
    : item.state === "open"
      ? SessionPRState.Open
      : SessionPRState.Closed;
}

export function SessionPRStatus({
  row,
  association,
  limiter,
  active: visible,
  diagnosticsTarget,
}: {
  row: Resource;
  association: Document;
  limiter: PRReadLimiter;
  active: boolean;
  diagnosticsTarget?: HTMLElement | null;
}) {
  useLocale();
  const active = useSessionActive() && visible;
  const transport = useTransport();
  const client = useQueryClient();
  const repositoryId = text(association.repository_id);
  const query = useMemo(
    () => ({
      kind: ItemKind.PullRequest,
      operation: QueryOperation.Detail,
      number: text(association.number),
    }),
    [association.number],
  );
  const request = useMemo(
    () => ({ repositoryId, schemaVersion: 1, queryJson: encode(query) }),
    [repositoryId, query],
  );
  const options = useMemo(
    () =>
      createQueryOptions(IntegrationQuery.queryRepositoryIntegration, request, {
        transport,
      }),
    [request, transport],
  );
  const key = useMemo(
    () => [
      ...options.queryKey,
      {
        sessionPR: row.id,
        revision: row.revision.toString(),
        sessionId: row.sessionId,
      },
    ],
    [options, row.id, row.revision, row.sessionId],
  );
  const result = useQuery({
    queryKey: key,
    queryFn: (context) =>
      limiter.read(context.signal, async () => {
        const repositoryOptions = createQueryOptions(
          ResourceQuery.getResource,
          { kind: EntityKind.REPOSITORY, id: repositoryId },
          { transport },
        );
        const selected = (
          await repositoryOptions.queryFn!({
            ...context,
            queryKey: repositoryOptions.queryKey,
          })
        ).resource;
        context.signal.throwIfAborted();
        const config = document(selected);
        if (
          !selected ||
          selected.id !== repositoryId ||
          selected.kind !== EntityKind.REPOSITORY ||
          selected.schemaVersion !== 1 ||
          selected.projectId !== row.projectId ||
          selected.revision <= 0n ||
          selected.revision >= 1n << 63n ||
          !uuid(config.integration_id) ||
          text(config.github_owner).toLowerCase() !==
            text(association.owner).toLowerCase() ||
          text(config.github_name).toLowerCase() !==
            text(association.name).toLowerCase()
        )
          throw new Error("Original PR repository unavailable");
        const reply = await options.queryFn!({
          ...context,
          queryKey: options.queryKey,
        });
        context.signal.throwIfAborted();
        const validated =
          reply.schemaVersion === 1
            ? githubResult(reply.documentJson, selected, query)
            : undefined;
        if (
          !validated ||
          sessionPRState(validated, association) === SessionPRState.Neutral
        )
          throw new Error("Original PR detail identity or state unavailable");
        return {
          state: sessionPRState(validated, association),
          observedAt: text(validated.observed_at),
        };
      }),
    enabled: active,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: "always",
  });
  useEffect(() => {
    if (!active) void client.cancelQueries({ queryKey: key, exact: true });
    return () => {
      void client.cancelQueries({ queryKey: key, exact: true });
    };
  }, [active, client, key]);
  const state =
    active && !result.isFetching && !result.error
      ? (result.data?.state ?? SessionPRState.Neutral)
      : SessionPRState.Neutral;
  const label = copy(
    state === SessionPRState.Open
      ? "session.prStateOpen"
      : state === SessionPRState.Merged
        ? "session.prStateMerged"
        : state === SessionPRState.Closed
          ? "session.prStateClosed"
          : "session.prStateUnavailable",
  );
  return (
    <>
      <span
        className="session-pr-icon"
        role="img"
        aria-label={label}
        title={label}
      >
        <svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true">
          <path
            fill="currentColor"
            d="M5 3.5a2 2 0 1 0-2.75 1.85v5.3a2 2 0 1 0 1.5 0v-5.3A2 2 0 0 0 5 3.5Zm-2-1a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm0 9a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM8 2.75 5.75 5 8 7.25V5.75h1.25c1.1 0 2 .9 2 2v2.9a2 2 0 1 0 1.5 0v-2.9a3.5 3.5 0 0 0-3.5-3.5H8Zm4 8.75a1 1 0 1 1 0 2 1 1 0 0 1 0-2Z"
          />
        </svg>
        <span className={`session-pr-dot session-pr-dot-${state}`} />
      </span>
      {diagnosticsTarget
        ? createPortal(
            <section>
              <h3>
                {copy("session.prStateEvidence", {
                  number: text(association.number),
                })}
              </h3>
              <p>{label}</p>
              {result.data ? (
                <p>
                  <Timestamp value={result.data.observedAt} />
                </p>
              ) : null}
              {result.error ? <p>{copy("session.prStateReadFailed")}</p> : null}
              <button
                disabled={!active || result.isFetching}
                onClick={() => void result.refetch()}
              >
                {copy("session.prRetryRead", {
                  number: text(association.number),
                })}
              </button>
            </section>,
            diagnosticsTarget,
          )
        : null}
    </>
  );
}
