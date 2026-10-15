import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import {
  QueryClient,
  QueryClientProvider,
  useQueryClient,
} from "@tanstack/react-query";
import {
  TransportProvider,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query";
import type { Transport } from "@connectrpc/connect";
import {
  cancelRun,
  exportReport,
  getAnalysis,
  getRunProgress,
  getSourceFile,
  listRuns,
  selectRun,
} from "./gen/bundlewhy/v1/bundlewhy-AnalysisService_connectquery";
import {
  CoverageState,
  EngineSupport,
  EvidenceStatus,
  LocationKind,
  OptimizationCategory,
  ReportMode,
  RunStatus,
  SourceRevisionEvidence,
  type Analysis,
  type ChunkSize,
  type CodeSnippet,
  type Diagnostic,
  type EngineIdentity,
  type RunProgress,
  type SourceLocation,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import { ExportUsageSection } from "./ExportUsageSection";
import {
  ALL_CATEGORIES,
  ALL_EVIDENCE_STATUSES,
  attributionSupported,
  bundlerLabel,
  categoryLabel,
  conditionOutcomePresentation,
  coverageLabel,
  engineSupportLabel,
  evidenceStatusExplanation,
  evidenceStatusPresentation,
  locationKindLabel,
  problemSeverityLabel,
  runStageLabel,
  runStatusPresentation,
  sourceOriginLabel,
  type StatusPresentation,
} from "./labels";
import {
  categoriesWithoutCoverage,
  countByCategory,
  countByStatus,
  coverageFor,
  coverageStateFor,
  emptyStateFor,
  EmptyState,
  filterByCategoryAndSearch,
  filterDiagnostics,
  toggleStatus,
} from "./filters";
import {
  engineSummary,
  formatBytes,
  formatElapsed,
  formatExactBytes,
  formatLocation,
  formatTimestamp,
} from "./format";
import { describeError } from "./errors";
import { Modal } from "./Modal";
import { bootTransport, TransportMode } from "./transport";
import type { BootError } from "./report";

const POLL_INTERVAL_MS = 1000;
const WIDE_LAYOUT_QUERY = "(min-width: 1280px)";

/* ------------------------------------------------------------------ */
/* Boot and providers                                                  */
/* ------------------------------------------------------------------ */

export function App() {
  const boot = useMemo(() => bootTransport(), []);
  if (!boot.ok) return <BootErrorView error={boot.error} />;
  return <BundlewhyApp transport={boot.transport} mode={boot.mode} />;
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });
}

export interface BundlewhyAppProps {
  transport: Transport;
  mode: TransportMode;
  queryClient?: QueryClient;
}

export function BundlewhyApp({
  transport,
  mode,
  queryClient,
}: BundlewhyAppProps) {
  const [client] = useState(() => queryClient ?? createQueryClient());
  return (
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <RunGate exported={mode === TransportMode.Exported} />
      </TransportProvider>
    </QueryClientProvider>
  );
}

export function BootErrorView({ error }: { error: BootError }) {
  return (
    <div className="page">
      <header className="app-header">
        <div className="brand">bundlewhy</div>
      </header>
      <main className="page-center">
        <ErrorPanel
          title={error.title}
          code={error.code}
          message={error.message}
          recovery={error.recovery}
        />
      </main>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Run lifecycle                                                       */
/* ------------------------------------------------------------------ */

function RunGate({ exported }: { exported: boolean }) {
  const progress = useQuery(
    getRunProgress,
    {},
    {
      refetchInterval: (query) =>
        query.state.data?.progress?.status === RunStatus.RUNNING
          ? POLL_INTERVAL_MS
          : false,
    },
  );
  if (progress.error)
    return (
      <SimplePage>
        <ConnectErrorPanel
          title="Cannot load the run status"
          error={progress.error}
        />
      </SimplePage>
    );
  if (!progress.data)
    return (
      <SimplePage>
        <p className="muted" role="status" aria-live="polite">
          Loading analysis…
        </p>
      </SimplePage>
    );
  const run = progress.data.progress;
  if (run?.status === RunStatus.RUNNING)
    return (
      <SimplePage status={RunStatus.RUNNING}>
        <ProgressView progress={run} />
      </SimplePage>
    );
  return (
    <AnalysisLoader exported={exported || run?.mode === ReportMode.EXPORTED} />
  );
}

function SimplePage({
  children,
  status,
}: {
  children: ReactNode;
  status?: RunStatus;
}) {
  return (
    <div className="page">
      <header className="app-header">
        <div className="brand">bundlewhy</div>
        {status !== undefined ? <RunStatusBadge status={status} /> : null}
      </header>
      <main className="page-center">{children}</main>
    </div>
  );
}

function ProgressView({ progress }: { progress: RunProgress }) {
  const [confirming, setConfirming] = useState(false);
  const client = useQueryClient();
  const cancel = useMutation(cancelRun, {
    onSuccess: () => client.invalidateQueries(),
  });
  return (
    <section className="card progress-card" aria-labelledby="progress-title">
      <h1 id="progress-title">Analyzing your bundle</h1>
      <div aria-live="polite" role="status" className="progress-status">
        <p className="progress-stage">
          <span className="spinner" aria-hidden="true" />
          {runStageLabel(progress.stage)}
        </p>
        {progress.message ? <p>{progress.message}</p> : null}
        <p className="muted">Elapsed {formatElapsed(progress.elapsedMs)}</p>
      </div>
      {cancel.error ? (
        <ConnectErrorPanel
          title="Cannot cancel the analysis"
          error={cancel.error}
        />
      ) : null}
      {progress.cancellable ? (
        <div className="actions">
          <button
            type="button"
            className="button-secondary"
            disabled={cancel.isPending}
            onClick={() => setConfirming(true)}
          >
            Cancel analysis
          </button>
        </div>
      ) : null}
      {confirming ? (
        <Modal
          title="Cancel analysis?"
          onClose={() => setConfirming(false)}
          footer={
            <>
              <button
                type="button"
                className="button-secondary"
                onClick={() => setConfirming(false)}
              >
                Keep running
              </button>
              <button
                type="button"
                className="button-danger"
                onClick={() => {
                  setConfirming(false);
                  cancel.mutate({});
                }}
              >
                Cancel analysis
              </button>
            </>
          }
        >
          <p>
            bundlewhy stops the running analysis. Results collected so far may
            be incomplete.
          </p>
        </Modal>
      ) : null}
    </section>
  );
}

function AnalysisLoader({ exported }: { exported: boolean }) {
  const analysis = useQuery(getAnalysis, {});
  if (analysis.error)
    return (
      <SimplePage>
        <ConnectErrorPanel
          title="Cannot load the analysis"
          error={analysis.error}
        />
      </SimplePage>
    );
  if (!analysis.data?.analysis)
    return (
      <SimplePage>
        <p className="muted" role="status" aria-live="polite">
          Loading analysis…
        </p>
      </SimplePage>
    );
  const data = analysis.data.analysis;
  return (
    <AnalysisView
      analysis={data}
      exported={exported || data.mode === ReportMode.EXPORTED}
    />
  );
}

/* ------------------------------------------------------------------ */
/* Main analysis layout                                                */
/* ------------------------------------------------------------------ */

function useMediaQuery(query: string, fallback: boolean): boolean {
  const get = () =>
    typeof window.matchMedia === "function"
      ? window.matchMedia(query).matches
      : fallback;
  const [matches, setMatches] = useState(get);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const list = window.matchMedia(query);
    const listener = () => setMatches(list.matches);
    listener();
    list.addEventListener("change", listener);
    return () => list.removeEventListener("change", listener);
  }, [query]);
  return matches;
}

enum CenterTab {
  Diagnostics = "diagnostics",
  Sizes = "sizes",
}

export function AnalysisView({
  analysis,
  exported,
}: {
  analysis: Analysis;
  exported: boolean;
}) {
  const wide = useMediaQuery(WIDE_LAYOUT_QUERY, true);
  const [category, setCategory] = useState<OptimizationCategory | undefined>();
  const [statuses, setStatuses] = useState<ReadonlySet<EvidenceStatus>>(
    new Set(),
  );
  const [search, setSearch] = useState("");
  const [selectedId, setSelectedId] = useState<string | undefined>();
  const [showDetails, setShowDetails] = useState(false);
  const [tab, setTab] = useState(CenterTab.Diagnostics);

  const diagnostics = analysis.diagnostics;
  const categoryCounts = useMemo(
    () => countByCategory(diagnostics),
    [diagnostics],
  );
  const searchFiltered = useMemo(
    () => filterByCategoryAndSearch(diagnostics, { category, search }),
    [diagnostics, category, search],
  );
  const statusCounts = useMemo(
    () => countByStatus(searchFiltered),
    [searchFiltered],
  );
  const filtered = useMemo(
    () => filterDiagnostics(diagnostics, { category, search, statuses }),
    [diagnostics, category, search, statuses],
  );
  const selected = filtered.find((d) => d.id === selectedId) ?? filtered[0];

  const narrowDetails = !wide && showDetails && selected !== undefined;

  function select(diagnostic: Diagnostic, openDetails: boolean) {
    setSelectedId(diagnostic.id);
    if (openDetails) setShowDetails(true);
  }

  return (
    <div className="page">
      <AnalysisHeader analysis={analysis} exported={exported} />
      <AnalysisNotices analysis={analysis} />
      <div className={wide ? "layout layout-wide" : "layout layout-narrow"}>
        {narrowDetails ? null : (
          <CategoryNav
            analysis={analysis}
            counts={categoryCounts}
            total={diagnostics.length}
            selected={category}
            onSelect={setCategory}
          />
        )}
        <main className="layout-main">
          {narrowDetails ? null : (
            <section className="column column-center" aria-label="Results">
              <Tabs tab={tab} onChange={setTab} />
              {tab === CenterTab.Diagnostics ? (
                <div
                  role="tabpanel"
                  id="panel-diagnostics"
                  aria-labelledby="tab-diagnostics"
                >
                  <div className="toolbar">
                    <label className="search">
                      <span className="label">Search diagnostics</span>
                      <input
                        type="search"
                        value={search}
                        placeholder="Title, module, rule or path"
                        onChange={(event) => setSearch(event.target.value)}
                      />
                    </label>
                    <div
                      className="chips"
                      role="group"
                      aria-label="Filter by evidence status"
                    >
                      {ALL_EVIDENCE_STATUSES.map((status) => {
                        const p = evidenceStatusPresentation(status);
                        return (
                          <button
                            key={status}
                            type="button"
                            className="chip"
                            aria-pressed={statuses.has(status)}
                            onClick={() =>
                              setStatuses(toggleStatus(statuses, status))
                            }
                          >
                            <span aria-hidden="true">{p.icon}</span> {p.label}{" "}
                            <span className="count">
                              {statusCounts.get(status) ?? 0}
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  </div>
                  <DiagnosticList
                    analysis={analysis}
                    diagnostics={filtered}
                    category={category}
                    selected={selected}
                    selectOnFocus={wide}
                    onSelect={(d) => select(d, !wide)}
                  />
                </div>
              ) : (
                <div
                  role="tabpanel"
                  id="panel-sizes"
                  aria-labelledby="tab-sizes"
                >
                  <ChunkTable chunks={analysis.chunks} />
                </div>
              )}
            </section>
          )}
          {wide || narrowDetails ? (
            <section
              className="column column-details"
              aria-label="Diagnostic details"
            >
              {narrowDetails ? (
                <button
                  type="button"
                  className="button-ghost back"
                  onClick={() => setShowDetails(false)}
                >
                  ← Back to list
                </button>
              ) : null}
              {selected ? (
                <DiagnosticDetails
                  diagnostic={selected}
                  sourceRevisionEvidence={
                    selected.location?.sourceId
                      ? analysis.sources.find(
                          (source) => source.id === selected.location?.sourceId,
                        )?.revisionEvidence
                      : undefined
                  }
                />
              ) : (
                <p className="muted empty-details">
                  Select a diagnostic to see why the optimization was not
                  applied.
                </p>
              )}
            </section>
          ) : null}
        </main>
      </div>
    </div>
  );
}

function Tabs({
  tab,
  onChange,
}: {
  tab: CenterTab;
  onChange: (tab: CenterTab) => void;
}) {
  const tabs = [
    { id: CenterTab.Diagnostics, label: "Diagnostics" },
    { id: CenterTab.Sizes, label: "Chunk sizes" },
  ];
  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    const index = tabs.findIndex((t) => t.id === tab);
    const next =
      tabs[
        (index + (event.key === "ArrowRight" ? 1 : tabs.length - 1)) %
          tabs.length
      ];
    onChange(next.id);
    document.getElementById(`tab-${next.id}`)?.focus();
  }
  return (
    <div
      className="tabs"
      role="tablist"
      aria-label="Result views"
      onKeyDown={onKeyDown}
    >
      {tabs.map((t) => (
        <button
          key={t.id}
          id={`tab-${t.id}`}
          type="button"
          role="tab"
          aria-selected={tab === t.id}
          aria-controls={`panel-${t.id}`}
          tabIndex={tab === t.id ? 0 : -1}
          className="tab"
          onClick={() => onChange(t.id)}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Header and notices                                                  */
/* ------------------------------------------------------------------ */

function AnalysisHeader({
  analysis,
  exported,
}: {
  analysis: Analysis;
  exported: boolean;
}) {
  const [dialog, setDialog] = useState<"runs" | "export" | null>(null);
  return (
    <header className="app-header">
      <div className="header-identity">
        <div className="brand">bundlewhy</div>
        <h1 className="project-name">
          {analysis.projectName || "Unnamed project"}
        </h1>
        <p className="engine-line">{engineSummary(analysis.engine)}</p>
      </div>
      <div className="header-actions">
        <RunStatusBadge status={analysis.status} />
        <button
          type="button"
          className="button-secondary"
          onClick={() => setDialog("runs")}
        >
          Open report
        </button>
        <button
          type="button"
          className="button-primary"
          disabled={exported}
          title={
            exported
              ? "Export is not available in an exported report."
              : undefined
          }
          aria-describedby={exported ? "export-disabled-hint" : undefined}
          onClick={() => setDialog("export")}
        >
          Export HTML
        </button>
        {exported ? (
          <span id="export-disabled-hint" className="hint">
            Export is not available in an exported report.
          </span>
        ) : null}
      </div>
      {dialog === "runs" ? (
        <OpenReportDialog exported={exported} onClose={() => setDialog(null)} />
      ) : null}
      {dialog === "export" ? (
        <ExportDialog onClose={() => setDialog(null)} />
      ) : null}
    </header>
  );
}

function AnalysisNotices({ analysis }: { analysis: Analysis }) {
  const engine = analysis.engine;
  const incomplete =
    analysis.status === RunStatus.PARTIAL ||
    analysis.status === RunStatus.CANCELLED ||
    analysis.status === RunStatus.FAILED;
  return (
    <div className="notices">
      {engine?.diagnosticEngine ? (
        <DiagnosticEngineNotice engine={engine} />
      ) : null}
      {engine?.support === EngineSupport.UNSUPPORTED_SELECTED ? (
        <div className="banner banner-warning" role="note">
          <strong>
            <span aria-hidden="true">⚠ </span>Unsupported engine version
            selected explicitly
          </strong>
          <p>
            Supported versions: {engine.supportedVersions || "not reported"}.
            Findings may not match the behavior of supported versions.
          </p>
        </div>
      ) : null}
      {incomplete ? (
        <div className="banner banner-warning" role="note">
          <strong>
            <span aria-hidden="true">⚠ </span>
            {analysis.status === RunStatus.FAILED
              ? "The analysis failed before it finished."
              : analysis.status === RunStatus.CANCELLED
                ? "The analysis was cancelled."
                : "Some results are partial."}
          </strong>
          <p>
            This analysis is incomplete. Missing results are not evidence that
            no blockers exist.
          </p>
        </div>
      ) : null}
      {analysis.problems.length > 0 ? (
        <section
          className="banner banner-problems"
          aria-labelledby="problems-title"
        >
          <h2 id="problems-title">Analysis problems</h2>
          <ul className="problems">
            {analysis.problems.map((problem, index) => (
              <li key={`${problem.code}-${index}`}>
                <span className="tag">
                  {problemSeverityLabel(problem.severity)}
                </span>{" "}
                <code className="code-label">{problem.code}</code>
                <p>{problem.message}</p>
                {problem.recovery ? (
                  <p className="muted">Recovery: {problem.recovery}</p>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

function DiagnosticEngineNotice({ engine }: { engine: EngineIdentity }) {
  return (
    <div
      className="banner banner-info"
      role="note"
      aria-label="Diagnostic build notice"
    >
      <strong>
        <span aria-hidden="true">ⓘ </span>Diagnostic build
      </strong>
      <p>
        {engine.diagnosticNotice ||
          "bundlewhy ran a separate diagnostic build with collection overrides. Findings describe that build."}
      </p>
      <details className="engine-details">
        <summary>Engine details</summary>
        <dl>
          <dt>Bundler</dt>
          <dd>
            {[
              engine.bundlerPackage || bundlerLabel(engine.bundler),
              engine.bundlerVersion,
            ]
              .filter(Boolean)
              .join(" ")}
          </dd>
          <dt>Minifier</dt>
          <dd>
            {[engine.minifier || "Unknown", engine.minifierVersion]
              .filter(Boolean)
              .join(" ")}
          </dd>
          <dt>Default minifier</dt>
          <dd>{engine.defaultMinifier ? "Yes" : "No"}</dd>
          <dt>Adapter</dt>
          <dd>
            {[engine.adapterId || "Unknown", engine.adapterVersion]
              .filter(Boolean)
              .join(" ")}
          </dd>
          <dt>Support</dt>
          <dd>{engineSupportLabel(engine.support)}</dd>
          <dt>Supported versions</dt>
          <dd>{engine.supportedVersions || "Not reported"}</dd>
          <dt>Overrides</dt>
          <dd>
            {engine.overrides.length > 0 ? (
              <ul>
                {engine.overrides.map((override, index) => (
                  <li key={index}>
                    <code>{override}</code>
                  </li>
                ))}
              </ul>
            ) : (
              "None"
            )}
          </dd>
        </dl>
      </details>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Status indicators                                                   */
/* ------------------------------------------------------------------ */

function Badge({ p, prefix }: { p: StatusPresentation; prefix?: string }) {
  return (
    <span className={`badge tone-${p.tone}`}>
      {prefix ? <span className="sr-only">{prefix} </span> : null}
      <span aria-hidden="true">{p.icon}</span> {p.label}
    </span>
  );
}

function RunStatusBadge({ status }: { status: RunStatus }) {
  return (
    <span aria-live="polite">
      <Badge p={runStatusPresentation(status)} prefix="Analysis status:" />
    </span>
  );
}

function EvidenceBadge({ status }: { status: EvidenceStatus }) {
  return <Badge p={evidenceStatusPresentation(status)} />;
}

/* ------------------------------------------------------------------ */
/* Category filters                                                    */
/* ------------------------------------------------------------------ */

function CategoryNav({
  analysis,
  counts,
  total,
  selected,
  onSelect,
}: {
  analysis: Analysis;
  counts: Map<OptimizationCategory, number>;
  total: number;
  selected: OptimizationCategory | undefined;
  onSelect: (category: OptimizationCategory | undefined) => void;
}) {
  return (
    <nav
      className="column column-categories"
      aria-label="Optimization categories"
    >
      <h2 className="column-title">Categories</h2>
      <ul className="category-list">
        <li>
          <button
            type="button"
            className="category"
            aria-pressed={selected === undefined}
            onClick={() => onSelect(undefined)}
          >
            <span className="category-name">All categories</span>
            <span className="count">{findingsLabel(total)}</span>
          </button>
        </li>
        {ALL_CATEGORIES.map((category) => {
          const coverage = coverageFor(analysis, category);
          const state = coverageStateFor(analysis, category);
          return (
            <li key={category}>
              <button
                type="button"
                className="category"
                aria-pressed={selected === category}
                onClick={() => onSelect(category)}
              >
                <span className="category-name">{categoryLabel(category)}</span>
                <span className="count">
                  {findingsLabel(counts.get(category) ?? 0)}
                </span>
                <span className={`coverage coverage-${coverageTone(state)}`}>
                  Coverage: {coverageLabel(state)}
                </span>
                {coverage?.note ? (
                  <span className="coverage-note">{coverage.note}</span>
                ) : null}
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

function findingsLabel(count: number): string {
  return `${count} ${count === 1 ? "finding" : "findings"}`;
}

function coverageTone(state: CoverageState): string {
  switch (state) {
    case CoverageState.COVERED:
      return "covered";
    case CoverageState.PARTIAL:
      return "partial";
    case CoverageState.NOT_APPLICABLE:
      return "na";
    default:
      return "unavailable";
  }
}

/* ------------------------------------------------------------------ */
/* Diagnostic list                                                     */
/* ------------------------------------------------------------------ */

function DiagnosticList({
  analysis,
  diagnostics,
  category,
  selected,
  selectOnFocus,
  onSelect,
}: {
  analysis: Analysis;
  diagnostics: Diagnostic[];
  category: OptimizationCategory | undefined;
  selected: Diagnostic | undefined;
  selectOnFocus: boolean;
  onSelect: (diagnostic: Diagnostic) => void;
}) {
  const listRef = useRef<HTMLUListElement>(null);
  const empty = emptyStateFor(analysis, diagnostics);
  if (empty !== EmptyState.None)
    return <EmptyList analysis={analysis} state={empty} category={category} />;

  function onKeyDown(event: KeyboardEvent<HTMLUListElement>) {
    const buttons = Array.from(
      listRef.current?.querySelectorAll<HTMLButtonElement>(
        "button.diagnostic",
      ) ?? [],
    );
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (event.key === "ArrowDown")
      next = Math.min(buttons.length - 1, index + 1);
    else if (event.key === "ArrowUp") next = Math.max(0, index - 1);
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = buttons.length - 1;
    if (next < 0) return;
    event.preventDefault();
    buttons[next].focus();
    if (selectOnFocus) onSelect(diagnostics[next]);
  }

  return (
    <>
      <p className="muted list-count" aria-live="polite">
        {diagnostics.length}{" "}
        {diagnostics.length === 1 ? "diagnostic" : "diagnostics"}
      </p>
      <ul
        className="diagnostic-list"
        aria-label="Diagnostics"
        ref={listRef}
        onKeyDown={onKeyDown}
      >
        {diagnostics.map((d) => (
          <li key={d.id}>
            <button
              type="button"
              className="diagnostic"
              aria-current={selected?.id === d.id ? "true" : undefined}
              onClick={() => onSelect(d)}
            >
              <span className="diagnostic-title">{d.title}</span>
              <span className="diagnostic-meta">
                <EvidenceBadge status={d.status} />
                <span className="tag">{categoryLabel(d.category)}</span>
                {d.dependency ? (
                  <span className="tag tag-dependency">dependency</span>
                ) : null}
              </span>
              <span className="diagnostic-path">
                {d.location ? formatLocation(d.location) : d.module}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </>
  );
}

function EmptyList({
  analysis,
  state,
  category,
}: {
  analysis: Analysis;
  state: EmptyState;
  category: OptimizationCategory | undefined;
}) {
  if (state === EmptyState.NoCoverage)
    return (
      <div className="empty" role="status">
        <h3>No analysis coverage</h3>
        <p>
          No detectors produced results for any category, so the absence of
          findings says nothing about blockers. Check the analysis problems
          above.
        </p>
      </div>
    );
  if (state === EmptyState.NoFindingsInCoveredCategories) {
    const uncovered = categoriesWithoutCoverage(analysis);
    return (
      <div className="empty" role="status">
        <h3>No blockers found in covered categories</h3>
        {uncovered.length > 0 ? (
          <p>
            Categories without full coverage:{" "}
            {uncovered.map(categoryLabel).join(", ")}. These were not fully
            analyzed.
          </p>
        ) : (
          <p>Every applicable category was analyzed.</p>
        )}
      </div>
    );
  }
  return (
    <div className="empty" role="status">
      <h3>No diagnostics match the filters</h3>
      {category !== undefined ? (
        <p>
          {categoryLabel(category)} coverage:{" "}
          {coverageLabel(coverageStateFor(analysis, category))}.
        </p>
      ) : null}
      <p>Change the category, status filter or search text.</p>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Diagnostic details                                                  */
/* ------------------------------------------------------------------ */

export function DiagnosticDetails({
  diagnostic,
  sourceRevisionEvidence,
}: {
  diagnostic: Diagnostic;
  sourceRevisionEvidence?: SourceRevisionEvidence;
}) {
  const [sourceOpen, setSourceOpen] = useState(false);
  const sourceId = diagnostic.location?.sourceId;
  const showSnippet =
    diagnostic.snippet?.kind !== LocationKind.ORIGINAL ||
    sourceRevisionEvidence === SourceRevisionEvidence.BUILD_SNAPSHOT;
  return (
    <article className="details" aria-labelledby="details-title">
      <h2 id="details-title">{diagnostic.title}</h2>
      <div className="details-status">
        <EvidenceBadge status={diagnostic.status} />
        <span className="muted">
          {evidenceStatusExplanation(diagnostic.status)}
        </span>
      </div>
      <dl className="facts">
        <dt>Category</dt>
        <dd>{categoryLabel(diagnostic.category)}</dd>
        <dt>Rule</dt>
        <dd>
          <code>{diagnostic.rule}</code>
        </dd>
        <dt>Engine</dt>
        <dd>{diagnostic.engine || "Not reported"}</dd>
        {diagnostic.module ? (
          <>
            <dt>Module</dt>
            <dd>
              {diagnostic.module}
              {diagnostic.dependency ? (
                <span className="tag tag-dependency">dependency</span>
              ) : null}
            </dd>
          </>
        ) : null}
      </dl>
      {diagnostic.summary ? (
        <p className="summary">{diagnostic.summary}</p>
      ) : null}

      <DetailSection title="Conditions">
        {diagnostic.conditions.length === 0 ? (
          <p className="muted">No conditions were reported.</p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Condition</th>
                <th scope="col">Outcome</th>
                <th scope="col">Detail</th>
              </tr>
            </thead>
            <tbody>
              {diagnostic.conditions.map((condition, index) => (
                <tr key={index}>
                  <td>{condition.description}</td>
                  <td>
                    <Badge
                      p={conditionOutcomePresentation(condition.outcome)}
                    />
                  </td>
                  <td>{condition.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </DetailSection>

      <DetailSection title="Location">
        {diagnostic.location || diagnostic.generatedLocation ? (
          <ul className="plain">
            {diagnostic.location ? (
              <LocationLine location={diagnostic.location} />
            ) : null}
            {diagnostic.generatedLocation ? (
              <LocationLine location={diagnostic.generatedLocation} />
            ) : null}
          </ul>
        ) : (
          <p className="muted">No location was reported.</p>
        )}
      </DetailSection>

      <DetailSection title="Code">
        {diagnostic.snippet && showSnippet ? (
          <Snippet snippet={diagnostic.snippet} />
        ) : (
          <p className="muted">No code snippet is available.</p>
        )}
        {sourceId ? (
          <button
            type="button"
            className="button-secondary"
            onClick={() => setSourceOpen(true)}
          >
            View full source
          </button>
        ) : null}
        {sourceOpen && sourceId ? (
          <SourceDialog
            sourceId={sourceId}
            onClose={() => setSourceOpen(false)}
          />
        ) : null}
      </DetailSection>

      <DetailSection title="Evidence">
        {diagnostic.evidence.length === 0 ? (
          <p className="muted">No evidence was recorded.</p>
        ) : (
          <ul className="evidence">
            {diagnostic.evidence.map((evidence, index) => (
              <li key={index}>
                <div className="evidence-head">
                  <EvidenceBadge status={evidence.status} />
                  <span className="provenance">{evidence.provenance}</span>
                </div>
                <p>{evidence.detail}</p>
              </li>
            ))}
          </ul>
        )}
      </DetailSection>

      <DetailSection title="Dependency paths">
        {diagnostic.dependencyPaths.length === 0 ? (
          <p className="muted">No dependency path was recorded.</p>
        ) : (
          diagnostic.dependencyPaths.map((path, index) => (
            <ol
              key={index}
              className="dependency-path"
              aria-label={`Dependency path ${index + 1}, from entry to module`}
            >
              {path.modules.map((module, moduleIndex) => (
                <li key={moduleIndex}>
                  <code>{module}</code>
                </li>
              ))}
            </ol>
          ))
        )}
      </DetailSection>

      {diagnostic.exportUsage ? (
        <DetailSection title="Export usage paths">
          <ExportUsageSection usage={diagnostic.exportUsage} />
        </DetailSection>
      ) : null}

      <DetailSection title="Corrective direction">
        {diagnostic.correctiveAction ? (
          <>
            <p>{diagnostic.correctiveAction.direction}</p>
            {diagnostic.correctiveAction.safety ? (
              <div className="safety">
                <h4>Safety conditions</h4>
                <p>{diagnostic.correctiveAction.safety}</p>
              </div>
            ) : null}
          </>
        ) : (
          <p className="muted">No corrective direction is available.</p>
        )}
      </DetailSection>
    </article>
  );
}

function DetailSection({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="detail-section">
      <h3>{title}</h3>
      {children}
    </section>
  );
}

function LocationLine({ location }: { location: SourceLocation }) {
  return (
    <li>
      <span className="tag">{locationKindLabel(location.kind)}</span>{" "}
      <code>{formatLocation(location)}</code>
    </li>
  );
}

export function Snippet({ snippet }: { snippet: CodeSnippet }) {
  const start = snippet.startLine > 0 ? snippet.startLine : 1;
  const lines = snippet.text.replace(/\n$/, "").split("\n");
  return (
    <figure className="snippet">
      <figcaption>
        <span className="tag">{locationKindLabel(snippet.kind)}</span>{" "}
        <code>{snippet.path}</code>
        {snippet.highlightLine > 0 ? (
          <span className="muted">
            {" "}
            · line {snippet.highlightLine} is marked with ▶
          </span>
        ) : null}
      </figcaption>
      <pre className="code">
        <code>
          {lines.map((line, index) => {
            const number = start + index;
            const highlighted = number === snippet.highlightLine;
            return (
              <span
                key={index}
                className={highlighted ? "line line-highlight" : "line"}
              >
                <span className="line-marker" aria-hidden={!highlighted}>
                  {highlighted ? "▶" : " "}
                </span>
                <span className="line-number">{number}</span>
                <span className="line-text">{line}</span>
                {"\n"}
              </span>
            );
          })}
        </code>
      </pre>
    </figure>
  );
}

function SourceDialog({
  sourceId,
  onClose,
}: {
  sourceId: string;
  onClose: () => void;
}) {
  const source = useQuery(getSourceFile, { sourceId });
  const file = source.data?.source;
  return (
    <Modal title={file?.path || "Source file"} onClose={onClose} wide>
      {source.error ? (
        <ConnectErrorPanel
          title="Cannot load the source file"
          error={source.error}
        />
      ) : !file ? (
        <p className="muted" role="status">
          Loading source…
        </p>
      ) : (
        <>
          <p className="muted">
            {sourceOriginLabel(file.origin)}
            {file.revisionEvidence === SourceRevisionEvidence.BUILD_SNAPSHOT &&
            file.sha256
              ? ` · Build SHA-256 ${file.sha256.slice(0, 12)}…`
              : ""}
          </p>
          {file.revisionEvidence !== SourceRevisionEvidence.BUILD_SNAPSHOT ? (
            <div className="banner banner-warning" role="note">
              <strong>
                <span aria-hidden="true">⚠ </span>Build source revision
                unavailable
              </strong>
              <p>
                {file.revisionEvidence === SourceRevisionEvidence.UNAVAILABLE
                  ? "This report has no verified build-time text for this source. Live file text is not shown as analyzed code."
                  : "This report does not record whether source text came from the build."}
              </p>
            </div>
          ) : null}
          {file.stale ? (
            <div className="banner banner-warning" role="note">
              <strong>
                <span aria-hidden="true">⚠ </span>Source changed since analysis
              </strong>
              <p>
                {file.revisionEvidence === SourceRevisionEvidence.BUILD_SNAPSHOT
                  ? "The current file differs from the version that was analyzed."
                  : file.revisionEvidence === SourceRevisionEvidence.UNSPECIFIED
                    ? "The current source file differs from or was removed since this analysis."
                    : "The source file could not be read; it may have been removed since this analysis."}
              </p>
            </div>
          ) : null}
          {file.contentIncluded &&
          file.revisionEvidence === SourceRevisionEvidence.BUILD_SNAPSHOT ? (
            <pre className="code source-view">
              <code>
                {file.content.split("\n").map((line, index) => (
                  <span key={index} className="line">
                    <span className="line-number">{index + 1}</span>
                    <span className="line-text">{line}</span>
                    {"\n"}
                  </span>
                ))}
              </code>
            </pre>
          ) : (
            <p className="empty">
              {file.revisionEvidence === SourceRevisionEvidence.UNAVAILABLE ||
              file.contentIncluded
                ? "Verified build source content is unavailable."
                : "Source content was not included in this report."}
            </p>
          )}
        </>
      )}
    </Modal>
  );
}

/* ------------------------------------------------------------------ */
/* Chunk sizes                                                         */
/* ------------------------------------------------------------------ */

function Bytes({ value }: { value: number }) {
  return <span title={formatExactBytes(value)}>{formatBytes(value)}</span>;
}

export function ChunkTable({ chunks }: { chunks: ChunkSize[] }) {
  if (chunks.length === 0)
    return (
      <div className="empty" role="status">
        <h3>No chunk sizes</h3>
        <p>The analysis did not measure any emitted chunks.</p>
      </div>
    );
  return (
    <div className="table-scroll">
      <table className="table chunk-table">
        <caption>
          Measured sizes of emitted chunks. These totals are not savings
          attributable to any single diagnostic.
        </caption>
        <thead>
          <tr>
            <th scope="col">File</th>
            <th scope="col">Entry</th>
            <th scope="col">Raw</th>
            <th scope="col">Gzip</th>
            <th scope="col">Brotli</th>
            <th scope="col">Mapped source bytes</th>
          </tr>
        </thead>
        <tbody>
          {chunks.map((chunk, index) => (
            <tr key={`${chunk.file}-${index}`}>
              <th scope="row">
                <code>{chunk.file}</code>
              </th>
              <td>{chunk.entry ? "Yes" : "No"}</td>
              <td>
                <Bytes value={chunk.rawBytes} />
              </td>
              <td>
                <Bytes value={chunk.gzipBytes} />
              </td>
              <td>
                <Bytes value={chunk.brotliBytes} />
              </td>
              <td>
                {attributionSupported(chunk.attribution) ? (
                  <details>
                    <summary>
                      <Bytes
                        value={chunk.sources.reduce(
                          (sum, s) => sum + s.generatedBytes,
                          0,
                        )}
                      />{" "}
                      mapped
                    </summary>
                    <ul className="plain attribution">
                      {chunk.sources.map((source, sourceIndex) => (
                        <li key={sourceIndex}>
                          <code>{source.path}</code>{" "}
                          <Bytes value={source.generatedBytes} />
                        </li>
                      ))}
                      <li>
                        Unmapped <Bytes value={chunk.unmappedBytes} />
                      </li>
                    </ul>
                  </details>
                ) : (
                  <span className="muted">Attribution unavailable</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Dialogs                                                             */
/* ------------------------------------------------------------------ */

function OpenReportDialog({
  exported,
  onClose,
}: {
  exported: boolean;
  onClose: () => void;
}) {
  const runs = useQuery(listRuns, {});
  const client = useQueryClient();
  const select = useMutation(selectRun, {
    onSuccess: async () => {
      onClose();
      await client.invalidateQueries();
    },
  });
  return (
    <Modal
      title="Open report"
      onClose={onClose}
      wide
      footer={
        <button type="button" className="button-secondary" onClick={onClose}>
          Close
        </button>
      }
    >
      {exported ? (
        <p>
          Exported reports contain one run. To browse other saved runs, run{" "}
          <code>bundlewhy open &lt;run-dir&gt;</code>.
        </p>
      ) : (
        <p className="muted">
          Saved runs in the output directory of the active run.
        </p>
      )}
      {runs.error ? (
        <ConnectErrorPanel title="Cannot list runs" error={runs.error} />
      ) : !runs.data ? (
        <p className="muted" role="status">
          Loading runs…
        </p>
      ) : runs.data.runs.length === 0 ? (
        <p className="empty">No saved runs were found.</p>
      ) : (
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Run</th>
                <th scope="col">Project</th>
                <th scope="col">Bundler</th>
                <th scope="col">Started</th>
                <th scope="col">Status</th>
                <th scope="col">
                  <span className="sr-only">Action</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {runs.data.runs.map((run) => (
                <tr key={run.runId}>
                  <td>
                    <code>{run.runId}</code>
                  </td>
                  <td>{run.projectName}</td>
                  <td>{bundlerLabel(run.bundler)}</td>
                  <td>{formatTimestamp(run.startedAt)}</td>
                  <td>
                    <Badge p={runStatusPresentation(run.status)} />
                  </td>
                  <td>
                    {run.active ? (
                      <span className="muted">Current</span>
                    ) : (
                      <button
                        type="button"
                        className="button-secondary"
                        disabled={exported || select.isPending}
                        aria-label={`Open run ${run.runId}`}
                        onClick={() => select.mutate({ runId: run.runId })}
                      >
                        Open
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {select.error ? (
        <ConnectErrorPanel title="Cannot open the run" error={select.error} />
      ) : null}
    </Modal>
  );
}

function ExportDialog({ onClose }: { onClose: () => void }) {
  const [includeSources, setIncludeSources] = useState(true);
  // The source-inclusion choice controls whether code is embedded; focus it
  // first so it is not overlooked.
  const sourcesCheckbox = useRef<HTMLInputElement>(null);
  const run = useMutation(exportReport);
  const result = run.data;
  return (
    <Modal
      title="Export HTML report"
      onClose={onClose}
      initialFocus={sourcesCheckbox}
      footer={
        result ? (
          <button type="button" className="button-primary" onClick={onClose}>
            Done
          </button>
        ) : (
          <>
            <button
              type="button"
              className="button-secondary"
              onClick={onClose}
            >
              Cancel
            </button>
            <button
              type="button"
              className="button-primary"
              disabled={run.isPending}
              onClick={() => run.mutate({ includeSources })}
            >
              {run.isPending ? "Exporting…" : "Export"}
            </button>
          </>
        )
      }
    >
      {result ? (
        <div role="status" aria-live="polite">
          <p>The report was saved in the run directory.</p>
          <dl className="facts">
            <dt>File</dt>
            <dd>
              <code>{result.fileName}</code>
            </dd>
            <dt>Size</dt>
            <dd>
              <Bytes value={result.sizeBytes} />
            </dd>
            <dt>Source content</dt>
            <dd>{result.sourcesIncluded ? "Included" : "Not included"}</dd>
          </dl>
        </div>
      ) : (
        <>
          <p>
            Create a single self-contained HTML file that anyone can open
            offline.
          </p>
          <label className="checkbox">
            <input
              ref={sourcesCheckbox}
              type="checkbox"
              checked={includeSources}
              onChange={(event) => setIncludeSources(event.target.checked)}
            />
            Include source content
          </label>
          <div className="banner banner-warning" role="note">
            <strong>
              <span aria-hidden="true">⚠ </span>Review before sharing
            </strong>
            <p>
              Reports that include source content may contain secrets from your
              code. Review before sharing.
            </p>
          </div>
          {run.error ? (
            <ConnectErrorPanel
              title="Cannot export the report"
              error={run.error}
            />
          ) : null}
        </>
      )}
    </Modal>
  );
}

/* ------------------------------------------------------------------ */
/* Errors                                                              */
/* ------------------------------------------------------------------ */

export function ErrorPanel({
  title,
  code,
  connectCode,
  message,
  recovery,
}: {
  title: string;
  code?: string;
  connectCode?: string;
  message: string;
  recovery: string;
}) {
  return (
    <div className="error-panel" role="alert">
      <p className="error-code">
        <span aria-hidden="true">✕ </span>
        {code ?? connectCode ?? "Error"}
      </p>
      <h2>{title}</h2>
      <p>{message}</p>
      {connectCode ? (
        <p className="muted">Connect code: {connectCode}</p>
      ) : null}
      <p className="recovery">
        <strong>What to do:</strong> {recovery}
      </p>
    </div>
  );
}

function ConnectErrorPanel({
  title,
  error,
}: {
  title: string;
  error: unknown;
}) {
  const d = describeError(error);
  return (
    <ErrorPanel
      title={title}
      code={d.code}
      connectCode={d.connectCode}
      message={d.message}
      recovery={d.recovery}
    />
  );
}
