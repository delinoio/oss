import {
  AttributionState,
  Bundler,
  ConditionOutcome,
  CoverageState,
  EngineSupport,
  EvidenceStatus,
  LocationKind,
  OptimizationCategory,
  ProblemSeverity,
  RunStage,
  RunStatus,
  SourceOrigin,
} from "./gen/bundlewhy/v1/bundlewhy_pb";

/** Display order of the optimization categories in the filter column. */
export const ALL_CATEGORIES: readonly OptimizationCategory[] = [
  OptimizationCategory.TREE_SHAKING,
  OptimizationCategory.DEAD_CODE_ELIMINATION,
  OptimizationCategory.INLINING,
  OptimizationCategory.CONSTANT_FOLDING,
  OptimizationCategory.NAME_MANGLING,
  OptimizationCategory.MODULE_CONCATENATION,
  OptimizationCategory.CHUNK_SPLITTING,
  OptimizationCategory.DUPLICATE_MODULES,
];

export const ALL_EVIDENCE_STATUSES: readonly EvidenceStatus[] = [
  EvidenceStatus.CONFIRMED,
  EvidenceStatus.INFERRED,
  EvidenceStatus.UNAVAILABLE,
];

export function categoryLabel(category: OptimizationCategory): string {
  switch (category) {
    case OptimizationCategory.TREE_SHAKING:
      return "Tree shaking";
    case OptimizationCategory.DEAD_CODE_ELIMINATION:
      return "Dead-code elimination";
    case OptimizationCategory.INLINING:
      return "Inlining";
    case OptimizationCategory.CONSTANT_FOLDING:
      return "Constant folding";
    case OptimizationCategory.NAME_MANGLING:
      return "Name mangling";
    case OptimizationCategory.MODULE_CONCATENATION:
      return "Module concatenation";
    case OptimizationCategory.CHUNK_SPLITTING:
      return "Chunk splitting";
    case OptimizationCategory.DUPLICATE_MODULES:
      return "Duplicate modules";
    default:
      return "Uncategorized";
  }
}

export interface StatusPresentation {
  label: string;
  /** Shape that conveys the state without relying on color. */
  icon: string;
  tone: "positive" | "caution" | "negative" | "neutral" | "progress";
}

export function evidenceStatusPresentation(
  status: EvidenceStatus,
): StatusPresentation {
  switch (status) {
    case EvidenceStatus.CONFIRMED:
      return { label: "Confirmed", icon: "●", tone: "positive" };
    case EvidenceStatus.INFERRED:
      return { label: "Inferred", icon: "◆", tone: "caution" };
    case EvidenceStatus.UNAVAILABLE:
      return { label: "Unavailable", icon: "○", tone: "neutral" };
    default:
      return { label: "Unknown", icon: "?", tone: "neutral" };
  }
}

export function evidenceStatusExplanation(status: EvidenceStatus): string {
  switch (status) {
    case EvidenceStatus.CONFIRMED:
      return "Observed in the analysis engine output or configuration";
    case EvidenceStatus.INFERRED:
      return "Derived by bundlewhy from supporting analysis; not an observed engine decision";
    case EvidenceStatus.UNAVAILABLE:
      return "Evidence could not be collected";
    default:
      return "The evidence status was not reported";
  }
}

export function coverageLabel(state: CoverageState | undefined): string {
  switch (state) {
    case CoverageState.COVERED:
      return "Covered";
    case CoverageState.NOT_APPLICABLE:
      return "Not applicable";
    case CoverageState.PARTIAL:
      return "Partial";
    case CoverageState.UNAVAILABLE:
    default:
      return "Unavailable";
  }
}

export function runStatusPresentation(status: RunStatus): StatusPresentation {
  switch (status) {
    case RunStatus.RUNNING:
      return { label: "Running", icon: "↻", tone: "progress" };
    case RunStatus.COMPLETE:
      return { label: "Complete", icon: "✓", tone: "positive" };
    case RunStatus.PARTIAL:
      return { label: "Partial", icon: "◐", tone: "caution" };
    case RunStatus.CANCELLED:
      return { label: "Cancelled", icon: "⊘", tone: "neutral" };
    case RunStatus.FAILED:
      return { label: "Failed", icon: "✕", tone: "negative" };
    default:
      return { label: "Unknown", icon: "?", tone: "neutral" };
  }
}

export function runStageLabel(stage: RunStage): string {
  switch (stage) {
    case RunStage.DETECTING:
      return "Detecting the project";
    case RunStage.BUILDING:
      return "Running the diagnostic build";
    case RunStage.COLLECTING:
      return "Collecting engine output";
    case RunStage.ANALYZING:
      return "Analyzing optimizations";
    case RunStage.MEASURING:
      return "Measuring chunk sizes";
    case RunStage.SAVING:
      return "Saving results";
    case RunStage.FINISHED:
      return "Finished";
    default:
      return "Starting";
  }
}

export function conditionOutcomePresentation(
  outcome: ConditionOutcome,
): StatusPresentation {
  switch (outcome) {
    case ConditionOutcome.MET:
      return { label: "Met", icon: "✓", tone: "positive" };
    case ConditionOutcome.UNMET:
      return { label: "Unmet", icon: "✕", tone: "negative" };
    default:
      return { label: "Unknown", icon: "?", tone: "neutral" };
  }
}

export function problemSeverityLabel(severity: ProblemSeverity): string {
  switch (severity) {
    case ProblemSeverity.ERROR:
      return "Error";
    case ProblemSeverity.WARNING:
      return "Warning";
    default:
      return "Notice";
  }
}

export function bundlerLabel(bundler: Bundler): string {
  switch (bundler) {
    case Bundler.VITE:
      return "Vite";
    case Bundler.RSPACK:
      return "Rspack";
    default:
      return "Unknown bundler";
  }
}

export function engineSupportLabel(support: EngineSupport): string {
  switch (support) {
    case EngineSupport.SUPPORTED:
      return "Supported";
    case EngineSupport.UNSUPPORTED_SELECTED:
      return "Unsupported (selected explicitly)";
    default:
      return "Unknown";
  }
}

export function locationKindLabel(kind: LocationKind): string {
  switch (kind) {
    case LocationKind.ORIGINAL:
      return "Original source";
    case LocationKind.GENERATED:
      return "Generated output";
    default:
      return "Location";
  }
}

export function sourceOriginLabel(origin: SourceOrigin): string {
  switch (origin) {
    case SourceOrigin.PROJECT:
      return "Project source";
    case SourceOrigin.DEPENDENCY:
      return "Dependency";
    case SourceOrigin.VIRTUAL:
      return "Virtual module";
    case SourceOrigin.GENERATED:
      return "Generated";
    default:
      return "Unknown origin";
  }
}

export function attributionSupported(state: AttributionState): boolean {
  return state === AttributionState.SUPPORTED;
}
