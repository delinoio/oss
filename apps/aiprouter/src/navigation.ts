// SPDX-License-Identifier: Apache-2.0
import { startTransition, useEffect, useState } from "react";
import { resolveRoute } from "./routes";
import { t as translate } from "./i18n";

const changed = "console-location-changed";
const leaving = "console-before-navigation";

function canLeave() {
  return window.dispatchEvent(new Event(leaving, { cancelable: true }));
}

export function navigate(
  href: string,
  { replace = false, confirmed = false } = {},
) {
  const url = new URL(href, window.location.href);
  const route = resolveRoute(url);
  if (url.origin !== window.location.origin || !route) return false;
  url.href = route.url.href;
  if (url.href === window.location.href) return true;
  if (!confirmed && !canLeave()) return false;
  window.history[replace ? "replaceState" : "pushState"](null, "", url);
  window.dispatchEvent(new Event(changed));
  return true;
}

export function useBrowserLocation() {
  const [href, setHref] = useState(() => window.location.href);
  useEffect(() => {
    const update = () => {
      const next = window.location.href;
      startTransition(() =>
        setHref((previous) => {
          const before = new URL(previous);
          const after = new URL(next);
          return before.pathname === after.pathname &&
            before.search === after.search
            ? previous
            : next;
        }),
      );
    };
    window.addEventListener(changed, update);
    window.addEventListener("popstate", update);
    return () => {
      window.removeEventListener(changed, update);
      window.removeEventListener("popstate", update);
    };
  }, []);
  return href;
}

export function useNavigationGuard(dirty: boolean) {
  useEffect(() => {
    const guard = (event: Event) => {
      if (
        dirty &&
        !window.confirm(translate("leaveThisPageUnsavedChangesWillBe"))
      )
        event.preventDefault();
    };
    window.addEventListener(leaving, guard);
    return () => window.removeEventListener(leaving, guard);
  }, [dirty]);
}

/** Keep native anchors while handling only ordinary clicks on console routes. */
export function useConsoleNavigation() {
  useEffect(() => {
    let current = window.location.href;
    const remember = () => {
      current = window.location.href;
    };
    const pop = (event: PopStateEvent) => {
      const previous = new URL(current);
      if (
        previous.pathname === window.location.pathname &&
        previous.search === window.location.search
      ) {
        remember();
        return;
      }
      if (!canLeave()) {
        // History has already moved. Keep the editor and restore its full URL.
        window.history.pushState(null, "", current);
        event.stopImmediatePropagation();
      } else {
        remember();
      }
    };
    const click = (event: MouseEvent) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      const anchor =
        event.target instanceof Element
          ? event.target.closest<HTMLAnchorElement>("a[href]")
          : null;
      if (
        !anchor ||
        anchor.hasAttribute("download") ||
        (anchor.target && anchor.target !== "_self") ||
        anchor.getAttribute("href")?.startsWith("#")
      )
        return;
      const url = new URL(anchor.href);
      if (url.origin !== window.location.origin || !resolveRoute(url)) return;
      if (
        url.pathname === window.location.pathname &&
        url.search === window.location.search &&
        url.hash
      )
        return;
      event.preventDefault();
      navigate(url.href);
    };
    window.addEventListener(changed, remember);
    window.addEventListener("popstate", pop, true);
    document.addEventListener("click", click);
    return () => {
      window.removeEventListener(changed, remember);
      window.removeEventListener("popstate", pop, true);
      document.removeEventListener("click", click);
    };
  }, []);
}
