// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";

interface HoverTarget {
  anchor: RefObject<HTMLButtonElement | null>;
  pointer: boolean;
  focus: boolean;
  card: boolean;
  suppressed: boolean;
}

function occupied(target: HoverTarget) { return target.pointer || target.focus || target.card; }

// One connection-local sidebar owns timers and listeners. Boolean snapshots keep
// opening a card from rerendering every retained navigation row.
class HoverController {
  active?: HoverTarget;
  pending?: HoverTarget;
  enabled = false;
  private opening?: ReturnType<typeof setTimeout>;
  private closing?: ReturnType<typeof setTimeout>;
  private listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish() { for (const listener of this.listeners) listener(); }
  private clearOpening() { clearTimeout(this.opening); this.opening = undefined; this.pending = undefined; }
  private clearClosing() { clearTimeout(this.closing); this.closing = undefined; }

  enter(target: HoverTarget, delay: number) {
    target.suppressed = false;
    this.clearOpening();
    if (!this.enabled) return;
    if (this.active === target) { this.clearClosing(); return; }
    const show = () => {
      this.clearOpening();
      const anchor = target.anchor.current;
      if (!this.enabled || target.suppressed || !occupied(target) || !anchor?.isConnected || anchor.closest("[hidden], [inert]")) return;
      this.clearClosing();
      if (this.active) this.active.card = false;
      this.active = target;
      this.publish();
    };
    if (delay === 0) show();
    else { this.pending = target; this.opening = setTimeout(show, delay); }
  }

  leave(target: HoverTarget) {
    if (occupied(target)) return;
    if (this.pending === target) this.clearOpening();
    if (this.active !== target) return;
    this.clearClosing();
    this.closing = setTimeout(() => {
      if (this.active !== target || occupied(target)) return;
      this.active = undefined;
      this.closing = undefined;
      this.publish();
    }, 150);
  }

  keep(target: HoverTarget) { if (this.active === target) this.clearClosing(); }
  release(target: HoverTarget) {
    if (this.pending === target) this.clearOpening();
    if (this.active === target) this.dismiss();
  }
  dismiss = () => {
    if (this.pending) this.pending.suppressed = true;
    if (this.active) { this.active.suppressed = true; this.active.card = false; }
    this.clearOpening();
    this.clearClosing();
    if (!this.active) return;
    this.active = undefined;
    this.publish();
  };
}

const HoverContext = createContext<HoverController | null>(null);

export function SessionHoverProvider({ enabled, scope, children }: { enabled: boolean; scope: string; children: ReactNode }) {
  const [controller] = useState(() => new HoverController());
  useLayoutEffect(() => {
    controller.enabled = enabled;
    controller.dismiss();
    return () => { controller.enabled = false; controller.dismiss(); };
  }, [controller, enabled, scope]);
  useLayoutEffect(() => {
    const insideCard = (node: EventTarget | null) => node instanceof Element && Boolean(node.closest(".sidebar-session-tooltip"));
    const scroll = (event: Event) => { if (!insideCard(event.target)) controller.dismiss(); };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || (!controller.active && !controller.pending)) return;
      event.preventDefault();
      event.stopPropagation();
      controller.dismiss();
    };
    const outside = (event: PointerEvent) => {
      if (!insideCard(event.target) && !controller.active?.anchor.current?.contains(event.target as Node)) controller.dismiss();
    };
    document.addEventListener("scroll", scroll, { capture: true, passive: true });
    document.addEventListener("keydown", escape, true);
    document.addEventListener("pointerdown", outside, true);
    window.addEventListener("resize", controller.dismiss);
    return () => {
      document.removeEventListener("scroll", scroll, true);
      document.removeEventListener("keydown", escape, true);
      document.removeEventListener("pointerdown", outside, true);
      window.removeEventListener("resize", controller.dismiss);
    };
  }, [controller]);
  return <HoverContext.Provider value={controller}>{children}</HoverContext.Provider>;
}

export function useSessionHover(anchor: RefObject<HTMLButtonElement | null>) {
  const controller = useContext(HoverContext);
  if (!controller) throw new Error("Session hover requires its sidebar provider");
  const [target] = useState<HoverTarget>(() => ({ anchor, pointer: false, focus: false, card: false, suppressed: false }));
  const visible = useSyncExternalStore(controller.subscribe, () => controller.active === target);
  useLayoutEffect(() => () => controller.release(target), [controller, target]);
  return {
    controller, target, visible,
    onPointerEnter: () => { target.pointer = true; controller.enter(target, 300); },
    onPointerLeave: () => { target.pointer = false; controller.leave(target); },
    onFocus: () => { target.focus = true; controller.enter(target, 0); },
    onBlur: () => { target.focus = false; controller.leave(target); },
    dismiss: controller.dismiss,
  };
}

export function SessionHoverCard({ hover, children }: { hover: ReturnType<typeof useSessionHover>; children: ReactNode }) {
  const card = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ left: number; top: number }>();
  const anchor = hover.target.anchor.current;
  const dialog = anchor?.closest<HTMLDialogElement>("dialog[open]");
  const host = dialog?.getAttribute("role") === "dialog" ? dialog : document.body;
  const supportsPopover = typeof HTMLElement.prototype.showPopover === "function";
  useLayoutEffect(() => {
    const node = card.current;
    if (!node || !anchor) return;
    // The manual top layer escapes drawer/list overflow without making the
    // tooltip a modal or moving focus. Its DOM stays inside the owning modal.
    if (supportsPopover) {
      try { node.showPopover(); }
      catch {
        console.warn("delidev.sidebar.session_hover_failed", { phase: "show", classification: "presentation-unavailable" });
        hover.controller.dismiss();
        return;
      }
    }
    const measure = () => {
      const source = anchor.getBoundingClientRect(), box = node.getBoundingClientRect();
      const edge = 8, gap = 8;
      const maximumLeft = Math.max(edge, window.innerWidth - box.width - edge);
      const maximumTop = Math.max(edge, window.innerHeight - box.height - edge);
      const right = source.right + gap;
      const left = right + box.width <= window.innerWidth - edge ? right : Math.min(source.left, maximumLeft);
      const top = right + box.width <= window.innerWidth - edge ? source.top
        : source.bottom + gap + box.height <= window.innerHeight - edge ? source.bottom + gap : source.top - gap - box.height;
      const next = { left: Math.max(edge, Math.min(left, maximumLeft)), top: Math.max(edge, Math.min(top, maximumTop)) };
      setPosition(previous => previous?.left === next.left && previous.top === next.top ? previous : next);
    };
    measure();
    const resize = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : undefined;
    resize?.observe(node);
    resize?.observe(anchor);
    return () => { resize?.disconnect(); if (supportsPopover && node.matches(":popover-open")) node.hidePopover(); };
  }, [anchor, hover.controller, supportsPopover]);
  return createPortal(<div ref={card} className="sidebar-session-tooltip" role="tooltip" popover={supportsPopover ? "manual" : undefined}
    style={{ left: position?.left ?? 0, top: position?.top ?? 0, visibility: position ? undefined : "hidden" }}
    onPointerEnter={() => { hover.target.card = true; hover.controller.keep(hover.target); }}
    onPointerLeave={() => { hover.target.card = false; hover.controller.leave(hover.target); }}>
    {children}
  </div>, host);
}
