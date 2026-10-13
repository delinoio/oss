import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { copy, useLocale } from "./localization";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  createQueryOptions,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  SessionQuery,
  clientFailure,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  Comparison,
  readDiff,
  readDiffOptions,
  referenceKey,
  referenceLabel,
  sameReference,
  type BaseReference,
  type Diff,
} from "./session-diff-model";
import { readReviewContext, type ReviewFile } from "./local-review-model";
import { workspaceReadOptions } from "./session-files";
import { Problem } from "./ui";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalReviewRecovery, LocalReviews } from "./local-reviews";
import { useRetainedMutationIntents } from "./mutation";
import "./session-diff.css";

export type ComparisonDescriptor = {
  repository: string;
  comparison: Comparison;
  path: string;
  base_ref?: BaseReference;
};

function comparisonOriginKey(value: ComparisonDescriptor) {
  return JSON.stringify([
    value.repository,
    value.comparison,
    value.path,
    value.base_ref?.type,
    value.base_ref?.name,
    value.base_ref?.remote,
  ]);
}

export function SessionDiff({
  sessionId,
  worktree,
  close,
  selected,
  openComparison,
}: {
  sessionId: string;
  worktree: boolean;
  close: () => void;
  selected?: ComparisonDescriptor;
  openComparison?: (value: ComparisonDescriptor) => void;
}) {
  useLocale();
  const panelRoot = useRef<HTMLElement>(null);
  const shortcuts = useShortcuts([
    {
      id: ShortcutId.DiffClose,
      scope: Surface.Sessions,
      label: "shortcuts.closeDiff",
      bindings: [{ key: "Escape" }],
      target: panelRoot,
      input: ShortcutInput.Target,
      run: close,
    },
  ]);
  const roots = useQuery(
    SessionQuery.readSessionWorkspace,
    { sessionId, queryJson: encode({ operation: "roots" }) },
    workspaceReadOptions,
  );
  const [repository, setRepository] = useState<string>();
  // A repository selection stays pending until that repository's own branch
  // base is negotiated, so no base-less branch tab is ever opened. It belongs
  // to the descriptor that was selected when the repository changed and stops
  // applying as soon as another tab (including the one it opens) is selected.
  const [pendingSelection, setPendingSelection] = useState<{
    repository: string;
    origin: string;
  }>();
  const [acceptedDeletionId, setAcceptedDeletionId] = useState<string>();
  const available = roots.data?.roots.filter((root) => root.repository_id);
  const origin = selected ? comparisonOriginKey(selected) : "";
  const pending =
    openComparison && pendingSelection?.origin === origin
      ? pendingSelection
      : undefined;
  const pendingRef = useRef(pending);
  pendingRef.current = pending;
  const selectedRepository =
    pending?.repository ??
    selected?.repository ??
    repository ??
    available?.find((root) => root.primary)?.repository_id ??
    available?.[0]?.repository_id;
  const chooseRepository = (value: string) => {
    setRepository(value);
    if (!openComparison) return;
    // Re-selecting the repository that owns the selected tab keeps that tab.
    setPendingSelection(
      value === selected?.repository
        ? undefined
        : { repository: value, origin },
    );
  };
  // Only the still-pending selection of the current owner may open its tab.
  const openNegotiated = (value: ComparisonDescriptor) => {
    const current = pendingRef.current;
    if (!openComparison || !current || current.repository !== value.repository)
      return;
    // The selection is complete once its descriptor is accepted. Clear it so
    // reselecting the originating tab later cannot revive a stale repository.
    pendingRef.current = undefined;
    setPendingSelection(undefined);
    openComparison(value);
  };
  return (
    <aside
      ref={panelRoot}
      aria-keyshortcuts={shortcuts.aria(ShortcutId.DiffClose)}
      className="session-files session-diff"
      aria-label={copy("session-diff.sessionGitDiff_d6706d")}
      onKeyDown={shortcuts.onKeyDown}
    >
      <Problem error={roots.error} />
      {roots.isPending ? (
        <p role="status">{copy("session-diff.loadingWorkspaceRoots_0d8c0f")}</p>
      ) : null}
      {roots.error ? (
        <button
          disabled={roots.isFetching}
          onClick={() => void roots.refetch()}
        >
          {copy("session-diff.retryWorkspaceRoots_6b5165")}
        </button>
      ) : null}
      {selectedRepository ? (
        <RepositoryDiff
          key={selectedRepository}
          sessionId={sessionId}
          repository={selectedRepository}
          initial={
            pending
              ? { comparison: Comparison.Branch, path: selected?.path ?? "." }
              : selected
          }
          openComparison={openComparison}
          onNegotiated={pending ? openNegotiated : undefined}
          worktree={worktree}
          acceptedDeletionId={acceptedDeletionId}
          close={close}
          repositoryControl={
            <label className="diff-select">
              <span className="diff-control-label">
                {copy("session-diff.diffRepository_12d492")}
              </span>
              <select
                aria-label={copy("session-diff.diffRepository_12d492")}
                value={selectedRepository}
                onChange={(event) => chooseRepository(event.target.value)}
              >
                {available?.map((root) => (
                  <option key={root.repository_id} value={root.repository_id}>
                    {root.name}
                    {root.primary ? copy("session-diff.primary_b88564") : ""}
                  </option>
                ))}
              </select>
            </label>
          }
        />
      ) : (
        <>
          <h2>{copy("session-diff.gitDiff_fa5e4e")}</h2>
          {roots.data ? (
            <p>{copy("session-diff.thisWorkspaceHasNoPreparedGit_9235fc")}</p>
          ) : null}
          <button onClick={close}>{copy("session-diff.close_7d9eb7")}</button>
        </>
      )}
      <LocalReviewRecovery
        sessionId={sessionId}
        onAccepted={setAcceptedDeletionId}
      />
    </aside>
  );
}

export function RepositoryDiff({
  sessionId,
  repository,
  worktree,
  acceptedDeletionId,
  initial,
  openComparison,
  onNegotiated,
  close,
  repositoryControl,
}: {
  sessionId: string;
  repository: string;
  worktree: boolean;
  acceptedDeletionId?: string;
  initial?: Omit<ComparisonDescriptor, "repository">;
  openComparison?: (value: ComparisonDescriptor) => void;
  /**
   * Set while a repository selection awaits its own negotiated base. Called at
   * most once per base with the exact typed descriptor of the displayed branch
   * request, and never while negotiation is pending, failed, unsupported or
   * without an available base.
   */
  onNegotiated?: (value: ComparisonDescriptor) => void;
  close?: () => void;
  repositoryControl?: ReactNode;
}) {
  useLocale();
  const heading = useRef<HTMLHeadingElement>(null);
  const [comparison, setComparison] = useState(
    initial?.comparison ?? Comparison.Branch,
  );
  const [path, setPath] = useState(initial?.path ?? "."),
    [pathDraft, setPathDraft] = useState(initial?.path ?? ".");
  const [baseRef, setBaseRef] = useState(initial?.base_ref);
  const [menu, setMenu] = useState(false);
  const menuTrigger = useRef<HTMLButtonElement>(null),
    pathInput = useRef<HTMLInputElement>(null);
  const optionsInput = {
    sessionId,
    queryJson: encode({
      operation: "git-diff-options",
      repository_id: repository,
      path: ".",
    }),
  };
  const options = useQuery(SessionQuery.readSessionWorkspace, optionsInput, {
    ...workspaceReadOptions,
    enabled: comparison === Comparison.Branch,
    select: (response) => readDiffOptions(response.documentJson, repository),
  });
  useEffect(() => {
    heading.current?.focus({ preventScroll: true });
  }, []);
  useEffect(() => {
    if (menu) pathInput.current?.focus();
  }, [menu]);
  useEffect(() => {
    if (initial) {
      setComparison(initial.comparison);
      setPath(initial.path);
      setPathDraft(initial.path);
      setBaseRef(initial.base_ref);
    }
  }, [
    initial?.comparison,
    initial?.path,
    initial?.base_ref?.type,
    initial?.base_ref?.name,
    initial?.base_ref?.remote,
  ]);
  const chosenBase = baseRef ?? options.data?.default;
  const supportedBase = Boolean(
    options.data &&
    chosenBase &&
    options.data.choices.some(
      (choice) =>
        choice.available && sameReference(choice.reference, chosenBase),
    ),
  );
  const enabled = comparison !== Comparison.Branch || supportedBase;
  const announced = useRef<string | undefined>(undefined);
  const negotiatedKey =
    onNegotiated &&
    comparison === Comparison.Branch &&
    supportedBase &&
    chosenBase
      ? [repository, path, referenceKey(chosenBase)].join("\u0000")
      : undefined;
  useEffect(() => {
    if (!negotiatedKey || !onNegotiated || !chosenBase) return;
    if (announced.current === negotiatedKey) return;
    announced.current = negotiatedKey;
    onNegotiated({
      repository,
      comparison: Comparison.Branch,
      path,
      base_ref: chosenBase,
    });
  }, [negotiatedKey]);
  // A failed explicit options refresh keeps the previous inventory (and any
  // diff derived from it) only as a stale observation.
  // The explicit refresh reads through a disposable query, so its failure is
  // recorded here, bound to the session and repository that issued it.
  const optionsOwner = [sessionId, repository].join("\u0000");
  const [refreshFailedOwner, setRefreshFailedOwner] = useState<
    string | undefined
  >();
  const optionsStale =
    comparison === Comparison.Branch &&
    Boolean(options.data) &&
    (options.isError || refreshFailedOwner === optionsOwner);
  const [refreshing, setRefreshing] = useState(false);
  const scope = [
    sessionId,
    repository,
    comparison,
    path,
    chosenBase ? referenceKey(chosenBase) : "",
  ].join("\u0000");
  // Every distinct scope receives a monotonically increasing generation, so a
  // request issued under one scope can never be revived by returning to an
  // equal scope later (ABA).
  const scopeGeneration = useRef(0),
    observedScope = useRef(scope),
    mounted = useRef(true);
  if (observedScope.current !== scope) {
    observedScope.current = scope;
    scopeGeneration.current += 1;
  }
  const transport = useTransport(),
    queryClient = useQueryClient();
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const query = {
    operation: "git-diff",
    repository_id: repository,
    comparison,
    path,
    ...(comparison === Comparison.Branch && supportedBase
      ? { base_ref: chosenBase }
      : {}),
  };
  const result = useQuery(
    SessionQuery.readSessionWorkspace,
    { sessionId, queryJson: encode(query) },
    {
      ...workspaceReadOptions,
      enabled,
      select: (response) =>
        readDiff(
          response.documentJson,
          repository,
          comparison,
          path,
          comparison === Comparison.Branch ? chosenBase : undefined,
        ),
    },
  );
  // Branch Refresh reads a fresh reference inventory first, then observes the
  // diff only when the same scope still selects an available base.
  const refresh = async () => {
    if (comparison !== Comparison.Branch) {
      void result.refetch();
      return;
    }
    const token = scopeGeneration.current,
      wasEnabled = enabled,
      previousBase = chosenBase;
    setRefreshing(true);
    // The explicit read uses a refresh-scoped disposable query identity, so a
    // late response never reaches the accepted options inventory unless the
    // originating scope still owns it when the response arrives.
    const accepted = createQueryOptions(
      SessionQuery.readSessionWorkspace,
      optionsInput,
      { transport },
    );
    const disposableKey = [
      ...accepted.queryKey,
      { optionsRefresh: newRequestId() },
    ];
    try {
      let response: { documentJson: Uint8Array };
      try {
        response = await queryClient.fetchQuery({
          queryKey: disposableKey,
          retry: false,
          gcTime: 0,
          staleTime: 0,
          queryFn: (context) =>
            accepted.queryFn({ ...context, queryKey: accepted.queryKey }),
        });
      } finally {
        queryClient.removeQueries({ queryKey: disposableKey, exact: true });
      }
      if (!mounted.current || scopeGeneration.current !== token) {
        console.debug("delidev.diff.options_refresh_retired", {
          sessionId,
          repository,
          reason: mounted.current ? "scope-changed" : "disposed",
        });
        return;
      }
      const fresh = readDiffOptions(response.documentJson, repository);
      queryClient.setQueryData(accepted.queryKey, response);
      // Only an accepted in-scope inventory ends the stale observation.
      setRefreshFailedOwner(undefined);
      const nextBase = baseRef ?? fresh.default;
      const available =
        nextBase !== undefined &&
        fresh.choices.some(
          (choice) =>
            choice.available && sameReference(choice.reference, nextBase),
        );
      // A changed implicit default or a newly available base changes the
      // diff query key, so that query observes automatically.
      if (
        wasEnabled &&
        available &&
        previousBase !== undefined &&
        sameReference(previousBase, nextBase)
      )
        await result.refetch();
    } catch (error) {
      if (mounted.current && scopeGeneration.current === token) {
        console.warn("delidev.diff.options_refresh_failed", {
          sessionId,
          repository,
          operation: "git-diff-options",
          classification: clientFailure(error).code,
        });
        setRefreshFailedOwner(optionsOwner);
      }
    } finally {
      if (mounted.current) setRefreshing(false);
    }
  };
  const creation = useRetainedMutationIntents(`review:create:${sessionId}`),
    submission = useRetainedMutationIntents(`review:submit:${sessionId}`),
    edits = useRetainedMutationIntents(`review:edit:${sessionId}:`);
  const pendingReviews = creation.length + submission.length + edits.length > 0;
  const reviews = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    if (pendingReviews && reviews.current) reviews.current.open = true;
  }, [pendingReviews]);
  const choose = (next: Comparison, nextPath = path, nextBase = chosenBase) => {
    const value = {
      repository,
      comparison: next,
      path: nextPath,
      ...(next === Comparison.Branch && options.data && nextBase
        ? { base_ref: nextBase }
        : {}),
    };
    if (openComparison) openComparison(value);
    else {
      setComparison(next);
      setPath(nextPath);
      setBaseRef(nextBase);
    }
  };
  const failureMessage = (error: unknown) =>
    error instanceof ConnectError && error.code === Code.PermissionDenied
      ? copy("session-diff.permissionFailure")
      : error instanceof ConnectError && error.code === Code.ResourceExhausted
        ? copy("session-diff.oversizedResult")
        : error instanceof ConnectError && error.code === Code.Aborted
          ? copy("session-diff.changedComparison")
          : copy("session-diff.unavailableComparison");
  const closeMenu = () => {
    setMenu(false);
    menuTrigger.current?.focus();
  };
  return (
    <>
      <header className="diff-navbar">
        <h2 ref={heading} tabIndex={-1}>
          {copy("session-diff.gitDiff_fa5e4e")}
        </h2>
        {repositoryControl}
        <label className="diff-select">
          <span className="diff-control-label">
            {copy("session-diff.comparison_571527")}
          </span>
          <select
            aria-label={copy("session-diff.comparison_571527")}
            value={comparison}
            onChange={(event) => choose(event.target.value as Comparison)}
          >
            <option value={Comparison.Branch}>
              {copy("session-diff.branchChanges")}
            </option>
            <option value={Comparison.WorkingTree}>
              {copy("session-diff.workingTreeAgainstCurrentHead_f0fac9")}
            </option>
            <option value={Comparison.Staged}>
              {copy("session-diff.stagedChangesAgainstCurrentHead_9974f1")}
            </option>
            {worktree ? (
              <option value={Comparison.Creation}>
                {copy("session-diff.workingTreeAgainstCreationCommit_3102e8")}
              </option>
            ) : null}
          </select>
        </label>
        {comparison === Comparison.Branch ? (
          <label className="diff-select">
            <span className="diff-control-label">
              {copy("session-diff.base_7b4736")}
            </span>
            <select
              aria-label={copy("session-diff.base_7b4736")}
              disabled={!options.data}
              value={chosenBase ? referenceKey(chosenBase) : ""}
              onChange={(event) => {
                const choice = options.data?.choices.find(
                  (item) => referenceKey(item.reference) === event.target.value,
                );
                if (choice) choose(Comparison.Branch, path, choice.reference);
              }}
            >
              <option value="" disabled>
                {copy("session-diff.chooseBase")}
              </option>
              {options.data?.choices.map((choice) => (
                <option
                  key={referenceKey(choice.reference)}
                  value={referenceKey(choice.reference)}
                  disabled={!choice.available}
                >
                  {referenceLabel(choice.reference)}
                  {choice.configured ? copy("session-diff.configuredBase") : ""}
                  {!choice.available
                    ? copy("session-diff.unavailableBase")
                    : ""}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        <span className="diff-navbar-space" />
        <button
          type="button"
          className="diff-icon"
          disabled={refreshing || result.isFetching || options.isFetching}
          aria-label={copy("session-diff.refreshDiff_f700bc")}
          title={copy("session-diff.refreshDiff_f700bc")}
          onClick={() => void refresh()}
        >
          ↻
        </button>
        <button
          type="button"
          className="diff-icon"
          ref={menuTrigger}
          aria-label={copy("session-diff.moreOptions")}
          aria-expanded={menu}
          aria-haspopup="dialog"
          onClick={() => setMenu(!menu)}
        >
          ⋯
        </button>
      </header>
      {menu ? (
        <div
          className="diff-options"
          role="dialog"
          aria-label={copy("session-diff.moreOptions")}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              closeMenu();
            }
          }}
        >
          <form
            onSubmit={(event) => {
              event.preventDefault();
              choose(comparison, pathDraft);
              closeMenu();
            }}
          >
            <label>
              {copy("session-diff.relativeDiffPath_e67374")}
              <input
                ref={pathInput}
                value={pathDraft}
                onChange={(event) => setPathDraft(event.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </label>
            <button>{copy("session-diff.comparePath_31d172")}</button>
          </form>
          <button type="button" onClick={closeMenu}>
            {copy("session-diff.closeOptions")}
          </button>
          {close ? (
            <button type="button" onClick={close}>
              {copy("session-diff.closeSessionDiff_43130b")}
            </button>
          ) : null}
        </div>
      ) : null}
      {path !== "." ? (
        <p className="file-path">
          {copy("session-diff.relativeDiffPath_e67374")}: {path}
        </p>
      ) : null}
      {comparison === Comparison.Branch && options.isPending ? (
        <p role="status">{copy("session-diff.loadingOptions")}</p>
      ) : null}
      {comparison === Comparison.Branch && options.error ? (
        <>
          <Problem error={options.error} />
          <p role="alert">
            {options.error instanceof ConnectError &&
            [Code.InvalidArgument, Code.Unimplemented].includes(
              options.error.code,
            )
              ? copy("session-diff.optionsUnavailable")
              : failureMessage(options.error)}
          </p>
        </>
      ) : null}
      {comparison === Comparison.Branch && options.data && !supportedBase ? (
        <p role="status">{copy("session-diff.missingBase")}</p>
      ) : null}
      <Problem error={result.error} />
      {result.error && !result.data ? (
        <p role="status">{failureMessage(result.error)}</p>
      ) : null}
      {enabled && result.isFetching ? (
        <p role="status">{copy("session-diff.readingGitDiff_13bd34")}</p>
      ) : null}
      {(result.error || optionsStale) && result.data && enabled ? (
        <p role="alert">
          {copy("session-diff.refreshFailedThePreviousComparisonIs_eef4e1")}
        </p>
      ) : null}
      {enabled && result.data ? (
        <section
          className="git-comparison"
          aria-label={copy("session-diff.gitComparison_de5514")}
        >
          {result.data.patch ? (
            <StructuredDiff
              key={result.data.revision}
              sessionId={sessionId}
              diff={result.data}
            />
          ) : (
            <p>
              {copy("session-diff.noTrackedChangesInThisComparison_3c7169")}
            </p>
          )}
          <Disclosure>
            <DisclosureSummary>
              {copy("session-diff.observationDetails")}
            </DisclosureSummary>
            <p>
              {copy(
                "session-diff.untrackedFilesAreListedSeparatelySubmodules_49f75e",
              )}
            </p>
            <dl>
              <dt>{copy("session-diff.base_7b4736")}</dt>
              <dd>
                {result.data.base === "empty-tree"
                  ? copy("session-diff.emptyGitTreeUnbornBranch_82b1a5")
                  : result.data.base_object}
              </dd>
              {result.data.base_ref ? (
                <>
                  <dt>{copy("session-diff.selectedBase")}</dt>
                  <dd>
                    {referenceLabel(result.data.base_ref)} ·{" "}
                    {result.data.base_commit}
                  </dd>
                  <dt>{copy("session-diff.mergeBase")}</dt>
                  <dd>{result.data.merge_base}</dd>
                </>
              ) : null}
              <dt>{copy("session-diff.observedHead_075434")}</dt>
              <dd>
                {result.data.head_commit ??
                  copy("session-diff.noCommitYet_c0724a")}
              </dd>
              <dt>{copy("session-diff.diffRevision_5eddeb")}</dt>
              <dd>{result.data.revision}</dd>
            </dl>
          </Disclosure>
          <Disclosure>
            <DisclosureSummary>
              {copy("session-diff.untrackedFiles_be1f1a")} (
              {result.data.untracked.length})
            </DisclosureSummary>
            {result.data.untracked.length ? (
              <ul>
                {result.data.untracked.map((file) => (
                  <li key={file}>{file}</li>
                ))}
              </ul>
            ) : (
              <p>{copy("session-diff.noUntrackedFilesInThisPath_bd7a62")}</p>
            )}
          </Disclosure>
        </section>
      ) : null}
      {result.data ? (
        <Disclosure
          ref={reviews}
          onToggle={(event) => {
            if (pendingReviews && !event.currentTarget.open)
              event.currentTarget.open = true;
          }}
        >
          <DisclosureSummary>
            {copy("session-diff.localReviews")}
            {pendingReviews ? ` · ${copy("session-diff.pendingReviews")}` : ""}
          </DisclosureSummary>
          <LocalReviews
            sessionId={sessionId}
            diff={result.data}
            reading={
              refreshing ||
              result.isFetching ||
              Boolean(result.error) ||
              optionsStale ||
              !enabled
            }
            acceptedDeletionId={acceptedDeletionId}
          />
        </Disclosure>
      ) : null}
    </>
  );
}

// Mode headers and missing-final-newline markers are not part of the line table.
// Fall back to the exact patch when the table would silently omit either fact.
const MODE_CHANGE_HEADER = /^(?:old|new) mode /m;
function hasUnrepresentedPatchFacts(
  patch: string,
  files: ReviewFile[],
): boolean {
  return (
    MODE_CHANGE_HEADER.test(patch) ||
    files.some((file) => file.lines.some((line) => !line.newline))
  );
}

function StructuredDiff({
  sessionId,
  diff,
}: {
  sessionId: string;
  diff: Diff;
}) {
  const context = useQuery(
    SessionQuery.readSessionReviewContext,
    {
      sessionId,
      queryJson: encode({
        operation: "git-diff",
        repository_id: diff.repository_id,
        comparison: diff.comparison,
        path: diff.path,
        ...(diff.base_ref ? { base_ref: diff.base_ref } : {}),
      }),
    },
    {
      ...workspaceReadOptions,
      refetchOnMount: "always",
      select: (response) => readReviewContext(response.documentJson, diff),
    },
  );
  if (
    !context.data ||
    context.error ||
    context.data.files.some((file) => file.kind === "non-line") ||
    hasUnrepresentedPatchFacts(diff.patch, context.data.files)
  )
    return <pre tabIndex={0}>{diff.patch}</pre>;
  return (
    <div className="structured-diff" tabIndex={0}>
      {context.data.files.map((file) => (
        <section key={file.path}>
          <h3>{file.path}</h3>
          {file.kind === "non-line" ? (
            <pre>
              {diff.patch
                .split("diff --git ")
                .filter((part) => part.includes(`b/${file.path}`))
                .map((part) => `diff --git ${part}`)
                .join("") || diff.patch}
            </pre>
          ) : (
            <table aria-label={file.path}>
              <thead className="diff-control-label">
                <tr>
                  <th>{copy("session-diff.oldLine")}</th>
                  <th>{copy("session-diff.newLine")}</th>
                  <th>{copy("session-diff.content")}</th>
                </tr>
              </thead>
              <tbody>
                {file.lines.map((line, index) => (
                  <tr
                    key={index}
                    className={
                      line.old === undefined
                        ? "diff-added"
                        : line.new === undefined
                          ? "diff-removed"
                          : ""
                    }
                  >
                    <td className="diff-line-number">{line.old ?? ""}</td>
                    <td className="diff-line-number">{line.new ?? ""}</td>
                    <td>
                      <span aria-hidden="true">
                        {line.old === undefined
                          ? "+"
                          : line.new === undefined
                            ? "−"
                            : " "}{" "}
                      </span>
                      {line.text}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      ))}
    </div>
  );
}
