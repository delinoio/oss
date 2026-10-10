// SPDX-License-Identifier: Apache-2.0
// Reveal only the owning horizontal strip. Do not scroll content panels or the
// document, change focus/selection, or operate native resources.
export function revealDesktopTab(target: EventTarget | null): void {
 if (!(target instanceof HTMLElement)) return;
 const item=target.closest<HTMLElement>(".desktop-tab-item"), strip=item?.closest<HTMLElement>(".desktop-tab-strip");
 if (!item||!strip||strip.closest("[hidden], [inert]")) return;
 const itemBounds=item.getBoundingClientRect(), stripBounds=strip.getBoundingClientRect();
 if (itemBounds.left<stripBounds.left) strip.scrollLeft-=stripBounds.left-itemBounds.left;
 else if (itemBounds.right>stripBounds.right) strip.scrollLeft+=itemBounds.right-stripBounds.right;
}
export function revealSelectedDesktopTab(root: HTMLElement | null): void {
 revealDesktopTab(root?.querySelector<HTMLElement>('.desktop-tab-item.is-selected, [aria-selected="true"], [aria-current="page"]') ?? null);
}
