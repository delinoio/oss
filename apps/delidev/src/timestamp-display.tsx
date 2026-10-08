// SPDX-License-Identifier: Apache-2.0
import { Fragment, useCallback, useSyncExternalStore, type ReactNode } from "react";
import { copy, useLocale, type MessageKey } from "./localization";
import { useDateFormat } from "./date-format";
import { formatTimestampLabel, nextTimestampTransition, timestampInstant, TimestampMode } from "./timestamp-format";
export { TimestampMode } from "./timestamp-format";

// One presentation scheduler per renderer window. It owns no business clocks,
// queries or native requests. All mounted labels share its next transition.
const subscribers = new Map<() => void, number>();
let observedNow = Date.now(), timer: ReturnType<typeof setTimeout> | undefined;
function schedule() {
  if (timer !== undefined) clearTimeout(timer);
  timer = undefined;
  if (!subscribers.size || document.visibilityState === "hidden") return;
  const now = Date.now();
  const next = [...subscribers.values()].map(value => nextTimestampTransition(value, now)).filter((value): value is number => value !== undefined);
  if (next.length) timer = setTimeout(tick, Math.max(1, Math.min(2147483647, Math.min(...next) - now)));
}
function tick() { observedNow = Date.now(); for (const notify of subscribers.keys()) notify(); schedule(); }
function subscribe(notify: () => void, instant: number | undefined) {
  if (instant === undefined) return () => {};
  if (!subscribers.size) {
    document.addEventListener("visibilitychange", tick);
    window.addEventListener("focus", tick);
  }
  subscribers.set(notify, instant);
  observedNow = Date.now();
  schedule();
  return () => {
    subscribers.delete(notify); schedule();
    if (!subscribers.size) {
      document.removeEventListener("visibilitychange", tick);
      window.removeEventListener("focus", tick);
    }
  };
}
const getClock = () => observedNow;

export function Timestamp({ value, fallback = "", mode = TimestampMode.Ordinary, timeZone, active = true }: {
  value?: string; fallback?: ReactNode; mode?: TimestampMode; timeZone?: string; active?: boolean;
}) {
  useLocale();
  const preference = useDateFormat();
  const instant = value ? timestampInstant(value) : undefined;
  const listen = useCallback((notify: () => void) => subscribe(notify, active && mode === TimestampMode.Ordinary ? instant : undefined), [instant, mode, active]);
  const now = useSyncExternalStore(listen, getClock, getClock);
  if (!value) return <>{fallback}</>;
  return <time dateTime={value} title={value} aria-description={value}>{formatTimestampLabel(value, { preference, mode, timeZone, now })}</time>;
}

/** Catalog interpolation with inert React slots. Timestamp children retain
 * their own clock and preference subscriptions without remounting workflows. */
export function TimestampText({ id, values }: { id: MessageKey; values: Record<string, ReactNode> }) {
  useLocale();
  const keys = Object.keys(values);
  const markers = Object.fromEntries(keys.map((key, index) => [key, `<timestamp-slot-${index}/>`]));
  return copy(id, markers).split(/(<timestamp-slot-\d+\/>)/).map((part, index) => {
    const slot = /^<timestamp-slot-(\d+)\/>$/.exec(part);
    return <Fragment key={slot ? `slot-${slot[1]}` : `text-${index}`}>{slot ? values[keys[Number(slot[1])]!] : part}</Fragment>;
  });
}
