// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { settleEnclosingEntrances } from "./entrance-motion";
import {
  pickerOverlayGeometry,
  pickerSurfaceBounds,
  pickerSurfaceOwner,
  type PickerBounds,
} from "./picker-overlay";
import "./anchored-popup.css";

/**
 * Shared presentation primitive for dropdown popups that must never change the
 * surrounding layout. A consumer keeps its markup, focus model,
 * selection controller and mutation guards; this hook only owns where the popup
 * is drawn.
 *
 * Consumer contract:
 * - Render the popup element in the original DOM position (so dialog, inert and
 *   `contains()` ownership are unchanged) with `popover="manual"` and the
 *   `anchored-popup` class, but only while `open` is true. It must be mounted
 *   in the same commit in which `open` becomes true because the hook reads
 *   `popup.current` from a layout effect.
 * - The hook calls `showPopover()` (top layer, no focus movement) and keeps the
 *   inline `left`, `top`, `width` and `maxHeight` of the popup current. It never
 *   scrolls the page, never touches the anchor and never reads or writes
 *   selection state.
 * - Empty, loading and error content of the dropdown belongs inside the popup
 *   element, so the popup height (capped at 280 CSS px) is the only thing that
 *   changes while the user filters.
 * - Dismissal semantics (Escape, Tab, outside pointer, blur) stay with the
 *   consumer. The hook only calls `onInvalid` when the popup can no longer be
 *   presented (anchor detached, disabled, inert, hidden, in a closed dialog or
 *   scrolled completely out of the box visible through its clipping ancestors);
 *   the consumer must close in response.
 * - Geometry comes from `picker-overlay.ts`: 4px gap below the anchor, 8px
 *   inset from the boundary, 280px maximum height, flip above when the space
 *   below is smaller, clamp inside the boundary.
 * - Listeners and observers are installed in a layout effect keyed by `open` and
 *   released on close and unmount; the popover is hidden in the same cleanup.
 */
export enum AnchoredPopupWidth {
  /** Match the anchor width (searchable selectors). */
  Anchor = "anchor",
  /** Use the popup's natural content width, clamped to the boundary. */
  Content = "content",
}
export enum AnchoredPopupAlign {
  /** Align the popup's left edge with the anchor's left edge. */
  Start = "start",
  /** Align the popup's right edge with the anchor's right edge. */
  End = "end",
}

export interface AnchoredPopupOptions {
  open: boolean;
  /** The element the popup is attached to (the combobox input or menu button). */
  anchor: RefObject<HTMLElement | null>;
  /** The `popover="manual"` element; see the consumer contract above. */
  popup: RefObject<HTMLElement | null>;
  /** The popup can no longer be presented. The consumer must close it. */
  onInvalid: () => void;
  /** Defaults to `AnchoredPopupWidth.Anchor`. */
  width?: AnchoredPopupWidth;
  /** Defaults to `AnchoredPopupAlign.Start`. */
  align?: AnchoredPopupAlign;
  /**
   * Optional owning surface. Its border box is intersected with the visual
   * viewport. When omitted, the shared picker owner lookup applies and falls
   * back to the visual viewport.
   */
  boundary?: (anchor: HTMLElement) => HTMLElement | null | undefined;
}

function clipsOverflow(style: CSSStyleDeclaration): boolean {
  return [style.overflow, style.overflowX, style.overflowY].some(
    (value) => value !== "" && value !== "visible",
  );
}

/**
 * True when the anchor lies completely outside the box that its clipping
 * ancestors leave visible, so a pointer or a person can no longer reach it.
 *
 * Every ancestor whose overflow is not `visible` (a scrolling list, the tray
 * that contains the list, the Settings task body or content pane) and the
 * window itself clip the anchor, so the visible box is the intersection of all
 * of their boxes, not only the nearest one. An absolutely positioned anchor is only clipped by
 * ancestors from its containing block outwards and a fixed one by none. An
 * ancestor without a measurable box (`display: contents`, detached or not yet
 * laid out) never dismisses. This is geometry only: it neither scrolls nor
 * writes anything, and the popup itself may still extend past these boxes.
 */
export function isAnchorClippedOut(anchor: HTMLElement): boolean {
  let position = getComputedStyle(anchor).position;
  if (position === "fixed") return false;
  let left = Number.NEGATIVE_INFINITY,
    top = Number.NEGATIVE_INFINITY,
    right = Number.POSITIVE_INFINITY,
    bottom = Number.POSITIVE_INFINITY,
    clipped = false;
  for (
    let owner = anchor.parentElement;
    owner && owner !== document.body && owner !== document.documentElement;
    owner = owner.parentElement
  ) {
    const style = getComputedStyle(owner);
    if (style.display === "contents") continue;
    // An absolutely positioned descendant escapes every ancestor before its
    // containing block.
    if (position === "absolute" && (style.position || "static") === "static")
      continue;
    if (clipsOverflow(style)) {
      const rect = owner.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) {
        clipped = true;
        left = Math.max(left, rect.left);
        top = Math.max(top, rect.top);
        right = Math.min(right, rect.right);
        bottom = Math.min(bottom, rect.bottom);
      }
    }
    position = style.position;
    if (position === "fixed") break;
  }
  const target = anchor.getBoundingClientRect();
  // The window is the root clipping box: a page that scrolls an anchor out of
  // the window leaves nothing to present the popup beside. An anchor without a
  // measurable box (not laid out) is never judged by the window.
  if (target.width > 0 && target.height > 0) {
    clipped = true;
    left = Math.max(left, 0);
    top = Math.max(top, 0);
    right = Math.min(right, window.innerWidth);
    bottom = Math.min(bottom, window.innerHeight);
  }
  if (!clipped) return false;
  if (right <= left || bottom <= top) return true;
  return (
    target.bottom <= top ||
    target.top >= bottom ||
    target.right <= left ||
    target.left >= right
  );
}

/** Visual viewport intersected with an explicit owning surface. */
export function anchoredPopupBounds(
  anchor: HTMLElement,
  boundary?: (anchor: HTMLElement) => HTMLElement | null | undefined,
): PickerBounds {
  const owner = boundary?.(anchor);
  if (!owner) return pickerSurfaceBounds(anchor);
  const viewport = window.visualViewport;
  const left = viewport?.offsetLeft ?? 0,
    top = viewport?.offsetTop ?? 0;
  const rect = owner.getBoundingClientRect();
  return {
    left: Math.max(left, rect.left),
    top: Math.max(top, rect.top),
    right: Math.min(left + (viewport?.width ?? window.innerWidth), rect.right),
    bottom: Math.min(
      top + (viewport?.height ?? window.innerHeight),
      rect.bottom,
    ),
  };
}

export function useAnchoredPopup(options: AnchoredPopupOptions): void {
  const { open, anchor, popup } = options;
  // Latest options are read from event handlers without re-running the effect,
  // so an inline `onInvalid` or `boundary` closure never reinstalls observers.
  const latest = useRef(options);
  latest.current = options;
  useLayoutEffect(() => {
    const opener = anchor.current,
      element = popup.current;
    if (!open || !opener || !element) return;
    const position = () => {
      const current = latest.current;
      const dialog = opener.closest("dialog");
      if (
        !opener.isConnected ||
        !element.isConnected ||
        opener.closest("[inert], [hidden]") ||
        opener.matches(":disabled") ||
        (dialog && !dialog.open)
      ) {
        current.onInvalid();
        return false;
      }
      // An anchor scrolled completely out of any clipping ancestor is no
      // longer visible. Clamping would draw the popup beside unrelated content
      // while its actions still target the hidden anchor, so close instead.
      if (isAnchorClippedOut(opener)) {
        current.onInvalid();
        return false;
      }
      const content = current.width === AnchoredPopupWidth.Content;
      // Content width is measured without the width written by the previous
      // pass so a shrinking list can narrow again.
      if (content) element.style.width = "";
      // Top-layer coordinates remain in CSS layout units under effective zoom.
      // Measure the popup scale instead of assuming ancestor zoom or device DPI.
      // offsetWidth rounds fractional CSS widths and can cause observer oscillation.
      const cssWidth = Number.parseFloat(getComputedStyle(element).width);
      const box = element.getBoundingClientRect();
      const scale = cssWidth > 0 ? box.width / cssWidth : 1;
      const factor = Number.isFinite(scale) && scale > 0 ? scale : 1;
      const rect = opener.getBoundingClientRect();
      const width = content ? box.width : rect.width;
      const left =
        current.align === AnchoredPopupAlign.End
          ? rect.right - width
          : rect.left;
      const geometry = pickerOverlayGeometry(
        {
          left,
          right: left + width,
          top: rect.top,
          bottom: rect.bottom,
          width,
        },
        anchoredPopupBounds(opener, current.boundary),
        (element.scrollHeight + 2) * factor,
      );
      Object.assign(element.style, {
        left: `${geometry.left / factor}px`,
        top: `${geometry.top / factor}px`,
        width: `${geometry.width / factor}px`,
        maxHeight: `${geometry.maxHeight / factor}px`,
      });
      return true;
    };
    // A top-layer popup never sits beneath an animated page. Settle the
    // enclosing entrance first; the observer below measures again when the
    // entrance attribute clears and the untransformed layout commits.
    settleEnclosingEntrances(element);
    if (!position()) return;
    element.showPopover?.();
    position();
    const outer = () => {
      position();
    };
    const lifetime = new MutationObserver(outer);
    // Ancestor child lists matter too: a row inserted, removed or reordered
    // before the anchor moves it without resizing the anchor itself.
    const ancestors: HTMLElement[] = [];
    for (
      let owner: HTMLElement | null = opener;
      owner;
      owner = owner.parentElement
    ) {
      ancestors.push(owner);
      lifetime.observe(owner, {
        attributes: true,
        attributeFilter: [
          "open",
          "inert",
          "hidden",
          "disabled",
          "data-entrance",
        ],
        childList: true,
      });
    }
    // Content changes (filtering, async rows) move the flip decision even when
    // the capped popup box keeps its size, so watch the popup subtree too.
    const subtree = new MutationObserver(outer);
    subtree.observe(element, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    const observer =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(outer);
    observer?.observe(element);
    // Every ancestor: a sibling that resizes shifts the anchor by resizing the
    // ancestors that contain it, even when the anchor box is unchanged.
    for (const owner of ancestors) observer?.observe(owner);
    const surface =
      latest.current.boundary?.(opener) ?? pickerSurfaceOwner(opener);
    if (surface) observer?.observe(surface);
    // Scrolling inside the popup itself never moves the anchor.
    const scrolled = (event: Event) => {
      if (event.target instanceof Node && element.contains(event.target))
        return;
      position();
    };
    window.addEventListener("resize", outer);
    document.addEventListener("scroll", scrolled, true);
    window.visualViewport?.addEventListener("resize", outer);
    window.visualViewport?.addEventListener("scroll", outer);
    return () => {
      observer?.disconnect();
      lifetime.disconnect();
      subtree.disconnect();
      try {
        element.hidePopover?.();
      } catch {
        // The element may already be detached; removal hides it natively.
      }
      window.removeEventListener("resize", outer);
      document.removeEventListener("scroll", scrolled, true);
      window.visualViewport?.removeEventListener("resize", outer);
      window.visualViewport?.removeEventListener("scroll", outer);
    };
  }, [open, anchor, popup]);
}

/**
 * Dismisses an open popup when a pointer press lands outside every listed
 * element (normally the anchor and the popup). A popup that can cover content
 * must not trap the user behind it. The press is observed in the capture phase
 * and never prevented, so the pressed control still receives it. The consumer
 * owns what dismissal means; this hook only reports it.
 */
export function useOutsidePointerDismiss(
  open: boolean,
  inside: ReadonlyArray<RefObject<Element | null>>,
  onDismiss: () => void,
): void {
  const latest = useRef({ inside, onDismiss });
  latest.current = { inside, onDismiss };
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (latest.current.inside.some((ref) => ref.current?.contains(target)))
        return;
      latest.current.onDismiss();
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, [open]);
}
