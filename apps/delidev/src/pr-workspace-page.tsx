// SPDX-License-Identifier: Apache-2.0
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { useEffect, useState, useSyncExternalStore } from "react";
import { items, object, text, document, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { QueryOperation, ItemKind, type GitHubQuery, sha, positive, actorValid, date, uuid, bounded } from "./github-query-model";
import { usePRWorkspaceContext } from "./pr-workspace-context";
import { validPRSource } from "./github-pr-source";
import { Problem } from "./ui";

export function changeCounts(raw: unknown): { additions: number; deletions: number } | undefined {
  const value = object(raw);
  return Number.isSafeInteger(value.additions) && Number(value.additions) >= 0 && Number.isSafeInteger(value.deletions) && Number(value.deletions) >= 0 ? value as { additions: number; deletions: number } : undefined;
}
export function PRCounts({ value }: { value?: unknown }) {
  const counts = changeCounts(value);
  return counts ? <span className="pr-change-counts"><span>+{counts.additions}</span> <span>−{counts.deletions}</span></span> : <span aria-label={copy("pr-workspace.countsUnavailable")}>—</span>;
}
export function scopedPRDocument(raw: Uint8Array, selected: Resource): Document | undefined {
  if (raw.length > 1 << 20) return;
  try {
    const value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))), remote = object(value.repository), config = document(selected);
    if (value.repository_id !== selected.id || value.repository_revision !== selected.revision.toString() || value.profile_id !== config.integration_id || !positive(remote.id) || remote.provider !== "github.com" || text(remote.owner).toLowerCase() !== text(config.github_owner).toLowerCase() || text(remote.name).toLowerCase() !== text(config.github_name).toLowerCase() || !uuid(value.generation_id) || !uuid(value.profile_id) || !positive(value.repository_revision)) return;
    return value;
  } catch { return; }
}
export function validWorkspace(value: Document, seeds: string[]): boolean {
  if (!["complete", "incomplete", "ambiguous"].includes(text(value.state)) || !Array.isArray(value.rows) || value.rows.length > 100 || !Array.isArray(value.edges) || value.edges.length > 10000) return false;
  const numbers = new Set<string>(), ids = new Set<string>();
  for (const raw of value.rows) {
    const row = object(raw), item = object(row.item);
    if (!positive(item.number) || !positive(item.id) || numbers.has(item.number) || ids.has(text(item.id)) || item.provider !== "github.com" || item.kind !== "pull-request" || item.identity_source !== "pull-request-api" || !bounded(item.title, 4096) || !bounded(item.body, 128 << 10, false) || !bounded(item.base_ref, 1024) || !bounded(item.head_ref, 1024) || !["open", "closed"].includes(text(item.state)) || typeof item.draft !== "boolean" || typeof item.merged !== "boolean" || !validPRSource(item.head_repository, object(value.repository)) || item.url !== `https://github.com/${text(object(value.repository).owner)}/${text(object(value.repository).name)}/pull/${text(item.number)}` || !sha(item.base_sha) || !sha(item.head_sha) || !date(item.updated_at) || !text(item.title) || typeof row.seed !== "boolean" || row.seed !== seeds.includes(text(item.number)) || item.author != null && !actorValid(item.author) || row.counts != null && !changeCounts(row.counts)) return false;
    numbers.add(text(item.number)); ids.add(text(item.id));
  }
  if (value.state === "complete" && seeds.some(seed => !numbers.has(seed))) return false;
  const rows = items(value.rows).map(object), byNumber = new Map(rows.map(row => [text(object(row.item).number), object(row.item)])), edges = new Set<string>(), children = new Map<string, string[]>(), parentCounts = new Map<string, number>();
  for (const raw of value.edges) {
    const edge = object(raw), parent = byNumber.get(text(edge.parent)), child = byNumber.get(text(edge.child)), key = `${text(edge.parent)}:${text(edge.child)}`;
    if (!parent || !child || edge.parent === edge.child || edges.has(key) || parent.head_ref !== child.base_ref || object(object(parent.head_repository).repository).id !== object(value.repository).id) return false;
    edges.add(key); parentCounts.set(text(edge.child), (parentCounts.get(text(edge.child)) ?? 0) + 1); children.set(text(edge.parent), [...children.get(text(edge.parent)) ?? [], text(edge.child)]);
  }
  if (value.state === "complete") {
    if ([...parentCounts.values()].some(count => count > 1)) return false;
    const visited = new Set<string>(), visiting = new Set<string>();
    const visit = (number: string): boolean => { if (visiting.has(number)) return false; if (visited.has(number)) return true; visiting.add(number); for (const child of children.get(number) ?? []) if (!visit(child)) return false; visiting.delete(number); visited.add(number); return true; };
    if ([...numbers].some(number => !visit(number))) return false;
    for (const [parentNumber, parent] of byNumber) for (const [childNumber, child] of byNumber) if (parentNumber !== childNumber && parent.head_ref === child.base_ref && object(object(parent.head_repository).repository).id === object(value.repository).id && !edges.has(`${parentNumber}:${childNumber}`)) return false;
  }
  return true;
}
export function orderWorkspace(rows: Document[], edges: Document[], seeds: string[], complete: boolean): { row: Document; depth: number; stack: boolean }[] {
  if (!complete) return rows.map(row => ({ row, depth: 0, stack: false }));
  const byNumber = new Map(rows.map(row => [text(object(row.item).number), row])), adjacency = new Map<string, Set<string>>(), parents = new Set<string>();
  for (const edge of edges) {
    const parent = text(edge.parent), child = text(edge.child); parents.add(child);
    for (const [a, b] of [[parent, child], [child, parent]]) { if (!adjacency.has(a!)) adjacency.set(a!, new Set()); adjacency.get(a!)!.add(b!); }
  }
  const result: { row: Document; depth: number; stack: boolean }[] = [], seen = new Set<string>();
  for (const seed of seeds) {
    if (seen.has(seed) || !byNumber.has(seed)) continue;
    const component = new Set<string>(), pending = [seed];
    while (pending.length) { const number = pending.pop()!; if (component.has(number)) continue; component.add(number); for (const next of adjacency.get(number) ?? []) pending.push(next); }
    const visit = (number: string, depth: number) => { if (seen.has(number)) return; seen.add(number); result.push({ row: byNumber.get(number)!, depth, stack: component.size > 1 }); for (const edge of edges.filter(edge => edge.parent === number).sort((a, b) => Number(a.child) - Number(b.child))) visit(text(edge.child), depth + 1); };
    for (const root of [...component].filter(number => !parents.has(number)).sort((a, b) => Number(a) - Number(b))) visit(root, 0);
  }
  return result;
}
export function PRAvatar({ reference }: { reference?: string }) {
  const context = usePRWorkspaceContext(), transport = useTransport();
  const [url, setURL] = useState<string>();
  useEffect(() => {
    setURL(undefined);
    if (!reference || !context?.supported || context.busy) return;
    const cached = context.queue.avatars.get(reference); if (cached) { setURL(cached); return; }
    const abort = new AbortController(); let current = true;
    const options = createQueryOptions(IntegrationQuery.readPullRequestAvatar, { reference }, { transport });
    void context.queue.run(abort.signal, async signal => options.queryFn!({ signal } as never)).then(reply => {
      if (!current || reply.reference !== reference || reply.mediaType !== "image/png" || !reply.raster.length || reply.raster.length > 128 << 10) return;
      const url = URL.createObjectURL(new Blob([new Uint8Array(reply.raster)], { type: "image/png" })); setURL(context.queue.remember(reference, url));
    }).catch(() => { /* Decorative fallback retains no remote authority. */ });
    return () => { current = false; abort.abort(); };
  }, [reference, context?.supported, context?.busy, context?.queue, transport]);
  return url ? <img className="pr-avatar" src={url} onError={() => setURL(undefined)} alt="" width="24" height="24" /> : <span className="pr-avatar pr-avatar-fallback" aria-hidden="true" />;
}
export function PRWorkspacePage({ selected, data, query, reading, previous, change }: { selected: Resource; data: Document; query: GitHubQuery; reading: boolean; previous: boolean; change: (query: GitHubQuery) => void }) {
  useLocale();
  const context = usePRWorkspaceContext(), transport = useTransport(), seeds = items(data.items).map(raw => text(object(raw).number));
  useSyncExternalStore(context?.queue.subscribe ?? (() => () => undefined), context?.queue.snapshot ?? (() => 0));
  const seedKey = JSON.stringify(seeds);
  useEffect(() => { context?.queue.page(query.page!, seeds); return () => context?.queue.page(query.page!); }, [context?.queue, query.page, seedKey]);
  const options = createQueryOptions(IntegrationQuery.getPullRequestWorkspace, { repositoryId: selected.id, expectedRevision: selected.revision, seeds: seeds.map(Number) }, { transport });
  const result = useQuery({ ...options, queryFn: args => context!.queue.run(args.signal, async signal => options.queryFn!({ ...args, signal })), enabled: Boolean(context?.supported && !reading && seeds.length), retry: false, staleTime: Infinity, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const value = result.data?.schemaVersion === 1 ? scopedPRDocument(result.data.documentJson, selected) : undefined;
  const workspace = value && validWorkspace(value, seeds) ? value : undefined;
  useEffect(() => { if (workspace) context?.queue.publish(items(workspace.rows).map(raw => ({ ...object(raw), generation_id: workspace.generation_id }))); }, [result.data, context?.queue]);
  const rows = workspace ? items(workspace.rows).map(object) : items(data.items).map(raw => ({ item: object(raw), seed: true }));
  // Failed seed enrichment never hides accepted matching rows.
  for (const raw of items(data.items)) { const item = object(raw); if (!rows.some(row => object(row.item).number === item.number)) rows.push({ item, seed: true }); }
  const edges = items(workspace?.edges).map(object);
  const earlier = new Set([...context?.queue.pages ?? []].filter(([page]) => page < query.page!).flatMap(([, numbers]) => numbers));
  const hidden = new Set<string>();
  if (workspace?.state === "complete") {
    const pending = [...earlier].filter(number => rows.some(row => object(row.item).number === number));
    while (pending.length) { const number = pending.pop()!; if (hidden.has(number)) continue; hidden.add(number); for (const edge of edges) { if (edge.parent === number) pending.push(text(edge.child)); if (edge.child === number) pending.push(text(edge.parent)); } }
  }
  const ordered = orderWorkspace(rows, edges, seeds, workspace?.state === "complete").filter(({ row }) => !hidden.has(text(object(row.item).number)));
  return <section aria-label={copy("pr-cards.pageLabel", { page: query.page })}>
    <p className="pr-list-applied">{copy("pr-cards.applied", { state: copy(query.state === "closed" ? "pull-requests.closed_c21ead" : query.state === "all" ? "pull-requests.all_a52ace" : "pull-requests.open_ed077f"), page: query.page, pageSize: query.page_size })}{query.search ? ` · ${query.search}` : ""}</p>
    {data.total_count != null ? <p>{copy("pr-workspace.matches", { count: text(data.total_count) })}</p> : null}
    {data.incomplete ? <p role="status">{copy("github-items.githubReturnedIncompleteSearchResultsMissing_29a156")}</p> : null}{data.search_limit_reached ? <p role="status">{copy("github-items.githubSSearchLimitHasBeen_c17e40")}</p> : null}
    <p><Timestamp value={text(data.observed_at)} mode={TimestampMode.Exact} /></p>
    {previous ? <p>{copy("github-items.previousObservation_1bd8a6")}</p> : null}
    {!context?.supported ? <p>{copy("pr-workspace.stackUnsupported")}</p> : null}
    {result.data && !workspace ? <p role="alert">{copy("github-items.theGithubResultDoesNotMatch_3c7624")}</p> : null}
    {workspace && workspace.state !== "complete" ? <p role="status">{copy(workspace.state === "ambiguous" ? "pr-workspace.ambiguous" : "pr-workspace.incomplete")}</p> : null}
    <Problem error={result.error} />
    {context?.supported && (result.error || !workspace || workspace.state !== "complete") ? <button type="button" disabled={reading || context.busy || result.isFetching} onClick={() => void result.refetch()}>{copy("pr-workspace.retryContext")}</button> : null}
    {ordered.map(({ row, depth, stack }) => { const item = object(row.item), author = object(item.author); return <article className="pr-workspace-row" key={text(item.number)} style={{ marginInlineStart: depth * 12 }}>
      <button type="button" className="pr-workspace-select" disabled={reading || context?.busy} data-pr-number={text(item.number)} onClick={() => change({ kind: ItemKind.PullRequest, operation: QueryOperation.Detail, number: text(item.number) })}>
        <span><span>{copy(item.state === "closed" ? "pull-requests.closed_c21ead" : "pull-requests.open_ed077f")}</span>{item.draft ? <span> · <span>{copy("pr-cards.draft")}</span></span> : ""}{stack ? <span className="pr-stack-badge">{copy("pr-workspace.stack")}</span> : null}{row.seed ? null : <span>{copy("pr-workspace.stackContext")}</span>}</span>
        <span>#{text(item.number)} <strong role="heading" aria-level={3}>{text(item.title)}</strong></span>
        <span className="pr-workspace-meta"><PRAvatar reference={text(row.avatar_reference)} />{text(author.login) || copy("github-items.extra.a326f4758492")}{author.kind === "unknown" ? ` · ${copy("pr-cards.unverifiedAuthor")}` : ""} <PRCounts value={row.counts} /></span>
        <Timestamp value={text(item.updated_at)} mode={TimestampMode.Exact} />
      </button>
    </article>; })}
    {!ordered.length ? <p>{copy("pr-cards.emptyPage", { page: query.page })}</p> : null}
  </section>;
}
