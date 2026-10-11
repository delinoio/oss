// SPDX-License-Identifier: Apache-2.0
export interface SidebarPresentation {
  generation: number;
  settled: boolean;
  collapsed: boolean;
  focusAllowed: boolean;
}

/** Owns visual geometry only. Logical accessibility and preference admission stay outside. */
export class SidebarPaneMotion {
  private generation: number;
  private collapsed = false;
  private retire = () => {};

  constructor(
    private readonly app: HTMLElement,
    private readonly pane: HTMLDialogElement,
    private readonly content: HTMLElement,
    private readonly changed: (value: SidebarPresentation) => void,
    generation = 0,
  ) {
    this.generation = generation;
  }

  present(
    collapsed: boolean,
    expandedWidth: number,
    animate: boolean,
    focusAllowed = true,
  ) {
    this.collapsed = collapsed;
    const current = Math.max(
      0,
      Number.parseFloat(getComputedStyle(this.app).gridTemplateColumns) - 53,
    );
    const owned = ++this.generation;
    this.retire();
    this.app.classList.remove("sidebar-motion");
    const target = collapsed ? 0 : expandedWidth;
    let completed = false;
    const write = (width: number) => {
      this.app.style.gridTemplateColumns = `${53 + width}px minmax(0, 1fr)`;
      this.content.style.transform = `translateX(${width - expandedWidth}px)`;
    };
    const settle = (cancelled = false) => {
      if (owned !== this.generation || completed) return;
      completed = true;
      // Cancellation retires pending destination focus as well as geometry.
      if (cancelled) this.generation++;
      this.retire();
      this.app.classList.remove("sidebar-motion");
      write(target);
      this.pane.hidden = collapsed;
      if (collapsed) this.pane.removeAttribute("open");
      else this.pane.setAttribute("open", "");
      this.pane.dataset.sidebarMotion = "settled";
      this.changed({
        generation: this.generation,
        settled: true,
        collapsed,
        focusAllowed: focusAllowed && !cancelled,
      });
    };
    if (
      !animate ||
      !Number.isFinite(current) ||
      Math.abs(current - target) < 0.5
    ) {
      settle();
      return;
    }
    // Freeze the current rendered track before replacing a transition. This
    // gives each admitted reversal its own full duration without a width jump.
    write(current);
    this.pane.hidden = false;
    this.pane.setAttribute("open", "");
    this.pane.dataset.sidebarMotion = collapsed ? "closing" : "opening";
    this.changed({
      generation: owned,
      settled: false,
      collapsed,
      focusAllowed,
    });
    this.app.getBoundingClientRect();
    this.app.classList.add("sidebar-motion");
    write(target);
    if (
      !getComputedStyle(this.app)
        .transitionDuration.split(",")
        .some((value) => Number.parseFloat(value) > 0)
    ) {
      settle();
      return;
    }
    const end = (event: TransitionEvent) => {
      if (
        event.target !== this.app ||
        event.propertyName !== "grid-template-columns"
      )
        return;
      // A queued end event from a replaced transition has no generation identity.
      // It cannot settle this generation before its rendered endpoint is reached.
      const rendered =
        Number.parseFloat(getComputedStyle(this.app).gridTemplateColumns) - 53;
      if (Math.abs(rendered - target) < 0.5) settle();
    };
    this.app.addEventListener("transitionend", end);
    const timer = window.setTimeout(settle, 240);
    this.retire = () => {
      window.clearTimeout(timer);
      this.app.removeEventListener("transitionend", end);
      this.retire = () => {};
    };
    // The actual CSS transition object distinguishes its cancellation from a
    // queued cancellation event belonging to an older, reversed transition.
    const transition = this.app
      .getAnimations?.()
      .find(
        (value) =>
          "transitionProperty" in value &&
          (value as CSSTransition).transitionProperty ===
            "grid-template-columns",
      );
    if (transition)
      void transition.finished.then(
        () => settle(),
        () => settle(true),
      );
  }

  dispose() {
    this.generation++;
    this.retire();
    this.app.classList.remove("sidebar-motion");
    this.app.style.removeProperty("grid-template-columns");
    this.content.style.removeProperty("transform");
    this.pane.hidden = this.collapsed;
    if (this.collapsed) this.pane.removeAttribute("open");
    else this.pane.setAttribute("open", "");
    delete this.pane.dataset.sidebarMotion;
    return this.generation;
  }
}
