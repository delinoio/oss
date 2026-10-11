// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, type RefObject } from "react";
import "./shared-tabs.css";

// Reveal only within the strip. scrollIntoView can also move retained panels
// and document scroll owners, which tab focus must leave unchanged.
export function revealSharedTab(control: HTMLElement | null | undefined) {
  const strip = control?.closest<HTMLElement>(".shared-tab-strip");
  const item = control?.closest<HTMLElement>(".shared-tab-item");
  if (!strip || !item) return;
  if (item.offsetLeft < strip.scrollLeft) strip.scrollLeft = item.offsetLeft;
  else if (
    item.offsetLeft + item.offsetWidth >
    strip.scrollLeft + strip.clientWidth
  )
    strip.scrollLeft = item.offsetLeft + item.offsetWidth - strip.clientWidth;
}

// This observer owns presentation only. Selection, activation, mounted state
// and resource-operation callbacks remain with each original component.
export function useSharedTabReveal(
  root: RefObject<HTMLElement | null>,
  selected: string,
) {
  useLayoutEffect(() => {
    const strip = root.current?.matches(".shared-tab-strip")
      ? root.current
      : root.current?.querySelector<HTMLElement>(".shared-tab-strip");
    if (!strip) return;
    const reveal = () =>
      revealSharedTab(
        strip.querySelector<HTMLElement>(
          '[aria-selected="true"], [data-selected="true"], .is-selected > .shared-tab-label',
        ),
      );
    reveal();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(reveal);
    observer.observe(strip);
    return () => observer.disconnect();
  }, [root, selected]);
}
