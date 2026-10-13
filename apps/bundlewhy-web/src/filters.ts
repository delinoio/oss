import {
  CoverageState,
  EvidenceStatus,
  OptimizationCategory,
  type Analysis,
  type CategoryCoverage,
  type Diagnostic,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import { ALL_CATEGORIES, ALL_EVIDENCE_STATUSES } from "./labels";

export interface DiagnosticFilters {
  /** `undefined` means all categories. */
  category?: OptimizationCategory;
  /** Empty set means every evidence status. */
  statuses: ReadonlySet<EvidenceStatus>;
  search: string;
}

export const EMPTY_FILTERS: DiagnosticFilters = {
  statuses: new Set(),
  search: "",
};

export function matchesSearch(diagnostic: Diagnostic, search: string): boolean {
  const needle = search.trim().toLowerCase();
  if (!needle) return true;
  const haystack = [
    diagnostic.title,
    diagnostic.summary,
    diagnostic.module,
    diagnostic.rule,
    diagnostic.location?.path ?? "",
    diagnostic.generatedLocation?.path ?? "",
  ];
  return haystack.some((value) => value.toLowerCase().includes(needle));
}

/** Applies the category and search filters, but not the status filter. */
export function filterByCategoryAndSearch(
  diagnostics: readonly Diagnostic[],
  filters: Pick<DiagnosticFilters, "category" | "search">,
): Diagnostic[] {
  return diagnostics.filter(
    (d) =>
      (filters.category === undefined || d.category === filters.category) &&
      matchesSearch(d, filters.search),
  );
}

export function filterDiagnostics(
  diagnostics: readonly Diagnostic[],
  filters: DiagnosticFilters,
): Diagnostic[] {
  return filterByCategoryAndSearch(diagnostics, filters).filter(
    (d) => filters.statuses.size === 0 || filters.statuses.has(d.status),
  );
}

export function countByCategory(
  diagnostics: readonly Diagnostic[],
): Map<OptimizationCategory, number> {
  const counts = new Map<OptimizationCategory, number>();
  for (const category of ALL_CATEGORIES) counts.set(category, 0);
  for (const d of diagnostics)
    counts.set(d.category, (counts.get(d.category) ?? 0) + 1);
  return counts;
}

export function countByStatus(
  diagnostics: readonly Diagnostic[],
): Map<EvidenceStatus, number> {
  const counts = new Map<EvidenceStatus, number>();
  for (const status of ALL_EVIDENCE_STATUSES) counts.set(status, 0);
  for (const d of diagnostics)
    counts.set(d.status, (counts.get(d.status) ?? 0) + 1);
  return counts;
}

export function coverageFor(
  analysis: Pick<Analysis, "coverage">,
  category: OptimizationCategory,
): CategoryCoverage | undefined {
  return analysis.coverage.find((c) => c.category === category);
}

/** Coverage state with missing entries treated as unavailable. */
export function coverageStateFor(
  analysis: Pick<Analysis, "coverage">,
  category: OptimizationCategory,
): CoverageState {
  const state = coverageFor(analysis, category)?.state;
  return state === undefined || state === CoverageState.UNSPECIFIED
    ? CoverageState.UNAVAILABLE
    : state;
}

function hasAnalysisCoverage(state: CoverageState): boolean {
  return state === CoverageState.COVERED || state === CoverageState.PARTIAL;
}

/** Applicable categories whose detectors did not fully run. */
export function categoriesWithoutCoverage(
  analysis: Pick<Analysis, "coverage">,
): OptimizationCategory[] {
  return ALL_CATEGORIES.filter((category) => {
    const state = coverageStateFor(analysis, category);
    return (
      state !== CoverageState.COVERED && state !== CoverageState.NOT_APPLICABLE
    );
  });
}

export enum EmptyState {
  /** The filtered list has entries. */
  None = "none",
  /** No diagnostics at all, and at least one category has coverage. */
  NoFindingsInCoveredCategories = "no-findings-covered",
  /** Diagnostics exist but the filters exclude every one. */
  NoMatches = "no-matches",
  /** No diagnostics and no category has analysis coverage. */
  NoCoverage = "no-coverage",
}

export function emptyStateFor(
  analysis: Pick<Analysis, "coverage" | "diagnostics">,
  filtered: readonly Diagnostic[],
): EmptyState {
  if (filtered.length > 0) return EmptyState.None;
  if (analysis.diagnostics.length > 0) return EmptyState.NoMatches;
  const anyCoverage = ALL_CATEGORIES.some((category) =>
    hasAnalysisCoverage(coverageStateFor(analysis, category)),
  );
  return anyCoverage
    ? EmptyState.NoFindingsInCoveredCategories
    : EmptyState.NoCoverage;
}

export function toggleStatus(
  statuses: ReadonlySet<EvidenceStatus>,
  status: EvidenceStatus,
): Set<EvidenceStatus> {
  const next = new Set(statuses);
  if (next.has(status)) next.delete(status);
  else next.add(status);
  return next;
}
