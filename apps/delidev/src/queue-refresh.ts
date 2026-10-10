// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { ReadStage, type PaginationSnapshot, type PaginationRow } from "./scroll-pagination";

/** Cached queue presentation only. It never establishes current mutation authority. */
export function useQueueBackgroundRead(identity: string, query: Pick<PaginationSnapshot<PaginationRow, unknown>, "loaded" | "loading" | "error" | "pages" | "payloadPages">) {
  const recovery = useRef({ identity, failed: false });
  if (recovery.current.identity !== identity) recovery.current = { identity, failed: false };
  if (query.error) recovery.current.failed = true;
  else if (!query.loading) recovery.current.failed = false;
  // An explicit retry/reload after a failure must retain visible recovery
  // progress. Only healthy reads with complete resident payloads stay quiet.
  return query.loaded && !query.error && !recovery.current.failed
    && query.pages.every(page => query.payloadPages.some(payload => payload.token === page.token))
    && (query.loading === ReadStage.Refresh || query.loading === ReadStage.Reload);
}
