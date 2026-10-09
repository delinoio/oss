import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { copy, useLocale } from "./localization";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SessionQuery } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Comparison, readDiff } from "./session-diff-model";
import { workspaceReadOptions } from "./session-files";
import { Problem } from "./ui";
import { LocalReviewRecovery, LocalReviews } from "./local-reviews";

export function SessionDiff({ sessionId, worktree, close }: { sessionId: string; worktree: boolean; close: () => void }) {
  useLocale();
  const panelRoot = useRef<HTMLElement>(null);
  const shortcuts = useShortcuts([{ id: ShortcutId.DiffClose, scope: Surface.Sessions, label: "shortcuts.closeDiff", bindings: [{ key: "Escape" }], target: panelRoot, input: ShortcutInput.Target, run: close }]);
  const heading = useRef<HTMLHeadingElement>(null);
  const roots = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: "roots" }) }, workspaceReadOptions);
  const [repository, setRepository] = useState<string>();
  const [acceptedDeletionId, setAcceptedDeletionId] = useState<string>();
  const available = roots.data?.roots.filter((root) => root.repository_id);
  const selected = repository ?? available?.find((root) => root.primary)?.repository_id ?? available?.[0]?.repository_id;
  useEffect(() => { heading.current?.focus(); }, []);
  return <aside ref={panelRoot} aria-keyshortcuts={shortcuts.aria(ShortcutId.DiffClose)} className="session-files" aria-label={copy("session-diff.sessionGitDiff_d6706d")} onKeyDown={shortcuts.onKeyDown}>
    <header><h2 ref={heading} tabIndex={-1}>{copy("session-diff.gitDiff_fa5e4e")}</h2><button onClick={close} aria-keyshortcuts={shortcuts.aria(ShortcutId.DiffClose)} aria-label={copy("session-diff.closeSessionDiff_43130b")}>{copy("session-diff.close_7d9eb7")}</button></header>
    <p>{copy("session-diff.gitComparisonOnThisSessionS_a10284")}</p>
    <Problem error={roots.error} />
    {roots.isPending ? <p role="status">{copy("session-diff.loadingWorkspaceRoots_0d8c0f")}</p> : null}
    {roots.error ? <button disabled={roots.isFetching} onClick={() => void roots.refetch()}>{copy("session-diff.retryWorkspaceRoots_6b5165")}</button> : null}
    {available?.length ? <label>{copy("session-diff.diffRepository_12d492")}<select value={selected} onChange={(event) => setRepository(event.target.value)}>{available.map((root) => <option key={root.repository_id} value={root.repository_id}>{root.name}{root.primary ? copy("session-diff.primary_b88564") : ""}</option>)}</select></label> : roots.data ? <p>{copy("session-diff.thisWorkspaceHasNoPreparedGit_9235fc")}</p> : null}
    <LocalReviewRecovery sessionId={sessionId} onAccepted={setAcceptedDeletionId} />
    {selected ? <RepositoryDiff key={selected} sessionId={sessionId} repository={selected} worktree={worktree} acceptedDeletionId={acceptedDeletionId} /> : null}
  </aside>;
}

function RepositoryDiff({ sessionId, repository, worktree, acceptedDeletionId }: { sessionId: string; repository: string; worktree: boolean; acceptedDeletionId?: string }) {
  useLocale();
  const [comparison, setComparison] = useState(worktree ? Comparison.Creation : Comparison.WorkingTree);
  const [path, setPath] = useState("."), [pathDraft, setPathDraft] = useState(".");
  const result = useQuery(SessionQuery.readSessionWorkspace, { sessionId, queryJson: encode({ operation: "git-diff", repository_id: repository, comparison, path }) }, { ...workspaceReadOptions, select: (response) => readDiff(response.documentJson, repository, comparison, path) });
  return <>
    <label>{copy("session-diff.comparison_571527")}<select value={comparison} onChange={(event) => setComparison(event.target.value as Comparison)}>
      <option value={Comparison.WorkingTree}>{copy("session-diff.workingTreeAgainstCurrentHead_f0fac9")}</option><option value={Comparison.Staged}>{copy("session-diff.stagedChangesAgainstCurrentHead_9974f1")}</option>{worktree ? <option value={Comparison.Creation}>{copy("session-diff.workingTreeAgainstCreationCommit_3102e8")}</option> : null}
    </select></label>
    <form onSubmit={(event) => { event.preventDefault(); setPath(pathDraft); }}><label>{copy("session-diff.relativeDiffPath_e67374")}<input value={pathDraft} onChange={(event) => setPathDraft(event.target.value)} autoComplete="off" spellCheck={false} /></label><button>{copy("session-diff.comparePath_31d172")}</button></form>
    <button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("session-diff.refreshDiff_f700bc")}</button>
    <p>{copy("session-diff.untrackedFilesAreListedSeparatelySubmodules_49f75e")}</p>
    <Problem error={result.error} />
    {result.isFetching ? <p role="status">{copy("session-diff.readingGitDiff_13bd34")}</p> : null}
    {result.error && result.data ? <p role="alert">{copy("session-diff.refreshFailedThePreviousComparisonIs_eef4e1")}</p> : null}
    {result.data ? <section className="git-comparison" aria-label={copy("session-diff.gitComparison_de5514")}>
      <dl><dt>{copy("session-diff.base_7b4736")}</dt><dd>{result.data.base === "empty-tree" ? copy("session-diff.emptyGitTreeUnbornBranch_82b1a5") : result.data.base_object}</dd><dt>{copy("session-diff.observedHead_075434")}</dt><dd>{result.data.head_commit ?? copy("session-diff.noCommitYet_c0724a")}</dd><dt>{copy("session-diff.diffRevision_5eddeb")}</dt><dd>{result.data.revision}</dd></dl>
      {result.data.patch ? <pre tabIndex={0}>{result.data.patch}</pre> : <p>{copy("session-diff.noTrackedChangesInThisComparison_3c7169")}</p>}
      <h3>{copy("session-diff.untrackedFiles_be1f1a")}</h3>{result.data.untracked.length ? <ul>{result.data.untracked.map((file) => <li key={file}>{file}</li>)}</ul> : <p>{copy("session-diff.noUntrackedFilesInThisPath_bd7a62")}</p>}
    </section> : null}
    {result.data ? <LocalReviews sessionId={sessionId} diff={result.data} reading={result.isFetching || Boolean(result.error)} acceptedDeletionId={acceptedDeletionId} /> : null}
  </>;
}
