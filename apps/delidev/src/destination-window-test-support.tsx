import { useCallback, useMemo, useRef, useState, type ReactNode } from "react";
import { vi } from "vitest";
import {
  DestinationWindowNotice,
  DestinationWindowProvider,
  PAGE_SURFACE,
  type DestinationWindowController,
} from "./destination-window";
import {
  NavigationDisposition,
  navigationDisposition,
} from "./navigation-disposition";
import type { ProductRoute } from "./product-route";

/**
 * A controller whose native side is a spy. It classifies clicks with the real
 * classifier, so call-site tests exercise the same disposition rules while
 * observing the typed route each control would send. `reject` makes the next
 * requests fail the way a native rejection does, and the failure notice is
 * presented by the surface the clicked control belongs to, like the Shell.
 */
export function fakeDestinationWindow() {
  const opened = vi.fn<(route: ProductRoute) => void>();
  const rejection: { current: string | undefined } = { current: undefined };
  function Shell({ children }: { children: ReactNode }) {
    const [failureSurface, setFailureSurface] = useState<string>();
    const surfaces = useRef(new Set<string>());
    // Stable like the Shell's: a changing identity would release and register
    // every surface again each time a failure is shown.
    const register = useCallback((surface: string) => {
      surfaces.current.add(surface);
      return () => {
        surfaces.current.delete(surface);
        setFailureSurface((shown) =>
          shown === surface ? PAGE_SURFACE : shown,
        );
      };
    }, []);
    const controller = useMemo<DestinationWindowController>(
      () => ({
        failed: failureSurface !== undefined,
        failureSurface,
        dismiss: () => setFailureSurface(undefined),
        register,
        claim: (event) => {
          if (navigationDisposition(event) !== NavigationDisposition.NewWindow)
            return false;
          event.preventDefault();
          event.stopPropagation();
          return true;
        },
        run: (task) => task(),
        open: async (route, surface = PAGE_SURFACE) => {
          setFailureSurface(undefined);
          opened(route);
          if (rejection.current !== undefined)
            setFailureSurface(
              surfaces.current.has(surface) ? surface : PAGE_SURFACE,
            );
        },
      }),
      [failureSurface, register],
    );
    return (
      <DestinationWindowProvider value={controller}>
        {children}
        <DestinationWindowNotice />
      </DestinationWindowProvider>
    );
  }
  return {
    opened,
    /** Makes every later request fail until called with `undefined`. */
    reject: (code: string | undefined = "permission-denied") => {
      rejection.current = code;
    },
    resolve: () => {
      rejection.current = undefined;
    },
    wrap: (children: ReactNode) => <Shell>{children}</Shell>,
  };
}

/**
 * Click init for a platform-primary pointer click under jsdom, whose empty
 * `navigator.platform` is a non-macOS platform (Control is primary).
 */
export const primaryClick = { ctrlKey: true, detail: 1 };
