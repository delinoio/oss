// SPDX-License-Identifier: Apache-2.0
import { Timestamp, TimestampText, TimestampMode } from "./timestamp-display";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@connectrpc/connect-query";
import { AccountTypeFilter, EntityKind, FailureCode, clientFailure, ResourceQuery, SystemCapability, SystemQuery, type Resource, type ClientFailure } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { subscriptionCatalog } from "./subscription-catalog";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { ScrollContinuation } from "./scroll-continuation";
import { ReadStage } from "./scroll-pagination";
import { freshWindow, railAccount, remainingBadge, type RailAccount, type RailWindow } from "./subscription-rail-data";
import "./subscription-rail.css";
import { Failure, InlineRemediation } from "./ui";
import { LocalConnectionHelp } from "./local-connection-presentation";

export function SubscriptionRail({ enabled, manage, focusFallback = () => undefined }: { enabled: boolean; manage: () => void; focusFallback?: () => void }) {
  useLocale();
  const [visible, setVisible] = useState(() => document.visibilityState !== "hidden");
  const [now, setNow] = useState(Date.now);
  const [authenticationLost, setAuthenticationLost] = useState(false);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled });
  const capable = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const allowed = enabled && !authenticationLost && visible && capable && !status.error && !status.data?.stopping;
  const request = useCallback((token: string) => ({ filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: token }, accountType: AccountTypeFilter.SUBSCRIPTION }), []);
  const project = useCallback((response: { resources: Resource[]; nextPageToken: string }) => {
    if (response.resources.length > 50 || new Set(response.resources.map(value => value.id)).size !== response.resources.length) throw new Error("Invalid subscription page");
    return { rows: response.resources.map(railAccount), nextPageToken: response.nextPageToken };
  }, []);
  const reader = useConnectPaginationReader(ResourceQuery.listResources, request, project);
  const query = usePaginationChain<RailAccount>(`subscription-rail:${enabled}:${authenticationLost}`, allowed, reader);
  useEffect(() => { if (!enabled) setAuthenticationLost(false); }, [enabled]);
  useEffect(() => { if (query.error?.failure.code === FailureCode.Unauthenticated || enabled && !status.isFetching && status.error && clientFailure(status.error).code === FailureCode.Unauthenticated) { setAuthenticationLost(true); setSelection(undefined); } }, [query.error, status.error, status.isFetching, enabled]);
  // Pagination clears transient errors at retry admission. Retained quota remains
  // unconfirmed until a complete accepted range replaces the failed snapshot.
  const [unconfirmedRead, setUnconfirmedRead] = useState(false);
  const retainedReadFailure = useRef<ClientFailure | undefined>(undefined);
  const failedRows = useRef<readonly RailAccount[] | undefined>(undefined);
  useEffect(() => {
    if (query.error) { retainedReadFailure.current = query.error.failure; failedRows.current = query.rows; setUnconfirmedRead(true); }
    else if (query.loaded && !query.loading && query.rows !== failedRows.current) { retainedReadFailure.current = undefined; failedRows.current = undefined; setUnconfirmedRead(false); }
  }, [query.error, query.loaded, query.loading, query.rows]);
  const root = useRef<HTMLDivElement>(null);
  const [selection, setSelection] = useState<{ id: string; opener: HTMLButtonElement }>();
  const popup = useRef<HTMLDivElement>(null);
  const fallbackFocus = useRef(focusFallback);
  fallbackFocus.current = focusFallback;
  const account = query.rows.find(row => row.id === selection?.id && row.connected);
  const close = useCallback(() => { setSelection(current => { if (current?.opener.isConnected) current.opener.focus(); else fallbackFocus.current(); return undefined; }); }, []);
  useEffect(() => {
    const visibility = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", visibility);
    return () => document.removeEventListener("visibilitychange", visibility);
  }, []);
  useEffect(() => {
    if (!allowed) return;
    setNow(Date.now());
    const refresh = () => { setNow(Date.now()); query.refresh(); };
    const timer = window.setInterval(refresh, 60000);
    // Staleness advances independently of requests; failed reads never keep a current badge.
    const clock = window.setInterval(() => setNow(Date.now()), 1000);
    window.addEventListener("focus", refresh);
    return () => { clearInterval(timer); clearInterval(clock); window.removeEventListener("focus", refresh); };
  }, [allowed, query.refresh]);
  useEffect(() => { if (selection && (!enabled || !account)) close(); }, [enabled, account, selection, close]);
  useLayoutEffect(() => {
    const element = popup.current, opener = selection?.opener;
    if (!element || !opener) return;
    const place = () => { const bounds = opener.getBoundingClientRect(); element.style.left = `${Math.max(4, Math.min(bounds.right + 8, window.innerWidth - element.offsetWidth - 4))}px`; element.style.top = `${Math.max(4, Math.min(bounds.top, window.innerHeight - element.offsetHeight - 4))}px`; };
    if (typeof element.showPopover === "function") element.showPopover();
    place(); element.focus();
    // Saved replacements can change window count or wrapping without a resize.
    // Re-clamp presentation only; this observer owns no reads or native actions.
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(place) : undefined;
    observer?.observe(element);
    const dismiss = (event: PointerEvent) => {
      if (element.contains(event.target as Node) || opener.contains(event.target as Node)) return;
      // A background pointer's default focus step would clear the restored
      // opener after close. Preserve ordinary focus/clicks on outside controls.
      const target = event.target instanceof Element ? event.target : undefined;
      if (!target?.closest("button, input, select, textarea, a[href], [tabindex], [contenteditable=true]")) event.preventDefault();
      close();
    };
    window.addEventListener("resize", place); document.addEventListener("pointerdown", dismiss);
    return () => { observer?.disconnect(); window.removeEventListener("resize", place); document.removeEventListener("pointerdown", dismiss); };
  }, [selection, close]);
  const accounts = enabled && !authenticationLost && capable && !status.error ? query.rows.filter(row => row.connected) : [];
  const unavailable = !allowed || Boolean(query.error) || unconfirmedRead;
  const limited = query.pages.length >= 200 && Boolean(query.nextPageToken);
  const brand = (value: RailAccount) => subscriptionCatalog.find(item => item.brand === value.service)!;
  if (!enabled) return null;
  if (authenticationLost) return <RailReadProblem summary={copy("subscription-rail.authentication")} />;
  if (status.error) return <RailReadProblem summary={copy("account-connection.inline.read")} failure={clientFailure(status.error)} retry={() => void status.refetch()} busy={status.isFetching} />;
  if (!capable) return status.data ? <RailReadProblem summary={copy("subscription-rail.unsupported")} retry={() => void status.refetch()} busy={status.isFetching} /> : <span role="status" className="subscription-rail-guidance">{copy("subscription-rail.loading")}</span>;
  if (query.loaded && !accounts.length && !query.nextPageToken && !query.error) return null;
  return <div className="subscription-rail-group" aria-label={copy("subscription-rail.title")}>
    <div ref={root} className="subscription-rail-scroll">
      {!query.loaded && !query.error ? <span role="status">{copy("subscription-rail.loading")}</span> : null}
      {accounts.map(row => { const percent = unavailable ? undefined : remainingBadge(row.windows, now); const label = `${brand(row).name} · ${row.alias} · ${percent === undefined ? copy("subscription-rail.unavailable") : copy("subscription-rail.remaining", { percent })}`; return <button type="button" key={row.id} className="sidebar-rail-button subscription-rail-account" aria-label={`${label} · ${row.id}`} title={label} aria-haspopup="dialog" aria-expanded={selection?.id === row.id} onClick={event => { if (selection?.id === row.id) close(); else setSelection({ id: row.id, opener: event.currentTarget }); }}><img src={brand(row).mark} alt="" width="24" height="24" /><span className="subscription-rail-badge" aria-hidden="true">{percent === undefined ? "—" : `${percent}%`}</span></button>; })}
      {/* Retained refreshes keep their accepted anchor without adding loading height. */}
      {query.nextPageToken || query.error || query.loading && query.loading !== ReadStage.Refresh ? <ScrollContinuation showErrors={false} query={{ ...query, loading: query.loading === ReadStage.Refresh ? undefined : query.loading, nextPageToken: limited ? "" : query.nextPageToken }} root={root} active={allowed && query.loading !== ReadStage.Refresh} label={copy("subscription-rail.title")} /> : null}
      {query.error || unconfirmedRead ? <RailReadProblem summary={copy("account-connection.inline.railPartial")} failure={query.error?.failure ?? retainedReadFailure.current} retry={query.error?.stalled || query.error?.failure.code === FailureCode.CursorExpired ? query.reload : query.retry} reload={query.error?.stalled || query.error?.failure.code === FailureCode.CursorExpired} busy={!allowed || Boolean(query.loading)} /> : null}
      {limited ? <p role="status">{copy("subscription-rail.limit")}</p> : null}
      {limited ? <button type="button" className="subscription-rail-reload" onClick={query.reload} disabled={!allowed || Boolean(query.loading)}>{copy("pagination.reload")}</button> : null}
    </div>
    {selection && account ? createPortal(<div ref={popup} popover="manual" role="dialog" aria-label={`${brand(account).name} · ${account.alias}`} tabIndex={-1} className="subscription-rail-popover subscription-account-popover" onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); } }}>
      <header><img src={brand(account).mark} alt="" width="24" height="24" /><strong>{brand(account).name}</strong><button type="button" onClick={close} aria-label={copy("subscription-rail.close")}>×</button></header>
      <p className="subscription-account-alias">{account.alias}</p>
      <p className="subscription-account-state">{copy(account.disabled ? "subscription-rail.disabled" : "subscription-rail.connected")}</p>
      {unavailable ? <p role="status">{copy("subscription-rail.readFailed")}</p> : null}
      {!unavailable && remainingBadge(account.windows, now) === undefined ? <p>{copy("subscription-rail.incomplete")}</p> : null}
      {account.windows.length ? account.windows.map((window, index) => <QuotaWindow key={`${window.id}:${index}`} window={window} index={index} now={now} unavailable={unavailable} />) : <p>{copy("subscription-rail.unknown")}</p>}
      <div className="subscription-account-actions">
        <button type="button" disabled={!allowed || Boolean(query.loading)} onClick={query.refresh}>{copy("account-connection.inline.railRecheck")}</button>
        <button type="button" onClick={() => { close(); manage(); }}>{copy("subscription-rail.manage")}</button>
      </div>
    </div>, document.body) : null}
  </div>;
}

/** Presentation only: preserve the original individual-value eligibility rules.
 * Historical saved values stay labelled stale/failed and never restore a badge. */
function QuotaWindow({ window, index, now, unavailable }: { window: RailWindow; index: number; now: number; unavailable: boolean }) {
  const percent = window.valid !== false && Number.isFinite(Date.parse(window.observedAt)) && Date.parse(window.observedAt) <= now && window.state !== "unknown" && window.state !== "unsupported" && typeof window.blocking === "boolean" && typeof window.remaining === "number" && Number.isFinite(window.remaining) && window.remaining >= 0 && window.remaining <= 1 ? Math.round(window.remaining * 100) : undefined;
  const current = !unavailable && freshWindow(window, now);
  const state = window.valid === false || typeof window.blocking !== "boolean" ? "subscription-rail.unknown" : !current && window.state === "observed" ? "subscription-rail.stale" : window.state === "observed" ? "subscription-rail.observed" : window.state === "failed" ? "subscription-rail.failed" : window.state === "unsupported" ? "subscription-rail.unsupportedQuota" : "subscription-rail.unknown";
  return <section className={`subscription-quota-window${current ? "" : " subscription-quota-historical"}`}>
    <p className="subscription-quota-id">{window.id || copy("subscription-rail.window", { index: index + 1 })}</p>
    <p className="subscription-quota-value">{percent === undefined ? copy("subscription-rail.unavailable") : <><strong>{percent}%</strong><span>{copy("subscription-rail.remainingLabel")}</span></>}</p>
    {percent !== undefined ? <div className="subscription-quota-bar" aria-hidden="true"><span style={{ width: `${percent}%` }} /></div> : null}
    <div className="subscription-quota-timing"><Timestamp value={window.resetAt} fallback="—" mode={TimestampMode.QuotaCountdown} expired={timestamp => <TimestampText id="subscription-rail.reset" values={{ time: timestamp }} />} /></div>
    <p className="subscription-quota-observation">{copy(state)} · <Timestamp value={window.observedAt} fallback="—" /></p>
  </section>;
}

/** A compact rail trigger keeps local explanation/actions out of navigation. */
function RailReadProblem({ summary, failure, retry, reload = false, busy = false }: { summary: string; failure?: ClientFailure; retry?: () => void; reload?: boolean; busy?: boolean }) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null), popup = useRef<HTMLDivElement>(null);
  const close = useCallback(() => { setOpen(false); trigger.current?.focus(); }, []);
  useLayoutEffect(() => {
    if (!open || !popup.current || !trigger.current) return;
    const element = popup.current;
    const place = () => { const bounds = trigger.current!.getBoundingClientRect(); element.style.left = `${Math.max(4, Math.min(bounds.right + 8, window.innerWidth - element.offsetWidth - 4))}px`; element.style.top = `${Math.max(4, Math.min(bounds.top, window.innerHeight - element.offsetHeight - 4))}px`; };
    element.showPopover?.(); place(); element.focus();
    const dismiss = (event: PointerEvent) => { if (!element.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) close(); };
    window.addEventListener("resize", place); document.addEventListener("pointerdown", dismiss);
    return () => { window.removeEventListener("resize", place); document.removeEventListener("pointerdown", dismiss); };
  }, [open, close]);
  const actions = <>{retry ? <button type="button" disabled={busy} onClick={retry}>{copy(reload ? "pagination.reload" : "pagination.retry")}</button> : null}<LocalConnectionHelp /></>;
  return <><button ref={trigger} type="button" className="sidebar-rail-button subscription-rail-account" aria-label={copy("account-connection.inline.railProblem")} title={summary} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}><span aria-hidden="true">!</span></button>{open ? createPortal(<div ref={popup} popover="manual" role="dialog" aria-label={copy("account-connection.inline.railProblem")} tabIndex={-1} className="subscription-rail-popover" onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); } }}><header><strong>{copy("subscription-rail.title")}</strong><button type="button" aria-label={copy("subscription-rail.close")} onClick={close}>×</button></header>{failure ? <Failure failure={failure} summary={<p>{summary}</p>} actions={actions} /> : <InlineRemediation summary={<p>{summary}</p>} actions={actions} />}</div>, document.body) : null}</>;
}
