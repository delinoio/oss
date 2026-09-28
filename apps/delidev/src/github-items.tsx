import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, text, type Document } from "./documents";
import { Problem } from "./ui";

enum ItemKind { PullRequest = "pull-request", Issue = "issue" }
enum QueryOperation { List = "list", Search = "search", Detail = "detail" }
enum ItemState { Open = "open", Closed = "closed", All = "all" }
export type GitHubQuery = { kind: ItemKind; operation: QueryOperation; state?: ItemState; search?: string; number?: string; page?: number; page_size?: number };
const positive = (value: unknown): value is string => typeof value === "string" && /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n;
const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
const bounded = (value: unknown, limit: number, required = true): value is string => typeof value === "string" && (!required || value.trim().length > 0) && new TextEncoder().encode(value).length <= limit && !value.includes("\0");
const date = (value: unknown) => bounded(value, 64) && Number.isFinite(Date.parse(value));
const sha = (value: unknown) => typeof value === "string" && /^[a-f0-9]{40}$/.test(value);
function actorValid(raw: unknown, identity = false) {
  const actor = object(raw), kind = text(actor.kind), native = text(actor.provider_type), login = kind === "bot" ? text(actor.login).replace(/\[bot\]$/, "") : text(actor.login);
  if (!positive(actor.id) || !bounded(actor.node_id, 256)) return false;
  if (identity) return /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(login);
  if (!bounded(native, 64) || !bounded(actor.login, 100) || /[\r\n]/.test(native + text(actor.login))) return false;
  if (kind === "unknown") return !["User", "Bot", "Organization"].includes(native);
  return ({ user: "User", bot: "Bot", organization: "Organization" } as Record<string, string>)[kind] === native && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(login);
}
export function githubResult(raw: Uint8Array, selected: Resource, query: GitHubQuery): Document | undefined {
  if (raw.byteLength > 1 << 20) return;
  let result: Document;
  try { result = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return; }
  const selection = document(selected), remote = object(result.repository), observedQuery = object(result.query);
  if (result.repository_id !== selected.id || result.repository_revision !== selected.revision.toString() || !positive(result.repository_revision) || result.profile_id !== selection.integration_id || !uuid(result.profile_id) || !uuid(result.generation_id) || !date(result.observed_at) || !actorValid(result.identity, true)) return;
  if (Object.keys(observedQuery).length !== Object.keys(query).length || Object.entries(query).some(([key, value]) => observedQuery[key] !== value)) return;
  if (remote.provider !== "github.com" || !positive(remote.id) || !bounded(remote.node_id, 256) || text(remote.owner).toLowerCase() !== text(selection.github_owner).toLowerCase() || text(remote.name).toLowerCase() !== text(selection.github_name).toLowerCase() || typeof remote.private !== "boolean") return;
  if (!Array.isArray(result.items)) return;
  if (query.operation === QueryOperation.Detail ? result.items.length !== 1 || result.next_page != null : result.items.length > (query.page_size ?? 0) || (result.next_page != null && (result.next_page !== (query.page ?? 0) + 1 || Number(result.next_page) > 10000))) return;
  if (query.operation === QueryOperation.Search) {
    if (typeof result.total_count !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(result.total_count) || BigInt(result.total_count) > 18446744073709551615n || BigInt(result.total_count) < BigInt(result.items.length) || typeof result.incomplete !== "boolean" || (result.search_limit_reached != null && typeof result.search_limit_reached !== "boolean")) return;
    if (result.next_page != null && (Number(result.next_page) - 1) * (query.page_size ?? 0) >= 1000) return;
  } else if (result.total_count != null || result.incomplete != null || result.search_limit_reached != null) return;
  const seen = new Set<string>();
  for (const raw of result.items) {
    const item = object(raw), source = query.kind === ItemKind.PullRequest && query.operation !== QueryOperation.Search ? "pull-request-api" : "issue-api";
    const expectedURL = `https://github.com/${text(remote.owner)}/${text(remote.name)}/${query.kind === ItemKind.PullRequest ? "pull" : "issues"}/${text(item.number)}`;
    if (item.provider !== "github.com" || item.kind !== query.kind || item.identity_source !== source || !positive(item.id) || seen.has(item.id) || !positive(item.number) || !bounded(item.node_id, 256) || !bounded(item.title, 4096) || !["open", "closed"].includes(text(item.state)) || !date(item.created_at) || !date(item.updated_at) || Date.parse(text(item.updated_at)) < Date.parse(text(item.created_at)) || item.url !== expectedURL || (item.author != null && !actorValid(item.author))) return;
    seen.add(item.id);
    if (item.draft != null && typeof item.draft !== "boolean") return;
    if (query.operation === QueryOperation.Detail) {
      if (item.number !== query.number || !bounded(item.body, 128 << 10, false)) return;
      if (query.kind === ItemKind.PullRequest && (typeof item.draft !== "boolean" || typeof item.merged !== "boolean" || (item.mergeable != null && typeof item.mergeable !== "boolean") || !bounded(item.base_ref, 1024) || !bounded(item.head_ref, 1024) || !sha(item.base_sha) || !sha(item.head_sha))) return;
    } else if (["body", "merged", "mergeable", "base_ref", "base_sha", "head_ref", "head_sha"].some((key) => item[key] != null)) return;
    if (query.kind === ItemKind.Issue && ["draft", "merged", "mergeable", "base_ref", "base_sha", "head_ref", "head_sha"].some((key) => item[key] != null)) return;
  }
  return result;
}
function QueryResult({ selected, query, change, back }: { selected: Resource; query: GitHubQuery; change: (query: GitHubQuery) => void; back?: () => void }) {
  const result = useQuery(IntegrationQuery.queryRepositoryIntegration, { repositoryId: selected.id, schemaVersion: 1, queryJson: encode(query) }, { retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, gcTime: 0, staleTime: 0 });
  const data = result.data?.schemaVersion === 1 ? githubResult(result.data.documentJson, selected, query) : undefined;
  return <section aria-label="GitHub query results"><div className="actions">{back ? <button onClick={back}>Back to results</button> : null}<button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh GitHub results</button></div>{result.isFetching ? <p role="status">Reading GitHub…</p> : null}<Problem error={result.error} />{result.data && !data ? <p role="alert">The GitHub result does not match the selected repository and query. Refresh repository settings.</p> : null}
    {data ? <><p>{result.error || result.isFetching ? "Previous observation" : "Observed"}: {text(data.observed_at)} · {query.kind === ItemKind.PullRequest ? "Pull requests" : "Issues"}{query.operation !== QueryOperation.Detail ? ` · ${query.operation} · ${query.state} · page ${query.page}` : ""}</p>{data.total_count != null ? <p>GitHub reports {text(data.total_count)} matches. Search exposes at most the first 1,000 matches.</p> : null}{data.incomplete ? <p role="status">GitHub returned incomplete search results. Missing items cannot be treated as absent.</p> : null}{data.search_limit_reached ? <p role="status">GitHub's search limit has been reached. Narrow the search terms.</p> : null}
      {items(data.items).map((raw) => { const item = object(raw), author = object(item.author); return <article className="result" key={`${text(item.identity_source)}:${text(item.id)}`}><h4>#{text(item.number)} {text(item.title)}</h4><p>{text(item.state)}{item.draft ? " · Draft" : ""} · {text(author.login) || "Author unavailable"}{author.kind === "unknown" ? " · Unverified author type" : ""} · updated {text(item.updated_at)}</p>{query.operation === QueryOperation.Detail ? <><p>GitHub address: <code>{text(item.url)}</code></p>{query.kind === ItemKind.PullRequest ? <><p>{text(item.head_ref)} → {text(item.base_ref)}</p><p>Head: <code>{text(item.head_sha)}</code></p><p>Merged: {item.merged ? "Yes" : "No"} · Mergeability: {item.mergeable == null ? "Unknown" : item.mergeable ? "Mergeable" : "Not mergeable"}</p><p>Mergeability does not establish CI status, active ruleset satisfaction or remediation eligibility.</p></> : null}<pre>{text(item.body) || "No body."}</pre></> : <button disabled={result.isFetching} onClick={() => change({ kind: query.kind, operation: QueryOperation.Detail, number: text(item.number) })}>Read #{text(item.number)}</button>}</article>; })}
      {items(data.items).length === 0 ? <p>No {query.kind === ItemKind.Issue ? "issues" : "pull requests"} on this returned page.</p> : null}{query.operation !== QueryOperation.Detail ? <nav aria-label="GitHub result pages"><button disabled={result.isFetching || query.page === 1} onClick={() => change({ ...query, page: 1 })}>First GitHub page</button><button disabled={result.isFetching || !data.next_page} onClick={() => change({ ...query, page: Number(data.next_page) })}>Next GitHub page</button></nav> : null}</> : null}
  </section>;
}
function ItemBrowser({ selected }: { selected: Resource }) {
  const [kind, setKind] = useState(ItemKind.PullRequest), [state, setState] = useState(ItemState.Open), [search, setSearch] = useState(""), [pageSize, setPageSize] = useState(20);
  const initial: GitHubQuery = { kind: ItemKind.PullRequest, operation: QueryOperation.List, state: ItemState.Open, page: 1, page_size: 20 };
  const [query, setQuery] = useState<GitHubQuery>(initial), [previous, setPrevious] = useState<GitHubQuery>();
  function change(next: GitHubQuery) { if (next.operation === QueryOperation.Detail) setPrevious(query); setQuery(next); }
  return <section aria-label="Browse repository GitHub items"><form onSubmit={(event) => { event.preventDefault(); setPrevious(undefined); setQuery({ kind, operation: search.trim() ? QueryOperation.Search : QueryOperation.List, state, page: 1, page_size: pageSize, ...(search.trim() ? { search: search.trim() } : {}) }); }}><label>GitHub item type<select value={kind} onChange={(event) => setKind(event.target.value as ItemKind)}><option value={ItemKind.PullRequest}>Pull requests</option><option value={ItemKind.Issue}>Issues</option></select></label><label>GitHub item state<select value={state} onChange={(event) => setState(event.target.value as ItemState)}><option value={ItemState.Open}>Open</option><option value={ItemState.Closed}>Closed</option><option value={ItemState.All}>All</option></select></label><label>GitHub page size<select value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))}><option value={1}>1</option><option value={5}>5</option><option value={10}>10</option><option value={20}>20</option></select></label><label>Search title and body<input value={search} maxLength={120} onChange={(event) => setSearch(event.target.value)} /></label><p>Use plain words, numbers, spaces, hyphens, underscores or periods. Leave search empty to list items.</p><button>Read GitHub items</button></form><QueryResult key={JSON.stringify(query)} selected={selected} query={query} change={change} back={query.operation === QueryOperation.Detail && previous ? () => setQuery(previous) : undefined} /></section>;
}
export function RepositoryGitHubItems({ selected, active }: { selected: Resource; active: boolean }) {
  const [open, setOpen] = useState(false), data = document(selected);
  const configured = Boolean(text(data.integration_id) && text(data.github_owner) && text(data.github_name));
  return <div><button disabled={!configured || selected.schemaVersion !== 1} aria-expanded={open} onClick={() => setOpen((value) => !value)}>{open ? "Close GitHub items" : "Browse GitHub items"}</button>{open && active ? <ItemBrowser key={`${selected.id}:${selected.revision}`} selected={selected} /> : null}</div>;
}
