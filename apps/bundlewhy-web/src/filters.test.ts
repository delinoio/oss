import { describe, expect, it } from "vitest";
import {
  CoverageState,
  EvidenceStatus,
  OptimizationCategory,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import {
  categoriesWithoutCoverage,
  countByCategory,
  countByStatus,
  coverageStateFor,
  EmptyState,
  emptyStateFor,
  filterDiagnostics,
  toggleStatus,
} from "./filters";
import {
  engineSummary,
  formatBytes,
  formatElapsed,
  formatExactBytes,
  formatLocation,
  splitCodedMessage,
} from "./format";
import {
  ALL_CATEGORIES,
  categoryLabel,
  coverageLabel,
  evidenceStatusPresentation,
  runStageLabel,
} from "./labels";
import { fixtureAnalysis } from "./test-fixtures";

describe("filterDiagnostics", () => {
  const analysis = fixtureAnalysis();
  const ids = (list: { id: string }[]) => list.map((d) => d.id);

  it("returns everything without filters", () => {
    expect(
      ids(
        filterDiagnostics(analysis.diagnostics, {
          statuses: new Set(),
          search: "",
        }),
      ),
    ).toEqual(["d1", "d2", "d3"]);
  });

  it("filters by category", () => {
    expect(
      ids(
        filterDiagnostics(analysis.diagnostics, {
          category: OptimizationCategory.INLINING,
          statuses: new Set(),
          search: "",
        }),
      ),
    ).toEqual(["d3"]);
  });

  it("filters by evidence status set", () => {
    const statuses = toggleStatus(new Set(), EvidenceStatus.INFERRED);
    expect(
      ids(filterDiagnostics(analysis.diagnostics, { statuses, search: "" })),
    ).toEqual(["d2"]);
    expect(toggleStatus(statuses, EvidenceStatus.INFERRED).size).toBe(0);
  });

  it("searches title, summary, module, rule and location path case-insensitively", () => {
    const search = (text: string) =>
      ids(
        filterDiagnostics(analysis.diagnostics, {
          statuses: new Set(),
          search: text,
        }),
      );
    expect(search("LODASH")).toEqual(["d1"]);
    expect(search("namespace object")).toEqual(["d2"]);
    expect(search("oxc/inline")).toEqual(["d3"]);
    expect(search("src/utils.ts")).toEqual(["d2"]);
    expect(search("index.js")).toEqual(["d1"]);
    expect(search("nothing-matches")).toEqual([]);
  });

  it("counts by category and status", () => {
    const byCategory = countByCategory(analysis.diagnostics);
    expect(byCategory.get(OptimizationCategory.TREE_SHAKING)).toBe(1);
    expect(byCategory.get(OptimizationCategory.CHUNK_SPLITTING)).toBe(0);
    const byStatus = countByStatus(analysis.diagnostics);
    expect(byStatus.get(EvidenceStatus.CONFIRMED)).toBe(1);
    expect(byStatus.get(EvidenceStatus.UNAVAILABLE)).toBe(1);
  });
});

describe("coverage and empty states", () => {
  it("treats missing coverage as unavailable", () => {
    const analysis = fixtureAnalysis();
    expect(
      coverageStateFor(analysis, OptimizationCategory.CHUNK_SPLITTING),
    ).toBe(CoverageState.UNAVAILABLE);
    expect(categoriesWithoutCoverage(analysis)).toEqual([
      OptimizationCategory.INLINING,
      OptimizationCategory.NAME_MANGLING,
      OptimizationCategory.CHUNK_SPLITTING,
      OptimizationCategory.DUPLICATE_MODULES,
    ]);
  });

  it("distinguishes covered-without-findings from no coverage", () => {
    const covered = fixtureAnalysis({ diagnostics: [] });
    expect(emptyStateFor(covered, [])).toBe(
      EmptyState.NoFindingsInCoveredCategories,
    );
    const uncovered = fixtureAnalysis({ diagnostics: [], coverage: [] });
    expect(emptyStateFor(uncovered, [])).toBe(EmptyState.NoCoverage);
    const onlyNotApplicable = fixtureAnalysis({
      diagnostics: [],
      coverage: [
        {
          category: OptimizationCategory.CONSTANT_FOLDING,
          state: CoverageState.NOT_APPLICABLE,
        },
      ],
    });
    expect(emptyStateFor(onlyNotApplicable, [])).toBe(EmptyState.NoCoverage);
  });

  it("reports filter exclusion when diagnostics exist", () => {
    const analysis = fixtureAnalysis();
    expect(emptyStateFor(analysis, [])).toBe(EmptyState.NoMatches);
    expect(emptyStateFor(analysis, analysis.diagnostics)).toBe(EmptyState.None);
  });
});

describe("labels and formatting", () => {
  it("labels every category", () => {
    expect(ALL_CATEGORIES.map(categoryLabel)).toEqual([
      "Tree shaking",
      "Dead-code elimination",
      "Inlining",
      "Constant folding",
      "Name mangling",
      "Module concatenation",
      "Chunk splitting",
      "Duplicate modules",
    ]);
    expect(coverageLabel(CoverageState.NOT_APPLICABLE)).toBe("Not applicable");
    expect(coverageLabel(undefined)).toBe("Unavailable");
    expect(evidenceStatusPresentation(EvidenceStatus.INFERRED).label).toBe(
      "Inferred",
    );
    expect(runStageLabel(0)).toBe("Starting");
  });

  it("formats bytes, elapsed time and locations", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(20480)).toBe("20.0 KiB");
    expect(formatBytes(3 * 1024 * 1024)).toBe("3.0 MiB");
    expect(formatExactBytes(20480)).toBe("20,480 bytes");
    expect(formatElapsed(4200)).toBe("4s");
    expect(formatElapsed(65000)).toBe("1m 05s");
    expect(formatLocation({ path: "a.ts", line: 3, column: 0 } as never)).toBe(
      "a.ts:3",
    );
  });

  it("summarizes the engine identity", () => {
    expect(engineSummary(fixtureAnalysis().engine)).toBe(
      "Vite 8.3.4 · Oxc minifier 1.2.13",
    );
  });

  it("splits coded server messages", () => {
    expect(splitCodedMessage("BW_RUN_NOT_FOUND: run is gone")).toEqual({
      code: "BW_RUN_NOT_FOUND",
      message: "run is gone",
    });
    expect(splitCodedMessage("plain")).toEqual({ message: "plain" });
  });
});
