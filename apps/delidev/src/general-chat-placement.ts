// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef } from "react";

/** Center the primary range without borrowing the Options scroll extent.
 * Separate refs retain the existing form/fieldset ownership and input lifetime. */
export function useGeneralChatPlacement(active: boolean, language: string) {
  const page = useRef<HTMLElement>(null), content = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLElement>(null), composer = useRef<HTMLDivElement>(null), hints = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const section = page.current, body = content.current, start = heading.current, input = composer.current, end = hints.current;
    const owner = section?.closest("main");
    if (!active || !section || !body || !start || !input || !end || !owner) return;
    const measure = () => {
      if (section.hidden || owner.clientHeight <= 0) return;
      const style = getComputedStyle(section);
      const primaryHeight = end.getBoundingClientRect().bottom - start.getBoundingClientRect().top;
      const padding = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
      const adjustment = parseFloat(style.getPropertyValue("--general-chat-center-adjustment")) || 0;
      const top = style.getPropertyValue("--general-chat-center").trim() === "0" ? 0 : Math.max(0, (owner.clientHeight - padding - primaryHeight) / 2 + adjustment);
      if (!Number.isFinite(top)) return;
      const value = `${top}px`;
      if (body.style.getPropertyValue("--general-chat-top") !== value) body.style.setProperty("--general-chat-top", value);
    };
    measure();
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : undefined;
    // Options and total page height are deliberately not observation inputs.
    for (const node of [owner, start, input, end]) observer?.observe(node);
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); window.removeEventListener("resize", measure); };
  }, [active, language]);
  return { page, content, heading, composer, hints };
}
