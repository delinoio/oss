import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SessionQuery } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Comparison, readDiff } from "./session-diff-model";
import { workspaceReadOptions } from "./session-files";
import { Problem } from "./ui";
import { LocalReviews } from "./local-reviews";

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
    {result.data ? <LocalReviews sessionId={sessionId} diff={result.data} reading={result.isFetching || Boolean(result.error)} /> : null}
  </>;
}
