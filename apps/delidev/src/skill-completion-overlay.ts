// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, type RefObject } from "react";
import {
  pickerSurfaceBounds,
  pickerSurfaceOwner,
  type PickerAnchor,
  type PickerBounds,
} from "./picker-overlay";

export function skillCompletionGeometry(
  anchor: PickerAnchor,
  bounds: PickerBounds,
  contentHeight: number,
) {
  const margin = 8,
    gap = 8;
  const leftBound = bounds.left + margin,
    rightBound = Math.max(leftBound, bounds.right - margin),
    topBound = bounds.top + margin,
    bottomBound = Math.max(topBound, bounds.bottom - margin);
  const above = Math.max(0, Math.min(bottomBound, anchor.top - gap) - topBound),
    below = Math.max(0, bottomBound - Math.max(topBound, anchor.bottom + gap)),
    desired = Math.min(220, Math.max(0, contentHeight));
  const useAbove = desired <= above || (desired > below && above >= below),
    maxHeight = Math.min(220, useAbove ? above : below),
    height = Math.min(desired, maxHeight),
    width = Math.min(Math.max(0, anchor.width), rightBound - leftBound),
    left = Math.max(leftBound, Math.min(anchor.left, rightBound - width)),
    top = Math.max(
      topBound,
      Math.min(
        useAbove ? anchor.top - gap - height : anchor.bottom + gap,
        bottomBound - height,
      ),
    );
  return { left, top, width, maxHeight };
}

export function skillCompletionAnchorUsable(
  anchor: HTMLTextAreaElement | null,
): boolean {
  if (
    !anchor?.isConnected ||
    anchor.matches(":disabled") ||
    anchor.closest("[inert], [hidden]")
  )
    return false;
  const dialog = anchor.closest("dialog");
  if (dialog && !dialog.open) return false;
  for (
    let owner: HTMLElement | null = anchor;
    owner;
    owner = owner.parentElement
  ) {
    const style = getComputedStyle(owner);
    if (
      style.display === "none" ||
      style.visibility === "hidden" ||
      style.visibility === "collapse"
    )
      return false;
  }
  return true;
}

/** Presentation stays in the original DOM/modal owner; it grants no edit authority. */
export function useSkillCompletionOverlay(
  popupRef: RefObject<HTMLDivElement | null>,
  textareaRef: RefObject<HTMLTextAreaElement | null>,
  visible: boolean,
  dismiss: () => void,
) {
  useLayoutEffect(() => {
    const popup = popupRef.current,
      anchor = textareaRef.current;
    if (!visible || !popup || !anchor) return;
    const dialog = anchor.closest("dialog");
    const position = () => {
      if (!skillCompletionAnchorUsable(anchor) || !popup.isConnected) {
        dismiss();
        return false;
      }
      const bounds = pickerSurfaceBounds(anchor);
      if (dialog) {
        const rect = dialog.getBoundingClientRect();
        bounds.left = Math.max(bounds.left, rect.left);
        bounds.right = Math.min(bounds.right, rect.right);
        bounds.top = Math.max(bounds.top, rect.top);
        bounds.bottom = Math.min(bounds.bottom, rect.bottom);
      }
      // Native top-layer coordinates use layout units, including inherited zoom.
      // Fractional computed width avoids offsetWidth rounding feedback loops.
      const cssWidth = Number.parseFloat(getComputedStyle(popup).width),
        measuredScale =
          cssWidth > 0 ? popup.getBoundingClientRect().width / cssWidth : 1,
        scale =
          Number.isFinite(measuredScale) && measuredScale > 0
            ? measuredScale
            : 1;
      const geometry = skillCompletionGeometry(
        anchor.getBoundingClientRect(),
        bounds,
        (popup.scrollHeight + 2) * scale,
      );
      Object.assign(popup.style, {
        left: `${geometry.left / scale}px`,
        top: `${geometry.top / scale}px`,
        width: `${geometry.width / scale}px`,
        maxHeight: `${geometry.maxHeight / scale}px`,
      });
      return true;
    };
    if (!position()) return;
    popup.showPopover?.();
    position();
    const observer =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(position);
    observer?.observe(anchor);
    observer?.observe(popup);
    if (dialog) observer?.observe(dialog);
    const surface = pickerSurfaceOwner(anchor);
    if (surface) observer?.observe(surface);
    const lifetime = new MutationObserver(position);
    for (
      let owner: HTMLElement | null = anchor;
      owner;
      owner = owner.parentElement
    )
      lifetime.observe(owner, {
        attributes: true,
        attributeFilter: [
          "open",
          "inert",
          "hidden",
          "disabled",
          "class",
          "style",
        ],
      });
    // Result transitions can change scrollHeight without changing a capped box.
    // Removal of the original composer must also dispose its top-layer host.
    lifetime.observe(document.documentElement, {
      childList: true,
      subtree: true,
    });
    window.addEventListener("resize", position);
    document.addEventListener("scroll", position, true);
    window.visualViewport?.addEventListener("resize", position);
    window.visualViewport?.addEventListener("scroll", position);
    return () => {
      observer?.disconnect();
      lifetime.disconnect();
      // Removal or modal teardown can already have retired the native popover.
      if (popup.hidePopover && popup.matches(":popover-open"))
        popup.hidePopover();
      window.removeEventListener("resize", position);
      document.removeEventListener("scroll", position, true);
      window.visualViewport?.removeEventListener("resize", position);
      window.visualViewport?.removeEventListener("scroll", position);
    };
  }, [visible, popupRef, textareaRef, dismiss]);
}
