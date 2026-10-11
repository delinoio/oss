// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { fromMarkdown } from "mdast-util-from-markdown";
import { PRAvatarCache } from "./pr-avatar-cache";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { IntegrationQuery, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { encode, items, object, text, type Document } from "./documents";
import { actorValid, bounded, date, ItemKind, positive, QueryOperation, sha, type GitHubQuery } from "./github-query-model";
import { validPRSource } from "./github-pr-source";
import { copy, useLocale } from "./localization";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { OpenGitHub } from "./github-opening";
import { PRFeedback } from "./github-feedback";
import { Problem } from "./ui";
import { changeCounts, commitResult, observationsConflict, orderWorkspace, workspaceResult } from "./pr-workspace-model";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation } from "./scroll-continuation";
import { invalidGitHubPage, paginationError, useGitHubScrollRoot } from "./github-scroll";
import "./pr-workspace.css";

export type PRPage = { data: Document; query: GitHubQuery };
export type PRSelection = { number: string; node?: Document; scope?: Document };
type Validate = (raw: Uint8Array, selected: Resource, query: GitHubQuery) => Document | undefined;
const options = { retry: false as const, refetchOnWindowFocus: false, refetchOnReconnect: false, gcTime: 0, staleTime: Infinity };
export function PRCounts({ value }: { value: unknown }) { useLocale(); const counts = changeCounts(value); return counts ? <span className="pr-counts"><span className="pr-added">+{counts.additions}</span> <span className="pr-deleted">−{counts.deletions}</span></span> : <span aria-label={copy("pr-workspace.countsUnavailable")}>—</span>; }
const AvatarContext = createContext<PRAvatarCache | undefined>(undefined);
function AvatarRead({ selected, reference, cache }: { selected: Resource; reference: string; cache: PRAvatarCache }) {
  const result = useQuery(IntegrationQuery.readPullRequestAvatar, { repositoryId: selected.id, expectedRevision: selected.revision, reference }, { ...options, enabled: true });
  useEffect(() => { if (result.error) cache.put(reference); else if (result.data) cache.put(reference, result.data.png); }, [result.data, result.error, cache, reference]);
  return <span className="pr-avatar pr-avatar-fallback" aria-hidden="true">●</span>;
}
function Avatar({ selected, reference, enabled }: { selected: Resource; reference?: string; enabled: boolean }) {
  const cache = useContext(AvatarContext)!; useSyncExternalStore(cache.subscribe, cache.snapshot);
  const url = reference && enabled ? cache.read(reference) : undefined;
  if (url) return <img className="pr-avatar" src={url} alt="" onError={() => cache.fail(reference!)} />;
  return reference && enabled && !cache.attempted(reference) ? <AvatarRead selected={selected} reference={reference} cache={cache} /> : <span className="pr-avatar pr-avatar-fallback" aria-hidden="true">●</span>;
}
function validWorkspaceItem(item: Document, scope: Document) {
  const repository = object(scope.repository);
  return item.provider === "github.com" && item.kind === "pull-request" && item.identity_source === "pull-request-api" && positive(item.id) && positive(item.number) && bounded(item.node_id, 256) && bounded(item.title, 4096) && ["open", "closed"].includes(text(item.state)) && date(item.created_at) && date(item.updated_at) && Date.parse(text(item.updated_at)) >= Date.parse(text(item.created_at)) && item.url === `https://github.com/${text(repository.owner)}/${text(repository.name)}/pull/${text(item.number)}` && (item.author == null || actorValid(item.author)) && typeof item.draft === "boolean" && typeof item.merged === "boolean" && (item.mergeable == null || typeof item.mergeable === "boolean") && bounded(item.body, 128 << 10, false) && bounded(item.base_ref, 1024) && bounded(item.head_ref, 1024) && sha(item.base_sha) && sha(item.head_sha) && validPRSource(item.head_repository, repository);
}
function Enrichment({ page, selected, enabled, observe }: { page: PRPage; selected: Resource; enabled: boolean; observe: (key: string, value?: Document) => void }) {
  const seedKey = JSON.stringify(items(page.data.items).map(raw => text(object(raw).number)));
  const seeds = useMemo(() => JSON.parse(seedKey) as string[], [seedKey]);
  const key = JSON.stringify([page.query.page, page.data.observed_at, seeds]);
  const result = useQuery(IntegrationQuery.getPullRequestWorkspace, { repositoryId: selected.id, expectedRevision: selected.revision, seedNumbers: seeds }, { ...options, enabled: enabled && seeds.length > 0 });
  const value = useMemo(() => result.data?.schemaVersion === 1 ? workspaceResult(result.data.documentJson, selected, seeds, validWorkspaceItem) : undefined, [result.data, selected, seeds]);
  useEffect(() => { observe(key, value); return () => observe(key); }, [key, value, observe]);
  return <>{result.error || result.data && !value ? <div role="status"><p>{copy("pr-workspace.incomplete")}</p><Problem error={result.error} /><button onClick={() => void result.refetch()} disabled={result.isFetching}>{copy("pr-workspace.retryEnrichment")}</button></div> : null}{value && value.state !== "complete" ? <p role="status">{copy(value.state === "ambiguous" ? "pr-workspace.ambiguous" : "pr-workspace.incomplete")}</p> : null}</>;
}
export function PRWorkspaceRows({ pages, selected, enabled, chosen, choose, observations, observe, allSeeds, previous }: { pages: PRPage[]; selected: Resource; enabled: boolean; chosen?: string; choose: (selection: PRSelection, button: HTMLButtonElement) => void; observations: Map<string, Document>; observe: (key: string, value?: Document) => void; allSeeds: string[]; previous?: boolean }) {
  useLocale();
  const seeds = pages.flatMap(page => items(page.data.items).map(raw => text(object(raw).number))), matching = new Set(allSeeds), nodes = new Map<string, Document>(), scopes = new Map<string, Document>();
  for (const page of pages) for (const raw of items(page.data.items)) { const item = object(raw); nodes.set(text(item.number), { item }); }
  const conflict = observationsConflict(observations.values());
  const edges: Document[] = []; let ambiguous = conflict;
  for (const scope of observations.values()) { if (scope.state === "ambiguous") ambiguous = true; for (const raw of items(scope.nodes)) { const node = object(raw), n = text(object(node.item).number); if (nodes.has(n) || nodes.size < 100) { nodes.set(n, node); scopes.set(n, scope); } } edges.push(...items(scope.edges).map(object)); }
  const ordered = orderWorkspace([...nodes.values()], edges, allSeeds, ambiguous).filter(row => seeds.includes(row.owner));
  return <section className="pr-workspace-rows" aria-label={copy("github-items.pullRequests_d9e3f2")}>
    {pages.map(page => <div className="pr-list-applied" key={`metadata:${page.query.page}`}><p>{copy("pr-cards.applied", { state: copy(page.query.state === "closed" ? "pull-requests.closed_c21ead" : page.query.state === "all" ? "pull-requests.all_a52ace" : "pull-requests.open_ed077f"), page: page.query.page, pageSize: page.query.page_size })}</p><p>{copy(previous ? "github-items.previousObservation_1bd8a6" : "github-items.observed_64fa8a")}: <Timestamp value={text(page.data.observed_at)} mode={TimestampMode.Exact} /></p>{page.data.total_count != null ? <p>{copy("pr-workspace.matches", { count: text(page.data.total_count) })}</p> : null}{page.data.incomplete ? <p role="status">{copy("github-items.githubReturnedIncompleteSearchResultsMissing_29a156")}</p> : null}{page.data.search_limit_reached ? <p role="status">{copy("github-items.githubSSearchLimitHasBeen_c17e40")}</p> : null}</div>)}
    {pages.map(page => <Enrichment key={`${page.query.page}:${text(page.data.observed_at)}`} page={page} selected={selected} enabled={enabled} observe={observe} />)}
    {!enabled ? <p role="status">{copy("pr-workspace.unsupportedStack")}</p> : null}
    {conflict ? <p role="status">{copy("pr-workspace.ambiguous")}</p> : null}
    {ordered.map(({ node, depth, stack }) => { const item = object(node.item), author = object(item.author), number = text(item.number); return <article key={number} className="pr-workspace-row" data-stack={stack} data-child={depth > 0} data-selected={chosen === number} style={{ marginInlineStart: `${depth * 12}px` }}>
      <button type="button" data-pr-number={number} aria-pressed={chosen === number} onClick={event => choose({ number, node, scope: scopes.get(number) }, event.currentTarget)}>
        <span className="pr-row-labels"><span className="pr-list-state" data-state={text(item.state)}>{copy(item.state === "closed" ? "pull-requests.closed_c21ead" : "pull-requests.open_ed077f")}</span>{item.draft ? <span>{copy("pr-cards.draft")}</span> : null}{stack ? <span className="pr-stack-badge">{copy("pr-workspace.stack")}</span> : null}{!matching.has(number) ? <span>{copy("pr-workspace.context")}</span> : null}</span>
        <span className="pr-row-title" role="heading" aria-level={3}>#{number} {text(item.title)}</span>
        <span className="pr-row-author"><Avatar selected={selected} reference={text(node.avatar_reference)} enabled={enabled} /><span>{text(author.login) || copy("github-items.extra.a326f4758492")}{author.kind === "unknown" ? <> · {copy("pr-cards.unverifiedAuthor")}</> : null}</span><PRCounts value={node.counts} /></span>
        <span>{copy("pr-cards.updated")}: <Timestamp value={text(item.updated_at)} mode={TimestampMode.Exact} /></span>
      </button>
    </article>; })}
    {!seeds.length ? <p>{copy("pr-cards.emptyPage", { page: pages[0]?.query.page ?? 1 })}</p> : null}
  </section>;
}

type MarkdownNode = { type: string; value?: string; depth?: number; ordered?: boolean; position?: { start: { offset?: number }; end: { offset?: number } }; children?: MarkdownNode[] };
export function InertMarkdown({ source }: { source: string }) {
  const tree = useMemo(() => fromMarkdown(source) as MarkdownNode, [source]);
  const render = (node: MarkdownNode, index: number): ReactNode => {
    const children = node.children?.map(render), raw = source.slice(node.position?.start.offset ?? 0, node.position?.end.offset ?? source.length);
    switch (node.type) {
      case "root": return <div key={index}>{children}</div>; case "text": return node.value;
      case "paragraph": return <p key={index}>{children}</p>; case "heading": return <div key={index} role="heading" aria-level={Math.min(6, Math.max(1, node.depth ?? 1))}>{children}</div>;
      case "list": return node.ordered ? <ol key={index}>{children}</ol> : <ul key={index}>{children}</ul>; case "listItem": return <li key={index}>{children}</li>;
      case "emphasis": return <em key={index}>{children}</em>; case "strong": return <strong key={index}>{children}</strong>;
      case "inlineCode": return <code key={index}>{node.value}</code>; case "code": return <pre key={index}><code>{node.value}</code></pre>;
      case "break": return <br key={index} />; default: return <span key={index} className="pr-inert-source">{raw}</span>;
    }
  };
  return <div className="pr-readable-markdown">{render(tree, 0)}</div>;
}
export function PRDiff({ value, item }: { value: Document; item: Document }) {
  const patch = text(value.patch), sections = patch.split(/(?=^diff --git )/m);
  return <><div className="pr-diff">{sections.map((section, index) => { let old = 0, next = 0, hunk = false; const lines = section.split("\n"), supported = section.startsWith("diff --git ") && !section.includes("GIT binary patch") && !section.includes("Binary files "); return <section key={index}>{supported ? <h4>{lines[0]}</h4> : null}{supported ? lines.map((line, index) => { const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line); if (match) { old = Number(match[1]); next = Number(match[2]); hunk = true; } const code = !match && hunk && /^[ +\-]/.test(line) && !line.startsWith("+++") && !line.startsWith("---"), deleted = code && line.startsWith("-"), added = code && line.startsWith("+"); return <div key={index} className="pr-diff-line" data-kind={added ? "added" : deleted ? "deleted" : "context"}><span>{code && !added ? old++ : ""}</span><span>{code && !deleted ? next++ : ""}</span><code>{line}</code></div>; }) : <pre>{section}</pre>}</section>; })}</div><details><summary>{copy("pr-workspace.snapshot")}</summary><p>{text(item.base_sha)} → {text(item.head_sha)}</p><p>SHA-256: {text(value.digest)}</p><pre>{patch}</pre></details></>;
}
function Commits({ selected, item, remote, active }: { selected: Resource; item: Document; remote: Document; active: boolean }) {
  useLocale(); const scope = JSON.stringify([selected.id, selected.revision.toString(), remote.id, item.id, item.base_sha, item.head_sha]);
  const acceptedPages = useMemo(() => new Map<string, string[]>(), [scope]);
  const request = useCallback((token: string) => ({ repositoryId: selected.id, expectedRevision: selected.revision, number: text(item.number), pullRequestId: text(item.id), remoteRepositoryId: text(remote.id), baseSha: text(item.base_sha), headSha: text(item.head_sha), pageToken: token }), [scope]);
  const project = useCallback((reply: { schemaVersion: number; documentJson: Uint8Array }, token: string) => { const value = reply.schemaVersion === 1 ? commitResult(reply.documentJson, selected, item, remote, token) : undefined; if (!value) invalidGitHubPage(); const shas = items(value.commits).map(raw => text(object(raw).sha)); for (const [page, old] of acceptedPages) { if (page === token ? JSON.stringify(old) !== JSON.stringify(shas) : shas.some(sha => old.includes(sha))) invalidGitHubPage(); } acceptedPages.set(token, shas); return { rows: items(value.commits).map(raw => ({ id: text(object(raw).sha), revision: 1n })), nextPageToken: text(value.next_page_token), payload: [value] }; }, [scope, acceptedPages]);
  const reader = useConnectPaginationReader(IntegrationQuery.listPullRequestCommits, request, project), chain = usePaginationChain(scope, active, reader);
  const { root, bindRoot } = useGitHubScrollRoot(); const [copied, setCopied] = useState<string>();
  return <div ref={bindRoot} className="pr-commits-scroll"><Problem error={paginationError(chain.error?.failure)} />{chain.error ? <p role="status">{copy("pr-workspace.commitReload")}</p> : null}<ScrollPayloadWindow query={chain} root={root} active={active}>{payload => payload.map(value => items(value.commits).map(raw => { const c = object(raw), author = object(c.author), committer = object(c.committer), message = text(c.message); return <article className="pr-commit" key={text(c.sha)}><h4>{message.split("\n")[0]}</h4><p className="pr-row-author"><Avatar selected={selected} reference={text(author.avatar_reference)} enabled={active} />{text(author.name)} · <Timestamp value={text(author.date)} mode={TimestampMode.Exact} /></p><p><code>{text(c.sha).slice(0, 12)}</code> · <PRCounts value={c.counts} /></p><button onClick={() => { void navigator.clipboard.writeText(text(c.sha)).then(() => setCopied(text(c.sha)), () => setCopied(undefined)); }}>{copy("pr-workspace.copySHA")}</button>{copied === c.sha ? <span role="status">{copy("pr-workspace.copied")}</span> : null}<details><summary>{copy("pr-workspace.details")}</summary><pre>{message}</pre><p>{text(c.sha)}</p><p className="pr-row-author"><Avatar selected={selected} reference={text(committer.avatar_reference)} enabled={active} />{text(committer.name)} · <Timestamp value={text(committer.date)} mode={TimestampMode.Exact} /></p>{items(c.parents).map(p => <p key={String(p)}>{String(p)}</p>)}</details></article>; }))}</ScrollPayloadWindow><ScrollContinuation query={chain} root={root} active={active} label={copy("pr-workspace.commits")} /></div>;
}
enum Tab { Description = "description", Diff = "diff", Reviews = "reviews", Commits = "commits" }
const tabs = [Tab.Description, Tab.Diff, Tab.Reviews, Tab.Commits];
function Detail({ selected, selection, supported, active, back, validate, reload }: { selected: Resource; selection: PRSelection; supported: boolean; active: boolean; back: () => void; validate: Validate; reload: () => void }) {
  useLocale(); const [tab, setTab] = useState(Tab.Description), [activated, setActivated] = useState(new Set([Tab.Description])), id = useId(), buttons = useRef<(HTMLButtonElement | null)[]>([]);
  const query = { kind: ItemKind.PullRequest, operation: QueryOperation.Detail, number: selection.number }, detail = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(query) }, { ...options, staleTime: 0, enabled: active });
  const value = detail.data?.schemaVersion === 1 ? validate(detail.data.documentJson, selected, query) : undefined, item = object(items(value?.items)[0]), remote = object(value?.repository);
  const diffQuery = { ...query, operation: QueryOperation.Diff }, feedbackQuery = { ...query, operation: QueryOperation.Feedback };
  const diff = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(diffQuery) }, { ...options, staleTime: 0, enabled: active && Boolean(value) && activated.has(Tab.Diff) });
  const reviews = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(feedbackQuery) }, { ...options, staleTime: 0, enabled: active && Boolean(value) && activated.has(Tab.Reviews) });
  const diffValue = diff.data?.schemaVersion === 1 ? validate(diff.data.documentJson, selected, diffQuery) : undefined, reviewValue = reviews.data?.schemaVersion === 1 ? validate(reviews.data.documentJson, selected, feedbackQuery) : undefined;
  const original = object(selection.node?.item), same = value && selection.scope && object(selection.scope.repository).id === remote.id && original.id === item.id && original.base_sha === item.base_sha && original.head_sha === item.head_sha;
  const sameTab = (observation?: Document) => { const p = object(items(observation?.items)[0]); return observation && object(observation.repository).id === remote.id && p.id === item.id && p.base_sha === item.base_sha && p.head_sha === item.head_sha; };
  return <section className="pr-workspace-detail"><button className="pr-workspace-back" onClick={back}>{copy("github-items.backToResults_c7ef0e")}</button><button disabled={detail.isFetching || diff.isFetching || reviews.isFetching} onClick={reload}>{copy("github-items.refreshGithubResults_bd77c0")}</button><Problem error={detail.error} />{detail.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}{detail.data && !value ? <p role="alert">{copy("github-items.theGithubResultDoesNotMatch_3c7624")}</p> : null}
    {value ? <><header><p>{copy(item.state === "closed" ? "pull-requests.closed_c21ead" : "pull-requests.open_ed077f")}{item.draft ? <> · {copy("pr-cards.draft")}</> : null}</p><h3>#{text(item.number)} {text(item.title)}</h3><p className="pr-row-author"><Avatar selected={selected} reference={same ? text(selection.node?.avatar_reference) : undefined} enabled={supported} />{text(object(item.author).login) || copy("github-items.extra.a326f4758492")}{object(item.author).kind === "unknown" ? <> · {copy("pr-cards.unverifiedAuthor")}</> : null}</p><p><Timestamp value={text(item.updated_at)} mode={TimestampMode.Exact} /> · <PRCounts value={same ? selection.node?.counts : undefined} /></p><p>{text(item.head_ref)} → {text(item.base_ref)}</p><OpenGitHub url={text(item.url)} disabled={detail.isFetching || Boolean(detail.error)} /></header>
      <div role="tablist" aria-label={copy("pr-workspace.tabs")} className="pr-workspace-tabs">{tabs.map((value, index) => <button key={value} ref={button => { buttons.current[index] = button; }} role="tab" aria-selected={value === tab} aria-controls={`${id}-${value}`} id={`${id}-${value}-tab`} tabIndex={value === tab ? 0 : -1} onClick={() => { setTab(value); setActivated(old => new Set([...old, value])); }} onKeyDown={event => { const next = event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : event.key === "ArrowRight" ? (index + 1) % tabs.length : event.key === "ArrowLeft" ? (index + tabs.length - 1) % tabs.length : undefined; if (next != null) { event.preventDefault(); buttons.current[next]?.focus(); } }}>{copy(`pr-workspace.${value}`)}</button>)}</div>
      <div role="tabpanel" hidden={tab !== Tab.Description} id={`${id}-description`} aria-labelledby={`${id}-description-tab`}><InertMarkdown source={text(item.body)} /><details><summary>{copy("pr-workspace.originalBody")}</summary><pre>{text(item.body)}</pre></details></div>
      <div role="tabpanel" hidden={tab !== Tab.Diff} id={`${id}-diff`} aria-labelledby={`${id}-diff-tab`}><Problem error={diff.error} />{diff.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}{sameTab(diffValue) ? <PRDiff value={object(diffValue?.diff)} item={item} /> : diff.data ? <p role="alert">{copy("pr-workspace.commitReload")}</p> : null}{diff.error ? <button onClick={() => void diff.refetch()}>{copy("pr-workspace.retryTab")}</button> : null}</div>
      <div role="tabpanel" hidden={tab !== Tab.Reviews} id={`${id}-reviews`} aria-labelledby={`${id}-reviews-tab`}><Problem error={reviews.error} />{reviews.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}{sameTab(reviewValue) ? <PRFeedback value={object(reviewValue?.feedback)} /> : reviews.data ? <p role="alert">{copy("pr-workspace.commitReload")}</p> : null}{reviews.error ? <button onClick={() => void reviews.refetch()}>{copy("pr-workspace.retryTab")}</button> : null}</div>
      <div role="tabpanel" hidden={tab !== Tab.Commits} id={`${id}-commits`} aria-labelledby={`${id}-commits-tab`}>{supported && activated.has(Tab.Commits) ? <Commits key={`${item.id}:${item.base_sha}:${item.head_sha}`} selected={selected} item={item} remote={remote} active={active} /> : <p>{copy("pr-workspace.unsupportedCommits")}</p>}</div>
    </> : null}
  </section>;
}
export function PullRequestWorkspace({ selected, active, validate, list }: { selected: Resource; active: boolean; validate: Validate; list: (options: { supported: boolean; selection?: string; choose: (selection: PRSelection, button: HTMLButtonElement) => void; back: () => void; enrich: (scope: Document) => void }) => ReactNode }) {
  useLocale(); const transport = useTransport(), avatars = useMemo(() => new PRAvatarCache(), [transport]); useEffect(() => () => avatars.clear(), [avatars]); const [selection, setSelection] = useState<PRSelection>(), [epoch, setEpoch] = useState(0), opener = useRef<HTMLButtonElement | undefined>(undefined), body = useRef<HTMLDivElement>(null), [wide, setWide] = useState(false);
  const status = useQuery(SystemQuery.getStatus, {}, { ...options, enabled: active }); const supported = Boolean(status.data?.capabilities.includes(SystemCapability.PULL_REQUEST_WORKSPACE_V1));
  useLayoutEffect(() => { if (!body.current || typeof ResizeObserver === "undefined") return; const observer = new ResizeObserver(([entry]) => setWide(entry!.contentRect.width >= 900)); observer.observe(body.current); return () => observer.disconnect(); }, []);
  const enrich = useCallback((scope: Document) => { setSelection(old => { if (!old || old.scope === scope) return old; const node = items(scope.nodes).map(object).find(n => object(n.item).number === old.number); return node ? { ...old, node, scope } : old; }); }, []);
  const back = () => { setSelection(undefined); requestAnimationFrame(() => { if (opener.current?.isConnected) opener.current.focus({ preventScroll: true }); }); };
  return <AvatarContext.Provider value={avatars}><div ref={body} className="pr-workspace" data-wide={wide} data-selected={Boolean(selection)}><div className="pr-workspace-list" hidden={!wide && Boolean(selection)}>{list({ supported, selection: selection?.number, choose: (selection, button) => { opener.current = button; setSelection(selection); }, back, enrich })}</div><div className="pr-workspace-selected" hidden={!wide && !selection}>{selection ? <Detail key={`${selection.number}:${epoch}`} reload={() => setEpoch(e => e + 1)} selected={selected} selection={selection} supported={supported} active={active} back={back} validate={validate} /> : <p className="pr-workspace-prompt">{copy("pr-workspace.select")}</p>}</div></div></AvatarContext.Provider>;
}
