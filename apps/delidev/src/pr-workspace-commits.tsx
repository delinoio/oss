// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { invalidGitHubPage } from "./github-scroll";
import { sha, date } from "./github-query-model";
import { object, items, text, type Document } from "./documents";
import { changeCounts, scopedPRDocument, PRCounts, PRAvatar } from "./pr-workspace-page";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { copy } from "./localization";

export function validCommitPage(value: Document, item: Document): boolean {
  const observed = object(value.item);
  if (observed.id !== item.id || observed.number !== item.number || observed.base_sha !== item.base_sha || observed.head_sha !== item.head_sha || !Array.isArray(value.commits) || value.commits.length > 20 || typeof value.next_page_token !== "string" || text(value.next_page_token).length > 256) return false;
  const seen = new Set<string>();
  return value.commits.every(raw => { const commit = object(raw); if (!sha(commit.sha) || seen.has(text(commit.sha)) || typeof commit.message !== "string" || typeof commit.author !== "string" || typeof commit.committer !== "string" || !date(commit.authored_at) || !date(commit.committed_at) || !Array.isArray(commit.parents) || commit.parents.length > 100 || !commit.parents.every(sha) || commit.counts != null && !changeCounts(commit.counts)) return false; seen.add(text(commit.sha)); return true; });
}
function Commit({ value }: { value: Document }) {
  const [copied, setCopied] = useState(false);
  return <article className="pr-commit"><h3>{text(value.message).split("\n")[0]}</h3><p><PRAvatar reference={text(value.avatar_reference)} />{text(value.author)} · <Timestamp value={text(value.authored_at)} mode={TimestampMode.Exact} /> · <PRCounts value={value.counts} /></p><code>{text(value.sha).slice(0, 12)}</code> <button type="button" onClick={() => { void navigator.clipboard.writeText(text(value.sha)).then(() => setCopied(true)).catch(() => setCopied(false)); }}>{copy("pr-workspace.copySHA")}</button>{copied ? <span role="status">{copy("pr-workspace.copied")}</span> : null}<details><summary>{copy("pr-workspace.details")}</summary><pre>{text(value.message)}</pre><p>{text(value.sha)}</p><p>{text(value.committer)} · <Timestamp value={text(value.committed_at)} mode={TimestampMode.Exact} /></p><ul>{items(value.parents).map(parent => <li key={text(parent)}><code>{text(parent)}</code></li>)}</ul></details></article>;
}
export function PRCommits({ selected, item, active, visible, busy }: { selected: Resource; item: Document; active: boolean; visible: boolean; busy: (value: boolean) => void }) {
  const root = useRef<HTMLDivElement>(null), scope = JSON.stringify([selected.id, String(selected.revision), item.id, item.base_sha, item.head_sha]);
  const binding = useMemo(() => ({ generation: "" }), [scope]);
  const request = useCallback((pageToken: string) => ({ repositoryId: selected.id, expectedRevision: selected.revision, number: Number(item.number), baseSha: text(item.base_sha), headSha: text(item.head_sha), pageToken }), [selected, item]);
  const project = useCallback((response: { schemaVersion: number; documentJson: Uint8Array }) => {
    const value = response.schemaVersion === 1 ? scopedPRDocument(response.documentJson, selected) : undefined;
    if (!value || !validCommitPage(value, item) || binding.generation && binding.generation !== value.generation_id) return invalidGitHubPage();
    binding.generation = text(value.generation_id);
    return { rows: items(value.commits).map(raw => ({ id: text(object(raw).sha), revision: 1n })), payload: [value], nextPageToken: text(value.next_page_token) };
  }, [selected, item, binding]);
  const reader = useConnectPaginationReader(IntegrationQuery.listPullRequestCommits, request, project), chain = usePaginationChain(scope, active, reader, true, true);
  useEffect(() => { busy(Boolean(chain.loading)); return () => busy(false); }, [chain.loading, busy]);
  return <div ref={root}><button type="button" disabled={Boolean(chain.loading)} onClick={chain.reload}>{copy("pr-workspace.reloadCommits")}</button><ScrollPayloadWindow query={chain} root={root} active={active && visible}>{(pages, rows) => pages.flatMap(page => items(page.commits).map(object).filter(commit => rows.some(row => row.id === commit.sha)).map(commit => <Commit value={commit} key={text(commit.sha)} />))}</ScrollPayloadWindow><ScrollContinuation query={chain} root={root} active={active && visible} label={copy("pr-workspace.commits")} /></div>;
}
