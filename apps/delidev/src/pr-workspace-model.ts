// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { actorValid, bounded, date, positive, sha, uuid } from "./github-query-model";

export function changeCounts(raw: unknown): { additions: string; deletions: string } | undefined {
  const v = object(raw), valid = (n: unknown): n is string => typeof n === "string" && /^(0|[1-9][0-9]{0,19})$/.test(n) && BigInt(n) <= 18446744073709551615n;
  return valid(v.additions) && valid(v.deletions) ? { additions: v.additions, deletions: v.deletions } : undefined;
}
export function readScoped(raw: Uint8Array, selected: Resource): Document | undefined {
  if (raw.byteLength > 1 << 20) return;
  let v: Document; try { v = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return; }
  const sel = document(selected), repo = object(v.repository);
  if (v.repository_id !== selected.id || v.repository_revision !== selected.revision.toString() || v.profile_id !== sel.integration_id || !uuid(v.profile_id) || !uuid(v.generation_id) || !date(v.observed_at) || repo.provider !== "github.com" || !positive(repo.id) || !bounded(repo.node_id, 256) || text(repo.owner).toLowerCase() !== text(sel.github_owner).toLowerCase() || text(repo.name).toLowerCase() !== text(sel.github_name).toLowerCase()) return;
  return v;
}
export function workspaceResult(raw: Uint8Array, selected: Resource, seeds: string[], validateItem: (item: Document, scope: Document) => boolean): Document | undefined {
  const v = readScoped(raw, selected); if (!v || ["commits","page_token","next_page_token","diff","feedback","ci","reviewers","items","query"].some(key => v[key] != null) || !["complete", "incomplete", "ambiguous"].includes(text(v.state)) || !Array.isArray(v.seeds) || JSON.stringify(v.seeds) !== JSON.stringify(seeds) || !Array.isArray(v.nodes) || v.nodes.length > 100 || !Array.isArray(v.edges) || v.edges.length > 9900 || !Array.isArray(v.reasons) || v.reasons.some(r => !bounded(r, 64))) return;
  const seen = new Map<string, Document>();
  for (const raw of v.nodes) { const n = object(raw), item = object(n.item); if (!validateItem(item, v) || seen.has(text(item.number)) || n.seed !== seeds.includes(text(item.number)) || n.counts != null && !changeCounts(n.counts) || n.avatar_reference != null && !uuid(n.avatar_reference)) return; seen.set(text(item.number), item); }
  for (const raw of v.edges) { const e = object(raw), a = seen.get(text(e.parent)), b = seen.get(text(e.child)); if (!a || !b || a.number === b.number || a.head_ref !== b.base_ref || object(object(a.head_repository).repository).id !== object(v.repository).id) return; }
  return v;
}
export function commitResult(raw: Uint8Array, selected: Resource, item: Document, remote: Document, token: string): Document | undefined {
  const v = readScoped(raw, selected); if (!v || ["nodes","edges","seeds","state","reasons","items","query","diff","feedback"].some(key => v[key] != null) || object(v.repository).id !== remote.id || object(v.repository).node_id !== remote.node_id || v.pull_request_id !== item.id || v.number !== item.number || v.base_sha !== item.base_sha || v.head_sha !== item.head_sha || v.page_token !== token || !bounded(v.next_page_token, 2048, false) || token !== "" && v.next_page_token === token || !Array.isArray(v.commits) || v.commits.length > 20) return;
  const seen = new Set<string>();
  for (const raw of v.commits) { const c = object(raw); if (!sha(c.sha) || seen.has(c.sha) || !bounded(c.message, 128 << 10, false) || c.counts != null && !changeCounts(c.counts) || !Array.isArray(c.parents) || c.parents.length > 100 || c.parents.some(p => !sha(p))) return; seen.add(c.sha);
    for (const raw of [c.author, c.committer]) { const p = object(raw); if (!bounded(p.name, 4096, false) || !date(p.date) || p.actor != null && !actorValid(p.actor) || p.avatar_reference != null && !uuid(p.avatar_reference)) return; }
  }
  return v;
}
// Ambiguous graphs remain flat. Complete components preserve accepted seed order
// and numeric sibling ties; no title/author/number proximity invents an edge.
export function orderWorkspace(nodes: Document[], edges: Document[], seeds: string[], ambiguous: boolean): { node: Document; depth: number; stack: boolean; owner: string }[] {
  const byNumber = new Map(nodes.map(n => [text(object(n.item).number), n])), adjacent = new Map<string, Set<string>>(), children = new Map<string, string[]>(), parent = new Map<string, string>();
  let invalid = ambiguous;
  for (const e of edges) { const a = text(e.parent), b = text(e.child); if (!byNumber.has(a) || !byNumber.has(b)) continue; if (parent.has(b) && parent.get(b) !== a) invalid = true; parent.set(b, a); children.set(a, [...new Set([...(children.get(a) ?? []), b])]); adjacent.set(a, new Set([...(adjacent.get(a) ?? []), b])); adjacent.set(b, new Set([...(adjacent.get(b) ?? []), a])); }
  const numeric = (a: string, b: string) => BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0;
  const output: { node: Document; depth: number; stack: boolean; owner: string }[] = [], seen = new Set<string>();
  for (const seed of seeds) { if (seen.has(seed) || !byNumber.has(seed)) continue; const component = new Set<string>(), queue = [seed]; while (queue.length) { const n = queue.shift()!; if (component.has(n)) continue; component.add(n); queue.push(...adjacent.get(n) ?? []); }
    const roots = [...component].filter(n => !parent.has(n)).sort(numeric); const ordered: { n: string; depth: number }[] = [], visiting = new Set<string>();
    const walk = (n: string, depth: number) => { if (visiting.has(n) || depth > 20) { invalid = true; return; } visiting.add(n); ordered.push({ n, depth }); for (const child of [...children.get(n) ?? []].sort(numeric)) walk(child, depth + 1); };
    if (!invalid) for (const root of roots) walk(root, 0); if (ordered.length !== component.size) invalid = true;
    for (const row of invalid ? [...component].sort(numeric).map(n => ({ n, depth: 0 })) : ordered) { if (!seen.has(row.n)) output.push({ node: byNumber.get(row.n)!, depth: row.depth, stack: component.size > 1 && !invalid, owner: seed }); seen.add(row.n); }
  }
  return output;
}
