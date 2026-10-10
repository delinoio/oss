// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, type ReactNode, type RefObject } from "react";
import { pickerSurfaceBounds, type PickerAnchor, type PickerBounds } from "./picker-overlay";

/** Presentation only: the original composer retains editing and inventory authority. */
export function skillCompletionGeometry(anchor: PickerAnchor, bounds: PickerBounds, contentHeight: number) {
  const leftBound = bounds.left + 8, rightBound = Math.max(leftBound, bounds.right - 8);
  const topBound = bounds.top + 8, bottomBound = Math.max(topBound, bounds.bottom - 8);
  const above = Math.max(0, Math.min(bottomBound, anchor.top - 8) - topBound);
  const below = Math.max(0, bottomBound - Math.max(topBound, anchor.bottom + 8));
  const desired = Math.min(220, Math.max(0, contentHeight));
  const useAbove = desired <= above || desired > below && above >= below;
  const maxHeight = Math.min(220, useAbove ? above : below), height = Math.min(desired, maxHeight);
  const width = Math.min(Math.max(0, anchor.width), rightBound - leftBound);
  const left = Math.max(leftBound, Math.min(anchor.left, rightBound - width));
  const top = Math.max(topBound, Math.min(useAbove ? anchor.top - 8 - height : anchor.bottom + 8, bottomBound - height));
  return { left, top, width, maxHeight };
}

function usable(input: HTMLTextAreaElement) {
  if (!input.isConnected || input.matches(":disabled") || input.closest("[hidden], [inert]")) return false;
  for (let node: HTMLElement | null = input; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse" || node instanceof HTMLDialogElement && !node.open) return false;
  }
  return true;
}

export function SkillCompletionOverlay({ anchor, panel, dismiss, children }: {
  anchor: RefObject<HTMLTextAreaElement | null>; panel: RefObject<HTMLDivElement | null>; dismiss: () => void; children: ReactNode;
}) {
  const latest = useRef(dismiss); latest.current = dismiss;
  useLayoutEffect(() => {
    const input = anchor.current, popup = panel.current;
    if (!input || !popup) return;
    let disposed = false;
    const position = () => {
      if (disposed) return;
      if (!usable(input)) { disposed = true; popup.hidePopover?.(); latest.current(); return; }
      const bounds = pickerSurfaceBounds(input);
      // Queue editors can live in another modal owner. Never escape its interaction surface.
      const dialog = input.closest("dialog, [role=dialog][aria-modal=true]");
      if (dialog) {
        const rect = dialog.getBoundingClientRect();
        bounds.left = Math.max(bounds.left, rect.left); bounds.top = Math.max(bounds.top, rect.top);
        bounds.right = Math.min(bounds.right, rect.right); bounds.bottom = Math.min(bounds.bottom, rect.bottom);
      }
      const rect = input.getBoundingClientRect();
      popup.style.width = `${Math.min(rect.width, Math.max(0, bounds.right - bounds.left - 16))}px`;
      // scrollHeight includes the whole list even after a previous constrained placement.
      const geometry = skillCompletionGeometry(rect, bounds, popup.scrollHeight + popup.clientTop * 2);
      Object.assign(popup.style, { left: `${geometry.left}px`, top: `${geometry.top}px`, width: `${geometry.width}px`, maxHeight: `${geometry.maxHeight}px` });
    };
    popup.showPopover?.(); position();
    const resize = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(position);
    resize?.observe(input); resize?.observe(popup);
    const mutations = new MutationObserver(position);
    for (let node: HTMLElement | null = input; node; node = node.parentElement) mutations.observe(node, { attributes: true });
    mutations.observe(document.documentElement, { childList: true, subtree: true });
    window.addEventListener("resize", position); window.addEventListener("scroll", position, true);
    const viewport = window.visualViewport;
    viewport?.addEventListener("resize", position); viewport?.addEventListener("scroll", position);
    return () => {
      disposed = true; resize?.disconnect(); mutations.disconnect(); popup.hidePopover?.();
      window.removeEventListener("resize", position); window.removeEventListener("scroll", position, true);
      viewport?.removeEventListener("resize", position); viewport?.removeEventListener("scroll", position);
    };
  }, [anchor, panel]);
  return <div ref={panel} className="skill-completion" popover="manual" onKeyDown={event => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); latest.current(); if (anchor.current && usable(anchor.current)) anchor.current.focus(); }
  }}>{children}</div>;
}
