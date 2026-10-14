// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useState } from "react";

/** App use means a visible, focused product document, regardless of the page,
 * popover or drawer that is currently open. */
export function isDocumentForeground() {
  return document.visibilityState === "visible" && document.hasFocus();
}

export interface DocumentForegroundState {
  foreground: boolean;
  /** Increments on every return to the foreground, even when blur and focus
   * are batched into one render, so owners can restart a fresh wait. */
  activation: number;
}

export interface DocumentForegroundTracker extends DocumentForegroundState {
  /** Reconciles a synchronous foreground observation when an event was missed. */
  reconcile: () => void;
}

/** Tracks the live visibility and focus of the current document only. Another
 * connection window has its own document, so it never inherits this state. */
export function useDocumentForeground(): DocumentForegroundTracker {
  const [state, setState] = useState<DocumentForegroundState>(() => ({
    foreground: isDocumentForeground(),
    activation: 0,
  }));
  const reconcile = useCallback(() => {
    const foreground = isDocumentForeground();
    setState((previous) =>
      previous.foreground === foreground
        ? previous
        : {
            foreground,
            activation: previous.activation + (foreground ? 1 : 0),
          },
    );
  }, []);
  useEffect(() => {
    reconcile();
    document.addEventListener("visibilitychange", reconcile);
    window.addEventListener("focus", reconcile);
    window.addEventListener("blur", reconcile);
    return () => {
      document.removeEventListener("visibilitychange", reconcile);
      window.removeEventListener("focus", reconcile);
      window.removeEventListener("blur", reconcile);
    };
  }, [reconcile]);
  return { ...state, reconcile };
}
