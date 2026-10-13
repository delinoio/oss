// SPDX-License-Identifier: Apache-2.0
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type RefCallback,
} from "react";
import "./entrance-motion.css";

// Stable identifiers shared with entrance-motion.css.
export enum EntranceMotion {
  // 160ms translateY(4px).
  Y4 = "y4",
  // 120ms translateY(2px).
  Y2 = "y2",
  // 120ms translateX(4px).
  X4 = "x4",
}
/**
 * Which element of an opted-in surface receives the entrance. `Surface` moves
 * the whole category content column or task dialog. `Heading` moves only the
 * static heading, for surfaces whose body contains persistent portal hosts or
 * native controllers that no ancestor transform may touch.
 */
export enum EntranceTarget {
  Surface = "surface",
  Heading = "heading",
}
const entranceAnimationNames: Record<EntranceMotion, string> = {
  [EntranceMotion.Y4]: "entrance-y4",
  [EntranceMotion.Y2]: "entrance-y2",
  [EntranceMotion.X4]: "entrance-x4",
};
// Dispatched on an animated ancestor when a native dialog mounts beneath it.
const ENTRANCE_SETTLE_EVENT = "delidev-entrance-settle";
const reducedMotionQuery = "(prefers-reduced-motion: reduce)";
// The longest visual motion lasts 160ms. This generation-fenced fallback only settles
// state when no animation end/cancel event can arrive (for example, an element
// that is never displayed). It never gates focus, navigation or disposal.
const ENTRANCE_FALLBACK_MS = 240;

export interface EntranceProps {
  "data-entrance"?: EntranceMotion;
  // React has no animationcancel prop, so the ref attaches native listeners.
  ref?: RefCallback<HTMLElement>;
}

/** True when neither Animation Off nor OS reduced motion suppresses motion. */
export function entranceMotionAllowed(): boolean {
  if (document.documentElement.dataset.animation === "false") return false;
  return !(
    typeof window.matchMedia === "function" &&
    window.matchMedia(reducedMotionQuery)?.matches
  );
}

/**
 * Settles every in-flight entrance above `host`. A native dialog calls this on
 * mount so no animated ancestor wraps the top-layer host; the dialog's own
 * inner entrance (a descendant) is untouched and nothing waits for the motion.
 */
export function settleEnclosingEntrances(host: Element | null): void {
  for (let node = host?.parentElement; node; node = node.parentElement)
    if (node.hasAttribute("data-entrance"))
      node.dispatchEvent(new Event(ENTRANCE_SETTLE_EVENT));
}

/** True while `node` has a running CSS animation with this name. */
function entranceAnimationRunning(node: HTMLElement, name: string): boolean {
  if (typeof node.getAnimations !== "function") return false;
  return node
    .getAnimations()
    .some(
      (animation) =>
        (animation as CSSAnimation).animationName === name &&
        animation.playState === "running",
    );
}

/**
 * Counts the rising edges of `active` (including a mount that starts active).
 * Use the count in an activation key so a retained surface animates once per
 * explicit return and never for renders, refetches or edits while it is active.
 */
export function useActivationCount(active: boolean): number {
  const [state, setState] = useState({ count: active ? 1 : 0, active });
  if (state.active !== active) {
    setState({ count: active ? state.count + 1 : state.count, active });
    return active ? state.count + 1 : state.count;
  }
  return state.count;
}

/**
 * Counts explicit presentations of a retained surface. A mount that starts
 * visible is one presentation; afterwards only a changed `request` value that
 * becomes visible adds one. Visibility changes alone (a host slot appearing, a
 * page becoming active) never count, so they stay immediate. A request made
 * while hidden stays pending until the surface is visible.
 */
export function usePresentationCount(
  visible: boolean,
  request: string | number = 0,
): number {
  const [state, setState] = useState({
    count: visible ? 1 : 0,
    request,
    pending: false,
  });
  let { count, pending } = state;
  if (request !== state.request) pending = true;
  if (visible && pending) {
    count += 1;
    pending = false;
  }
  if (
    count !== state.count ||
    pending !== state.pending ||
    request !== state.request
  )
    setState({ count, request, pending });
  return count;
}

/**
 * Owns one explicit activation's entrance. A new `activation` key starts the
 * motion once; every later render, refetch, edit or preference re-enable keeps
 * it settled. A new key while the previous entrance is still running restarts
 * the CSS animation, because an unchanged declaration would otherwise continue
 * the old timeline. Turning motion off mid-flight clears the attribute
 * immediately, so re-enabling never replays the current activation. No exit
 * motion exists.
 */
export function useEntranceActivation(
  activation: string,
  enabled = true,
  motion = EntranceMotion.Y4,
): EntranceProps {
  const [state, setState] = useState(() => ({
    activation,
    active: enabled && entranceMotionAllowed(),
  }));
  let current = state;
  if (state.activation !== activation) {
    current = { activation, active: enabled && entranceMotionAllowed() };
    setState(current);
  }
  const active = current.active;
  const settle = useCallback(
    () =>
      setState((latest) =>
        latest.activation === activation && latest.active
          ? { activation, active: false }
          : latest,
      ),
    [activation],
  );
  useEffect(() => {
    if (!active) return;
    const settleWhenDisabled = () => {
      if (!entranceMotionAllowed()) settle();
    };
    const observer = new MutationObserver(settleWhenDisabled);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-animation"],
    });
    const media =
      typeof window.matchMedia === "function"
        ? window.matchMedia(reducedMotionQuery)
        : undefined;
    media?.addEventListener?.("change", settleWhenDisabled);
    const timer = window.setTimeout(settle, ENTRANCE_FALLBACK_MS);
    return () => {
      observer.disconnect();
      media?.removeEventListener?.("change", settleWhenDisabled);
      window.clearTimeout(timer);
    };
  }, [active, settle]);
  const surface = useRef<HTMLElement | null>(null);
  const started = useRef(activation);
  useLayoutEffect(() => {
    if (started.current === activation) return;
    started.current = activation;
    const node = surface.current;
    if (!active || !node) return;
    // Re-declare the attribute with a style flush between, so the browser
    // starts a fresh animation instead of continuing the previous timeline.
    node.removeAttribute("data-entrance");
    void node.getBoundingClientRect();
    node.setAttribute("data-entrance", motion);
  }, [activation, active, motion]);
  const attach = useCallback<RefCallback<HTMLElement>>(
    (node) => {
      if (!node) return;
      surface.current = node;
      const finished = (event: Event) => {
        // Descendant spinners and unrelated animations bubble here.
        if (
          event.target !== node ||
          (event as globalThis.AnimationEvent).animationName !==
            entranceAnimationNames[motion]
        )
          return;
        // Restarting removes the running animation, which queues an
        // animationcancel after the new activation has already begun. A live
        // animation of this name means the event is that stale cancellation.
        if (
          event.type === "animationcancel" &&
          entranceAnimationRunning(node, entranceAnimationNames[motion])
        )
          return;
        settle();
      };
      const settleNow = () => settle();
      node.addEventListener("animationend", finished);
      node.addEventListener("animationcancel", finished);
      node.addEventListener(ENTRANCE_SETTLE_EVENT, settleNow);
      return () => {
        if (surface.current === node) surface.current = null;
        node.removeEventListener("animationend", finished);
        node.removeEventListener("animationcancel", finished);
        node.removeEventListener(ENTRANCE_SETTLE_EVENT, settleNow);
      };
    },
    [settle, motion],
  );
  return active ? { "data-entrance": motion, ref: attach } : {};
}
