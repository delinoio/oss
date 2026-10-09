// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef } from "react";

/** Measure the original primary range without closing or cloning live controls. */
export function useProjectSessionPlacement(active: boolean, language: string) {
  const page = useRef<HTMLElement>(null), content = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLElement>(null), composer = useRef<HTMLDivElement>(null), hints = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const section = page.current, body = content.current, start = heading.current, end = hints.current;
    const owner = section?.closest("main");
    if (!active || !section || !body || !start || !end || !owner) return;
    const measure = () => {
      if (section.hidden || owner.clientHeight <= 0) return;
      let primaryHeight = end.getBoundingClientRect().bottom - start.getBoundingClientRect().top;
      // Only the direct Additional repositories body contributes. Branch popups
      // are fixed top-layer overlays and never contribute to primary flow height.
      for (const details of body.querySelectorAll<HTMLDetailsElement>(".starting-branches > details[open]")) {
        const summary = details.querySelector<HTMLElement>(":scope > summary");
        if (!summary) continue;
        const style = getComputedStyle(details);
        const frame = [style.paddingTop, style.paddingBottom, style.borderTopWidth, style.borderBottomWidth].reduce((sum, value) => sum + (parseFloat(value) || 0), 0);
        primaryHeight -= Math.max(0, details.getBoundingClientRect().height - summary.getBoundingClientRect().height - frame);
      }
      const style = getComputedStyle(section);
      const padding = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
      const adjustment = parseFloat(style.getPropertyValue("--project-session-center-adjustment")) || 0;
      const top = style.getPropertyValue("--project-session-center").trim() === "0" ? 0 : Math.max(0, (owner.clientHeight - padding - primaryHeight) / 2 + adjustment);
      if (!Number.isFinite(top)) return;
      const value = `${top}px`;
      if (body.style.getPropertyValue("--project-session-top") !== value) body.style.setProperty("--project-session-top", value);
    };
    measure();
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : undefined;
    for (const node of [owner, body]) observer?.observe(node);
    const mutations = new MutationObserver(measure);
    mutations.observe(body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["open"] });
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); mutations.disconnect(); window.removeEventListener("resize", measure); };
  }, [active, language]);
  return { page, content, heading, composer, hints };
}
