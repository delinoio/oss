// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@connectrpc/connect-query";
import { AccountTypeFilter, EntityKind, FailureCode, clientFailure, ResourceQuery, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { subscriptionCatalog } from "./subscription-catalog";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { ScrollContinuation } from "./scroll-continuation";
import { freshWindow, railAccount, remainingBadge, type RailAccount } from "./subscription-rail-data";
import "./subscription-rail.css";

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
  const failedRows = useRef<readonly RailAccount[] | undefined>(undefined);
  useEffect(() => {
    if (query.error) { failedRows.current = query.rows; setUnconfirmedRead(true); }
    else if (query.loaded && !query.loading && query.rows !== failedRows.current) { failedRows.current = undefined; setUnconfirmedRead(false); }
  }, [query.error, query.loaded, query.loading, query.rows]);
  const root = useRef<HTMLDivElement>(null), fallback = useRef<HTMLButtonElement>(null);
  const [selection, setSelection] = useState<{ id: string; opener: HTMLButtonElement }>();
  const popup = useRef<HTMLDivElement>(null);
  const fallbackFocus = useRef(focusFallback);
  fallbackFocus.current = focusFallback;
  const account = query.rows.find(row => row.id === selection?.id && row.connected);
  const close = useCallback(() => { setSelection(current => { if (current?.opener.isConnected) current.opener.focus(); else if (fallback.current?.isConnected) fallback.current.focus(); else fallbackFocus.current(); return undefined; }); }, []);
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
    const dismiss = (event: PointerEvent) => { if (!element.contains(event.target as Node) && !opener.contains(event.target as Node)) close(); };
    window.addEventListener("resize", place); document.addEventListener("pointerdown", dismiss);
    return () => { window.removeEventListener("resize", place); document.removeEventListener("pointerdown", dismiss); };
  }, [selection, close]);
  const accounts = enabled && !authenticationLost && capable && !status.error ? query.rows.filter(row => row.connected) : [];
  const unavailable = !allowed || Boolean(query.error) || unconfirmedRead;
  const limited = query.pages.length >= 200 && Boolean(query.nextPageToken);
  const brand = (value: RailAccount) => subscriptionCatalog.find(item => item.brand === value.service)!;
  if (!enabled) return null;
  if (authenticationLost) return <span role="status" className="subscription-rail-guidance">{copy("subscription-rail.authentication")}</span>;
  if (status.error) return <div className="subscription-rail-guidance" role="status">{copy("subscription-rail.readFailed")}<button type="button" onClick={() => void status.refetch()}>{copy("pagination.retry")}</button></div>;
  if (!capable) return status.data || status.error ? <span className="subscription-rail-guidance" title={copy("subscription-rail.unsupported")} role="status">{copy("subscription-rail.unavailable")}</span> : <span role="status" className="subscription-rail-guidance">{copy("subscription-rail.loading")}</span>;
  if (query.loaded && !accounts.length && !query.nextPageToken && !query.error) return null;
  return <div className="subscription-rail-group" aria-label={copy("subscription-rail.title")}>
    <div ref={root} className="subscription-rail-scroll">
      {!query.loaded && !query.error ? <span role="status">{copy("subscription-rail.loading")}</span> : null}
      {accounts.map(row => { const percent = unavailable ? undefined : remainingBadge(row.windows, now); const label = `${brand(row).name} · ${row.alias} · ${percent === undefined ? copy("subscription-rail.unavailable") : copy("subscription-rail.remaining", { percent })}`; return <button type="button" key={row.id} className="sidebar-rail-button subscription-rail-account" aria-label={`${label} · ${row.id}`} title={label} aria-haspopup="dialog" aria-expanded={selection?.id === row.id} onClick={event => { if (selection?.id === row.id) close(); else setSelection({ id: row.id, opener: event.currentTarget }); }}><img src={brand(row).mark} alt="" width="24" height="24" /><span className="subscription-rail-badge" aria-hidden="true">{percent === undefined ? "—" : `${percent}%`}</span></button>; })}
      {query.nextPageToken || query.error || query.loading ? <ScrollContinuation query={{ ...query, nextPageToken: limited ? "" : query.nextPageToken }} root={root} active={allowed} label={copy("subscription-rail.title")} /> : null}
      {query.error?.failure.code === FailureCode.PermissionDenied ? <p role="status">{copy("subscription-rail.denied")}</p> : null}
      {limited ? <p role="status">{copy("subscription-rail.limit")}</p> : null}
      <button ref={fallback} type="button" className="subscription-rail-reload" onClick={query.reload} disabled={!allowed || Boolean(query.loading)}>{copy("pagination.reload")}</button>
    </div>
    {selection && account ? createPortal(<div ref={popup} popover="manual" role="dialog" aria-label={`${brand(account).name} · ${account.alias}`} tabIndex={-1} className="subscription-rail-popover" onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); } }}>
      <header><strong>{brand(account).name} · {account.alias}</strong><button type="button" onClick={close} aria-label={copy("subscription-rail.close")}>×</button></header>
      <p>{copy(account.disabled ? "subscription-rail.disabled" : "subscription-rail.connected")}</p>
      {unavailable ? <p role="status">{copy("subscription-rail.readFailed")}</p> : null}
      <p>{copy("subscription-rail.explanation")}</p>
      {!unavailable && remainingBadge(account.windows, now) === undefined ? <p>{copy("subscription-rail.incomplete")}</p> : null}
      {account.windows.length ? account.windows.map((window, index) => <section key={`${window.id}:${index}`}><strong>{window.id || copy("subscription-rail.window", { index: index + 1 })}</strong><p>{window.valid !== false && typeof window.blocking === "boolean" && typeof window.remaining === "number" && Number.isFinite(window.remaining) && window.remaining >= 0 && window.remaining <= 1 ? copy("subscription-rail.remaining", { percent: Math.round(window.remaining * 100) }) : copy("subscription-rail.unavailable")}</p><p>{copy(window.valid === false || typeof window.blocking !== "boolean" ? "subscription-rail.unknown" : !freshWindow(window, now) && window.state === "observed" ? "subscription-rail.stale" : window.state === "observed" ? "subscription-rail.observed" : window.state === "failed" ? "subscription-rail.failed" : window.state === "unsupported" ? "subscription-rail.unsupportedQuota" : "subscription-rail.unknown")}</p><small>{copy("subscription-rail.observation", { time: window.observedAt || "—" })}<br />{copy("subscription-rail.reset", { time: window.resetAt || "—" })}</small></section>) : <p>{copy("subscription-rail.unknown")}</p>}
      <details><summary>{copy("subscription-rail.identity")}</summary><p>{account.id}</p></details>
      <button type="button" onClick={() => { close(); manage(); }}>{copy("subscription-rail.manage")}</button>
    </div>, document.body) : null}
  </div>;
}
