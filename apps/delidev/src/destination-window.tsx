// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
} from "react";
import { copy, useLocale } from "./localization";
import {
  NavigationDisposition,
  destinationWindowAvailable,
  destinationWindowFailure,
  navigationDisposition,
  openDestinationWindow,
} from "./navigation-disposition";
import type { ProductRoute } from "./product-route";

/** Click fields the controller needs; React and DOM mouse events both fit. */
export type DestinationClick = Pick<
  MouseEvent,
  | "button"
  | "detail"
  | "metaKey"
  | "ctrlKey"
  | "altKey"
  | "shiftKey"
  | "preventDefault"
  | "stopPropagation"
>;

export interface DestinationWindowController {
  /** Whether the last operation failed and its localized notice is visible. */
  failed: boolean;
  dismiss: () => void;
  /**
   * Returns true when this click selects a new window. A handled click has its
   * default action and propagation suppressed so no second navigation runs.
   */
  claim: (event: DestinationClick) => boolean;
  /**
   * Runs one destination operation under pending admission. A second request
   * while one is pending is dropped, never queued or retried.
   */
  run: (task: () => Promise<void>) => Promise<void>;
  /** Opens a typed route in a new window under pending admission. */
  open: (route: ProductRoute) => Promise<void>;
}

const inert: DestinationWindowController = {
  failed: false,
  dismiss: () => {},
  claim: () => false,
  run: async () => {},
  open: async () => {},
};

const DestinationWindowContext =
  createContext<DestinationWindowController>(inert);

export const DestinationWindowProvider = DestinationWindowContext.Provider;

/** The Shell owns one controller: its pending admission and failure notice. */
export function useDestinationWindowController(): DestinationWindowController {
  const pending = useRef(false);
  const mounted = useRef(true);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const run = useCallback(async (task: () => Promise<void>) => {
    if (pending.current) {
      console.info("delidev.destination_window", { phase: "dropped" });
      return;
    }
    pending.current = true;
    if (mounted.current) setFailed(false);
    console.info("delidev.destination_window", { phase: "requested" });
    try {
      await task();
      console.info("delidev.destination_window", { phase: "settled" });
    } catch (error) {
      // Only the phase and the closed failure code are logged, never the
      // destination, IDs or titles.
      console.warn("delidev.destination_window", {
        phase: "failed",
        code: destinationWindowFailure(error),
      });
      if (mounted.current) setFailed(true);
    } finally {
      pending.current = false;
    }
  }, []);
  const open = useCallback(
    (route: ProductRoute) => run(() => openDestinationWindow(route)),
    [run],
  );
  const claim = useCallback((event: DestinationClick) => {
    if (
      !destinationWindowAvailable() ||
      navigationDisposition(event) !== NavigationDisposition.NewWindow
    )
      return false;
    event.preventDefault();
    event.stopPropagation();
    return true;
  }, []);
  const dismiss = useCallback(() => setFailed(false), []);
  return useMemo(
    () => ({ failed, dismiss, claim, run, open }),
    [failed, dismiss, claim, run, open],
  );
}

export function useDestinationWindow(): DestinationWindowController {
  return useContext(DestinationWindowContext);
}

/**
 * Returns the one dispatcher destination controls call from their click
 * handlers. A new-window click opens `route` in a new window and nothing else;
 * every other activation runs `current`, the control's existing navigation.
 */
export function useDestinationDispatch() {
  const controller = useDestinationWindow();
  return useCallback(
    (
      event: DestinationClick,
      route: ProductRoute | undefined,
      current: () => void,
    ) => {
      if (route && controller.claim(event)) {
        void controller.open(route);
        return;
      }
      current();
    },
    [controller],
  );
}

/** Operation-scoped failure of a destination window request. */
export function DestinationWindowNotice() {
  useLocale();
  const { failed, dismiss } = useDestinationWindow();
  if (!failed) return null;
  return (
    <div className="sidebar-preference-notice">
      <p role="alert">{copy("destination-window.failed")}</p>
      <button type="button" onClick={dismiss}>
        {copy("destination-window.dismiss")}
      </button>
    </div>
  );
}
