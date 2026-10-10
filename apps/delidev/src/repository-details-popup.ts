// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState, type RefObject } from "react";

const gap = 8;
interface Bounds { left: number; right: number; top: number; bottom: number }
export function repositoryDetailsPlacement(anchor: Bounds, width: number, height: number, viewportWidth: number, viewportHeight: number) {
  const availableHeight = Math.max(1, viewportHeight - gap * 2);
  let maxHeight = availableHeight, left = anchor.right + gap, top = anchor.top;
  if (left + width > viewportWidth - gap) {
    left = anchor.left;
    const below = Math.max(1, viewportHeight - anchor.bottom - gap * 2), above = Math.max(1, anchor.top - gap * 2);
    const useBelow = height <= below || below >= above;
    maxHeight = Math.min(availableHeight, useBelow ? below : above);
    top = useBelow ? anchor.bottom + gap : anchor.top - Math.min(height, maxHeight) - gap;
  }
  return { left: Math.max(gap, Math.min(left, viewportWidth - width - gap)), top: Math.max(gap, Math.min(top, viewportHeight - Math.min(height, maxHeight) - gap)), maxHeight };
}
function openerVisible(opener: HTMLElement) {
  if (!opener.isConnected || opener.closest("[hidden], [inert], dialog:not([open])")) return false;
  const bounds = opener.getBoundingClientRect();
  if ((bounds.width || bounds.height) && (bounds.bottom <= 0 || bounds.top >= window.innerHeight || bounds.right <= 0 || bounds.left >= window.innerWidth)) return false;
  for (let node: HTMLElement | null = opener; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    if (style.display === "none" || style.visibility === "hidden") return false;
    if (node !== opener && (bounds.width || bounds.height)) {
      const clip = node.getBoundingClientRect();
      if (/auto|scroll|hidden|clip/.test(style.overflowY) && (bounds.bottom <= clip.top || bounds.top >= clip.bottom)) return false;
      if (/auto|scroll|hidden|clip/.test(style.overflowX) && (bounds.right <= clip.left || bounds.left >= clip.right)) return false;
    }
  }
  return true;
}

// The caller retains one exact expanded identity. This hook owns only the
// disposable native popup; it never reads metadata or creates another portal.
export function useRepositoryDetailsPopup(expanded: boolean, active: boolean, opener: RefObject<HTMLButtonElement | null>, popup: RefObject<HTMLDivElement | null>, close: () => void) {
  const closeCurrent = useRef(close); closeCurrent.current = close;
  const [concealed, setConcealed] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const visible = expanded && active && !concealed && !dismissed;
  // Outside dismissal is transient: navigation must retain the existing bounded
  // connection-memory identity without persisting another preference.
  useEffect(() => { setDismissed(false); }, [expanded, active]);
  useEffect(() => {
    if (!expanded || !active || !opener.current) return;
    const trigger = opener.current;
    const check = () => setConcealed(!openerVisible(trigger));
    const observer = new MutationObserver(check);
    observer.observe(document.documentElement, { childList: true, subtree: true });
    for (let parent: HTMLElement | null = trigger; parent; parent = parent.parentElement) observer.observe(parent, { attributes: true, attributeFilter: ["hidden", "inert", "class", "style", "open"] });
    document.addEventListener("scroll", check, true); window.addEventListener("resize", check);
    check();
    return () => { observer.disconnect(); document.removeEventListener("scroll", check, true); window.removeEventListener("resize", check); };
  }, [expanded, active]);
  useEffect(() => {
    if (!visible || !opener.current || !popup.current) return;
    const trigger = opener.current, content = popup.current;
    if (!openerVisible(trigger)) { setConcealed(true); return; }
    content.showPopover?.();
    const position = () => {
      if (!openerVisible(trigger)) { setConcealed(true); return; }
      const naturalHeight = Math.max(content.offsetHeight, content.scrollHeight + content.offsetHeight - content.clientHeight);
      const location = repositoryDetailsPlacement(trigger.parentElement!.getBoundingClientRect(), content.offsetWidth, naturalHeight, window.innerWidth, window.innerHeight);
      content.style.left = `${location.left}px`; content.style.top = `${location.top}px`; content.style.maxHeight = `${location.maxHeight}px`;
    };
    position();
    const resize = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(position);
    resize?.observe(content); resize?.observe(trigger);
    const outside = (event: Event) => { if (!content.contains(event.target as Node) && !trigger.contains(event.target as Node)) setDismissed(true); };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // Capture prevents the original compact drawer's native cancel action.
      event.preventDefault(); event.stopPropagation();
      closeCurrent.current(); trigger.focus({ preventScroll: true });
    };
    const scroll = (event: Event) => { if (!content.contains(event.target as Node)) position(); };
    document.addEventListener("pointerdown", outside, true); document.addEventListener("click", outside, true); document.addEventListener("keydown", escape, true);
    window.addEventListener("resize", position); document.addEventListener("scroll", scroll, true);
    return () => {
      resize?.disconnect();
      // Removing a popover already removes it from the native top layer.
      if (content.isConnected) content.hidePopover?.();
      document.removeEventListener("pointerdown", outside, true); document.removeEventListener("click", outside, true); document.removeEventListener("keydown", escape, true);
      window.removeEventListener("resize", position); document.removeEventListener("scroll", scroll, true);
    };
  }, [visible]);
  return { visible, reveal: () => setDismissed(false) };
}
