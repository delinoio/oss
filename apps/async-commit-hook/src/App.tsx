import { useEffect, useMemo, useRef, useState } from "react";
import {
  QueryClient,
  QueryClientProvider,
  useQueryClient,
} from "@tanstack/react-query";
import {
  TransportProvider,
  useQuery,
  useInfiniteQuery,
  useMutation,
} from "@connectrpc/connect-query";
import {
  ExecutionState,
  LocalQuery,
  type Repository,
  type Run,
  type Failure,
} from "@delinoio/async-commit-hook-api-client";
import {
  readConnection,
  transportFor,
  describeError,
} from "./connection";

const stateNames: Record<number, string> = {
  [ExecutionState.QUEUED]: "Queued",
  [ExecutionState.PREPARING]: "Preparing",
  [ExecutionState.RUNNING]: "Running",
  [ExecutionState.COLLECTING]: "Collecting",
  [ExecutionState.PASSED]: "Passed",
  [ExecutionState.FAILED]: "Failed",
  [ExecutionState.BLOCKED]: "Blocked",
  [ExecutionState.CANCELLED]: "Cancelled",
  [ExecutionState.REPLACED]: "Replaced",
  [ExecutionState.INTERRUPTED]: "Interrupted",
  [ExecutionState.SKIPPED]: "Not applicable",
  [ExecutionState.EXPIRED]: "Evidence expired",
};
function active(state: ExecutionState) {
  return state >= ExecutionState.QUEUED && state <= ExecutionState.COLLECTING;
}
export function Status({ state }: { state: ExecutionState }) {
  return (
    <span className={`status state-${state}`}>
      <span aria-hidden="true">
        {state === ExecutionState.PASSED ? "✓" : active(state) ? "◷" : "·"}
      </span>{" "}
      {stateNames[state] || "Unknown"}
    </span>
  );
}
export function ErrorNotice({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  return (
    <div className="notice error" role="alert">
      <strong>Something needs attention</strong>
      <p>{describeError(error)}</p>
      {retry && <button onClick={retry}>Try again</button>}
    </div>
  );
}

export function App() {
  const [connection] = useState(readConnection);
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: false, refetchOnWindowFocus: true },
        },
      }),
  );
  const [transport] = useState(transportFor);
  return (
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <a
          className="skip-link"
          href="#main"
          onClick={(event) => {
            event.preventDefault();
            document.getElementById("main")?.focus();
          }}
        >
          Skip to content
        </a>
        <header className="topbar">
          <a href="/" className="brand" aria-label="ach home">
            <span className="brand-mark">a</span> ach{" "}
            <span className="brand-caption">committed checks</span>
          </a>
          <div className="header-right">
            <span className="local-tag">Local to your computer</span>
            <a href="https://ach.delino.io" target="_blank" rel="noreferrer">Documentation ↗</a>
          </div>
        </header>
        <Workspace initialRun={connection.run} />
      </TransportProvider>
    </QueryClientProvider>
  );
}

type Tab = "checks" | "changes" | "commits" | "inbox";
export function Workspace({
  initialRun,
}: {
  initialRun: string;
}) {
  const repos = useInfiniteQuery(LocalQuery.listRepositories, { cursor: "", limit: 50 }, {
    pageParamKey: "cursor",
    getNextPageParam: (page) => page.nextCursor || undefined,
  });
  const repositories = useMemo(() => {
    const merged = new Map<string, Repository>();
    for (const page of repos.data?.pages ?? []) {
      for (const repo of page.repositories) {
        const prior = merged.get(repo.id);
        const trees = new Map((prior?.worktrees ?? []).map((w) => [w.id, w]));
        for (const tree of repo.worktrees) trees.set(tree.id, tree);
        merged.set(repo.id, { ...repo, worktrees: [...trees.values()] });
      }
    }
    return [...merged.values()];
  }, [repos.data]);
  const version = useQuery(LocalQuery.getVersion, {});
  const [worktree, setWorktree] = useState("");
  const [branch, setBranch] = useState("");
  const [tab, setTab] = useState<Tab>("checks");
  const [run, setRun] = useState(initialRun);
  const [runCursor, setRunCursor] = useState("");
  useEffect(() => setRunCursor(""), [worktree, branch, tab]);
  const lastOpenedRun = useRef(initialRun);
  const replaceRunFragment = (id: string) => {
    const fragment = new URLSearchParams();
    if (id) fragment.set("run", id);
    history.replaceState(
      null,
      "",
      window.location.pathname +
        window.location.search +
        (fragment.toString() ? `#${fragment}` : ""),
    );
  };
  const selectRun = (id: string) => {
    lastOpenedRun.current = id;
    replaceRunFragment(id);
    setRun(id);
  };
  const clearRun = () => {
    replaceRunFragment("");
    setRun("");
  };
  // This shares RunDetail's query/cache entry, so a deep link can bind the page
  // identity without another fetch or a second polling/acknowledgement path.
  const execution = useQuery(LocalQuery.getRun, { runId: run }, { enabled: Boolean(run) });
  const openedRun = execution.data?.run?.id === run ? execution.data.run : undefined;
  const currentRepo = run
    ? repositories.find((r) => r.id === openedRun?.repositoryId && r.worktrees.some((w) => w.id === openedRun.worktreeId))
    : repositories.find((r) => r.worktrees.some((w) => w.id === worktree));
  const selectedTree = currentRepo?.worktrees.find((w) => w.id === (run ? openedRun?.worktreeId : worktree));
  // Suppress stale navigation identity until this execution's workspace is
  // loaded. Registry pagination stays explicit and evidence remains readable.
  const displayedWorktree = selectedTree?.id || "";
  const displayedBranch = displayedWorktree === worktree ? branch : selectedTree?.branchId || selectedTree?.branch || "";
  const branches = useInfiniteQuery(
    LocalQuery.listBranches,
    { worktreeId: displayedWorktree, cursor: "", limit: 50 },
    { enabled: Boolean(displayedWorktree), pageParamKey: "cursor", getNextPageParam: (page) => page.nextCursor || undefined },
  );
  const branchOptions = useMemo(() => {
    const merged = new Map<string, { value: string; name: string; id: string }>();
    for (const page of branches.data?.pages ?? []) {
      for (const item of page.branches) {
        const value = item.id || item.name;
        merged.set(value, { value, name: item.name, id: item.id });
      }
    }
    // Preserve the checkout identity before its branch page has loaded.
    if (displayedBranch && !merged.has(displayedBranch)) {
      const current = (selectedTree?.branchId || selectedTree?.branch) === displayedBranch;
      merged.set(displayedBranch, { value: displayedBranch, name: current ? selectedTree!.branch : displayedBranch, id: current ? selectedTree!.branchId : "" });
    }
    return [...merged.values()];
  }, [branches.data, displayedBranch, selectedTree]);
  const selectedBranch = branchOptions.find((item) => item.value === displayedBranch);
  const branchId = selectedBranch?.id || "";
  const branchName = branchId ? "" : selectedBranch?.name || displayedBranch;
  useEffect(() => {
    if (run) {
      if (selectedTree && selectedTree.id !== worktree) {
        setWorktree(selectedTree.id);
        setBranch(selectedTree.branchId || selectedTree.branch);
      }
      return;
    }
    if (!worktree && repositories[0]?.worktrees[0]) {
      const w = repositories[0].worktrees[0];
      setWorktree(w.id);
      setBranch(w.branchId || w.branch);
    }
  }, [run, selectedTree, worktree, repositories]);
  if (version.data && version.data.apiVersion !== 1)
    return (
      <main id="main">
        <div className="notice error" role="alert">
          Incompatible local API version. Install a matching ach version.
        </div>
      </main>
    );
  return (
    <div className="workspace">
      <aside className="sidebar" aria-label="Repositories">
        <div className="side-heading">
          WORKSPACES <span>{repositories.length || 0}</span>
        </div>
        {repos.isPending && <p role="status">Loading repositories…</p>}
        {repos.error && (
          <>
            <ErrorNotice
              error={repos.error}
              retry={() => {
                void (repos.isFetchNextPageError ? repos.fetchNextPage() : repos.refetch());
              }}
            />
          </>
        )}
        {repositories.map((repo) => (
          <section key={repo.id}>
            <h2>{repo.name}</h2>
            {repo.worktrees.map((w) => (
              <button
                key={w.id}
                className={displayedWorktree === w.id ? "worktree selected" : "worktree"}
                aria-current={displayedWorktree === w.id ? "true" : undefined}
                onClick={() => {
                  setWorktree(w.id);
                  setBranch(w.branchId || w.branch);
                  clearRun();
                }}
              >
                <span aria-hidden="true">⌘</span>
                <span>
                  {w.branch || "Detached HEAD"}
                  <small>{w.path}</small>
                  {!w.available && (
                    <small>Source unavailable · history retained</small>
                  )}
                </span>
              </button>
            ))}
          </section>
        ))}
        {(repos.hasNextPage || (repos.data?.pages.length ?? 0) > 1) && (
          <button
            aria-busy={repos.isFetchingNextPage}
            aria-disabled={repos.isFetchingNextPage || !repos.hasNextPage}
            onClick={() => { if (repos.hasNextPage && !repos.isFetchingNextPage) void repos.fetchNextPage(); }}
          >
            {repos.isFetchingNextPage ? "Loading workspaces…" : repos.hasNextPage ? "Load more workspaces" : "All workspaces loaded"}
          </button>
        )}
        <div className="sidebar-footer">
          <span className="dot" /> LOCAL HISTORY
          <p>
            Persistent results.
            <br />
            Explicit validation.
          </p>
          <code>ach inbox --repo .</code>
        </div>
      </aside>
      <main id="main" className="content" tabIndex={-1}>
        <div className="page-title">
          <div>
            <p className="eyebrow">
              {tab === "inbox" ? "YOUR REVIEW QUEUE" : "WORKSPACE OVERVIEW"}
            </p>
            <h1>{currentRepo?.name || (run ? "Execution workspace" : "Your workspaces")}</h1>
            <p className="muted">
              {selectedTree?.path || (run
                ? execution.isPending ? "Resolving execution workspace…" : "Workspace details are not loaded. Load more workspaces or select one to browse; retained execution evidence remains available."
                : "Register a repository with ach init to get started.")}
            </p>
          </div>
          {displayedWorktree && (
            <label className="branch-select">
              Branch
              <select
                value={displayedBranch}
                onChange={(e) => {
                  setBranch(e.target.value);
                  clearRun();
                }}
              >
                <option value="">Detached HEAD / current commit</option>
                {branchOptions.map((item) => (
                  <option value={item.value} key={item.value}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        {branches.error && <ErrorNotice error={branches.error} retry={() => { void (branches.isFetchNextPageError ? branches.fetchNextPage() : branches.refetch()); }} />}
        {(branches.hasNextPage || (branches.data?.pages.length ?? 0) > 1) && (
          <button aria-busy={branches.isFetchingNextPage} aria-disabled={branches.isFetchingNextPage || !branches.hasNextPage}
            onClick={() => { if (branches.hasNextPage && !branches.isFetchingNextPage) void branches.fetchNextPage(); }}>
            {branches.isFetchingNextPage ? "Loading branches…" : branches.hasNextPage ? "Load more branches" : "All branches loaded"}
          </button>
        )}
        <nav className="tabs" aria-label="Workspace views">
          {(["checks", "changes", "commits", "inbox"] as Tab[]).map((v) => (
            <button
              key={v}
              aria-current={tab === v ? "page" : undefined}
              onClick={() => {
                setTab(v);
                clearRun();
              }}
            >
              {v[0].toUpperCase() + v.slice(1)}
            </button>
          ))}
        </nav>
        {run ? (
          <RunDetail
            key={run}
            id={run}
            onBack={clearRun}
            onSelect={selectRun}
          />
        ) : tab === "checks" || tab === "inbox" ? (
          <RunList
            repository={currentRepo?.id || ""}
            worktree={worktree}
            branch={tab === "checks" ? branchName : ""}
            branchId={tab === "checks" ? branchId : ""}
            inbox={tab === "inbox"}
            onSelect={selectRun}
            focusRun={lastOpenedRun.current}
            cursor={runCursor}
            setCursor={setRunCursor}
          />
        ) : tab === "changes" ? (
          <Changes worktree={worktree} branch={branchName} branchId={branchId} />
        ) : (
          <Commits worktree={worktree} branch={branchName} branchId={branchId} />
        )}
      </main>
    </div>
  );
}

export function RunList({
  repository,
  worktree,
  branch,
  branchId = "",
  inbox,
  onSelect,
  focusRun,
  cursor,
  setCursor,
}: {
  repository: string;
  worktree: string;
  branch: string;
  branchId?: string;
  inbox: boolean;
  onSelect: (id: string) => void;
  focusRun: string;
  cursor: string;
  setCursor: (cursor: string) => void;
}) {
  const list = useRef<HTMLElement>(null);
  const restoredFocus = useRef(false);
  const runs = useQuery(
    LocalQuery.listRuns,
    {
      repositoryId: repository,
      worktreeId: worktree,
      branch,
      branchId,
      detached: !inbox && worktree !== "" && branch === "" && branchId === "",
      inbox,
      cursor,
      limit: 50,
    },
    { enabled: Boolean(repository && worktree), refetchInterval: 2000 },
  );
  useEffect(() => {
    if (!runs.data || restoredFocus.current) return;
    restoredFocus.current = true;
    // Restore the row after detail unmounts, without stealing focus on polling.
    const row = Array.from(
      list.current?.querySelectorAll<HTMLButtonElement>("[data-run-id]") || [],
    ).find((element) => element.dataset.runId === focusRun);
    row?.focus();
  }, [runs.data, focusRun]);
  if (!repository || !worktree)
    return <p role="status" className="empty">Select a registered worktree to view its results.</p>;
  if (runs.isPending)
    return (
      <p role="status" className="empty">
        Loading local results…
      </p>
    );
  if (runs.error)
    return (
      <ErrorNotice
        error={runs.error}
        retry={() => {
          void runs.refetch();
        }}
      />
    );
  const items = runs.data?.runs || [];
  return (
    <section ref={list}>
      <div className="section-heading">
        <h2>{inbox ? "Waiting for your review" : "Committed executions"}</h2>
        <span>{items.length} on this page</span>
      </div>
      {items.length === 0 ? (
        <div className="empty">
          <span className="empty-symbol">✓</span>
          <h3>
            {inbox ? "You're all caught up" : "Ready for your next commit"}
          </h3>
          <p>
            {inbox
              ? "Pending work and unacknowledged results appear here."
              : "Run ach run or commit in a registered worktree. A commit alone is not validation."}
          </p>
          <code>{inbox ? "ach inbox --repo ." : "ach run --repo ."}</code>
        </div>
      ) : (
        <div className="run-list">
          {items.map((r) => (
            <button
              className="run-row"
              key={r.id}
              data-run-id={r.id}
              onClick={() => onSelect(r.id)}
            >
              <Status state={r.state} />
              <span className="run-identity">
                <strong>{r.commit.slice(0, 12)}</strong>
                <small>
                  {r.branch || "Detached HEAD"} · {r.os}/{r.arch}
                </small>
              </span>
              <span className="run-meta">
                {r.checkCount ?? r.checks.length} checks
                <small>{r.acknowledgedAt ? "Reviewed" : "Needs review"}</small>
              </span>
              <time dateTime={r.createdAt}>
                {new Date(r.createdAt).toLocaleString()}
              </time>
              <span aria-hidden="true">→</span>
            </button>
          ))}
        </div>
      )}
      <div className="pagination">
        {cursor && (
          <button onClick={() => setCursor("")}>Newest results</button>
        )}
        {runs.data?.nextCursor && (
          <button onClick={() => setCursor(runs.data!.nextCursor)}>
            Older results →
          </button>
        )}
      </div>
    </section>
  );
}

export function RunDetail({
  id,
  onBack,
  onSelect,
}: {
  id: string;
  onBack: () => void;
  onSelect: (id: string) => void;
}) {
  const query = useQuery(
    LocalQuery.getRun,
    { runId: id },
    {
      refetchInterval: (query) =>
        active(query.state.data?.run?.state ?? ExecutionState.UNSPECIFIED)
          ? 1500
          : false,
    },
  );
  const cache = useQueryClient();
  const ack = useMutation(LocalQuery.acknowledge);
  const rerun = useMutation(LocalQuery.rerun);
  const cancel = useMutation(LocalQuery.cancel);
  const [confirm, setConfirm] = useState(false);
  const [check, setCheck] = useState("");
  const [previous, setPrevious] = useState("");
  const [comparison, setComparison] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const previousFocus = useRef(document.activeElement as HTMLElement | null);
  useEffect(() => {
    if (query.data) heading.current?.focus();
  }, [id, Boolean(query.data)]);
  useEffect(
    () => () => {
      previousFocus.current?.focus();
    },
    [],
  );
  if (query.isPending) return <p role="status">Loading execution…</p>;
  if (query.error)
    return (
      <ErrorNotice
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  const run = query.data?.run;
  if (!run)
    return (
      <p role="alert">
        Execution data is missing. Inspect ach status from your terminal.
      </p>
    );
  const mutationError = ack.error || rerun.error || cancel.error;
  return (
    <section className="run-detail">
      <button className="back" onClick={onBack}>
        ← All executions
      </button>
      <div className="run-heading">
        <div>
          <Status state={run.state} />
          <h2 tabIndex={-1} ref={heading}>
            Commit <code>{run.commit.slice(0, 12)}</code>
          </h2>
          <p className="muted">
            {run.os}/{run.arch} · {run.branch || "Detached HEAD"} ·{" "}
            {new Date(run.createdAt).toLocaleString()}
          </p>
        </div>
        <div className="actions">
          <button disabled={query.isFetching} onClick={() => { void query.refetch(); }}>
            Refresh execution
          </button>
          <button
            disabled={active(run.state) || run.state === ExecutionState.UNSPECIFIED || Boolean(run.acknowledgedAt) || ack.isPending}
            onClick={async () => {
              try {
                await ack.mutateAsync({ runId: id });
                await cache.invalidateQueries();
              } catch {}
            }}
          >
            {run.acknowledgedAt ? "✓ Acknowledged" : "Acknowledge"}
          </button>
          {active(run.state) ? (
            <button className="danger" onClick={() => setConfirm(true)}>
              Cancel execution
            </button>
          ) : (
            <>
              <button
                disabled={
                  run.state === ExecutionState.EXPIRED || rerun.isPending || Boolean(rerun.data)
                }
                onClick={async () => {
                  try {
                    const result = await rerun.mutateAsync({
                      runId: id,
                      failedOnly: false,
                    });
                    if (!result.startupDiagnostic) onSelect(result.runId);
                  } catch {}
                }}
              >
                Rerun all checks
              </button>
              <button
                className="primary"
                disabled={
                  run.state === ExecutionState.EXPIRED || rerun.isPending || Boolean(rerun.data)
                }
                onClick={async () => {
                  try {
                    const r = await rerun.mutateAsync({
                      runId: id,
                      failedOnly: true,
                    });
                    if (!r.startupDiagnostic) onSelect(r.runId);
                  } catch {}
                }}
              >
                Rerun failed checks
              </button>
            </>
          )}
        </div>
      </div>
      <div className={run.gatePassed ? "notice success" : "notice"}>
        <strong>
          {run.gatePassed
            ? "Required checks passed for this attempt"
            : "Validation is not complete"}
        </strong>
        <p>{run.gateReason}</p>
        <code>ach check --commit {run.commit}</code>
        <p className="small">
          The final gate selects the latest compatible attempt. Acknowledgement
          does not change validation.
        </p>
      </div>
      {run.parentId && (
        <p>
          Rerun of{" "}
          <button onClick={() => onSelect(run.parentId)}>
            {run.parentId.slice(0, 8)}
          </button>
        </p>
      )}
      {mutationError && <ErrorNotice error={mutationError} />}
      {rerun.data?.startupDiagnostic && (
        <div className="notice error" role="alert">
          <strong>Rerun accepted; startup needs attention</strong>
          <p>{rerun.data.startupDiagnostic.message}</p>
          <p>{rerun.data.startupDiagnostic.hint}</p>
          <p>Accepted execution: <code>{rerun.data.runId}</code></p>
          <code>ach status --run {rerun.data.runId}</code>
          <p>
            <button onClick={() => onSelect(rerun.data!.runId)}>
              Open accepted execution
            </button>
          </p>
        </div>
      )}
      {run.diagnostics.map((d, i) => (
        <div className="notice error" role="alert" key={i}>
          <strong>{d.code}</strong>
          <p>{d.message}</p>
        </div>
      ))}
      <h3>Checks</h3>
      <div className="check-list">
        {run.checks.map((c) => (
          <article key={c.id}>
            <div className="check-top">
              <button
                onClick={() => setCheck(check === c.id ? "" : c.id)}
                aria-expanded={check === c.id}
              >
                {c.name}
              </button>
              <Status state={c.state} />
              <span className="muted">
                {c.optional ? "Optional" : "Required"}
                {c.exitCode !== undefined ? ` · exit ${c.exitCode}` : ""}
              </span>
            </div>
            <code className="command">{c.command}</code>
            {c.inheritedFrom && (
              <p className="small">
                Inherited successful evidence from{" "}
                <button onClick={() => onSelect(c.inheritedFrom)}>
                  {c.inheritedFrom.slice(0, 8)}
                </button>
              </p>
            )}
            {c.diagnostics.map((d, i) => (
              <p role="status" key={i}>
                {d.code}: {d.message}
              </p>
            ))}
            <FailureList failures={c.failures} />
            {check === c.id && (
              <>
                <Logs run={id} check={c.id} state={c.state} />
                {c.reports.map((report) => (
                  <ReportView
                    key={report.id}
                    run={id}
                    id={report.id}
                    name={report.name}
                  />
                ))}
              </>
            )}
          </article>
        ))}
      </div>
      <div className="compare-controls">
        <h3>Compare failures</h3>
        <label htmlFor="previous">Previous run ID (optional)</label>
        <input
          id="previous"
          value={previous}
          onChange={(e) => setPrevious(e.target.value)}
          placeholder="Defaults to previous compatible earlier commit"
        />
        <button onClick={() => setComparison(true)}>Compare</button>
      </div>
      {comparison && <Comparison run={id} previous={previous} />}
      {confirm && (
        <Confirm
          title="Cancel this execution?"
          onClose={() => setConfirm(false)}
          onConfirm={async () => {
            await cancel.mutateAsync({ runId: id });
            setConfirm(false);
            await cache.invalidateQueries();
          }}
        >
          Owned check processes and their descendants will be stopped. The
          attempt and collected evidence remain in history.
        </Confirm>
      )}
    </section>
  );
}

export function FailureList({ failures }: { failures: Failure[] }) {
  return (
    <div className="failures">
      {failures.map((f) => (
        <section key={f.id}>
          <h4>{f.test || f.check}</h4>
          {f.file && (
            <p>
              {f.file}
              {f.line ? `:${f.line}` : ""}
            </p>
          )}
          <pre>
            {f.message ||
              "No detailed failure message was reported. Open the check log."}
          </pre>
        </section>
      ))}
    </div>
  );
}
function Logs({ run, check, state }: { run: string; check: string; state: ExecutionState }) {
  const polling = active(state);
  const wasPolling = useRef(polling);
  const [offset, setOffset] = useState(0n);
  const log = useQuery(
    LocalQuery.getLogs,
    { runId: run, checkId: check, offset, limit: 65536 },
    { refetchInterval: polling ? 2000 : false },
  );
  const { refetch } = log;
  useEffect(() => {
    // Completion can arrive between log polls. Read the final bytes once before
    // leaving this view idle, even when another check keeps the run active.
    if (wasPolling.current && !polling) void refetch();
    wasPolling.current = polling;
  }, [polling, refetch]);
  return (
    <div className="logs">
      <div className="section-heading">
        <h4>Check log</h4>
        <button onClick={() => { void log.refetch(); }}>Refresh log</button>
        <span>Offset {String(offset)} · local data</span>
      </div>
      {log.error ? (
        <ErrorNotice error={log.error} />
      ) : (
        <pre aria-label="Check log output">
          {log.data?.text ||
            (log.isPending ? "Loading log…" : "No output at this offset.")}
        </pre>
      )}
      <div className="pagination">
        {offset > 0n && (
          <button onClick={() => setOffset(0n)}>Start of log</button>
        )}
        {log.data && log.data.nextOffset > offset && !log.data.complete && (
          <button onClick={() => setOffset(log.data!.nextOffset)}>
            Next log page
          </button>
        )}
      </div>
    </div>
  );
}
function ReportView({
  run,
  id,
  name,
}: {
  run: string;
  id: string;
  name: string;
}) {
  const [open, setOpen] = useState(false);
  const [offset, setOffset] = useState(0n);
  const report = useQuery(
    LocalQuery.getReport,
    { runId: run, reportId: id, offset, limit: 65536 },
    { enabled: open },
  );
  return (
    <section>
      <button onClick={() => setOpen(!open)} aria-expanded={open}>
        Report: {name}
      </button>
      {open && (
        <>
          {report.error ? (
            <ErrorNotice error={report.error} />
          ) : (
            <pre aria-label={name}>
              {report.data?.text || "Loading report…"}
            </pre>
          )}
          <div className="pagination">
            {offset > 0n && (
              <button onClick={() => setOffset(0n)}>Start of report</button>
            )}
            {report.data && !report.data.complete && (
              <button onClick={() => setOffset(report.data!.nextOffset)}>
                Next report page
              </button>
            )}
          </div>
        </>
      )}
    </section>
  );
}
function Comparison({ run, previous }: { run: string; previous: string }) {
  const result = useQuery(LocalQuery.compare, {
    runId: run,
    previousId: previous,
  });
  if (result.error) return <ErrorNotice error={result.error} />;
  if (!result.data) return <p role="status">Comparing evidence…</p>;
  if (!result.data.available)
    return (
      <p className="notice" role="status">
        Comparison unavailable: {result.data.reason}
      </p>
    );
  return (
    <div className="comparison">
      {[
        ["New", result.data.newFailures],
        ["Continuing", result.data.continuingFailures],
        ["Resolved", result.data.resolvedFailures],
      ].map(([name, failures]) => (
        <section key={name as string}>
          <h4>
            {name as string} · {(failures as Failure[]).length}
          </h4>
          <FailureList failures={failures as Failure[]} />
        </section>
      ))}
    </div>
  );
}
function Changes({ worktree, branch, branchId }: { worktree: string; branch: string; branchId: string }) {
  const [base, setBase] = useState("");
  const [chosen, setChosen] = useState("");
  useEffect(() => {
    setBase("");
    setChosen("");
  }, [worktree, branch, branchId]);
  const changes = useQuery(
    LocalQuery.getChanges,
    { worktreeId: worktree, ref: branch, branchId, base: chosen },
    { enabled: Boolean(worktree) },
  );
  return (
    <section>
      <div className="section-heading">
        <h2>Branch changes</h2>
        <span>Local refs · no automatic fetch</span>
      </div>
      <form
        className="inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          setChosen(base);
        }}
      >
        <label htmlFor="base">Diff base</label>
        <input
          id="base"
          value={base}
          onChange={(e) => setBase(e.target.value)}
          placeholder="Configured base or local origin/HEAD"
        />
        <button>Apply base</button>
      </form>
      {changes.error ? (
        <ErrorNotice error={changes.error} />
      ) : changes.data ? (
        <>
          <p className="small">
            Merge base <code>{changes.data.mergeBase}</code>
            <br />
            Head <code>{changes.data.head}</code>
          </p>
          <pre className="source">
            {changes.data.diff || "No committed changes from the merge base."}
          </pre>
          {changes.data.truncated && (
            <p role="status">
              Large diff truncated. Inspect the exact commits with local Git.
            </p>
          )}
        </>
      ) : (
        <p role="status">
          {worktree
            ? "Loading committed changes…"
            : "Select a registered worktree."}
        </p>
      )}
    </section>
  );
}
function Commits({ worktree, branch, branchId }: { worktree: string; branch: string; branchId: string }) {
  const [offset, setOffset] = useState(0);
  useEffect(() => setOffset(0), [worktree, branch, branchId]);
  const commits = useQuery(
    LocalQuery.listCommits,
    { worktreeId: worktree, ref: branch, branchId, offset },
    { enabled: Boolean(worktree) },
  );
  return (
    <section>
      <h2>Commits</h2>
      {commits.error ? (
        <ErrorNotice error={commits.error} />
      ) : (
        <ol className="commits">
          {commits.data?.commits.map((c) => (
            <li key={c.id}>
              <code>{c.id.slice(0, 12)}</code>
              <strong>{c.subject}</strong>
            </li>
          ))}
        </ol>
      )}
      {commits.isFetching && <p role="status">Loading commits…</p>}
      <div className="pagination">
        {offset > 0 && (
          <button onClick={() => setOffset(Math.max(0, offset - 100))}>
            Newer
          </button>
        )}
        {commits.data?.commits.length === 100 && (
          <button onClick={() => setOffset(offset + 100)}>Older</button>
        )}
      </div>
    </section>
  );
}
export function Confirm({
  title,
  children,
  onClose,
  onConfirm,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const previousFocus = useRef(document.activeElement as HTMLElement | null);
  const ref = useRef<HTMLDialogElement>(null);
  const [error, setError] = useState<unknown>();
  const [pending, setPending] = useState(false);
  useEffect(() => {
    const dialog = ref.current;
    dialog?.showModal();
    return () => {
      dialog?.close();
      previousFocus.current?.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      aria-labelledby="confirm-title"
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <h2 id="confirm-title">{title}</h2>
      <p>{children}</p>
      {error != null && <ErrorNotice error={error} />}
      <div className="actions">
        <button autoFocus onClick={onClose} disabled={pending}>
          Keep running
        </button>
        <button
          className="danger"
          disabled={pending}
          onClick={async () => {
            setPending(true);
            try {
              await onConfirm();
            } catch (e) {
              setError(e);
            } finally {
              setPending(false);
            }
          }}
        >
          {pending ? "Requesting cancellation…" : "Cancel execution"}
        </button>
      </div>
    </dialog>
  );
}
