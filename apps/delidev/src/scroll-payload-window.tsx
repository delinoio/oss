// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, type ReactNode, type RefObject } from "react";
import { copy, useLocale } from "./localization";
import { continuationDistance, resolveScrollRoot } from "./scroll-continuation";
import { type PaginationPage, type PaginationPayloadPage, type PaginationRow } from "./scroll-pagination";

export interface PayloadWindowQuery<Row extends PaginationRow, Payload> {
  pages: PaginationPage<Row>[];
  payloadPages: PaginationPayloadPage<Payload>[];
  loading?: unknown;
  error?: unknown;
  restore: (token: string) => void;
  measure: (token: string, height: number) => void;
  protect?: (token?: string) => void;
}

function PayloadPage<Row extends PaginationRow, Payload>({ page, payload, query, root, active, children }: {
  page: PaginationPage<Row>; payload?: Payload[]; query: PayloadWindowQuery<Row, Payload>;
  root: RefObject<HTMLElement | null>; active: boolean; children: (payload: Payload[], rows: Row[]) => ReactNode;
}) {
  useLocale();
  const element = useRef<HTMLDivElement>(null);
  const attemptedPosition = useRef<number | undefined>(undefined);
  useLayoutEffect(() => {
    const node = element.current, container = resolveScrollRoot(root.current);
    if (!node || !container) return;
    if (payload) {
      const height = node.getBoundingClientRect().height;
      query.measure(page.token, height);
      const resize = typeof ResizeObserver === "function" ? new ResizeObserver(() => query.measure(page.token, node.getBoundingClientRect().height)) : undefined;
      resize?.observe(node);
      return () => resize?.disconnect();
    }
    if (!active || query.loading || query.error) return;
    const check = () => {
      if (document.visibilityState === "hidden" || container.clientHeight <= 0 || node.closest("[hidden], [inert], [aria-hidden='true']")) return;
      for (let parent: HTMLElement | null = node; parent; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement && !parent.open) return;
      const viewport = container.getBoundingClientRect(), position = node.getBoundingClientRect();
      if (position.top <= viewport.bottom + continuationDistance && position.bottom >= viewport.top - continuationDistance && attemptedPosition.current !== container.scrollTop) {
        attemptedPosition.current = container.scrollTop;
        query.restore(page.token);
      }
    };
    const observer = typeof IntersectionObserver === "function" ? new IntersectionObserver(check, { root: container, rootMargin: `${continuationDistance}px 0px` }) : undefined;
    observer?.observe(node);
    container.addEventListener("scroll", check, { passive: true });
    document.addEventListener("visibilitychange", check);
    check();
    return () => { observer?.disconnect(); container.removeEventListener("scroll", check); document.removeEventListener("visibilitychange", check); };
  }, [active, page.token, page.height, payload, query.loading, query.error, query.restore, query.measure, root]);
  return <div ref={element} style={payload ? undefined : { minHeight: page.height ?? 48 }} data-payload-page={page.token} onFocusCapture={() => query.protect?.(page.token)} onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) query.protect?.(); }}>
    {payload ? children(payload, page.rows) : <button type="button" disabled={!active || Boolean(query.loading) || Boolean(query.error)} onClick={() => query.restore(page.token)}>{copy("pagination.restore")}</button>}
  </div>;
}

/** Retains measured placeholders for reached pages whose full payload expired
 * from the three-page window. Render callbacks receive authoritative payloads
 * only for restored ranges; adapters keep mutations separate from projections. */
export function ScrollPayloadWindow<Row extends PaginationRow, Payload>({ query, root, active, children, identity, revision }: {
  query: PayloadWindowQuery<Row, Payload>; root: RefObject<HTMLElement | null>; active: boolean;
  children: (payload: Payload[], rows: Row[]) => ReactNode;
  identity?: (payload: Payload) => string; revision?: (payload: Payload) => bigint;
}) {
  // Capture focus before React mounts newly restored forms. Their existing
  // autoFocus behavior must not steal focus from a connected composer/control.
  const container = typeof window === "undefined" ? null : resolveScrollRoot(root.current);
  const bounds = container?.getBoundingClientRect();
  const anchor = container && bounds ? [...container.querySelectorAll<HTMLElement>("[data-payload-page]")].find(node => node.getBoundingClientRect().bottom > bounds.top) : undefined;
  const anchorToken = anchor?.getAttribute("data-payload-page"), anchorTop = anchor?.getBoundingClientRect().top;
  const priorFocus = typeof document === "undefined" ? null : document.activeElement;
  useLayoutEffect(() => {
    if (container && anchorToken !== undefined && anchorToken !== null && anchorTop !== undefined) {
      const retained = [...container.querySelectorAll<HTMLElement>("[data-payload-page]")].find(node => node.getAttribute("data-payload-page") === anchorToken);
      if (retained) container.scrollTop += retained.getBoundingClientRect().top - anchorTop;
    }
    if (priorFocus instanceof HTMLElement && priorFocus.isConnected && priorFocus !== document.body && document.activeElement !== priorFocus && document.activeElement?.closest("[data-payload-page]")) priorFocus.focus({ preventScroll: true });
  }, [query.payloadPages, query.pages]);
  const owners = new Map<string, string>();
  for (const page of query.pages) for (const row of page.rows) if (!owners.has(row.id)) owners.set(row.id, page.token);
  const newest = new Map<string, Payload>();
  if (identity) for (const page of query.payloadPages) for (const payload of page.payload) {
    const id = identity(payload), prior = newest.get(id);
    if (!prior || !revision || revision(payload) >= revision(prior)) newest.set(id, payload);
  }
  return query.pages.map(page => {
    const rows = page.rows.filter((row, index, values) => owners.get(row.id) === page.token && values.findIndex(candidate => candidate.id === row.id) === index);
    const original = query.payloadPages.find(value => value.token === page.token)?.payload;
    const payload = original && identity ? rows.flatMap(row => { const value = newest.get(row.id); return value ? [value] : []; }) : original;
    return <PayloadPage key={page.token} page={{ ...page, rows }} payload={payload} query={query} root={root} active={active}>{children}</PayloadPage>;
  });
}
