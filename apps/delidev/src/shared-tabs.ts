// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, type RefObject } from "react";
import "./shared-tabs.css";

// Write only the strip's horizontal position. scrollIntoView can also move
// retained panels and document scroll owners during tab focus or resize.
export function revealSharedTab(control: HTMLElement | null | undefined) {
  const strip = control?.closest<HTMLElement>(".shared-tab-strip");
  const item = control?.closest<HTMLElement>(".shared-tab-item");
  if (!strip || !item || strip.clientWidth === 0) return;
  let left = item.offsetLeft;
  let width = item.offsetWidth;
  // An oversized fixed label cannot fit in full. Keep its start reachable;
  // an independent close target can still be revealed within that same item.
  if (width > strip.clientWidth) {
    if (control !== item && control?.matches(".shared-tab-close")) {
      left += control.offsetLeft;
      width = control.offsetWidth;
    } else width = strip.clientWidth;
  }
  const right = left + width;
  if (left < strip.scrollLeft) strip.scrollLeft = left;
  else if (right > strip.scrollLeft + strip.clientWidth)
    strip.scrollLeft = right - strip.clientWidth;
}

function selectedControl(strip: HTMLElement) {
  return strip.querySelector<HTMLElement>(
    '[aria-selected="true"], [data-selected="true"], .is-selected > .shared-tab-label',
  );
}

// This observer owns presentation only. Original components retain selection,
// focus handoff, mounted state, query owners and resource-operation callbacks.
export function useSharedTabReveal(
  root: RefObject<HTMLElement | null>,
  selected: string,
) {
  useLayoutEffect(() => {
    const strip = root.current;
    if (!strip) return;
    const reveal = () => {
      const focused = strip.ownerDocument.activeElement;
      revealSharedTab(
        focused instanceof HTMLElement &&
          focused.matches(".shared-tab-control") &&
          focused.closest(".shared-tab-strip") === strip
          ? focused
          : selectedControl(strip),
      );
    };
    const focus = (event: FocusEvent) => {
      if (
        event.target instanceof HTMLElement &&
        event.target.matches(".shared-tab-control")
      )
        revealSharedTab(event.target);
    };
    strip.addEventListener("focusin", focus);
    const observer =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(reveal);
    const observeItems = () => {
      observer?.disconnect();
      observer?.observe(strip);
      strip.querySelectorAll(".shared-tab-item").forEach((item) => {
        observer?.observe(item);
      });
      reveal();
    };
    observeItems();
    // Labels and inventory can change while the selected identity stays fixed.
    const inventory = new MutationObserver(observeItems);
    inventory.observe(strip, {
      childList: true,
      characterData: true,
      subtree: true,
    });
    return () => {
      strip.removeEventListener("focusin", focus);
      inventory.disconnect();
      observer?.disconnect();
    };
  });
  useLayoutEffect(() => {
    if (root.current) revealSharedTab(selectedControl(root.current));
  }, [root, selected]);
}
