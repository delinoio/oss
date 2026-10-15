import { create, type MessageInitShape } from "@bufbuild/protobuf";
import {
  AnalysisSchema,
  AttributionState,
  Bundler,
  ConditionOutcome,
  CoverageState,
  EngineSupport,
  EvidenceStatus,
  ExportScope,
  ExportUsagePathEnd,
  ExportUsageSchema,
  ExportUsageState,
  ExportUsageTruncation,
  LocationKind,
  OptimizationCategory,
  ReportMode,
  RunStatus,
  SourceFileSchema,
  SourceFileSummarySchema,
  SourceOrigin,
  SourceRevisionEvidence,
  type Analysis,
} from "./gen/bundlewhy/v1/bundlewhy_pb";

export function fixtureAnalysis(
  overrides: Exclude<MessageInitShape<typeof AnalysisSchema>, Analysis> = {},
): Analysis {
  return create(AnalysisSchema, {
    schemaVersion: 1,
    runId: "run-2026-10-13",
    projectName: "storefront",
    configFile: "vite.config.ts",
    status: RunStatus.COMPLETE,
    mode: ReportMode.SAVED,
    startedAt: "2026-10-13T09:00:00Z",
    finishedAt: "2026-10-13T09:00:42Z",
    durationMs: 42000,
    toolVersion: "0.1.0",
    engine: {
      bundler: Bundler.VITE,
      bundlerPackage: "vite",
      bundlerVersion: "8.3.4",
      minifier: "Oxc",
      minifierVersion: "1.2.13",
      defaultMinifier: true,
      adapterId: "bundlewhy-collector-vite",
      adapterVersion: "0.1.0",
      support: EngineSupport.SUPPORTED,
      supportedVersions: ">=8.3.0 <8.4.0",
      diagnosticEngine: true,
      diagnosticNotice:
        "Findings describe a separate diagnostic build with collection overrides.",
      overrides: ["build.sourcemap=true", "build.reportCompressedSize=false"],
    },
    diagnostics: [
      {
        id: "d1",
        category: OptimizationCategory.TREE_SHAKING,
        rule: "vite/side-effects-retained",
        status: EvidenceStatus.CONFIRMED,
        title: "Side effects keep lodash in the bundle",
        summary: "The package does not declare sideEffects: false.",
        module: "node_modules/lodash/lodash.js",
        dependency: true,
        engine: "vite 8.3.4 + oxc 1.2.13",
        location: {
          kind: LocationKind.ORIGINAL,
          sourceId: "src-1",
          path: "src/main.ts",
          line: 3,
          column: 1,
        },
        generatedLocation: {
          kind: LocationKind.GENERATED,
          path: "dist/assets/index.js",
          line: 120,
          column: 4,
        },
        snippet: {
          kind: LocationKind.ORIGINAL,
          path: "src/main.ts",
          startLine: 2,
          highlightLine: 3,
          text: "import { render } from './render';\nimport _ from 'lodash';\nrender(_);",
        },
        conditions: [
          {
            description: "Package declares sideEffects: false",
            outcome: ConditionOutcome.UNMET,
            detail: "package.json has no sideEffects field",
          },
          {
            description: "Import is an ES module",
            outcome: ConditionOutcome.MET,
            detail: "",
          },
        ],
        evidence: [
          {
            provenance: "rolldown module info",
            detail: "Module marked as having side effects",
            status: EvidenceStatus.CONFIRMED,
          },
        ],
        dependencyPaths: [
          { modules: ["src/main.ts", "node_modules/lodash/lodash.js"] },
        ],
        correctiveAction: {
          direction:
            "Import lodash-es named exports instead of the default export.",
          safety:
            "Only when the code does not rely on lodash global side effects.",
        },
      },
      {
        id: "d2",
        category: OptimizationCategory.MODULE_CONCATENATION,
        rule: "vite/module-concatenation-bailout",
        status: EvidenceStatus.INFERRED,
        title: "Module concatenation bailout",
        summary: "Dynamic access to the namespace object.",
        module: "src/utils.ts",
        location: {
          kind: LocationKind.ORIGINAL,
          path: "src/utils.ts",
          line: 10,
          column: 2,
        },
      },
      {
        id: "d3",
        category: OptimizationCategory.INLINING,
        rule: "oxc/inline-unknown",
        status: EvidenceStatus.UNAVAILABLE,
        title: "Inlining evidence missing",
        summary: "The minifier did not report inlining decisions.",
        module: "src/format.ts",
        location: {
          kind: LocationKind.ORIGINAL,
          path: "src/format.ts",
          line: 1,
          column: 0,
        },
      },
    ],
    coverage: [
      {
        category: OptimizationCategory.TREE_SHAKING,
        state: CoverageState.COVERED,
      },
      {
        category: OptimizationCategory.DEAD_CODE_ELIMINATION,
        state: CoverageState.COVERED,
      },
      {
        category: OptimizationCategory.INLINING,
        state: CoverageState.PARTIAL,
        note: "Only top-level functions were inspected.",
      },
      {
        category: OptimizationCategory.CONSTANT_FOLDING,
        state: CoverageState.NOT_APPLICABLE,
        note: "Not reported by this minifier.",
      },
      {
        category: OptimizationCategory.MODULE_CONCATENATION,
        state: CoverageState.COVERED,
      },
      {
        category: OptimizationCategory.NAME_MANGLING,
        state: CoverageState.UNAVAILABLE,
      },
    ],
    chunks: [
      {
        file: "assets/index.js",
        entry: true,
        rawBytes: 20480,
        gzipBytes: 7000,
        brotliBytes: 6100,
        attribution: AttributionState.SUPPORTED,
        sources: [
          { path: "src/main.ts", generatedBytes: 4096 },
          { path: "node_modules/lodash/lodash.js", generatedBytes: 15000 },
        ],
        unmappedBytes: 1384,
      },
      {
        file: "assets/vendor.js",
        entry: false,
        rawBytes: 900,
        gzipBytes: 400,
        brotliBytes: 350,
        attribution: AttributionState.UNAVAILABLE,
      },
    ],
    problems: [],
    sources: [
      create(SourceFileSummarySchema, {
        id: "src-1",
        path: "src/main.ts",
        origin: SourceOrigin.PROJECT,
        revisionEvidence: SourceRevisionEvidence.BUILD_SNAPSHOT,
      }),
    ],
    ...overrides,
  });
}

export function fixtureSource() {
  return create(SourceFileSchema, {
    id: "src-1",
    path: "src/main.ts",
    origin: SourceOrigin.PROJECT,
    sha256: "abcdef0123456789",
    content:
      "// header\nimport { render } from './render';\nimport _ from 'lodash';\nrender(_);",
    contentIncluded: true,
    stale: true,
    revisionEvidence: SourceRevisionEvidence.BUILD_SNAPSHOT,
  });
}

type ExportReferenceInit = {
  module: string;
  exportPath?: string[];
};

/** Builds an export reference; no export path means module scope. */
export function exportReference({
  module,
  exportPath = [],
}: ExportReferenceInit) {
  return {
    module,
    exportPath,
    scope: exportPath.length > 0 ? ExportScope.NAMED : ExportScope.MODULE,
  };
}

export function exportEdge(
  consumer: ExportReferenceInit,
  provider: ExportReferenceInit,
  dependencyId = "",
  location = "",
) {
  return {
    consumer: exportReference(consumer),
    provider: exportReference(provider),
    dependencyId,
    location,
  };
}

/** A two-edge chain: entry (module scope) -> lib.foo -> shared.bar. */
export function fixtureExportUsage(
  overrides: MessageInitShape<typeof ExportUsageSchema> = {},
) {
  return create(ExportUsageSchema, {
    state: ExportUsageState.AVAILABLE,
    truncation: ExportUsageTruncation.NONE,
    paths: [
      {
        edges: [
          exportEdge(
            { module: "src/main.ts" },
            { module: "src/lib.ts", exportPath: ["foo"] },
            "27",
            "6:13-16",
          ),
          exportEdge(
            { module: "src/lib.ts", exportPath: ["foo"] },
            { module: "src/shared.ts", exportPath: ["bar"] },
            "37",
            "4:32-35",
          ),
        ],
        end: ExportUsagePathEnd.ROOT,
      },
    ],
    ...overrides,
  });
}
