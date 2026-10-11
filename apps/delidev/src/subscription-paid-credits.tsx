// SPDX-License-Identifier: Apache-2.0
import "./subscription-paid-credits.css";
import { useEffect, useId, useReducer, useRef, useState } from "react";
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
export function PaidCreditValue({ bucket }: { bucket: PaidCreditBucket }) {
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
            <span id={id} role="tooltip" className="subscription-paid-exact">
              {copy("paid-credits.exact", { balance: bucket.balance })}
            </span>
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
}: {
  buckets?: readonly PaidCreditBucket[];
  state: PaidCreditState;
  compact?: boolean;
  active?: boolean;
  now?: number;
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
