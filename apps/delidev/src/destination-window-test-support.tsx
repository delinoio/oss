import type { ReactNode } from "react";
import { vi } from "vitest";
import {
  DestinationWindowProvider,
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
 * observing the typed route each control would send.
 */
export function fakeDestinationWindow() {
  const opened = vi.fn<(route: ProductRoute) => void>();
  const controller: DestinationWindowController = {
    failed: false,
    dismiss: () => {},
    claim: (event) => {
      if (navigationDisposition(event) !== NavigationDisposition.NewWindow)
        return false;
      event.preventDefault();
      event.stopPropagation();
      return true;
    },
    run: (task) => task(),
    open: async (route) => {
      opened(route);
    },
  };
  return {
    opened,
    controller,
    wrap: (children: ReactNode) => (
      <DestinationWindowProvider value={controller}>
        {children}
      </DestinationWindowProvider>
    ),
  };
}

/**
 * Click init for a platform-primary pointer click under jsdom, whose empty
 * `navigator.platform` is a non-macOS platform (Control is primary).
 */
export const primaryClick = { ctrlKey: true, detail: 1 };
