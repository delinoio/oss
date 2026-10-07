// SPDX-License-Identifier: Apache-2.0
import { type DescMessage, type DescMethodUnary, type MessageInitShape, type MessageShape } from "@bufbuild/protobuf";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { newRequestId } from "@delinoio/delidev-api-client";
import { useLayoutEffect, useMemo, useSyncExternalStore } from "react";
import { PaginationChain, type PaginationBatch, type PaginationReader, type PaginationRow } from "./scroll-pagination";

/** scopeKey contains stable connection/query/domain identity, never locale or
 * geometry. Explicit Load/Search/Apply callers set initialRead to false. */
export function usePaginationChain<Row extends PaginationRow, Payload = never>(
  scopeKey: string, active: boolean, reader: PaginationReader<Row, Payload>, initialRead = true,
) {
  const chain = useMemo(() => new PaginationChain<Row, Payload>(), [scopeKey]);
  useLayoutEffect(() => {
    if (active) {
      chain.activate();
      if (initialRead) void chain.refresh(reader);
    }
    return () => chain.suspend();
  }, [chain, active, reader, initialRead]);
  const snapshot = useSyncExternalStore(chain.subscribe, chain.getSnapshot);
  const actions = useMemo(() => ({
    append: () => { void chain.append(reader); },
    retry: () => { void chain.retry(reader); },
    reload: () => { void chain.reload(reader); },
    refresh: () => { void chain.refresh(reader); },
    restore: (token: string) => { void chain.restore(token, reader); },
    measure: (token: string, height: number) => chain.measure(token, height),
    protect: (token?: string) => chain.protect(token),
  }), [chain, reader]);
  return { ...snapshot, ...actions };
}

/** A generated Connect Query read uses a disposable cache key so a bounded
 * full response cannot alias ordinary inventory queries or survive disposal. */
export function useConnectPaginationReader<I extends DescMessage, O extends DescMessage, Row extends PaginationRow, Payload = never>(
  method: DescMethodUnary<I, O>, request: (token: string) => MessageInitShape<I>,
  project: (response: MessageShape<O>, token: string) => PaginationBatch<Row, Payload>,
): PaginationReader<Row, Payload> {
  const transport = useTransport();
  const client = useQueryClient();
  return useMemo(() => async (token, signal) => {
    const options = createQueryOptions(method, request(token), { transport });
    const queryKey = [...options.queryKey, { scrollPaginationBatch: newRequestId() }];
    const abort = () => { void client.cancelQueries({ queryKey, exact: true }); };
    signal.addEventListener("abort", abort, { once: true });
    try {
      if (signal.aborted) throw new DOMException("Read canceled", "AbortError");
      return await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0,
        queryFn: async context => project(await options.queryFn({ ...context, queryKey: options.queryKey }), token) });
    } finally {
      signal.removeEventListener("abort", abort);
      client.removeQueries({ queryKey, exact: true });
    }
  }, [client, transport, method, request, project]);
}

/** Domain cadences/invalidation refresh accepted ranges without reading a new
 * tail. Prefix invalidation reaches this marker while batch caches stay empty. */
export function usePaginationRefresh<I extends DescMessage, O extends DescMessage>(
  method: DescMethodUnary<I, O>, input: MessageInitShape<I>, active: boolean,
  refresh: () => void, interval: number | false = false,
) {
  const transport = useTransport();
  const options = createQueryOptions(method, input, { transport });
  useQuery({ queryKey: [...options.queryKey, { scrollPaginationRefresh: true }],
    queryFn: async () => { refresh(); return null; }, initialData: null, enabled: active,
    refetchOnMount: false, refetchOnWindowFocus: false, refetchIntervalInBackground: false,
    refetchInterval: active ? interval : false, retry: false, staleTime: Infinity, gcTime: 0 });
}
