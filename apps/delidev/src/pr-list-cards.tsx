// SPDX-License-Identifier: Apache-2.0
import { Timestamp, TimestampMode } from "./timestamp-display";
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { SettingsActionGlyph, SettingsActionIcon } from "./settings-action";
import type { Resource } from "@delinoio/delidev-api-client";
import { resourceName, items, object, text, type Document } from "./documents";
import { ItemKind, QueryOperation, type GitHubQuery } from "./github-query-model";
import { LocalizedText, copy, useLocale } from "./localization";
import "./pr-list-cards.css";

function PRRefreshButton({ disabled, refresh }: { disabled: boolean; refresh: () => void }) {
  const label = copy("github-items.refreshGithubResults_bd77c0"), tooltipId = useId();
  const [tooltip, setTooltip] = useState<{ left: number; top: number }>();
  const tooltipRef = useRef<HTMLDivElement>(null);
  const visible = Boolean(tooltip);
  useLayoutEffect(() => {
    if (!tooltip || !tooltipRef.current) return;
    const zoom = Number(getComputedStyle(document.body).zoom) || 1;
    const top = Math.max(8, Math.min(tooltip.top, window.innerHeight / zoom - tooltipRef.current.getBoundingClientRect().height / zoom - 8));
    if (top !== tooltip.top) setTooltip({ ...tooltip, top });
  }, [tooltip, label]);
  useEffect(() => {
    if (!visible) return;
    const hide = () => setTooltip(undefined);
    window.addEventListener("scroll", hide, true);
    window.addEventListener("resize", hide);
    return () => { window.removeEventListener("scroll", hide, true); window.removeEventListener("resize", hide); };
  }, [visible]);
  const reveal = (button: HTMLButtonElement) => {
    const rect = button.getBoundingClientRect(), zoom = Number(getComputedStyle(document.body).zoom) || 1;
    const width = window.innerWidth / zoom;
    setTooltip({ left: Math.max(8, Math.min(rect.left / zoom, width - Math.min(280, width - 16) - 8)), top: rect.bottom / zoom + 8 });
  };
  return <><button className="pr-list-refresh" type="button" aria-label={label} aria-describedby={tooltip ? tooltipId : undefined} disabled={disabled} onClick={refresh}
    onPointerEnter={event => reveal(event.currentTarget)}
    onPointerLeave={event => { if (document.activeElement !== event.currentTarget) setTooltip(undefined); }}
    onFocus={event => reveal(event.currentTarget)} onBlur={() => setTooltip(undefined)}
    onKeyDown={event => { if (event.key === "Escape") setTooltip(undefined); }}>
    <SettingsActionGlyph icon={SettingsActionIcon.Refresh} />
  </button>{tooltip ? createPortal(<div id={tooltipId} ref={tooltipRef} role="tooltip" className="settings-action-tooltip" style={tooltip}>{label}</div>, document.body) : null}</>;
}

export function PRListHeader({ selected, pending, reading, reloadRequired = false, refresh }: { selected: Resource; pending?: ReactNode; reading: boolean; reloadRequired?: boolean; refresh: () => void }) {
  useLocale();
  return <><header className="pr-list-header"><div><h2>{copy("pull-requests.pullRequests_d9e3f2")}</h2><p>{resourceName(selected)}</p></div><PRRefreshButton disabled={reading || reloadRequired} refresh={refresh} /></header>{pending}</>;
}

// Only validated standalone list/search pages use cards. The server's original
// UTC strings retain fractional precision; locale changes never reformat them.
export function PRListCards({ data, query, reading, previous, emptyPage, change }: { data: Document; query: GitHubQuery; reading: boolean; previous: boolean; emptyPage: boolean; change: (query: GitHubQuery) => void }) {
  useLocale();
  return <section className="pr-list-page" aria-label={copy("pr-cards.pageLabel", { page: query.page })}>
    <div className="pr-list-applied"><p>{copy("pr-cards.applied", { state: copy(query.state === "closed" ? "pull-requests.closed_c21ead" : query.state === "all" ? "pull-requests.all_a52ace" : "pull-requests.open_ed077f"), page: query.page, pageSize: query.page_size })}{query.operation === QueryOperation.Search ? <> · {copy("pr-cards.search", { terms: query.search })}</> : null}</p><p>{copy(previous || reading ? "github-items.previousObservation_1bd8a6" : "github-items.observed_64fa8a")}: <Timestamp value={text(data.observed_at)} mode={TimestampMode.Exact} /></p></div>
    {data.total_count != null ? <p className="pr-list-notice"><LocalizedText id="github-items.githubReportsMatchesSearchExposesAt_f846ae" components={{ s0: <>{text(data.total_count)}</> }} /></p> : null}
    {data.incomplete ? <p role="status" className="pr-list-notice">{copy("github-items.githubReturnedIncompleteSearchResultsMissing_29a156")}</p> : null}
    {data.search_limit_reached ? <p role="status" className="pr-list-notice">{copy("github-items.githubSSearchLimitHasBeen_c17e40")}</p> : null}
    <div className="pr-list-cards">{items(data.items).map(raw => {
      const item = object(raw), author = object(item.author);
      return <article className="pr-list-card" key={`${text(item.identity_source)}:${text(item.id)}`}>
        <div className="pr-list-card-state"><span className="pr-list-state" data-state={text(item.state)}>{copy(item.state === "closed" ? "pull-requests.closed_c21ead" : "pull-requests.open_ed077f")}</span><span className="pr-list-number">#{text(item.number)}</span>{item.draft ? <span className="pr-list-draft">{copy("pr-cards.draft")}</span> : null}</div>
        <h3>{text(item.title)}</h3>
        <div className="pr-list-card-footer"><div className="pr-list-card-metadata"><p>{text(author.login) || copy("github-items.extra.a326f4758492")}{author.kind === "unknown" ? <> · {copy("pr-cards.unverifiedAuthor")}</> : null}</p><p>{copy("pr-cards.updated")}: <Timestamp value={text(item.updated_at)} mode={TimestampMode.Exact} /></p></div><button type="button" disabled={reading} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Detail, number: text(item.number) })}><LocalizedText id="github-items.read_37b452" components={{ s0: <>{text(item.number)}</> }} /></button></div>
      </article>;
    })}</div>
    {emptyPage ? <p className="pr-list-empty">{copy("pr-cards.emptyPage", { page: query.page })}</p> : null}
  </section>;
}
