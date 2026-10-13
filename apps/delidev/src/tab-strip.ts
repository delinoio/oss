// SPDX-License-Identifier: Apache-2.0
/** Scrolls only the owning strip (and ancestors if needed) so a selected or
 * keyboard-focused tab is reachable. It never changes focus or selection. */
export function revealTabItem(element: Element | null | undefined) {
  element?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
}
