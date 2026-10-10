// SPDX-License-Identifier: Apache-2.0
export const sidebarMotionDuration = 180;
export const sidebarMotionFallback = 240;

/** Visual motion never admits a preference write or owns pane controllers. */
export class SidebarPaneMotion {
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private removeCompletion: (() => void) | undefined;
  private collapsed = false;

  private readonly layout: HTMLElement;
  private readonly pane: HTMLDialogElement;
  private readonly toggle: () => HTMLElement | null | undefined;
  private readonly ready: (value: boolean) => void;
  constructor(layout: HTMLElement, pane: HTMLDialogElement, toggle: () => HTMLElement | null | undefined, ready: (value: boolean) => void) {
    this.layout = layout; this.pane = pane; this.toggle = toggle; this.ready = ready;
  }

  private retire() {
    this.generation++;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
    this.removeCompletion?.();
    this.removeCompletion = undefined;
  }

  update(collapsed: boolean, animate: boolean) {
    this.retire();
    this.collapsed = collapsed;
    if (collapsed && this.pane.contains(document.activeElement)) this.toggle()?.focus({ preventScroll: true });
    // Block input and accessibility before the brief visual-only closing span.
    this.pane.inert = collapsed;
    this.pane.setAttribute("aria-hidden", String(collapsed));
    this.ready(false);
    if (!animate) { this.settle(); return; }

    this.pane.hidden = false;
    this.pane.setAttribute("open", "");
    // Restore the mounted content at the existing endpoint before an opening.
    // During reversal retain the in-flight CSS transition's rendered geometry.
    this.layout.classList.add("sidebar-motion");
    void this.pane.getBoundingClientRect();
    this.layout.classList.toggle("sidebar-collapsed", collapsed);
    const owned = this.generation;
    const complete = (event: TransitionEvent) => {
      if (owned !== this.generation || event.target !== this.layout || event.propertyName !== "grid-template-columns") return;
      const content = this.pane.firstElementChild?.getBoundingClientRect().width ?? 0;
      const endpoint = collapsed ? 0 : content;
      // A completion queued before reversal cannot settle newer geometry.
      if (Math.abs(this.pane.getBoundingClientRect().width - endpoint) < 0.5) this.settle();
    };
    const canceled = (event: TransitionEvent) => {
      if (owned !== this.generation || event.target !== this.layout || event.propertyName !== "grid-template-columns") return;
      // CSS reversal cancels the predecessor while a successor is running.
      // A genuine cancellation without that successor settles the latest choice.
      if (!this.layout.getAnimations().some(animation => animation.playState === "running")) this.settle();
    };
    this.layout.addEventListener("transitionend", complete);
    this.layout.addEventListener("transitioncancel", canceled);
    this.removeCompletion = () => { this.layout.removeEventListener("transitionend", complete); this.layout.removeEventListener("transitioncancel", canceled); };
    this.timer = setTimeout(() => { if (owned === this.generation) this.settle(); }, sidebarMotionFallback);
  }

  private settle() {
    this.retire();
    this.layout.classList.remove("sidebar-motion");
    this.layout.classList.toggle("sidebar-collapsed", this.collapsed);
    this.pane.hidden = this.collapsed;
    if (this.collapsed) this.pane.removeAttribute("open");
    else this.pane.setAttribute("open", "");
    this.ready(!this.collapsed);
  }

  /** Breakpoints, reduced motion, navigation and disposal retire old callbacks. */
  cancel() { this.settle(); }
  dispose() {
    this.retire(); this.layout.classList.remove("sidebar-motion");
    this.layout.classList.toggle("sidebar-collapsed", this.collapsed);
    this.pane.hidden = this.collapsed;
    if (this.collapsed) this.pane.removeAttribute("open"); else this.pane.setAttribute("open", "");
  }
}

export interface SidebarMotionBoundary {
  collapsed: boolean; compact: boolean; narrowWide: boolean; surface: string;
  selected: string; connectionReady: boolean; allowed: boolean; saving: boolean;
  reduced: boolean; navigation: string; drawer: boolean;
}
/** Capture only presentation admission; persistence keeps its original owner. */
export class SidebarMotionAdmission {
  private previous: SidebarMotionBoundary | undefined;
  private interrupted = false;
  update(next: SidebarMotionBoundary) {
    const prior = this.previous;
    const scopeChanged = Boolean(prior && (prior.compact !== next.compact || prior.narrowWide !== next.narrowWide || prior.surface !== next.surface || prior.selected !== next.selected || prior.connectionReady !== next.connectionReady || prior.navigation !== next.navigation || prior.reduced !== next.reduced));
    if (next.saving && !prior?.saving) this.interrupted = false;
    if (scopeChanged && (next.saving || prior?.saving)) this.interrupted = true;
    const animate = Boolean(prior && !scopeChanged && !next.compact && !next.reduced && next.allowed && prior.allowed && !this.interrupted && prior.collapsed !== next.collapsed);
    const changed = !prior || scopeChanged || prior.collapsed !== next.collapsed || prior.allowed !== next.allowed || prior.drawer !== next.drawer;
    this.previous = next;
    if (!next.saving) this.interrupted = false;
    return { animate, changed };
  }
}
