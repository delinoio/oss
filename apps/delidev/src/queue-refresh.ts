// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef } from "react";
import { ReadStage } from "./scroll-pagination";
import type { ScrollContinuationQuery } from "./scroll-continuation";

/** Retained queue presentation is not fresh read or mutation authority. Preserve
 * its geometry only during healthy rereads; explicit failed-boundary recovery
 * remains visible even after the controller clears its error for the attempt. */
export function useQueueRefreshPresentation(query: Pick<ScrollContinuationQuery, "loaded" | "loading" | "error">): boolean {
  const recovering = useRef(false);
  useLayoutEffect(() => {
    if (query.error) recovering.current = true;
    else if (query.loaded && !query.loading) recovering.current = false;
  }, [query.loaded, query.loading, query.error]);
  return query.loaded && !query.error && !recovering.current &&
    (query.loading === ReadStage.Refresh || query.loading === ReadStage.Reload);
}
