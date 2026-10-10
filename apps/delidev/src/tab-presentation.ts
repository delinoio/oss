// SPDX-License-Identifier: Apache-2.0
/** Reveal inside this strip only; do not scroll the document or activate resources. */
export function revealTabItem(root: HTMLElement, item: HTMLElement): void {
  const strip = root.getBoundingClientRect(), bounds = item.getBoundingClientRect();
  if (bounds.left < strip.left) root.scrollLeft += bounds.left - strip.left;
  else if (bounds.right > strip.left + root.clientWidth) root.scrollLeft += bounds.right - strip.left - root.clientWidth;
}
export function revealSelectedTab(root: HTMLElement | null): void {
  if (!root) return;
  const focused = root.ownerDocument.activeElement;
  const item = focused && root.contains(focused) ? focused.closest<HTMLElement>(".tab-item") : root.querySelector<HTMLElement>('.tab-item:is(.is-selected, [aria-selected="true"], [aria-current="page"], [data-tab-selected="true"])');
  if (item) revealTabItem(root, item);
}
export function revealTabFocus(event: { currentTarget: HTMLElement; target: EventTarget | null }): void {
  const item = (event.target as HTMLElement | null)?.closest<HTMLElement>(".tab-item");
  if (item) revealTabItem(event.currentTarget, item);
}
