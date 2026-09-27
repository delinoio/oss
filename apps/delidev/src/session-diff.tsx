import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SessionQuery } from "@delinoio/delidev-api-client";
import { encode, object, type Document } from "./documents";
import { workspaceReadOptions } from "./session-files";
import { Problem } from "./ui";

enum Comparison { WorkingTree = "working-tree", Staged = "staged", Creation = "creation" }
type Diff = { comparison: Comparison; repository_id: string; path: string; base: "commit" | "empty-tree"; base_object: string; head_commit?: string; patch: string; untracked: string[]; revision: string };
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const oid = (v: unknown): v is string => typeof v === "string" && /^([0-9a-f]{40}|[0-9a-f]{64})$/.test(v);
function afterUTF8(value: string, previous: string): boolean {
  // Go sorts path bytes; JavaScript's UTF-16 order differs for supplementary
  // characters. Compare the wire encoding so valid filenames stay visible.
  const a = new TextEncoder().encode(value), b = new TextEncoder().encode(previous);
  for (let i = 0; i < Math.min(a.length, b.length); i++) if (a[i] !== b[i]) return a[i] > b[i];
  return a.length > b.length;
}

function readDiff(raw: Uint8Array, repository: string, comparison: Comparison, path: string): Diff {
  const invalid = () => { throw new Error("The Git diff observation is malformed. Refresh the comparison."); };
  if (raw.byteLength > 512 * 1024) return invalid();
  let value: Document;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const v = object(value.diff);
  if (!exact(value, ["size", "binary", "truncated", "diff"]) || value.size !== "0" || value.binary !== false || value.truncated !== false || !exact(v, ["comparison", "repository_id", "path", "base", "base_object", "patch", "untracked", "revision", ...(v.head_commit !== undefined ? ["head_commit"] : [])]) || v.repository_id !== repository || v.comparison !== comparison || v.path !== path || !oid(v.base_object) || typeof v.revision !== "string" || !/^[0-9a-f]{64}$/.test(v.revision) || typeof v.patch !== "string" || v.patch.includes("\0") || /[\uD800-\uDFFF]/u.test(v.patch) || new TextEncoder().encode(v.patch).length > 65536 || !Array.isArray(v.untracked) || v.untracked.length > 100) return invalid();
  if (v.base === "commit" ? !oid(v.head_commit) || v.head_commit.length !== v.base_object.length || (comparison !== Comparison.Creation && v.head_commit !== v.base_object) : v.base !== "empty-tree" || v.head_commit !== undefined || comparison === Comparison.Creation) return invalid();
  let last = "", size = 0;
  for (const file of v.untracked) {
    if (typeof file !== "string" || !file || file.startsWith("/") || file.split("/").some((part) => !part || part === "." || part === "..") || /[\u0000-\u001F\\\uD800-\uDFFF]/u.test(file) || !afterUTF8(file, last)) return invalid();
    size += new TextEncoder().encode(file).length; last = file;
    if (size > 16384) return invalid();
  }
  return v as Diff;
}

export function SessionDiff({ sessionId, worktree, close }: { sessionId: string; worktree: boolean; close: () => void }) {
  const heading = useRef<HTMLHeadingElement>(null);
  const roots = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: "roots" }) }, workspaceReadOptions);
  const [repository, setRepository] = useState<string>();
  const available = roots.data?.roots.filter((root) => root.repository_id);
  const selected = repository ?? available?.find((root) => root.primary)?.repository_id ?? available?.[0]?.repository_id;
  useEffect(() => { heading.current?.focus(); }, []);
  return <aside className="session-files" aria-label="Session Git diff" onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
    <header><h2 ref={heading} tabIndex={-1}>Git diff</h2><button onClick={close} aria-label="Close session diff">Close</button></header>
    <p>Git comparison on this session's execution machine.</p>
    <Problem error={roots.error} />
    {roots.isPending ? <p role="status">Loading workspace roots…</p> : null}
    {roots.error ? <button disabled={roots.isFetching} onClick={() => void roots.refetch()}>Retry workspace roots</button> : null}
    {available?.length ? <label>Diff repository<select value={selected} onChange={(event) => setRepository(event.target.value)}>{available.map((root) => <option key={root.repository_id} value={root.repository_id}>{root.name}{root.primary ? " · Primary" : ""}</option>)}</select></label> : roots.data ? <p>This workspace has no prepared Git repository.</p> : null}
    {selected ? <RepositoryDiff key={selected} sessionId={sessionId} repository={selected} worktree={worktree} /> : null}
  </aside>;
}

function RepositoryDiff({ sessionId, repository, worktree }: { sessionId: string; repository: string; worktree: boolean }) {
  const [comparison, setComparison] = useState(worktree ? Comparison.Creation : Comparison.WorkingTree);
  const [path, setPath] = useState("."), [pathDraft, setPathDraft] = useState(".");
  const result = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: "git-diff", repository_id: repository, comparison, path }) }, { ...workspaceReadOptions, select: (response) => readDiff(response.documentJson, repository, comparison, path) });
  return <>
    <label>Comparison<select value={comparison} onChange={(event) => setComparison(event.target.value as Comparison)}>
      <option value={Comparison.WorkingTree}>Working tree against current HEAD</option><option value={Comparison.Staged}>Staged changes against current HEAD</option>{worktree ? <option value={Comparison.Creation}>Working tree against creation commit</option> : null}
    </select></label>
    <form onSubmit={(event) => { event.preventDefault(); setPath(pathDraft); }}><label>Relative diff path<input value={pathDraft} onChange={(event) => setPathDraft(event.target.value)} autoComplete="off" spellCheck={false} /></label><button>Compare path</button></form>
    <button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh diff</button>
    <p>Untracked files are listed separately. Submodules show Git commit changes only. This is a live observation, not an atomic snapshot.</p>
    <Problem error={result.error} />
    {result.isFetching ? <p role="status">Reading Git diff…</p> : null}
    {result.error && result.data ? <p role="alert">Refresh failed. The previous comparison is shown below.</p> : null}
    {result.data ? <section className="git-comparison" aria-label="Git comparison">
      <dl><dt>Base</dt><dd>{result.data.base === "empty-tree" ? "Empty Git tree (unborn branch)" : result.data.base_object}</dd><dt>Observed HEAD</dt><dd>{result.data.head_commit ?? "No commit yet"}</dd><dt>Diff revision</dt><dd>{result.data.revision}</dd></dl>
      {result.data.patch ? <pre tabIndex={0}>{result.data.patch}</pre> : <p>No tracked changes in this comparison.</p>}
      <h3>Untracked files</h3>{result.data.untracked.length ? <ul>{result.data.untracked.map((file) => <li key={file}>{file}</li>)}</ul> : <p>No untracked files in this path.</p>}
    </section> : null}
  </>;
}
