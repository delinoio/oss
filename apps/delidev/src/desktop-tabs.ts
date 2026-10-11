// SPDX-License-Identifier: Apache-2.0
import "./desktop-tabs.css";

/** Reveal only the tab strip; retained panel and document scroll stay unchanged. */
export function revealDesktopTab(node: HTMLElement | null): void {
  const item = node?.closest<HTMLElement>(".desktop-tab-item");
  const strip = item?.closest<HTMLElement>(".desktop-tab-strip");
  if (!item || !strip || strip.closest("[hidden], [inert]")) return;
  const viewport = strip.getBoundingClientRect(), bounds = item.getBoundingClientRect();
  if (viewport.width <= 0) return;
  if (bounds.left < viewport.left) strip.scrollLeft += bounds.left - viewport.left;
  else if (bounds.right > viewport.right) strip.scrollLeft += bounds.right - viewport.right;
}
