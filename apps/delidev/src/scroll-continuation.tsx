import { useSidebarActivity } from "./sidebar-context";
// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { FailureCode } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { ReadStage, type PaginationFailure } from "./scroll-pagination";

export interface ScrollContinuationQuery {
  loaded: boolean;
  nextPageToken: string;
  loading?: ReadStage;
  error?: PaginationFailure;
  append: () => void;
  retry: () => void;
  reload: () => void;
}

export const continuationDistance = 96;

/** The adapter owns admission, tokens, validation and disposal. This component
 * observes only the loaded tail and never starts an initial read or mutation. */
export function ScrollContinuation({ query, label, root, active, showErrors = true, showInitial = true }: {
  query: ScrollContinuationQuery;
  label: string;
  root: RefObject<HTMLElement | null>;
  active: boolean;
  showErrors?: boolean;
  showInitial?: boolean;
}) {
  const sidebarActivity = useSidebarActivity();
  active = active && sidebarActivity;
  useLocale();
  const anchor = useRef<HTMLDivElement>(null);
  const [documentVisible, setDocumentVisible] = useState(() => document.visibilityState !== "hidden");
  useEffect(() => {
    const update = () => setDocumentVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  const { loaded, nextPageToken, loading, error, append } = query;
  const allowed = active && documentVisible && loaded && Boolean(nextPageToken) && !loading && !error;
  useLayoutEffect(() => {
    const container = resolveScrollRoot(root.current), element = anchor.current;
    if (!container || !element || !allowed) return;
    const check = () => {
      if (container.clientHeight <= 0 || document.visibilityState === "hidden" || element.closest("[hidden], [inert], [aria-hidden='true']")) return;
      for (let parent: HTMLElement | null = element; parent; parent = parent.parentElement) {
        if (parent instanceof HTMLDetailsElement && !parent.open) return;
      }
      const bounds = container.getBoundingClientRect(), position = element.getBoundingClientRect();
      if (position.top <= bounds.bottom + continuationDistance && position.bottom >= bounds.top) append();
    };
    const observer = typeof IntersectionObserver === "function" ? new IntersectionObserver(check, { root: container, rootMargin: `0px 0px ${continuationDistance}px 0px` }) : undefined;
    observer?.observe(element);
    const resize = typeof ResizeObserver === "function" ? new ResizeObserver(check) : undefined;
    resize?.observe(container);
    resize?.observe(element);
    container.addEventListener("scroll", check, { passive: true });
    window.addEventListener("resize", check);
    check();
    return () => { observer?.disconnect(); resize?.disconnect(); container.removeEventListener("scroll", check); window.removeEventListener("resize", check); };
  }, [allowed, append, root]);
  const reloadRequired = error?.stalled || error?.failure.code === FailureCode.CursorExpired;
  return <div ref={anchor} className="sidebar-continuation" data-continuation={label}>
    {loading && (showInitial || loading === ReadStage.Additional) ? <span role="status">{copy(loading === ReadStage.Additional ? "pagination.loadingMore" : "pagination.loading", { label })}</span> : null}
    {showErrors && error ? <><span role="status">{copy(loaded ? "pagination.previousData" : "pagination.readFailed", { label })}</span><SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={!active || !documentVisible || Boolean(loading)} onClick={reloadRequired ? query.reload : query.retry}>{copy(reloadRequired ? "pagination.reload" : "pagination.retry")}</SettingsActionButton></> : null}
    {loaded && nextPageToken && !error ? <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" aria-label={copy("pagination.loadMoreLabel", { label })} disabled={!allowed} onClick={append}>{copy("pagination.loadMore")}</SettingsActionButton> : null}
  </div>;
}

/** Resolve at observation time: compact portal reflow can replace an overflow
 * owner without replacing the connection-owned pagination controller. */
export function resolveScrollRoot(content: HTMLElement | null) {
  let node = content;
  while (node) {
    const style = getComputedStyle(node);
    if (/(auto|scroll)/.test(`${style.overflowY} ${style.overflow}`)) return node;
    node = node.parentElement;
  }
  return content;
}
export function useScrollRoot(content: RefObject<HTMLElement | null>) { return content; }
