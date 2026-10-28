// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";

/** Structural subset of a React Query result used to detect that a refetch finished. */
export type FocusRestoreQuery = {
  data: unknown;
  fetchStatus: "fetching" | "paused" | "idle";
  dataUpdatedAt: number;
  errorUpdatedAt: number;
};

/**
 * Keeps focus on a stable page target after a confirmed deletion removes the control that invoked it.
 * Dialog cleanup first returns focus to the invoking button; once that button leaves the document the
 * page heading takes focus, so keyboard users never fall back to the document body.
 */
export function useRestoreHeadingFocus(query: FocusRestoreQuery) {
  // Timestamp of a confirmed deletion whose focus target may disappear with the list row.
  const [restoreFocus, setRestoreFocus] = useState<number>();
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (restoreFocus === undefined) return;
    const active = document.activeElement;
    if (!active || active === document.body || !active.isConnected) {
      headingRef.current?.focus();
      setRestoreFocus(undefined);
    } else if (
      query.fetchStatus === "idle" &&
      Math.max(query.dataUpdatedAt, query.errorUpdatedAt) >= restoreFocus
    ) {
      setRestoreFocus(undefined);
    }
  }, [
    restoreFocus,
    query.data,
    query.fetchStatus,
    query.dataUpdatedAt,
    query.errorUpdatedAt,
  ]);
  return { headingRef, requestRestore: () => setRestoreFocus(Date.now()) };
}
