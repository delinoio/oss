// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
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
  | "currentTarget"
>;

/**
 * The surface that presents a failure when no registered surface encloses the
 * clicked control: the Shell's main page content.
 */
export const PAGE_SURFACE = "page";

const SURFACE_ATTRIBUTE = "data-destination-surface";

/**
 * Resolves the surface of the control that received a click: the nearest
 * enclosing registered surface (an open modal dialog, the compact drawer or an
 * open popover), otherwise the page. Call it synchronously from the click
 * handler, because the event target is reset after dispatch.
 */
export function destinationSurface(event: DestinationClick): string {
  const target = event.currentTarget;
  if (!(target instanceof Element)) return PAGE_SURFACE;
  return (
    target.closest(`[${SURFACE_ATTRIBUTE}]`)?.getAttribute(SURFACE_ATTRIBUTE) ??
    PAGE_SURFACE
  );
}

export interface DestinationWindowController {
  /** Whether the last operation failed and its localized notice is visible. */
  failed: boolean;
  /**
   * The one surface that presents the visible failure. A modal dialog makes
   * everything outside it inert, so a failure raised from inside a dialog, the
   * compact drawer or a popover is presented inside that surface.
   */
  failureSurface: string | undefined;
  dismiss: () => void;
  /**
   * Registers a mounted surface and returns its release. Releasing a surface
   * moves a failure it presents to the page, and a failure raised after its
   * surface is gone is presented by the page, so a failure is never left
   * inside a surface that no longer exists.
   */
  register: (surface: string) => () => void;
  /**
   * Returns true when this click selects a new window. A handled click has its
   * default action and propagation suppressed so no second navigation runs.
   */
  claim: (event: DestinationClick) => boolean;
  /**
   * Runs one destination operation under pending admission. A second request
   * while one is pending is dropped, never queued or retried.
   */
  run: (task: () => Promise<void>, surface?: string) => Promise<void>;
  /** Opens a typed route in a new window under pending admission. */
  open: (route: ProductRoute, surface?: string) => Promise<void>;
}

const inert: DestinationWindowController = {
  failed: false,
  failureSurface: undefined,
  dismiss: () => {},
  register: () => () => {},
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
  const surfaces = useRef(new Set<string>());
  // The surface presenting the visible failure; undefined when none is shown.
  const [failureSurface, setFailureSurface] = useState<string>();
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const run = useCallback(
    async (task: () => Promise<void>, surface = PAGE_SURFACE) => {
      if (pending.current) {
        console.info("delidev.destination_window", { phase: "dropped" });
        return;
      }
      pending.current = true;
      if (mounted.current) setFailureSurface(undefined);
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
        if (mounted.current)
          setFailureSurface(
            surfaces.current.has(surface) ? surface : PAGE_SURFACE,
          );
      } finally {
        pending.current = false;
      }
    },
    [],
  );
  const open = useCallback(
    (route: ProductRoute, surface?: string) =>
      run(() => openDestinationWindow(route), surface),
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
  const dismiss = useCallback(() => setFailureSurface(undefined), []);
  const register = useCallback((surface: string) => {
    surfaces.current.add(surface);
    return () => {
      surfaces.current.delete(surface);
      if (mounted.current)
        setFailureSurface((shown) =>
          shown === surface ? PAGE_SURFACE : shown,
        );
    };
  }, []);
  const failed = failureSurface !== undefined;
  return useMemo(
    () => ({ failed, failureSurface, dismiss, register, claim, run, open }),
    [failed, failureSurface, dismiss, register, claim, run, open],
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
        void controller.open(route, destinationSurface(event));
        return;
      }
      current();
    },
    [controller],
  );
}

/**
 * Operation-scoped failure of a destination window request, presented only by
 * the surface the failing control belongs to.
 */
export function DestinationWindowNotice({
  surface = PAGE_SURFACE,
}: {
  surface?: string;
}) {
  useLocale();
  const { failureSurface, dismiss } = useDestinationWindow();
  if (failureSurface !== surface) return null;
  return (
    <div className="sidebar-preference-notice">
      <p role="alert">{copy("destination-window.failed")}</p>
      <button type="button" onClick={dismiss}>
        {copy("destination-window.dismiss")}
      </button>
    </div>
  );
}

/**
 * Registers an enclosing modal dialog, compact drawer or popover as a failure
 * surface. Spread `props` on its element and render `notice` inside it, so a
 * failure from a control within it stays reachable while the rest of the
 * document is inert. An inactive surface (a non-modal presentation of the same
 * element) registers nothing, and the failure stays on the page.
 */
export function useDestinationSurface(active = true) {
  const id = useId();
  const { register } = useDestinationWindow();
  useEffect(() => (active ? register(id) : undefined), [active, id, register]);
  return {
    props: active ? { [SURFACE_ATTRIBUTE]: id } : {},
    notice: active ? <DestinationWindowNotice surface={id} /> : null,
  };
}
