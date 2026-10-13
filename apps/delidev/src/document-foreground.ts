// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";

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

/** Tracks the live visibility and focus of the current document only. Another
 * connection window has its own document, so it never inherits this state. */
export function useDocumentForeground(): DocumentForegroundState {
  const [state, setState] = useState<DocumentForegroundState>(() => ({
    foreground: isDocumentForeground(),
    activation: 0,
  }));
  useEffect(() => {
    const update = () => {
      const foreground = isDocumentForeground();
      setState((previous) =>
        previous.foreground === foreground
          ? previous
          : {
              foreground,
              activation: previous.activation + (foreground ? 1 : 0),
            },
      );
    };
    update();
    document.addEventListener("visibilitychange", update);
    window.addEventListener("focus", update);
    window.addEventListener("blur", update);
    return () => {
      document.removeEventListener("visibilitychange", update);
      window.removeEventListener("focus", update);
      window.removeEventListener("blur", update);
    };
  }, []);
  return state;
}
