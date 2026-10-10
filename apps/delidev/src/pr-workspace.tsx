// SPDX-License-Identifier: Apache-2.0
import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { IntegrationQuery, SystemQuery, SystemCapability, type Resource } from "@delinoio/delidev-api-client";
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { QueryResult, githubResult, type PullRequestNavigation } from "./github-items";
import { ItemKind, QueryOperation, type GitHubQuery } from "./github-query-model";
import { encode, object, text, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";
import { PRBackgroundQueue, PRWorkspaceContext, usePRWorkspaceContext } from "./pr-workspace-context";
import { PRAvatar, PRCounts } from "./pr-workspace-page";
import { InertPRMarkdown, PRDiff } from "./pr-workspace-content";
import { PRCommits } from "./pr-workspace-commits";
import { PRFeedback } from "./github-feedback";
import { OpenGitHub } from "./github-opening";
import { Timestamp, TimestampMode } from "./timestamp-display";
import "./pr-workspace.css";

const tabs = ["description", "diff", "reviews", "commits"] as const;
type Tab = typeof tabs[number];
function PRDetail({ selected, number, supported, queue, active, back, refresh, activity }: { selected: Resource; number: string; supported: boolean; queue: PRBackgroundQueue; active: boolean; back: () => void; refresh: () => void; activity: (busy: boolean) => void }) {
  const [commitBusy, setCommitBusy] = useState(false);
  const [tab, setTab] = useState<Tab>("description"), [visited, setVisited] = useState<Set<Tab>>(() => new Set(["description"])), id = useId();
  const operations = [QueryOperation.Detail, QueryOperation.Diff, QueryOperation.Feedback] as const;
  const options = { retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, staleTime: Infinity, gcTime: 0 } as const;
  const query = (operation: QueryOperation): GitHubQuery => ({ kind: ItemKind.PullRequest, operation, number });
  const description = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(query(operations[0])) }, { ...options, enabled: active });
  const diff = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(query(operations[1])) }, { ...options, enabled: active && visited.has("diff") });
  const reviews = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(query(operations[2])) }, { ...options, enabled: active && visited.has("reviews") });
  const results = [description, diff, reviews];
  const data = results.map((result, index) => result.data?.schemaVersion === 1 ? githubResult(result.data.documentJson, selected, query(operations[index]!)) : undefined), item = object(data[0]?.items && (data[0].items as unknown[])[0]);
  for (let index = 1; index < data.length; index++) {
    const observed = object((data[index]?.items as unknown[] | undefined)?.[0]);
    if (data[index] && (data[index]?.generation_id !== data[0]?.generation_id || observed.id !== item.id || observed.base_sha !== item.base_sha || observed.head_sha !== item.head_sha)) data[index] = undefined;
  }
  const busy = commitBusy || results.some(result => result.isFetching);
  useLayoutEffect(() => { queue.prioritize(busy); activity(busy); return () => { queue.prioritize(false); activity(false); }; }, [queue, busy, tab, activity]);
  const activate = (next: Tab) => { queue.prioritize(true); setTab(next); setVisited(previous => new Set([...previous, next])); };
  // Cached observations belong to this mounted PR owner and leave with it.
  return <section className="pr-workspace-detail" aria-label={copy("pr-workspace.detail")}>
    <div className="actions"><button type="button" onClick={back}>{copy("github-items.backToResults_c7ef0e")}</button><button type="button" disabled={busy || !active} onClick={refresh}>{copy("github-items.refreshGithubResults_bd77c0")}</button></div>
    <Problem error={description.error} />{description.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}{description.data && !data[0] ? <p role="alert">{copy("github-items.theGithubResultDoesNotMatch_3c7624")}</p> : null}
    {data[0] ? <><header><p>{copy(item.state === "closed" ? "pull-requests.closed_c21ead" : "pull-requests.open_ed077f")}{item.draft ? ` · ${copy("pr-cards.draft")}` : ""}</p><h2>#{text(item.number)} {text(item.title)}</h2><p>{text(object(item.author).login) || copy("github-items.extra.a326f4758492")}{object(item.author).kind === "unknown" ? ` · ${copy("pr-cards.unverifiedAuthor")}` : ""} · <Timestamp value={text(item.updated_at)} mode={TimestampMode.Exact} /> · <SelectedCounts selected={selected} item={item} supported={supported} generation={text(data[0]?.generation_id)} /></p><p>{text(item.head_ref)} → {text(item.base_ref)}</p><OpenGitHub url={text(item.url)} /></header>
      <div role="tablist" aria-label={copy("pr-workspace.tabs")} className="pr-workspace-tabs">{tabs.map((name, index) => <button type="button" role="tab" disabled={busy || !active} id={`${id}-${name}`} aria-controls={`${id}-${name}-panel`} aria-selected={tab === name} tabIndex={tab === name ? 0 : -1} key={name} onClick={() => activate(name)} onKeyDown={event => { const offset = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0; if (offset || event.key === "Home" || event.key === "End") { event.preventDefault(); const target = event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : (index + offset + tabs.length) % tabs.length; event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>("button")[target]?.focus(); } }}>{copy(`pr-workspace.${name}`)}</button>)}</div>
      {tabs.map((name, index) => <div role="tabpanel" id={`${id}-${name}-panel`} aria-labelledby={`${id}-${name}`} hidden={tab !== name} tabIndex={0} key={name}>{index < 3 ? <><Problem error={results[index]?.error} />{results[index]?.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}{results[index]?.data && !data[index] ? <p role="alert">{copy("github-items.theGithubResultDoesNotMatch_3c7624")}</p> : null}{data[index] ? name === "description" ? <InertPRMarkdown body={text(item.body)} /> : name === "diff" ? <PRDiff value={data[index]!} /> : <PRFeedback value={object(data[index]?.feedback)} workspace /> : null}{results[index]?.error ? <button type="button" disabled={busy} onClick={() => void results[index]?.refetch()}>{copy("pagination.retry")}</button> : null}</> : supported && visited.has(name) ? <PRCommits selected={selected} item={item} active={active} visible={tab === name} busy={setCommitBusy} /> : <p>{copy("pr-workspace.commitsUnsupported")}</p>}</div>)}
    </> : null}
  </section>;
}
function SelectedCounts({ item, generation }: { selected: Resource; item: Document; supported: boolean; generation: string }) {
  const context = usePRWorkspaceContext()!;
  useSyncExternalStore(context.queue.subscribe, context.queue.snapshot);
  const row = context.queue.rows.get(text(item.number)), observed = object(row?.item);
  return <><PRAvatar reference={row?.generation_id === generation && observed.id === item.id && observed.base_sha === item.base_sha && observed.head_sha === item.head_sha ? text(row?.avatar_reference) : undefined} /><PRCounts value={row?.generation_id === generation && observed.id === item.id && observed.base_sha === item.base_sha && observed.head_sha === item.head_sha ? row?.counts : undefined} /></>;
}
export function PRWorkspace({ selected, navigation, active, pending }: { selected: Resource; navigation: PullRequestNavigation; active: boolean; pending?: ReactNode }) {
  useLocale();
  const root = useRef<HTMLDivElement>(null), list = useRef<HTMLDivElement>(null), [number, setNumber] = useState(""), [epoch, setEpoch] = useState(0), [compact, setCompact] = useState(false);
  const [busy, setBusy] = useState(false);
  const client = useQueryClient(), transport = useTransport();
  const queue = useMemo(() => new PRBackgroundQueue(), []);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false });
  const supported = Boolean(status.data?.capabilities.includes(SystemCapability.PULL_REQUEST_WORKSPACE_V1));
  useEffect(() => { queue.prioritize(false); return () => queue.dispose(); }, [queue]);
  useLayoutEffect(() => { const element = root.current; if (!element) return; const update = () => setCompact(element.getBoundingClientRect().width < 900); update(); const observer = typeof ResizeObserver === "function" ? new ResizeObserver(update) : undefined; observer?.observe(element); return () => observer?.disconnect(); }, []);
  const refreshDetail = () => {
    queue.prioritize(true);
    for (const operation of [QueryOperation.Detail, QueryOperation.Diff, QueryOperation.Feedback]) {
      const options = createQueryOptions(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode({ kind: ItemKind.PullRequest, operation, number }) }, { transport });
      client.removeQueries({ queryKey: options.queryKey, exact: true });
    }
    setEpoch(value => value + 1);
  };
  const back = () => { setNumber(""); requestAnimationFrame(() => list.current?.querySelector<HTMLButtonElement>(`button[data-pr-number="${number}"]`)?.focus({ preventScroll: true })); };
  return <PRWorkspaceContext.Provider value={{ supported, busy, queue }}><div ref={root} className="pr-workspace-owner">{pending}<div className={`pr-workspace ${compact ? "pr-workspace-compact" : ""}`}><div ref={list} className="pr-workspace-list" hidden={compact && Boolean(number)}><QueryResult selected={selected} query={navigation.query} active={active} standaloneCards change={next => { if (next.number === number) return; queue.prioritize(true); setNumber(next.number ?? ""); }} /></div><div className="pr-workspace-detail-scroll" hidden={compact && !number}>{number ? <PRDetail key={`${number}:${epoch}`} selected={selected} number={number} supported={supported} queue={queue} activity={setBusy} active={active} back={back} refresh={refreshDetail} /> : <p className="pr-selection-prompt">{copy("pr-workspace.select")}</p>}</div></div></div></PRWorkspaceContext.Provider>;
}
