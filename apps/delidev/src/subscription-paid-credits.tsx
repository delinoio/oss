// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import {
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
  useLayoutEffect,
  useReducer,
  useRef,
  useState,
} from "react";
import { settleEnclosingEntrances } from "./entrance-motion";
import { copy, useLocale } from "./localization";
import { formatPaidCreditBalance } from "./paid-credit-format";
export { formatPaidCreditBalance } from "./paid-credit-format";
import { Timestamp } from "./timestamp-display";
import { object, items, text } from "./documents";
export enum PaidCreditState {
  Loading = "loading",
  Unsupported = "unsupported",
  Unknown = "unknown",
  Observed = "observed",
  Failed = "failed",
}
/**
 * Inline keeps the exact decimal in normal flow (Manage subscription and other
 * consumers). Overlay renders it out of flow so opening or closing it never
 * changes the geometry of the owning card, rail popover or Settings row.
 */
export enum ExactDisclosurePresentation {
  Inline = "inline",
  Overlay = "overlay",
}
export enum ExactDisclosurePlacement {
  Right = "right",
  Below = "below",
  Above = "above",
}
const DISCLOSURE_EDGE = 8;
const DISCLOSURE_GAP = 8;
export interface DisclosureRect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}
export interface DisclosureSize {
  width: number;
  height: number;
}
/** Pure viewport placement: right of the trigger, else below, else above, always clamped. */
export function placeExactDisclosure(
  anchor: DisclosureRect,
  box: DisclosureSize,
  viewport: DisclosureSize,
): { left: number; top: number; placement: ExactDisclosurePlacement } {
  const maximumLeft = Math.max(
    DISCLOSURE_EDGE,
    viewport.width - box.width - DISCLOSURE_EDGE,
  );
  const maximumTop = Math.max(
    DISCLOSURE_EDGE,
    viewport.height - box.height - DISCLOSURE_EDGE,
  );
  const clamp = (value: number, maximum: number) =>
    Math.round(Math.max(DISCLOSURE_EDGE, Math.min(value, maximum)));
  const right = anchor.right + DISCLOSURE_GAP;
  if (right + box.width <= viewport.width - DISCLOSURE_EDGE)
    return {
      left: clamp(right, maximumLeft),
      top: clamp(anchor.top, maximumTop),
      placement: ExactDisclosurePlacement.Right,
    };
  const left = clamp(anchor.left, maximumLeft);
  if (
    anchor.bottom + DISCLOSURE_GAP + box.height <=
    viewport.height - DISCLOSURE_EDGE
  )
    return {
      left,
      top: clamp(anchor.bottom + DISCLOSURE_GAP, maximumTop),
      placement: ExactDisclosurePlacement.Below,
    };
  if (anchor.top - DISCLOSURE_GAP - box.height >= DISCLOSURE_EDGE)
    return {
      left,
      top: clamp(anchor.top - DISCLOSURE_GAP - box.height, maximumTop),
      placement: ExactDisclosurePlacement.Above,
    };
  return {
    left,
    top: clamp(anchor.bottom + DISCLOSURE_GAP, maximumTop),
    placement: ExactDisclosurePlacement.Below,
  };
}
/** True when the trigger is scrolled fully outside the viewport or any clipping ancestor. */
function anchorLost(anchor: HTMLElement) {
  if (!anchor.isConnected) return true;
  const bounds = anchor.getBoundingClientRect();
  // An unmeasured (zero-size) trigger carries no clipping evidence.
  if (bounds.right - bounds.left <= 0 && bounds.bottom - bounds.top <= 0)
    return false;
  const outside = (clip: DisclosureRect) =>
    bounds.bottom <= clip.top ||
    bounds.top >= clip.bottom ||
    bounds.right <= clip.left ||
    bounds.left >= clip.right;
  if (
    outside({
      left: 0,
      top: 0,
      right: window.innerWidth,
      bottom: window.innerHeight,
    })
  )
    return true;
  for (
    let node = anchor.parentElement;
    node && node !== document.body;
    node = node.parentElement
  ) {
    const style = getComputedStyle(node);
    if (
      /(auto|scroll|hidden|clip)/.test(`${style.overflowX}${style.overflowY}`)
    )
      if (outside(node.getBoundingClientRect())) return true;
  }
  return false;
}
function ExactDisclosureOverlay({
  id,
  anchor,
  onLost,
  children,
}: {
  id: string;
  anchor: RefObject<HTMLElement | null>;
  onLost: () => void;
  children: ReactNode;
}) {
  const node = useRef<HTMLSpanElement>(null);
  const lost = useRef(onLost);
  lost.current = onLost;
  const supportsPopover =
    typeof HTMLElement.prototype.showPopover === "function";
  useLayoutEffect(() => {
    const element = node.current,
      source = anchor.current;
    if (!element || !source) return;
    // The overlay is a fixed positioning host: an in-flight category entrance
    // above it settles first (focus and interaction never wait for it) and the
    // anchor is measured again once the untransformed layout has committed.
    settleEnclosingEntrances(element);
    // The manual top layer escapes parent overflow without becoming modal or
    // moving focus; the element stays a DOM child of its owning parent so
    // outside-pointer and Escape ownership checks keep treating it as inside.
    if (supportsPopover) {
      try {
        element.showPopover();
      } catch {
        console.warn("delidev.subscription.paid_credit_disclosure_failed", {
          phase: "show",
          classification: "presentation-unavailable",
        });
      }
    }
    const place = () => {
      if (anchorLost(source)) {
        lost.current();
        return;
      }
      const box = element.getBoundingClientRect();
      const next = placeExactDisclosure(
        source.getBoundingClientRect(),
        { width: box.width, height: box.height },
        { width: window.innerWidth, height: window.innerHeight },
      );
      element.style.left = `${next.left}px`;
      element.style.top = `${next.top}px`;
      element.dataset.placement = next.placement;
    };
    place();
    const settled =
      typeof requestAnimationFrame === "function"
        ? requestAnimationFrame(place)
        : undefined;
    const observer =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(place)
        : undefined;
    observer?.observe(element);
    observer?.observe(source);
    window.addEventListener("resize", place);
    // Capture also sees scrolling of any ancestor container.
    window.addEventListener("scroll", place, true);
    return () => {
      if (settled !== undefined) cancelAnimationFrame(settled);
      observer?.disconnect();
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
      try {
        if (supportsPopover && element.matches(":popover-open"))
          element.hidePopover();
      } catch {
        // The element is already detached or hidden.
      }
    };
  }, [anchor, supportsPopover]);
  return (
    <span
      ref={node}
      id={id}
      role="tooltip"
      className="subscription-paid-exact subscription-paid-exact-overlay"
      popover={supportsPopover ? "manual" : undefined}
    >
      {children}
    </span>
  );
}
export interface PaidCreditBucket {
  id: string;
  hasCredits: boolean;
  unlimited: boolean;
  balance: string | null;
  observedAt: string;
}
const decimal = /^[0-9]+(?:\.[0-9]+)?$/;
export function paidCreditProjection(
  subscription: Record<string, unknown>,
): PaidCreditBucket[] {
  const values = items(subscription.paid_credits);
  if (values.length > 32) return [];
  const result: PaidCreditBucket[] = [];
  const seen = new Set<string>();
  for (const entry of values) {
    const b = object(entry),
      id = text(b.id),
      balance = b.balance,
      at = text(b.observed_at);
    if (
      !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,109}$/.test(id) ||
      seen.has(id) ||
      typeof b.has_credits !== "boolean" ||
      typeof b.unlimited !== "boolean" ||
      (balance !== null &&
        (typeof balance !== "string" ||
          balance.length > 64 ||
          !decimal.test(balance))) ||
      !Number.isFinite(Date.parse(at))
    )
      return [];
    seen.add(id);
    result.push({
      id,
      hasCredits: b.has_credits,
      unlimited: b.unlimited,
      balance,
      observedAt: at,
    });
  }
  return result;
}
/** The disclosure owns only presentation; opening it never reads balance data. */
export function PaidCreditValue({
  bucket,
  exactPresentation = ExactDisclosurePresentation.Inline,
}: {
  bucket: PaidCreditBucket;
  exactPresentation?: ExactDisclosurePresentation;
}) {
  useLocale();
  const formatted =
    bucket.balance === null ? null : formatPaidCreditBalance(bucket.balance);
  const [open, setOpen] = useState(false);
  const pinned = useRef(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  const container = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open || formatted === null) return;
    // A hovered value can be open while focus remains outside the card. Capture
    // Escape before the parent Settings dismissal, and preserve outside focus.
    const close = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      pinned.current = false;
      if (container.current?.contains(document.activeElement))
        trigger.current?.focus({ preventScroll: true });
      setOpen(false);
    };
    document.addEventListener("keydown", close, true);
    return () => document.removeEventListener("keydown", close, true);
  }, [open, formatted]);
  return (
    <div
      ref={container}
      className="subscription-paid-value"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={(event) => {
        if (
          !pinned.current &&
          !event.currentTarget.contains(document.activeElement)
        )
          setOpen(false);
      }}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) {
          pinned.current = false;
          setOpen(false);
        }
      }}
    >
      <span className="subscription-paid-balance">
        {bucket.unlimited
          ? copy("paid-credits.unlimited")
          : (formatted ?? copy("paid-credits.unknown"))}
      </span>
      {!bucket.unlimited && formatted !== null ? (
        <span className="subscription-paid-unit">
          {copy("paid-credits.unit")}
        </span>
      ) : null}
      {formatted !== null ? (
        <>
          <button
            ref={trigger}
            type="button"
            className="subscription-paid-info"
            aria-label={copy("paid-credits.exact-label")}
            aria-expanded={open}
            aria-controls={id}
            aria-describedby={open ? id : undefined}
            onFocus={() => setOpen(true)}
            onClick={() => {
              pinned.current = !pinned.current;
              setOpen(pinned.current);
            }}
          >
            <svg
              aria-hidden="true"
              focusable="false"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
            >
              <circle cx="10" cy="10" r="8" />
              <path d="M10 9v5M10 5.5v1" />
            </svg>
          </button>
          {open ? (
            exactPresentation === ExactDisclosurePresentation.Overlay ? (
              <ExactDisclosureOverlay
                id={id}
                anchor={trigger}
                onLost={() => {
                  pinned.current = false;
                  setOpen(false);
                }}
              >
                {copy("paid-credits.exact", { balance: bucket.balance })}
              </ExactDisclosureOverlay>
            ) : (
              <span id={id} role="tooltip" className="subscription-paid-exact">
                {copy("paid-credits.exact", { balance: bucket.balance })}
              </span>
            )
          ) : null}
        </>
      ) : null}
    </div>
  );
}
function stale(bucket: PaidCreditBucket, now: number) {
  const at = Date.parse(bucket.observedAt);
  return !Number.isFinite(at) || at > now || now - at > 300000;
}
function PaidCreditObservation({
  buckets,
  state,
  active,
  now,
}: {
  buckets: readonly PaidCreditBucket[];
  state: PaidCreditState;
  active: boolean;
  now: number;
}) {
  const failed = state === PaidCreditState.Failed;
  return (
    <small className="subscription-paid-observation">
      {copy(
        failed
          ? "paid-credits.failed"
          : buckets.some((bucket) => stale(bucket, now))
            ? "paid-credits.stale"
            : "paid-credits.observed",
      )}
      {buckets.map((bucket, index) => (
        <span key={bucket.id}>
          {" "}
          · {buckets.length > 1 ? `${bucket.id}: ` : ""}
          <Timestamp active={active} value={bucket.observedAt} />
        </span>
      ))}
    </small>
  );
}
export function PaidCredits({
  buckets = [],
  state,
  compact = false,
  active = true,
  now: givenNow,
  exactPresentation = ExactDisclosurePresentation.Inline,
}: {
  buckets?: readonly PaidCreditBucket[];
  state: PaidCreditState;
  compact?: boolean;
  active?: boolean;
  now?: number;
  exactPresentation?: ExactDisclosurePresentation;
}) {
  useLocale();
  const [, tick] = useReducer((v) => v + 1, 0);
  const now = givenNow ?? Date.now();
  useEffect(() => {
    if (!active || givenNow !== undefined) return;
    const due = buckets
      .flatMap((b) => [
        Date.parse(b.observedAt),
        Date.parse(b.observedAt) + 300001,
      ])
      .filter((t) => t > now);
    if (!due.length) return;
    const timer = setTimeout(
      tick,
      Math.min(2147483647, Math.max(1, Math.min(...due) - now)),
    );
    return () => clearTimeout(timer);
  }, [active, buckets, givenNow, now]);
  const unavailable =
    state === PaidCreditState.Loading
      ? copy("paid-credits.loading")
      : state === PaidCreditState.Unsupported
        ? copy("paid-credits.unsupported")
        : state === PaidCreditState.Failed
          ? copy("paid-credits.unavailable")
          : copy("paid-credits.unknown");
  const hasEvidence =
    state !== PaidCreditState.Unsupported &&
    state !== PaidCreditState.Loading &&
    buckets.length > 0;
  return (
    <section
      className={`subscription-paid-credits${compact ? " subscription-paid-summary" : ""}`}
      aria-label={copy("paid-credits.title")}
    >
      <h3>{copy("paid-credits.title")}</h3>
      {!hasEvidence ? (
        <div role="status">
          <p className="subscription-paid-empty">{unavailable}</p>
          {state === PaidCreditState.Failed ? (
            <small className="subscription-paid-observation">
              {copy("paid-credits.failed-empty")}
            </small>
          ) : null}
        </div>
      ) : compact ? (
        <>
          {buckets.length === 1 ? (
            <PaidCreditValue
              key={`${buckets[0].id}:${buckets[0].balance}`}
              bucket={buckets[0]}
              exactPresentation={exactPresentation}
            />
          ) : (
            <p className="subscription-paid-empty">
              {copy("paid-credits.count", { count: buckets.length })}
            </p>
          )}
          <PaidCreditObservation
            buckets={buckets}
            state={state}
            active={active}
            now={now}
          />
        </>
      ) : (
        <ul>
          {buckets.map((bucket) => (
            <li key={bucket.id}>
              <strong className="subscription-paid-bucket">{bucket.id}</strong>
              <PaidCreditValue
                key={`${bucket.id}:${bucket.balance}`}
                bucket={bucket}
                exactPresentation={exactPresentation}
              />
              <PaidCreditObservation
                buckets={[bucket]}
                state={state}
                active={active}
                now={now}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
