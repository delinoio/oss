// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef } from "react";
import { ReadStage, type PaginationFailure } from "./scroll-pagination";
import { copy } from "./localization";

/** Cached presentation never changes the pagination or mutation authority.
 * Failed reads and their explicit recovery keep normal-flow progress. */
export function useBackgroundQueueRead(query: { loaded: boolean; loading?: ReadStage; error?: PaginationFailure }) {
  const healthy = useRef(false);
  useLayoutEffect(() => {
    if (!query.loading) healthy.current = query.loaded && !query.error;
  }, [query.loaded, query.loading, query.error]);
  return query.loaded && healthy.current && !query.error
    && (query.loading === ReadStage.Refresh || query.loading === ReadStage.Reload);
}

export function QueueRefreshStatus({ refreshing }: { refreshing: boolean }) {
  return <p className="sidebar-sr-only" role={refreshing ? "status" : undefined} aria-live="polite">{refreshing ? copy("session.loadingQueue") : ""}</p>;
}
