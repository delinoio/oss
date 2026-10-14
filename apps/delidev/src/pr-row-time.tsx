// SPDX-License-Identifier: Apache-2.0
import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type FocusEvent,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { settleEnclosingEntrances } from "./entrance-motion";
import { formatUtcInstant } from "./timestamp-format";

/** What revealed the tooltip decides which departure event dismisses it. */
enum RevealSource {
  Pointer = "pointer",
  Focus = "focus",
}
type Anchor = { left: number; top: number; bottom: number };
type Placement = { left: number; top: number };

const edge = 8,
  gap = 6;

export interface PRRowTime {
  /** Attach to the element that visually carries the relative time. */
  timeRef: RefObject<HTMLSpanElement | null>;
  timeProps: {
    onPointerEnter: () => void;
    onPointerLeave: () => void;
  };
  /** Attach to the row's native button so keyboard focus exposes the instant. */
  buttonProps: {
    onFocus: (event: FocusEvent<HTMLButtonElement>) => void;
    onBlur: () => void;
    "aria-describedby": string | undefined;
  };
  tooltip: ReactNode;
}

/** Row-owned tooltip for the exact original UTC instant. It is a fixed portal
 * that never occupies row layout, performs no request and owns no timer. It
 * is dismissed by Escape, blur, pointer departure, scroll, resize or when the
 * owning row unmounts. */
export function usePRRowTime(value: string): PRRowTime {
  const exact = formatUtcInstant(value),
    id = useId(),
    timeRef = useRef<HTMLSpanElement>(null),
    tooltipRef = useRef<HTMLDivElement>(null);
  const [revealed, setRevealed] = useState<{
    anchor: Anchor;
    source: RevealSource;
  }>();
  const [placement, setPlacement] = useState<Placement>();
  const visible = Boolean(revealed) && exact !== undefined;
  const hide = () => {
    setRevealed(undefined);
    setPlacement(undefined);
  };
  useLayoutEffect(() => {
    if (!revealed || !tooltipRef.current) return;
    const zoom = Number(getComputedStyle(document.body).zoom) || 1,
      box = tooltipRef.current.getBoundingClientRect(),
      width = window.innerWidth / zoom,
      height = box.height / zoom,
      above = revealed.anchor.top - height - gap;
    setPlacement({
      left: Math.max(
        edge,
        Math.min(revealed.anchor.left, width - box.width / zoom - edge),
      ),
      top: above >= edge ? above : revealed.anchor.bottom + gap,
    });
  }, [revealed, exact]);
  useEffect(() => {
    if (!visible) return;
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") hide();
    };
    window.addEventListener("scroll", hide, true);
    window.addEventListener("resize", hide);
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("scroll", hide, true);
      window.removeEventListener("resize", hide);
      window.removeEventListener("keydown", key);
    };
  }, [visible]);
  // A different instant (for example after a refresh) never keeps a stale tip.
  useEffect(() => {
    hide();
  }, [value]);
  const reveal = (source: RevealSource) => {
    const target = timeRef.current;
    if (!target || exact === undefined) return;
    // Measure the untransformed target, not one inside an in-flight page entrance.
    settleEnclosingEntrances(target);
    const rect = target.getBoundingClientRect(),
      zoom = Number(getComputedStyle(document.body).zoom) || 1;
    setPlacement(undefined);
    setRevealed({
      source,
      anchor: {
        left: rect.left / zoom,
        top: rect.top / zoom,
        bottom: rect.bottom / zoom,
      },
    });
  };
  return {
    timeRef,
    timeProps: {
      onPointerEnter: () => reveal(RevealSource.Pointer),
      onPointerLeave: () => {
        if (revealed?.source === RevealSource.Pointer) hide();
      },
    },
    buttonProps: {
      onFocus: (event) => {
        // Pointer clicks also focus the button; only keyboard focus reveals.
        let keyboard = true;
        try {
          keyboard = event.currentTarget.matches(":focus-visible");
        } catch {
          keyboard = true;
        }
        if (keyboard) reveal(RevealSource.Focus);
      },
      onBlur: hide,
      "aria-describedby": visible ? id : undefined,
    },
    tooltip:
      visible && revealed
        ? createPortal(
            <div
              id={id}
              ref={tooltipRef}
              role="tooltip"
              className="settings-action-tooltip pr-row-time-tooltip"
              style={
                placement ?? {
                  left: revealed.anchor.left,
                  top: revealed.anchor.top,
                  opacity: 0,
                }
              }
            >
              {exact}
            </div>,
            document.body,
          )
        : null,
  };
}
