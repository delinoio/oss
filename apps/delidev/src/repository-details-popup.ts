// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, useState } from "react";
import { settleEnclosingEntrances } from "./entrance-motion";

export enum RepositoryDetailsPresentation {
  Closed = "closed",
  Open = "open",
  Unavailable = "unavailable",
  Failed = "failed",
}

interface Viewport {
  left: number;
  top: number;
  width: number;
  height: number;
}

/** All coordinates are CSS viewport units, independently of device pixel ratio. */
export function repositoryDetailsGeometry(
  row: Pick<DOMRect, "left" | "right" | "top" | "bottom">,
  viewport: Viewport,
  height: number,
) {
  const inset = 8,
    gap = 8,
    leftEdge = viewport.left + inset,
    topEdge = viewport.top + inset,
    rightEdge = viewport.left + viewport.width - inset,
    bottomEdge = viewport.top + viewport.height - inset;
  const width = Math.max(1, Math.min(320, rightEdge - leftEdge));
  const right = row.right + gap;
  let left = right,
    top = row.top,
    maxHeight = Math.max(1, bottomEdge - topEdge);
  if (right + width > rightEdge) {
    left = row.left;
    const below = Math.max(0, bottomEdge - row.bottom - gap),
      above = Math.max(0, row.top - gap - topEdge);
    const useBelow = height <= below || below >= above;
    maxHeight = Math.max(1, useBelow ? below : above);
    top = useBelow
      ? row.bottom + gap
      : row.top - gap - Math.min(height, maxHeight);
    // A wrapped row can occupy the effective viewport at 200% reflow. Keep
    // the card usable there instead of squeezing its padding into zero space.
    if (Math.max(below, above) < 48) {
      top = topEdge;
      maxHeight = Math.max(1, bottomEdge - topEdge);
    }
  }
  left = Math.max(leftEdge, Math.min(left, rightEdge - width));
  top = Math.max(
    topEdge,
    Math.min(top, bottomEdge - Math.min(height, maxHeight)),
  );
  return { left, top, width, maxHeight };
}

function viewport(): Viewport {
  const visual = window.visualViewport;
  return {
    left: visual?.offsetLeft ?? 0,
    top: visual?.offsetTop ?? 0,
    width: visual?.width ?? window.innerWidth,
    height: visual?.height ?? window.innerHeight,
  };
}

function openerVisible(opener: HTMLElement, bounds: Viewport) {
  if (
    !opener.isConnected ||
    opener.closest("[hidden], [inert]") ||
    opener.closest("dialog")?.open === false ||
    !opener.getClientRects().length
  )
    return false;
  const rect = opener.getBoundingClientRect();
  let left = bounds.left,
    top = bounds.top,
    right = left + bounds.width,
    bottom = top + bounds.height;
  for (
    let owner: HTMLElement | null = opener;
    owner;
    owner = owner.parentElement
  ) {
    const style = getComputedStyle(owner);
    if (style.display === "none" || style.visibility === "hidden") return false;
    if (owner !== opener) {
      const box = owner.getBoundingClientRect();
      if (/(auto|scroll|hidden|clip)/.test(style.overflowX)) {
        left = Math.max(left, box.left);
        right = Math.min(right, box.right);
      }
      if (/(auto|scroll|hidden|clip)/.test(style.overflowY)) {
        top = Math.max(top, box.top);
        bottom = Math.min(bottom, box.bottom);
      }
    }
  }
  return (
    rect.right > left &&
    rect.left < right &&
    rect.bottom > top &&
    rect.top < bottom
  );
}

/** The row owns presentation only; the parent retains the exact expanded ID. */
export function useRepositoryDetailsPopup({
  expanded,
  active,
  contentKey,
}: {
  expanded: boolean;
  active: boolean;
  contentKey: string;
}) {
  const trigger = useRef<HTMLButtonElement>(null),
    popup = useRef<HTMLDivElement>(null),
    reposition = useRef<(() => void) | undefined>(undefined);
  const [presentation, setPresentation] = useState(
    RepositoryDetailsPresentation.Closed,
  );
  const [revealGeneration, setRevealGeneration] = useState(0);

  useLayoutEffect(() => {
    const element = popup.current,
      opener = trigger.current;
    setPresentation(RepositoryDetailsPresentation.Closed);
    if (!expanded || !active || !element || !opener) return;
    if (!openerVisible(opener, viewport())) return;
    let retired = false,
      opened = false;
    let dispose = () => {};
    const hide = () => {
      try {
        if (
          typeof element.hidePopover === "function" &&
          (opened || element.matches(":popover-open"))
        )
          element.hidePopover();
      } catch {
        // Hiding still conceals the original DOM if its native layer was lost.
        console.warn({
          event: "repository_details_popup",
          outcome: "hide_failed",
        });
      }
      element.hidden = true;
      opened = false;
    };
    const retire = () => {
      retired = true;
      dispose();
      hide();
      setPresentation(RepositoryDetailsPresentation.Closed);
    };
    const fail = (
      outcome:
        | RepositoryDetailsPresentation.Unavailable
        | RepositoryDetailsPresentation.Failed,
    ) => {
      retired = true;
      hide();
      setPresentation(outcome);
      console.warn({ event: "repository_details_popup", outcome });
    };
    if (
      typeof element.showPopover !== "function" ||
      typeof element.hidePopover !== "function"
    ) {
      fail(RepositoryDetailsPresentation.Unavailable);
      return;
    }
    // DisclosureContent owns mounted DOM. Its hidden-state synchronization can
    // finish after this parent layout effect; remove concealment before showing.
    element.hidden = false;
    try {
      element.showPopover();
      opened = element.matches(":popover-open");
      if (!opened) {
        fail(RepositoryDetailsPresentation.Failed);
        return;
      }
    } catch {
      fail(RepositoryDetailsPresentation.Failed);
      return;
    }
    const position = () => {
      if (retired) return;
      const bounds = viewport();
      if (!openerVisible(opener, bounds)) {
        retire();
        return;
      }
      // Native top-layer boxes can retain inherited CSS zoom. Convert their
      // measured viewport coordinates back to style coordinates, as ScrollPicker does.
      const cssWidth = Number.parseFloat(getComputedStyle(element).width),
        measuredWidth = element.getBoundingClientRect().width;
      const scale =
        cssWidth > 0 && measuredWidth > 0 ? measuredWidth / cssWidth : 1;
      const width = Math.max(1, Math.min(320, bounds.width - 16));
      element.style.width = `${width / scale}px`;
      const naturalHeight =
        (element.scrollHeight + element.offsetHeight - element.clientHeight) *
        scale;
      const row =
        opener.parentElement?.getBoundingClientRect() ??
        opener.getBoundingClientRect();
      const geometry = repositoryDetailsGeometry(row, bounds, naturalHeight);
      Object.assign(element.style, {
        left: `${geometry.left / scale}px`,
        top: `${geometry.top / scale}px`,
        width: `${geometry.width / scale}px`,
        maxHeight: `${geometry.maxHeight / scale}px`,
      });
    };
    // The opener can sit beneath an in-flight page entrance; settle it so the
    // first placement measures the untransformed row.
    settleEnclosingEntrances(opener);
    position();
    if (retired) return;
    setPresentation(RepositoryDetailsPresentation.Open);
    reposition.current = position;
    const close = (restore: boolean) => {
      retire();
      if (restore && openerVisible(opener, viewport()))
        opener.focus({ preventScroll: true });
    };
    const outside = (event: Event) => {
      if (retired || !(event.target instanceof Node)) return;
      if (!element.contains(event.target) && !opener.contains(event.target))
        close(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (
        retired ||
        event.key !== "Escape" ||
        event.defaultPrevented ||
        event.isComposing ||
        event.repeat
      )
        return;
      // Other inputs, dialogs and the drawer keep their own Escape authority.
      if (
        !(event.target instanceof Node) ||
        (!element.contains(event.target) && !opener.contains(event.target))
      )
        return;
      event.preventDefault();
      event.stopPropagation();
      close(true);
    };
    const scroll = (event: Event) => {
      if (event.target instanceof Node && element.contains(event.target))
        return;
      position();
    };
    const lifetime = new MutationObserver(position);
    for (
      let owner: HTMLElement | null = opener;
      owner;
      owner = owner.parentElement
    )
      lifetime.observe(owner, {
        attributes: true,
        attributeFilter: ["hidden", "inert", "open", "class", "style"],
        childList: true,
      });
    const resize =
      typeof ResizeObserver === "undefined"
        ? undefined
        : new ResizeObserver(position);
    resize?.observe(element);
    resize?.observe(opener);
    const intersection =
      typeof IntersectionObserver === "undefined"
        ? undefined
        : new IntersectionObserver(position);
    intersection?.observe(opener);
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("click", outside, true);
    document.addEventListener("keydown", escape, true);
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", position);
    window.visualViewport?.addEventListener("resize", position);
    window.visualViewport?.addEventListener("scroll", position);
    dispose = () => {
      reposition.current = undefined;
      lifetime.disconnect();
      resize?.disconnect();
      intersection?.disconnect();
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("click", outside, true);
      document.removeEventListener("keydown", escape, true);
      document.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", position);
      window.visualViewport?.removeEventListener("resize", position);
      window.visualViewport?.removeEventListener("scroll", position);
    };
    return () => {
      retired = true;
      dispose();
      hide();
    };
  }, [expanded, active, revealGeneration]);
  useLayoutEffect(() => reposition.current?.(), [contentKey]);
  return {
    trigger,
    popup,
    presentation,
    reveal: () => setRevealGeneration((current) => current + 1),
  };
}
