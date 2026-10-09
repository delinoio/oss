// SPDX-License-Identifier: Apache-2.0
import { Timestamp } from "./timestamp-display";
import { PRListHeader, PRListCards } from "./pr-list-cards";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { useStablePageRevisions, paginationError, invalidGitHubPage, useGitHubScrollRoot, visiblePageIds } from "./github-scroll";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useMemo, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { FailureCode, IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, text, type Document } from "./documents";
import { OpenPRProblemHistory } from "./pr-problems";
import { Problem } from "./ui";
import { OpenGitHub } from "./github-opening";
import { ItemKind, QueryOperation, type GitHubQuery, positive, uuid, bounded, date, sha, actorValid } from "./github-query-model";
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
function QueryResultPage({ selected, query, change, back, result, validated }: { selected: Resource; query: GitHubQuery; change: (query: GitHubQuery) => void; back?: () => void; result: { data?: unknown; error?: unknown; isFetching: boolean; refetch: () => void }; validated?: Document }) {
  useLocale();
  const data = validated;

  return <section aria-label={copy("github-items.githubQueryResults_66fac2")}>{query.operation === QueryOperation.Checks || query.operation === QueryOperation.Statuses ? <label>{copy("github-items.prResultPageSize_70aca4")}<select value={query.page_size} disabled={result.isFetching} onChange={(event) => change({ ...query, page: 1, page_size: Number(event.target.value) })}><option value={1}>1</option><option value={5}>5</option><option value={10}>10</option><option value={20}>20</option></select></label> : null}<div className="actions">{back ? <button onClick={back}>{copy("github-items.backToResults_c7ef0e")}</button> : null}<button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("github-items.refreshGithubResults_bd77c0")}</button></div>{result.isFetching ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}<Problem error={result.error} />{result.data && !data ? <p role="alert">{copy("github-items.theGithubResultDoesNotMatch_3c7624")}</p> : null}
    {data ? <><p>{result.error || result.isFetching ? copy("github-items.previousObservation_1bd8a6") : copy("github-items.observed_64fa8a")}: {<Timestamp value={text(data.observed_at)} />} · {query.kind === ItemKind.PullRequest ? copy("github-items.pullRequests_d9e3f2") : copy("github-items.issues_666067")}{query.page ? copy("github-items.page_bff0c8", { v0: query.operation, v1: query.state ? copy("github-items.message_2fa20b", { v0: query.state }) : "", v2: query.page }) : ""}</p>{data.total_count != null ? <p><LocalizedText id="github-items.githubReportsMatchesSearchExposesAt_f846ae" components={{ s0: <>{text(data.total_count)}</> }} /></p> : null}{data.incomplete ? <p role="status">{copy("github-items.githubReturnedIncompleteSearchResultsMissing_29a156")}</p> : null}{data.search_limit_reached ? <p role="status">{copy("github-items.githubSSearchLimitHasBeen_c17e40")}</p> : null}
      {items(data.items).map((raw) => { const item = object(raw), author = object(item.author); return <article className="result" key={`${text(item.identity_source)}:${text(item.id)}`}><h4>#{text(item.number)} {text(item.title)}</h4><p><LocalizedText id="github-items.updated_e646db" components={{ s0: <>{text(item.state)}</>, s1: <>{item.draft ? copy("github-items.draft_e98b44") : ""}</>, s2: <>{text(author.login) || copy("github-items.extra.a326f4758492")}</>, s3: <>{author.kind === "unknown" ? copy("github-items.unverifiedAuthorType_716680") : ""}</>, s4: <><Timestamp value={text(item.updated_at)} /></> }} /></p>{query.operation === QueryOperation.Detail || isObservation(query) ? <><p><LocalizedText id="github-items.githubAddress_e2d25b" components={{ s0: <code>{text(item.url)}</code> }} /></p><OpenGitHub url={text(item.url)} disabled={result.isFetching || Boolean(result.error)} />{query.kind === ItemKind.PullRequest ? <><p>{text(item.head_ref)} → {text(item.base_ref)}</p><p><LocalizedText id="github-items.head_20fc49" components={{ s0: <code>{text(item.head_sha)}</code> }} /></p><PRSource value={item.head_repository} /><p><LocalizedText id="github-items.mergedMergeability_7591f8" components={{ s0: <>{item.merged ? copy("github-items.yes_85a39a") : copy("github-items.no_1ea442")}</>, s1: <>{item.mergeable == null ? copy("github-items.unknown_b764cd") : item.mergeable ? copy("github-items.mergeable_e8007a") : copy("github-items.notMergeable_89299a")}</> }} /></p><p>{copy("github-items.mergeabilityDoesNotEstablishCiStatus_54c5f6")}</p>{query.operation === QueryOperation.Detail ? <div className="actions"><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Diff, number: text(item.number) })}>{copy("github-items.readPrDiff_ae38b9")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Checks, number: text(item.number), page: 1, page_size: 20 })}>{copy("github-items.readPrChecks_6a6724")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Statuses, number: text(item.number), page: 1, page_size: 20 })}>{copy("github-items.readPrCommitStatuses_8b822d")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Rules, number: text(item.number) })}>{copy("github-items.readActivePrRules_54e2ce")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.CI, number: text(item.number) })}>{copy("github-items.evaluateRequiredCi_397b69")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Feedback, number: text(item.number) })}>{copy("github-items.readPublishedFeedback_a07548")}</button><button disabled={result.isFetching} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Reviewers, number: text(item.number) })}>{copy("github-items.verifyFeedbackAuthors_8f3a58")}</button></div> : null}</> : null}{query.kind === ItemKind.PullRequest ? <OpenPRProblemHistory selection={{ repositoryId: selected.id, remoteRepositoryId: text(object(data.repository).id), pullRequestId: text(item.id), number: text(item.number) }} /> : null}{isObservation(query) ? <PRObservation value={data} query={query} item={item} /> : <pre>{text(item.body) || copy("github-items.extra.c8c2e1d98310")}</pre>}</> : <button disabled={result.isFetching} onClick={() => change({ kind: query.kind, operation: QueryOperation.Detail, number: text(item.number) })}><LocalizedText id="github-items.read_37b452" components={{ s0: <>{text(item.number)}</> }} /></button>}</article>; })}
      {items(data.items).length === 0 ? <p><LocalizedText id="github-items.noOnThisReturnedPage_87c317" components={{ s0: <>{query.kind === ItemKind.Issue ? copy("github-items.issues_02e3fe") : copy("github-items.pullRequests_dcf2de")}</> }} /></p> : null}</> : null}
  </section>;
}

type QueryProps = { selected: Resource; query: GitHubQuery; change: (query: GitHubQuery) => void; back?: () => void; active?: boolean; standaloneCards?: boolean; pending?: ReactNode };
function QueryResult(props: QueryProps) { return props.query.page ? <PaginatedQueryResult {...props} /> : <SingleQueryResult {...props} />; }
function SingleQueryResult({ active = true, ...props }: QueryProps) {
  const result = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: props.selected.id, schemaVersion: 1, queryJson: encode(props.query) }, { enabled: active, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, gcTime: 0, staleTime: 0 });
  const data = result.data?.schemaVersion === 1 ? githubResult(result.data.documentJson, props.selected, props.query) : undefined;
  return <QueryResultPage {...props} result={result} validated={data} />;
}
function PaginatedQueryResult({ active = true, ...props }: QueryProps) {
  const { root, bindRoot } = useGitHubScrollRoot();
  const scope = JSON.stringify([props.selected.id, String(props.selected.revision), props.query]);
  const binding = useMemo(() => ({ head: "" }), [scope]);
  const validateBoundary = useStablePageRevisions(scope);
  const request = useCallback((token: string) => ({ repositoryId: props.selected.id, schemaVersion: 1, queryJson: encode({ ...props.query, page: token ? Number(token) : props.query.page }) }), [props.selected, props.query]);
  const project = useCallback((reply: { schemaVersion: number; documentJson: Uint8Array }, token: string) => {
    const query = { ...props.query, page: token ? Number(token) : props.query.page };
    const data = reply.schemaVersion === 1 ? githubResult(reply.documentJson, props.selected, query) : undefined;
    if (!data) invalidGitHubPage();
    if (props.standaloneCards && (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(text(data.observed_at)) || items(data.items).some(raw => !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(text(object(raw).updated_at))))) invalidGitHubPage();
    // Checks and statuses page their observations, while the PR envelope is
    // repeated. Retain the original head and deduplicate the observed rows.
    const item = object(items(data.items)[0]);
    const observation = query.operation === QueryOperation.Checks ? "checks" : query.operation === QueryOperation.Statuses ? "statuses" : undefined;
    if (observation) {
      const head = text(item.id) + ":" + text(item.head_sha) + ":" + text(item.base_sha);
      if (!token) binding.head = head;
      else if (binding.head !== head) invalidGitHubPage();
    }
    const source = observation ? items(object(data[observation])[observation === "checks" ? "runs" : "contexts"]) : items(data.items);
    const rows = source.map(raw => { const row = object(raw); return { id: observation ? observation + ":" + text(row.id) : text(row.identity_source) + ":" + text(row.id), revision: BigInt(Date.parse(text(row.updated_at) || text(item.updated_at))) }; });
    validateBoundary(token, rows);
    return { rows, nextPageToken: data.next_page ? String(data.next_page) : "", payload: [{ data, query }] };
  }, [props.selected, props.query, props.standaloneCards, validateBoundary, binding]);
  const reader = useConnectPaginationReader(IntegrationQuery.queryRepositoryIntegration, request, project);
  const chain = usePaginationChain(scope, active, reader);
  usePaginationRefresh(IntegrationQuery.queryRepositoryIntegration, request(""), active, chain.refresh);
  return <div ref={bindRoot} role={props.standaloneCards ? "region" : undefined} aria-label={props.standaloneCards ? copy("github-items.githubQueryResults_66fac2") : undefined}>
    {props.standaloneCards ? <PRListHeader selected={props.selected} pending={props.pending} reading={Boolean(chain.loading)} reloadRequired={Boolean(chain.error?.stalled || chain.error?.failure.code === FailureCode.CursorExpired)} refresh={chain.refreshExplicit} /> : null}
    {props.standaloneCards && chain.loading ? <p role="status">{copy("github-items.readingGithub_ebcef8")}</p> : null}
    <Problem error={paginationError(chain.error?.failure)} />
    <ScrollPayloadWindow query={chain} root={root} active={active}>{(payload, projections) => payload.map(({ data, query }) => { const ids = visiblePageIds(chain.pages, projections); const observation = query.operation === QueryOperation.Checks ? "checks" : query.operation === QueryOperation.Statuses ? "statuses" : undefined; const field = observation === "checks" ? "runs" : "contexts";
      const visible = observation ? { ...data, [observation]: { ...object(data[observation]), [field]: items(object(data[observation])[field]).filter(raw => ids.has(observation + ":" + text(object(raw).id))) } } : { ...data, items: items(data.items).filter(raw => { const item = object(raw); return ids.has(text(item.identity_source) + ":" + text(item.id)); }) }; return props.standaloneCards ? <PRListCards key={query.page} data={visible} query={query} reading={Boolean(chain.loading)} previous={Boolean(chain.error)} emptyPage={!items(visible.items).length} change={props.change} /> : <QueryResultPage key={query.page} {...props} query={query} validated={visible} result={{ data, error: paginationError(chain.error?.failure), isFetching: Boolean(chain.loading), refetch: chain.refresh }} />; })}</ScrollPayloadWindow>
    <ScrollContinuation query={chain} root={root} active={active} label={copy("github-items.githubQueryResults_66fac2")} />
  </div>;
}

export interface PullRequestNavigation {
  scopeKey: string;
  query: GitHubQuery;
  previous: GitHubQuery[];
}

export function StandalonePullRequestResults({ selected, navigation, active, changeNavigation, pending }: { selected: Resource; navigation: PullRequestNavigation; active: boolean; pending?: ReactNode; changeNavigation: (navigation: PullRequestNavigation) => void }) {
  useLocale();
  function change(next: GitHubQuery) {
    const previous = next.operation !== navigation.query.operation ? [...navigation.previous.slice(-7), navigation.query] : navigation.previous;
    changeNavigation({ ...navigation, query: next, previous });
  }
  return <QueryResult key={JSON.stringify(navigation.query)} selected={selected} query={navigation.query} active={active} standaloneCards={navigation.query.operation === QueryOperation.List || navigation.query.operation === QueryOperation.Search} pending={pending} change={change} back={navigation.previous.length ? () => changeNavigation({ ...navigation, query: navigation.previous[navigation.previous.length - 1]!, previous: navigation.previous.slice(0, -1) }) : undefined} />;
}
