import { LocalizedText, copy, useLocale } from "./localization";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, text, type Document } from "./documents";
import { OpenPRProblemHistory } from "./pr-problems";
import { Problem } from "./ui";
import { OpenGitHub } from "./github-opening";
import { ItemKind, QueryOperation, ItemState, type GitHubQuery, positive, uuid, bounded, date, sha, actorValid } from "./github-query-model";
import { PRSource, validPRSource } from "./github-pr-source";
import { PRObservation, validPRObservation, isObservation } from "./github-pr-observations";

export function githubResult(raw: Uint8Array, selected: Resource, query: GitHubQuery): Document | undefined {
  if (raw.byteLength > 1 << 20) return;
  let result: Document;
  try { result = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return; }
  const selection = document(selected), remote = object(result.repository), observedQuery = object(result.query);
  if (result.repository_id !== selected.id || result.repository_revision !== selected.revision.toString() || !positive(result.repository_revision) || result.profile_id !== selection.integration_id || !uuid(result.profile_id) || !uuid(result.generation_id) || !date(result.observed_at) || !actorValid(result.identity, true)) return;
  if (Object.keys(observedQuery).length !== Object.keys(query).length || Object.entries(query).some(([key, value]) => observedQuery[key] !== value)) return;
  if (remote.provider !== "github.com" || !positive(remote.id) || !bounded(remote.node_id, 256) || text(remote.owner).toLowerCase() !== text(selection.github_owner).toLowerCase() || text(remote.name).toLowerCase() !== text(selection.github_name).toLowerCase() || typeof remote.private !== "boolean") return;
  if (!Array.isArray(result.items)) return;
  if ((query.operation === QueryOperation.Detail || query.operation === QueryOperation.Diff || query.operation === QueryOperation.Rules || query.operation === QueryOperation.CI || query.operation === QueryOperation.Feedback || query.operation === QueryOperation.Reviewers) ? result.items.length !== 1 || result.next_page != null : result.items.length > (query.page_size ?? 0) || (result.next_page != null && (result.next_page !== (query.page ?? 0) + 1 || Number(result.next_page) > 10000))) return;
  if (query.operation === QueryOperation.Search) {
    if (typeof result.total_count !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(result.total_count) || BigInt(result.total_count) > 18446744073709551615n || BigInt(result.total_count) < BigInt(result.items.length) || typeof result.incomplete !== "boolean" || (result.search_limit_reached != null && typeof result.search_limit_reached !== "boolean")) return;
    if (result.next_page != null && (Number(result.next_page) - 1) * (query.page_size ?? 0) >= 1000) return;
  } else if (result.total_count != null || result.incomplete != null || result.search_limit_reached != null) return;
  if (isObservation(query) && result.items.length !== 1) return;
  const seen = new Set<string>();
  for (const raw of result.items) {
    const item = object(raw), source = query.kind === ItemKind.PullRequest && query.operation !== QueryOperation.Search ? "pull-request-api" : "issue-api";
    const expectedURL = `https://github.com/${text(remote.owner)}/${text(remote.name)}/${query.kind === ItemKind.PullRequest ? "pull" : "issues"}/${text(item.number)}`;
    if (item.provider !== "github.com" || item.kind !== query.kind || item.identity_source !== source || !positive(item.id) || seen.has(item.id) || !positive(item.number) || !bounded(item.node_id, 256) || !bounded(item.title, 4096) || !["open", "closed"].includes(text(item.state)) || !date(item.created_at) || !date(item.updated_at) || Date.parse(text(item.updated_at)) < Date.parse(text(item.created_at)) || item.url !== expectedURL || (item.author != null && !actorValid(item.author))) return;
    seen.add(item.id);
    if (item.draft != null && typeof item.draft !== "boolean") return;
    if (query.operation === QueryOperation.Detail || isObservation(query)) {
      if (item.number !== query.number || !bounded(item.body, 128 << 10, false)) return;
      if (query.kind === ItemKind.PullRequest && !validPRSource(item.head_repository, remote)) return;
      if (query.kind === ItemKind.PullRequest && (typeof item.draft !== "boolean" || typeof item.merged !== "boolean" || (item.mergeable != null && typeof item.mergeable !== "boolean") || !bounded(item.base_ref, 1024) || !bounded(item.head_ref, 1024) || !sha(item.base_sha) || !sha(item.head_sha))) return;
    } else if (["body", "merged", "mergeable", "base_ref", "base_sha", "head_ref", "head_sha", "head_repository"].some((key) => item[key] != null)) return;
    if (query.kind === ItemKind.Issue && ["draft", "merged", "mergeable", "base_ref", "base_sha", "head_ref", "head_sha", "head_repository"].some((key) => item[key] != null)) return;
  }
  if (!validPRObservation(result, query, object(result.items[0]))) return;
  return result;
}
function QueryResult({ selected, query, change, back, active = true }: { selected: Resource; query: GitHubQuery; change: (query: GitHubQuery) => void; back?: () => void; active?: boolean }) {
  useLocale();
  const result = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(query) }, { enabled: active, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, gcTime: 0, staleTime: 0 });
  const data = result.data?.schemaVersion === 1 ? githubResult(result.data.documentJson, selected, query) : undefined;
  return <section aria-label={copy("github-items.githubQueryResults_66fac2")}>{query.operation === QueryOperation.Checks || query.operation === QueryOperation.Statuses ? <label>{copy("github-items.prResultPageSize_70aca4")}<select value={query.page_size} disabled={result.isFetching} onChange={(event) => change({ ...query, page: 1, page_size: Number(event.target.value) })}><option value={1}>1</option><option value={5}>5</option><option value={10}>10</option><option value={20}>20</option></select></label> : null}<div className="actions">{back ? <button onClick={back}>{copy("github-items.backToResults_c7ef0e")}</button> : null}<button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("github-items.refreshGithubResults_bd77c0")}</button></div>{result.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}<Problem error={result.error} />{result.data && !data ? <p role="alert">{copy("github-items.theGithubResultDoesNotMatch_3c7624")}</p> : null}
    {data ? <><p>{result.error || result.isFetching ? copy("github-items.previousObservation_1bd8a6") : copy("github-items.observed_64fa8a")}: {text(data.observed_at)} · {query.kind === ItemKind.PullRequest ? copy("github-items.pullRequests_d9e3f2") : copy("github-items.issues_666067")}{query.page ? copy("github-items.page_bff0c8", { v0: query.operation, v1: query.state ? copy("github-items.message_2fa20b", { v0: query.state }) : "", v2: query.page }) : ""}</p>{data.total_count != null ? <p><LocalizedText id="github-items.githubReportsMatchesSearchExposesAt_f846ae" components={{ s0: <>{text(data.total_count)}</> }} /></p> : null}{data.incomplete ? <p role="status">{copy("github-items.githubReturnedIncompleteSearchResultsMissing_29a156")}</p> : null}{data.search_limit_reached ? <p role="status">{copy("github-items.githubSSearchLimitHasBeen_c17e40")}</p> : null}
      {items(data.items).map((raw) => { const item = object(raw), author = object(item.author); return <article className="result" key={`${text(item.identity_source)}:${text(item.id)}`}><h4>#{text(item.number)} {text(item.title)}</h4><p><LocalizedText id="github-items.updated_e646db" components={{ s0: <>{text(item.state)}</>, s1: <>{item.draft ? copy("github-items.draft_e98b44") : ""}</>, s2: <>{text(author.login) || copy("github-items.extra.a326f4758492")}</>, s3: <>{author.kind === "unknown" ? copy("github-items.unverifiedAuthorType_716680") : ""}</>, s4: <>{text(item.updated_at)}</> }} /></p>{query.operation === QueryOperation.Detail || isObservation(query) ? <><p><LocalizedText id="github-items.githubAddress_e2d25b" components={{ s0: <code>{text(item.url)}</code> }} /></p><OpenGitHub url={text(item.url)} disabled={result.isFetching || Boolean(result.error)} />{query.kind === ItemKind.PullRequest ? <><p>{text(item.head_ref)} → {text(item.base_ref)}</p><p><LocalizedText id="github-items.head_20fc49" components={{ s0: <code>{text(item.head_sha)}</code> }} /></p><PRSource value={item.head_repository} /><p><LocalizedText id="github-items.mergedMergeability_7591f8" components={{ s0: <>{item.merged ? copy("github-items.yes_85a39a") : copy("github-items.no_1ea442")}</>, s1: <>{item.mergeable == null ? copy("github-items.unknown_b764cd") : item.mergeable ? copy("github-items.mergeable_e8007a") : copy("github-items.notMergeable_89299a")}</> }} /></p><p>{copy("github-items.mergeabilityDoesNotEstablishCiStatus_54c5f6")}</p>{query.operation === QueryOperation.Detail ? <div className="actions"><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Diff, number: text(item.number) })}>{copy("github-items.readPrDiff_ae38b9")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Checks, number: text(item.number), page: 1, page_size: 20 })}>{copy("github-items.readPrChecks_6a6724")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Statuses, number: text(item.number), page: 1, page_size: 20 })}>{copy("github-items.readPrCommitStatuses_8b822d")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Rules, number: text(item.number) })}>{copy("github-items.readActivePrRules_54e2ce")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.CI, number: text(item.number) })}>{copy("github-items.evaluateRequiredCi_397b69")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Feedback, number: text(item.number) })}>{copy("github-items.readPublishedFeedback_a07548")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Reviewers, number: text(item.number) })}>{copy("github-items.verifyFeedbackAuthors_8f3a58")}</button></div> : null}</> : null}{query.kind === ItemKind.PullRequest ? <OpenPRProblemHistory selection={{ repositoryId: selected.id, remoteRepositoryId: text(object(data.repository).id), pullRequestId: text(item.id), number: text(item.number) }} /> : null}{isObservation(query) ? <PRObservation value={data} query={query} item={item} /> : <pre>{text(item.body) || copy("github-items.extra.c8c2e1d98310")}</pre>}</> : <button disabled={result.isFetching} onClick={() => change({ kind: query.kind, operation: QueryOperation.Detail, number: text(item.number) })}><LocalizedText id="github-items.read_37b452" components={{ s0: <>{text(item.number)}</> }} /></button>}</article>; })}
      {items(data.items).length === 0 ? <p><LocalizedText id="github-items.noOnThisReturnedPage_87c317" components={{ s0: <>{query.kind === ItemKind.Issue ? copy("github-items.issues_02e3fe") : copy("github-items.pullRequests_dcf2de")}</> }} /></p> : null}{query.page ? <nav aria-label={copy("github-items.githubResultPages_52bc9b")}><button disabled={result.isFetching || query.page === 1} onClick={() => change({ ...query, page: 1 })}>{copy("github-items.firstGithubPage_fbf165")}</button><button disabled={result.isFetching || !data.next_page} onClick={() => change({ ...query, page: Number(data.next_page) })}>{copy("github-items.nextGithubPage_1ae370")}</button></nav> : null}</> : null}
  </section>;
}

export interface PullRequestNavigation {
  scopeKey: string;
  query: GitHubQuery;
  previous: GitHubQuery[];
}

export function StandalonePullRequestResults({ selected, navigation, active, changeNavigation }: { selected: Resource; navigation: PullRequestNavigation; active: boolean; changeNavigation: (navigation: PullRequestNavigation) => void }) {
  useLocale();
  function change(next: GitHubQuery) {
    const previous = next.operation !== navigation.query.operation ? [...navigation.previous.slice(-7), navigation.query] : navigation.previous;
    changeNavigation({ ...navigation, query: next, previous });
  }
  return <QueryResult key={JSON.stringify(navigation.query)} selected={selected} query={navigation.query} active={active} change={change} back={navigation.previous.length ? () => changeNavigation({ ...navigation, query: navigation.previous[navigation.previous.length - 1]!, previous: navigation.previous.slice(0, -1) }) : undefined} />;
}
function ItemBrowser({ selected }: { selected: Resource }) {
  useLocale();
  const [kind, setKind] = useState(ItemKind.PullRequest), [state, setState] = useState(ItemState.Open), [search, setSearch] = useState(""), [pageSize, setPageSize] = useState(20);
  const initial: GitHubQuery = { kind: ItemKind.PullRequest, operation: QueryOperation.List, state: ItemState.Open, page: 1, page_size: 20 };
  const [query, setQuery] = useState<GitHubQuery>(initial), [previous, setPrevious] = useState<GitHubQuery[]>([]);
  function change(next: GitHubQuery) { if (next.operation !== query.operation) setPrevious((old) => [...old.slice(-7), query]); setQuery(next); }
  return <section aria-label={copy("github-items.browseRepositoryGithubItems_25c5d1")}><form onSubmit={(event) => { event.preventDefault(); setPrevious([]); setQuery({ kind, operation: search.trim() ? QueryOperation.Search : QueryOperation.List, state, page: 1, page_size: pageSize, ...(search.trim() ? { search: search.trim() } : {}) }); }}><label>{copy("github-items.githubItemType_bdad5e")}<select value={kind} onChange={(event) => setKind(event.target.value as ItemKind)}><option value={ItemKind.PullRequest}>{copy("github-items.pullRequests_d9e3f2")}</option><option value={ItemKind.Issue}>{copy("github-items.issues_666067")}</option></select></label><label>{copy("github-items.githubItemState_e62ca5")}<select value={state} onChange={(event) => setState(event.target.value as ItemState)}><option value={ItemState.Open}>{copy("github-items.open_ed077f")}</option><option value={ItemState.Closed}>{copy("github-items.closed_c21ead")}</option><option value={ItemState.All}>{copy("github-items.all_a52ace")}</option></select></label><label>{copy("github-items.githubPageSize_703f1f")}<select value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))}><option value={1}>1</option><option value={5}>5</option><option value={10}>10</option><option value={20}>20</option></select></label><label>{copy("github-items.searchTitleAndBody_f2c94c")}<input value={search} maxLength={120} onChange={(event) => setSearch(event.target.value)} /></label><p>{copy("github-items.usePlainWordsNumbersSpacesHyphens_a1e3a4")}</p><button>{copy("github-items.readGithubItems_9d9b08")}</button></form><QueryResult key={JSON.stringify(query)} selected={selected} query={query} change={change} back={previous.length ? () => { setQuery(previous[previous.length - 1]); setPrevious((old) => old.slice(0, -1)); } : undefined} /></section>;
}
export function RepositoryGitHubItems({ selected, active }: { selected: Resource; active: boolean }) {
  useLocale();
  const [open, setOpen] = useState(false), data = document(selected);
  const configured = Boolean(text(data.integration_id) && text(data.github_owner) && text(data.github_name));
  return <div><button disabled={!configured || selected.schemaVersion !== 1} aria-expanded={open} onClick={() => setOpen((value) => !value)}>{open ? copy("github-items.closeGithubItems_ed30c0") : copy("github-items.browseGithubItems_40ff50")}</button>{open && active ? <ItemBrowser key={`${selected.id}:${selected.revision}`} selected={selected} /> : null}</div>;
}
