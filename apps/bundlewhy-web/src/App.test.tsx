import { create, fromJson, toJson, type JsonValue } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type ServiceImpl,
} from "@connectrpc/connect";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { App, BundlewhyApp } from "./App";
import {
  AnalysisSchema,
  AnalysisService,
  CoverageState,
  EngineSupport,
  EvidenceStatus,
  OptimizationCategory,
  ProblemSeverity,
  ReportMode,
  RunProgressSchema,
  RunStage,
  RunStatus,
  SourceFileSchema,
  SourceFileSummarySchema,
  SourceRevisionEvidence,
  type Analysis,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import { TransportMode } from "./transport";
import {
  fixtureAnalysis,
  fixtureExportUsage,
  fixtureSource,
} from "./test-fixtures";

type Impl = Partial<ServiceImpl<typeof AnalysisService>>;

function completeProgress(analysis: Analysis) {
  return {
    progress: create(RunProgressSchema, {
      runId: analysis.runId,
      status: analysis.status,
      stage: RunStage.FINISHED,
      mode: ReportMode.SAVED,
    }),
  };
}

function renderWith(
  analysis: Analysis,
  impl: Impl = {},
  mode = TransportMode.Server,
) {
  const transport = createRouterTransport((router) =>
    router.service(AnalysisService, {
      getRunProgress: () => completeProgress(analysis),
      getAnalysis: () => ({ analysis }),
      getSourceFile: () => ({ source: fixtureSource() }),
      listRuns: () => ({ runs: [] }),
      ...impl,
    }),
  );
  return render(<BundlewhyApp transport={transport} mode={mode} />);
}

async function renderAnalysis(analysis = fixtureAnalysis(), impl: Impl = {}) {
  const result = renderWith(analysis, impl);
  await screen.findByRole("heading", { level: 1, name: analysis.projectName });
  return result;
}

function diagnosticList() {
  return screen.queryByRole("list", { name: "Diagnostics" });
}

function listTitles(): string[] {
  const list = diagnosticList();
  if (!list) return [];
  return within(list)
    .getAllByRole("button")
    .map((b) => b.querySelector(".diagnostic-title")?.textContent ?? "");
}

afterEach(() => {
  document.getElementById("bundlewhy-report")?.remove();
  sessionStorage.clear();
  history.replaceState(null, "", "/");
  vi.restoreAllMocks();
});

describe("analysis layout", () => {
  it("renders categories with coverage, the list and details of the first diagnostic", async () => {
    await renderAnalysis();
    expect(screen.getByText("Vite 8.3.4 · Oxc minifier 1.2.13")).toBeTruthy();
    const nav = screen.getByRole("navigation", {
      name: "Optimization categories",
    });
    const tree = within(nav).getByRole("button", { name: /^Tree shaking/ });
    expect(tree.textContent).toContain("1 finding");
    expect(tree.textContent).toContain("Coverage: Covered");
    const folding = within(nav).getByRole("button", {
      name: /^Constant folding/,
    });
    expect(folding.textContent).toContain("Coverage: Not applicable");
    expect(folding.textContent).toContain("Not reported by this minifier.");
    const chunk = within(nav).getByRole("button", { name: /^Chunk splitting/ });
    expect(chunk.textContent).toContain("0 findings");
    expect(chunk.textContent).toContain("Coverage: Unavailable");
    expect(
      within(nav)
        .getByRole("button", { name: /^All categories/ })
        .getAttribute("aria-pressed"),
    ).toBe("true");

    expect(listTitles()).toEqual([
      "Side effects keep lodash in the bundle",
      "Module concatenation bailout",
      "Inlining evidence missing",
    ]);
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    expect(within(details).getByRole("heading", { level: 2 }).textContent).toBe(
      "Side effects keep lodash in the bundle",
    );
    expect(
      within(details).getByText(
        "Observed in the analysis engine output or configuration",
      ),
    ).toBeTruthy();
    expect(
      within(details).getByText("vite/side-effects-retained"),
    ).toBeTruthy();
    expect(within(details).getByText("Unmet")).toBeTruthy();
    expect(within(details).getByText("Met")).toBeTruthy();
    expect(within(details).getByText("src/main.ts:3:1")).toBeTruthy();
    expect(
      within(details).getByText("dist/assets/index.js:120:4"),
    ).toBeTruthy();
    expect(
      within(details).getByText("Original source", { selector: "li .tag" }),
    ).toBeTruthy();
    expect(within(details).getByText("Generated output")).toBeTruthy();
    const highlighted = details.querySelector(".line-highlight");
    expect(highlighted?.textContent).toContain("▶");
    expect(highlighted?.textContent).toContain("3");
    expect(highlighted?.textContent).toContain("import _ from 'lodash';");
    expect(within(details).getByText("rolldown module info")).toBeTruthy();
    const path = within(details).getByRole("list", {
      name: /Dependency path 1/,
    });
    expect(
      within(path)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["src/main.ts", "node_modules/lodash/lodash.js"]);
    expect(within(details).getByText("Safety conditions")).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/saving/i);
  });

  it("hides original snippets when a legacy source has no provenance", async () => {
    const analysis = fixtureAnalysis({
      sources: [
        create(SourceFileSummarySchema, {
          id: "src-1",
          path: "src/main.ts",
          revisionEvidence: SourceRevisionEvidence.UNSPECIFIED,
        }),
      ],
    });
    await renderAnalysis(analysis);
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    expect(
      within(details).getByText("No code snippet is available."),
    ).toBeTruthy();
    expect(
      within(details).queryByText("import { render } from './render';"),
    ).toBeNull();
  });

  it("selects another diagnostic by click and arrow keys", async () => {
    await renderAnalysis();
    const list = diagnosticList()!;
    const buttons = within(list).getAllByRole("button");
    fireEvent.click(buttons[1]);
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    expect(within(details).getByRole("heading", { level: 2 }).textContent).toBe(
      "Module concatenation bailout",
    );
    expect(
      within(details).getByText(
        "Derived by bundlewhy from supporting analysis; not an observed engine decision",
      ),
    ).toBeTruthy();
    buttons[1].focus();
    fireEvent.keyDown(buttons[1], { key: "ArrowDown" });
    expect(document.activeElement).toBe(buttons[2]);
    expect(within(details).getByRole("heading", { level: 2 }).textContent).toBe(
      "Inlining evidence missing",
    );
    expect(
      within(details).getByText("Evidence could not be collected"),
    ).toBeTruthy();
    expect(buttons[2].getAttribute("aria-current")).toBe("true");
  });

  it("shows evidence statuses as text labels and a dependency tag", async () => {
    await renderAnalysis();
    const list = diagnosticList()!;
    expect(within(list).getByText("Confirmed")).toBeTruthy();
    expect(within(list).getByText("Inferred")).toBeTruthy();
    expect(within(list).getByText("Unavailable")).toBeTruthy();
    expect(within(list).getAllByText("dependency")).toHaveLength(1);
  });

  it("narrows the list with category, status and search filters", async () => {
    await renderAnalysis();
    const nav = screen.getByRole("navigation", {
      name: "Optimization categories",
    });
    fireEvent.click(within(nav).getByRole("button", { name: /^Inlining/ }));
    expect(listTitles()).toEqual(["Inlining evidence missing"]);
    fireEvent.click(
      within(nav).getByRole("button", { name: /^All categories/ }),
    );

    const chips = screen.getByRole("group", {
      name: "Filter by evidence status",
    });
    const confirmed = within(chips).getByRole("button", { name: /Confirmed/ });
    expect(confirmed.textContent).toContain("1");
    fireEvent.click(confirmed);
    expect(confirmed.getAttribute("aria-pressed")).toBe("true");
    expect(listTitles()).toEqual(["Side effects keep lodash in the bundle"]);
    fireEvent.click(confirmed);

    const search = screen.getByLabelText("Search diagnostics");
    fireEvent.change(search, { target: { value: "UTILS" } });
    expect(listTitles()).toEqual(["Module concatenation bailout"]);

    fireEvent.click(
      within(nav).getByRole("button", { name: /^Chunk splitting/ }),
    );
    expect(diagnosticList()).toBeNull();
    expect(screen.getByText("No diagnostics match the filters")).toBeTruthy();
    expect(
      screen.getByText("Chunk splitting coverage: Unavailable."),
    ).toBeTruthy();
  });

  it("distinguishes no findings in covered categories from no coverage", async () => {
    const { unmount } = await renderAnalysis(
      fixtureAnalysis({ diagnostics: [] }),
    );
    expect(
      screen.getByText("No blockers found in covered categories"),
    ).toBeTruthy();
    expect(
      screen.getByText(
        /Categories without full coverage: Inlining, Name mangling/,
      ),
    ).toBeTruthy();
    expect(screen.queryByText("No analysis coverage")).toBeNull();
    unmount();

    await renderAnalysis(fixtureAnalysis({ diagnostics: [], coverage: [] }));
    expect(screen.getByText("No analysis coverage")).toBeTruthy();
    expect(
      screen.queryByText("No blockers found in covered categories"),
    ).toBeNull();
  });

  it("renders hostile report content as inert text", async () => {
    const hostile =
      "<script>window.pwned = 1</script><img src=x onerror=alert(1)>";
    const analysis = fixtureAnalysis();
    analysis.projectName = hostile;
    analysis.diagnostics[0].title = hostile;
    analysis.diagnostics[0].summary = hostile;
    const { container } = await renderAnalysis(analysis);
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getAllByText(hostile).length).toBeGreaterThanOrEqual(3);
    expect((window as { pwned?: number }).pwned).toBeUndefined();
  });

  it("switches to details with a back button in the narrow layout", async () => {
    const original = window.matchMedia;
    window.matchMedia = ((query: string) =>
      ({
        matches: false,
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }) as unknown as MediaQueryList) as typeof window.matchMedia;
    onTestFinished(() => {
      window.matchMedia = original;
    });
    await renderAnalysis();
    expect(
      screen.queryByRole("region", { name: "Diagnostic details" }),
    ).toBeNull();
    fireEvent.click(within(diagnosticList()!).getAllByRole("button")[1]);
    expect(diagnosticList()).toBeNull();
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    expect(within(details).getByRole("heading", { level: 2 }).textContent).toBe(
      "Module concatenation bailout",
    );
    fireEvent.click(screen.getByRole("button", { name: "← Back to list" }));
    expect(diagnosticList()).not.toBeNull();
  });
});

describe("source viewer", () => {
  it("lazily loads the full source and marks stale content", async () => {
    const getSourceFile = vi.fn(() => ({ source: fixtureSource() }));
    await renderAnalysis(fixtureAnalysis(), { getSourceFile });
    expect(getSourceFile).not.toHaveBeenCalled();
    const trigger = screen.getByRole("button", { name: "View full source" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("dialog", { name: "src/main.ts" });
    expect(getSourceFile).toHaveBeenCalledOnce();
    expect(
      within(dialog).getByText("Source changed since analysis"),
    ).toBeTruthy();
    expect(within(dialog).getByText("// header")).toBeTruthy();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("explains when source content is not included", async () => {
    const source = create(SourceFileSchema, {
      ...fixtureSource(),
      content: "",
      contentIncluded: false,
      stale: false,
    });
    await renderAnalysis(fixtureAnalysis(), {
      getSourceFile: () => ({ source }),
    });
    fireEvent.click(screen.getByRole("button", { name: "View full source" }));
    expect(
      await screen.findByText(
        "Source content was not included in this report.",
      ),
    ).toBeTruthy();
  });

  it("describes legacy stale source state accurately", async () => {
    const source = create(SourceFileSchema, {
      ...fixtureSource(),
      revisionEvidence: SourceRevisionEvidence.UNSPECIFIED,
      stale: true,
      sha256: "legacy-hash",
    });
    await renderAnalysis(fixtureAnalysis(), {
      getSourceFile: () => ({ source }),
    });
    fireEvent.click(screen.getByRole("button", { name: "View full source" }));
    const dialog = await screen.findByRole("dialog", { name: "src/main.ts" });
    expect(
      within(dialog).getByText(
        "The current source file differs from or was removed since this analysis.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).queryByText(
        "The source file was removed since this analysis.",
      ),
    ).toBeNull();
  });

  it("labels source text without a build snapshot as unverified", async () => {
    const source = create(SourceFileSchema, {
      ...fixtureSource(),
      sha256: "",
      content: "",
      contentIncluded: false,
      stale: false,
      revisionEvidence: SourceRevisionEvidence.UNAVAILABLE,
    });
    await renderAnalysis(fixtureAnalysis(), {
      getSourceFile: () => ({ source }),
    });
    fireEvent.click(screen.getByRole("button", { name: "View full source" }));
    const dialog = await screen.findByRole("dialog", { name: "src/main.ts" });
    expect(
      within(dialog).getByText("Build source revision unavailable"),
    ).toBeTruthy();
    expect(
      within(dialog).getByText("Verified build source content is unavailable."),
    ).toBeTruthy();
    expect(within(dialog).queryByText("// header")).toBeNull();
  });

  it("does not render content marked unavailable as analyzed source", async () => {
    const source = create(SourceFileSchema, {
      ...fixtureSource(),
      contentIncluded: true,
      revisionEvidence: SourceRevisionEvidence.UNAVAILABLE,
    });
    await renderAnalysis(fixtureAnalysis(), {
      getSourceFile: () => ({ source }),
    });
    fireEvent.click(screen.getByRole("button", { name: "View full source" }));
    const dialog = await screen.findByRole("dialog", { name: "src/main.ts" });
    expect(
      within(dialog).getByText("Verified build source content is unavailable."),
    ).toBeTruthy();
    expect(within(dialog).queryByText("// header")).toBeNull();
  });

  it("describes unavailable stale source without assuming removal", async () => {
    const source = create(SourceFileSchema, {
      ...fixtureSource(),
      content: "",
      contentIncluded: false,
      revisionEvidence: SourceRevisionEvidence.UNAVAILABLE,
    });
    await renderAnalysis(fixtureAnalysis(), {
      getSourceFile: () => ({ source }),
    });
    fireEvent.click(screen.getByRole("button", { name: "View full source" }));
    const dialog = await screen.findByRole("dialog", { name: "src/main.ts" });
    expect(
      within(dialog).getByText(
        "The source file could not be read; it may have been removed since this analysis.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).queryByText(
        "The source file was removed since this analysis.",
      ),
    ).toBeNull();
  });
});

describe("notices and banners", () => {
  it("shows the persistent diagnostic engine notice with engine details", async () => {
    await renderAnalysis();
    const notice = screen.getByRole("note", {
      name: "Diagnostic build notice",
    });
    expect(
      within(notice).getByText(
        "Findings describe a separate diagnostic build with collection overrides.",
      ),
    ).toBeTruthy();
    expect(
      within(notice).queryByRole("button", { name: /dismiss|close/i }),
    ).toBeNull();
    expect(within(notice).getByText("Engine details")).toBeTruthy();
    expect(within(notice).getByText("vite 8.3.4")).toBeTruthy();
    expect(within(notice).getByText("Oxc 1.2.13")).toBeTruthy();
    expect(within(notice).getByText("Yes")).toBeTruthy();
    expect(
      within(notice).getByText("bundlewhy-collector-vite 0.1.0"),
    ).toBeTruthy();
    expect(within(notice).getByText("Supported")).toBeTruthy();
    expect(within(notice).getByText(">=8.3.0 <8.4.0")).toBeTruthy();
    expect(within(notice).getByText("build.sourcemap=true")).toBeTruthy();
  });

  it("warns about an explicitly selected unsupported engine", async () => {
    const analysis = fixtureAnalysis();
    analysis.engine!.support = EngineSupport.UNSUPPORTED_SELECTED;
    await renderAnalysis(analysis);
    expect(
      screen.getByText("Unsupported engine version selected explicitly"),
    ).toBeTruthy();
    expect(screen.getByText(/Supported versions: >=8.3.0 <8.4.0/)).toBeTruthy();
  });

  it("shows incomplete banners and problems with codes and recovery", async () => {
    const analysis = fixtureAnalysis({
      status: RunStatus.PARTIAL,
      problems: [
        {
          code: "BW_SOURCE_MAP_MISSING",
          severity: ProblemSeverity.WARNING,
          message: "No source map for assets/vendor.js.",
          recovery: "Enable build.sourcemap.",
        },
      ],
    });
    const { unmount } = await renderAnalysis(analysis);
    expect(screen.getByText("Partial")).toBeTruthy();
    expect(
      screen.getByText(
        "This analysis is incomplete. Missing results are not evidence that no blockers exist.",
      ),
    ).toBeTruthy();
    const problems = screen.getByRole("region", { name: "Analysis problems" });
    expect(within(problems).getByText("BW_SOURCE_MAP_MISSING")).toBeTruthy();
    expect(within(problems).getByText("Warning")).toBeTruthy();
    expect(
      within(problems).getByText("Recovery: Enable build.sourcemap."),
    ).toBeTruthy();
    unmount();

    await renderAnalysis(fixtureAnalysis({ status: RunStatus.FAILED }));
    expect(screen.getByText("Failed")).toBeTruthy();
    expect(
      screen.getByText("The analysis failed before it finished."),
    ).toBeTruthy();
  });

  it("does not show the incomplete banner for a complete run", async () => {
    await renderAnalysis();
    expect(screen.getByText("Complete")).toBeTruthy();
    expect(screen.queryByText(/This analysis is incomplete/)).toBeNull();
  });
});

describe("run lifecycle", () => {
  it("polls progress while running and then shows the analysis", async () => {
    const analysis = fixtureAnalysis();
    let calls = 0;
    const getRunProgress = vi.fn(() => {
      calls += 1;
      if (calls === 1)
        return {
          progress: create(RunProgressSchema, {
            runId: analysis.runId,
            status: RunStatus.RUNNING,
            stage: RunStage.BUILDING,
            message: "vite build --mode production",
            elapsedMs: 5000,
            mode: ReportMode.LIVE,
            cancellable: true,
          }),
        };
      return completeProgress(analysis);
    });
    const getAnalysis = vi.fn(() => ({ analysis }));
    renderWith(analysis, { getRunProgress, getAnalysis });
    expect(
      await screen.findByText("Running the diagnostic build"),
    ).toBeTruthy();
    expect(screen.getByText("vite build --mode production")).toBeTruthy();
    expect(screen.getByText("Elapsed 5s")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Cancel analysis" }),
    ).toBeTruthy();
    expect(getAnalysis).not.toHaveBeenCalled();
    await screen.findByRole(
      "heading",
      { level: 1, name: "storefront" },
      { timeout: 3000 },
    );
    expect(getRunProgress).toHaveBeenCalledTimes(2);
    expect(getAnalysis).toHaveBeenCalled();
  });

  it("cancels the run after confirmation", async () => {
    const analysis = fixtureAnalysis({ status: RunStatus.CANCELLED });
    let cancelled = false;
    const running = create(RunProgressSchema, {
      status: RunStatus.RUNNING,
      stage: RunStage.ANALYZING,
      cancellable: true,
      mode: ReportMode.LIVE,
    });
    const cancelRun = vi.fn(() => {
      cancelled = true;
      return {
        progress: create(RunProgressSchema, { status: RunStatus.CANCELLED }),
      };
    });
    renderWith(analysis, {
      getRunProgress: () =>
        cancelled ? completeProgress(analysis) : { progress: running },
      cancelRun,
    });
    const trigger = await screen.findByRole("button", {
      name: "Cancel analysis",
    });
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "Cancel analysis?" });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Keep running" }),
    );
    expect(cancelRun).not.toHaveBeenCalled();
    fireEvent.click(trigger);
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Cancel analysis",
      }),
    );
    await waitFor(() => expect(cancelRun).toHaveBeenCalledOnce());
    expect(await screen.findByText("Cancelled")).toBeTruthy();
    expect(screen.getByText("The analysis was cancelled.")).toBeTruthy();
  });

  it("hides cancel when the run is not cancellable", async () => {
    const analysis = fixtureAnalysis();
    renderWith(analysis, {
      getRunProgress: () => ({
        progress: create(RunProgressSchema, {
          status: RunStatus.RUNNING,
          stage: RunStage.SAVING,
          cancellable: false,
        }),
      }),
    });
    expect(await screen.findByText("Saving results")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Cancel analysis" }),
    ).toBeNull();
  });

  it("shows Connect errors with the bundlewhy code and recovery", async () => {
    renderWith(fixtureAnalysis(), {
      getRunProgress: () => {
        throw new ConnectError(
          "BW_ACCESS_TOKEN_INVALID: The token was rejected.",
          Code.Unauthenticated,
        );
      },
    });
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("BW_ACCESS_TOKEN_INVALID")).toBeTruthy();
    expect(within(alert).getByText("The token was rejected.")).toBeTruthy();
    expect(
      within(alert).getByText("Connect code: unauthenticated"),
    ).toBeTruthy();
    expect(alert.textContent).toContain(
      "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.",
    );
  });
});

describe("header dialogs", () => {
  it("exports without sources and closes on Escape with focus restored", async () => {
    const exportReport = vi.fn((request: { includeSources: boolean }) => ({
      fileName: "bundlewhy-report.html",
      sourcesIncluded: request.includeSources,
      sizeBytes: 2048,
    }));
    await renderAnalysis(fixtureAnalysis(), { exportReport });
    const trigger = screen.getByRole("button", { name: "Export HTML" });
    trigger.focus();
    fireEvent.click(trigger);
    let dialog = screen.getByRole("dialog", { name: "Export HTML report" });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(
      within(dialog).getByText(
        "Reports that include source content may contain secrets from your code. Review before sharing.",
      ),
    ).toBeTruthy();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);

    fireEvent.click(trigger);
    dialog = screen.getByRole("dialog", { name: "Export HTML report" });
    const checkbox = within(dialog).getByRole("checkbox", {
      name: "Include source content",
    });
    expect((checkbox as HTMLInputElement).checked).toBe(true);
    expect(document.activeElement).toBe(checkbox);
    fireEvent.click(checkbox);
    fireEvent.click(within(dialog).getByRole("button", { name: "Export" }));
    expect(
      await within(dialog).findByText("bundlewhy-report.html"),
    ).toBeTruthy();
    expect(exportReport).toHaveBeenCalledOnce();
    expect(exportReport.mock.calls[0][0].includeSources).toBe(false);
    expect(within(dialog).getByText("2.0 KiB")).toBeTruthy();
    expect(within(dialog).getByText("Not included")).toBeTruthy();
  });

  it("traps focus inside dialogs", async () => {
    await renderAnalysis();
    fireEvent.click(screen.getByRole("button", { name: "Export HTML" }));
    const dialog = screen.getByRole("dialog");
    const focusable = within(dialog).getAllByRole("button");
    const last = focusable[focusable.length - 1];
    last.focus();
    fireEvent.keyDown(dialog, { key: "Tab" });
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it("lists runs and selects a saved run", async () => {
    const other = fixtureAnalysis({
      runId: "run-old",
      projectName: "storefront-old",
    });
    let active = fixtureAnalysis();
    const selectRun = vi.fn((request: { runId: string }) => {
      if (request.runId === "run-old") active = other;
      return { progress: completeProgress(active).progress };
    });
    renderWith(active, {
      getRunProgress: () => completeProgress(active),
      getAnalysis: () => ({ analysis: active }),
      listRuns: () => ({
        runs: [
          {
            runId: "run-2026-10-13",
            projectName: "storefront",
            status: RunStatus.COMPLETE,
            active: true,
          },
          {
            runId: "run-old",
            projectName: "storefront-old",
            status: RunStatus.PARTIAL,
            active: false,
          },
        ],
      }),
      selectRun,
    });
    await screen.findByRole("heading", { level: 1, name: "storefront" });
    fireEvent.click(screen.getByRole("button", { name: "Open report" }));
    const dialog = screen.getByRole("dialog", { name: "Open report" });
    expect(await within(dialog).findByText("run-old")).toBeTruthy();
    expect(within(dialog).getByText("Current")).toBeTruthy();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Open run run-old" }),
    );
    await waitFor(() => expect(selectRun).toHaveBeenCalledOnce());
    expect(
      await screen.findByRole("heading", { level: 1, name: "storefront-old" }),
    ).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("chunk sizes", () => {
  it("shows measured sizes with attribution and the not-savings caption", async () => {
    await renderAnalysis();
    fireEvent.click(screen.getByRole("tab", { name: "Chunk sizes" }));
    const table = screen.getByRole("table", {
      name: /Measured sizes of emitted chunks/,
    });
    expect(table.textContent).toContain(
      "These totals are not savings attributable to any single diagnostic.",
    );
    const rows = within(table).getAllByRole("row");
    const first = rows[1];
    expect(within(first).getByText("assets/index.js")).toBeTruthy();
    expect(within(first).getByText("Yes")).toBeTruthy();
    expect(within(first).getByText("20.0 KiB").getAttribute("title")).toBe(
      "20,480 bytes",
    );
    expect(within(first).getByText("6.8 KiB")).toBeTruthy();
    expect(within(first).getByText("6.0 KiB")).toBeTruthy();
    expect(within(first).getByText("18.6 KiB").getAttribute("title")).toBe(
      "19,096 bytes",
    );
    expect(
      within(first).getByText("node_modules/lodash/lodash.js"),
    ).toBeTruthy();
    expect(within(first).getByText("Unmapped", { exact: false })).toBeTruthy();
    const second = rows[2];
    expect(within(second).getByText("Attribution unavailable")).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/potential savings/i);
  });
});

describe("App boot", () => {
  it("shows BW_ACCESS_TOKEN_MISSING without a token", () => {
    render(<App />);
    const alert = screen.getByRole("alert");
    expect(within(alert).getByText("BW_ACCESS_TOKEN_MISSING")).toBeTruthy();
    expect(alert.textContent).toContain(
      "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.",
    );
  });

  function embed(text: string) {
    const script = document.createElement("script");
    script.type = "application/json";
    script.id = "bundlewhy-report";
    script.textContent = text;
    document.head.appendChild(script);
  }

  it("renders an exported report without network access", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const analysis = fixtureAnalysis({
      coverage: [
        {
          category: OptimizationCategory.TREE_SHAKING,
          state: CoverageState.COVERED,
        },
      ],
    });
    analysis.diagnostics[0].status = EvidenceStatus.CONFIRMED;
    embed(
      JSON.stringify({
        analysis: toJson(AnalysisSchema, analysis),
        sources: [toJson(SourceFileSchema, fixtureSource())],
      }),
    );
    render(<App />);
    await screen.findByRole("heading", { level: 1, name: "storefront" });
    const exportButton = screen.getByRole("button", { name: "Export HTML" });
    expect((exportButton as HTMLButtonElement).disabled).toBe(true);
    expect(
      screen.getByText("Export is not available in an exported report."),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Open report" }));
    const dialog = screen.getByRole("dialog", { name: "Open report" });
    expect(
      within(dialog).getByText(/Exported reports contain one run/),
    ).toBeTruthy();
    expect(within(dialog).getByText("bundlewhy open <run-dir>")).toBeTruthy();
    fireEvent.keyDown(dialog, { key: "Escape" });
    fireEvent.click(screen.getByRole("button", { name: "View full source" }));
    expect(await screen.findByText("// header")).toBeTruthy();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("shows BW_RESULT_INVALID for an invalid embedded report", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    embed("{ broken");
    render(<App />);
    const alert = screen.getByRole("alert");
    expect(within(alert).getByText("BW_RESULT_INVALID")).toBeTruthy();
    expect(alert.textContent).toContain("Export the report again");
  });

  it("shows BW_SCHEMA_UNSUPPORTED for an unknown schema version", () => {
    embed(
      JSON.stringify({
        analysis: {
          ...(toJson(AnalysisSchema, fixtureAnalysis()) as object),
          schemaVersion: 2,
        },
        sources: [],
      }),
    );
    render(<App />);
    expect(
      within(screen.getByRole("alert")).getByText("BW_SCHEMA_UNSUPPORTED"),
    ).toBeTruthy();
  });
});

describe("export usage in diagnostic details", () => {
  function embedReport(analysis: Analysis) {
    const script = document.createElement("script");
    script.type = "application/json";
    script.id = "bundlewhy-report";
    script.textContent = JSON.stringify({
      analysis: toJson(AnalysisSchema, analysis),
      sources: [toJson(SourceFileSchema, fixtureSource())],
    });
    document.head.appendChild(script);
  }

  function exportUsageText(): string {
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    const heading = within(details).getByRole("heading", {
      level: 3,
      name: "Export usage paths",
    });
    return heading.closest("section")!.textContent!;
  }

  it("shows the section after the module dependency paths", async () => {
    const analysis = fixtureAnalysis();
    analysis.diagnostics[0].exportUsage = fixtureExportUsage();
    await renderAnalysis(analysis);
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    const headings = within(details)
      .getAllByRole("heading", { level: 3 })
      .map((heading) => heading.textContent);
    expect(headings.indexOf("Export usage paths")).toBe(
      headings.indexOf("Dependency paths") + 1,
    );
    expect(exportUsageText()).toContain("Observed by Rspack");
    // The module dependency path is still shown.
    expect(
      within(details).getByRole("list", {
        name: "Dependency path 1, from entry to module",
      }),
    ).toBeTruthy();
  });

  it("omits the section for reports saved without export usage", async () => {
    await renderAnalysis(fixtureAnalysis());
    const details = screen.getByRole("region", { name: "Diagnostic details" });
    expect(
      within(details).queryByRole("heading", { name: "Export usage paths" }),
    ).toBeNull();
    expect(within(details).getByText("Dependency paths")).toBeTruthy();
  });

  it("shows the same paths in a live view and in an exported report", async () => {
    const analysis = fixtureAnalysis();
    analysis.diagnostics[0].exportUsage = fixtureExportUsage();
    const live = await renderAnalysis(analysis);
    const liveText = exportUsageText();
    live.unmount();

    embedReport(analysis);
    render(<App />);
    await screen.findByRole("heading", { level: 1, name: "storefront" });
    expect(exportUsageText()).toBe(liveText);
  });

  it("parses export usage JSON written by the Rust analysis", async () => {
    // Shape of `Diagnostic.exportUsage` as bundlewhy serializes it (proto3
    // JSON names, empty arrays for module scope).
    const rustJson = {
      state: "EXPORT_USAGE_STATE_AVAILABLE",
      note: "",
      paths: [
        {
          edges: [
            {
              consumer: {
                module: "src/main.js",
                exportPath: [],
                scope: "EXPORT_SCOPE_MODULE",
              },
              provider: {
                module: "src/exports/namespace.js",
                exportPath: ["viaNamespace"],
                scope: "EXPORT_SCOPE_NAMED",
              },
              dependencyId: "28",
              location: "29:52-64",
            },
            {
              consumer: {
                module: "src/exports/namespace.js",
                exportPath: ["viaNamespace"],
                scope: "EXPORT_SCOPE_NAMED",
              },
              provider: {
                module: "src/exports/leaf.js",
                exportPath: [],
                scope: "EXPORT_SCOPE_MODULE",
              },
              dependencyId: "51",
              location: "4:10-20",
            },
          ],
          end: "EXPORT_USAGE_PATH_END_ROOT",
        },
      ],
      truncation: "EXPORT_USAGE_TRUNCATION_NONE",
    };
    const base = toJson(AnalysisSchema, fixtureAnalysis()) as {
      diagnostics: Record<string, JsonValue>[];
    };
    base.diagnostics[0].exportUsage = rustJson;
    const analysis = fromJson(AnalysisSchema, base as unknown as JsonValue);
    await renderAnalysis(analysis);
    const text = exportUsageText();
    expect(text).toContain("src/exports/namespace.js");
    expect(text).toContain("export viaNamespace");
    expect(text).toContain("Dependency ID 51");
    expect(text).toContain("module or namespace scope (no named export)");
  });
});
