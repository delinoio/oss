// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceService, SessionService, type Resource } from "@delinoio/delidev-api-client";
import { appendPage, ConversationLane, emptyPages, type ConversationPage, type ConversationPages } from "./conversation-pages";

export function useConversationPages(lane: ConversationLane, sessionId: string, enabled: boolean) {
  const transport = useTransport(), cache = useQueryClient();
  const key = ["mobile-conversation-pages", lane, sessionId];
  const reached = useRef(1), generation = useRef(0), scope = useRef(new AbortController());
  const current = useRef(enabled); current.current = enabled;
  const [continuing, setContinuing] = useState(false), [continuationError, setContinuationError] = useState(false);
  const read = async (token: string, signal: AbortSignal): Promise<ConversationPage> => {
    if (lane === ConversationLane.Queue) {
      const result = await createClient(SessionService, transport).listQueue({ sessionId, pageSize: 50, pageToken: token }, { signal });
      return { resources: result.inputs, nextPageToken: result.nextPageToken };
    }
    return createClient(ResourceService, transport).listResources({ filter: { kind: EntityKind.INTERACTION, sessionId, pageSize: 50, pageToken: token } }, { signal });
  };
  const query = useQuery({ queryKey: key, enabled, retry: false, queryFn: async ({ signal }) => {
    // Refresh the reached chain atomically with the server's new exact cursors.
    let pages = emptyPages();
    try {
      for (let index = 0; index < reached.current; index++) {
        pages = appendPage(pages, await read(pages.nextPageToken, signal), pages.nextPageToken, lane, sessionId);
        if (!pages.nextPageToken) break;
      }
      return pages;
    } catch (error) {
      if (!signal.aborted) console.warn({ component: "mobile-conversation-pages", lane, operation: "refresh", outcome: "incomplete" });
      throw error;
    }
  } });
  useEffect(() => {
    generation.current++;
    scope.current.abort(); scope.current = new AbortController();
    if (!enabled) { reached.current = 1; setContinuing(false); setContinuationError(false); }
    return () => { generation.current++; scope.current.abort(); };
  }, [enabled, sessionId, lane]);
  const latest = useRef({ data: query.data, fetching: query.isFetching, error: query.isError, continuing, continuationError });
  latest.current = { data: query.data, fetching: query.isFetching, error: query.isError, continuing, continuationError };
  const ready = () => current.current && !scope.current.signal.aborted && !!latest.current.data && !latest.current.fetching && !latest.current.error && !latest.current.continuing && !latest.current.continuationError && !latest.current.data.incomplete;
  const more = async () => {
    const previous = latest.current.data;
    if (!current.current || !previous?.nextPageToken || latest.current.fetching || latest.current.continuing) return;
    const originalGeneration = generation.current, controller = scope.current;
    // Lock synchronously so repeated clicks cannot create competing page reads.
    latest.current.continuing = true; setContinuing(true); setContinuationError(false);
    try {
      const result = await cache.fetchQuery({ queryKey: [...key, "continuation", previous.nextPageToken], retry: false, staleTime: 0, gcTime: 0, queryFn: ({ signal }) => read(previous.nextPageToken, AbortSignal.any([signal, controller.signal])) });
      if (!current.current || generation.current !== originalGeneration || controller.signal.aborted) return;
      const pages = appendPage(previous, result, previous.nextPageToken, lane, sessionId);
      // A synchronization refresh owns the new chain; never append to replaced data.
      if (cache.getQueryData(key) !== previous) return;
      reached.current = pages.tokens.length;
      cache.setQueryData<ConversationPages>(key, pages);
    } catch {
      if (current.current && generation.current === originalGeneration) {
        console.warn({ component: "mobile-conversation-pages", lane, operation: "continuation", outcome: "incomplete" });
        setContinuationError(true);
      }
    } finally {
      if (generation.current === originalGeneration) { latest.current.continuing = false; setContinuing(false); }
    }
  };
  const refresh = () => { if (!current.current || scope.current.signal.aborted || latest.current.continuing) return; setContinuationError(false); void query.refetch(); };
  return {
    resources: enabled ? query.data?.resources ?? [] : [],
    nextPageToken: enabled ? query.data?.nextPageToken ?? "" : "",
    loading: query.isFetching || continuing,
    error: query.isError || continuationError || !!query.data?.incomplete,
    loaded: enabled && !!query.data,
    more,
    refresh,
    retry: () => continuationError ? void more() : refresh(),
    canWrite: (resource: Resource) => ready() && !!latest.current.data?.resources.some(original => original.id === resource.id && original.revision === resource.revision),
    signal: () => scope.current.signal,
  };
}
