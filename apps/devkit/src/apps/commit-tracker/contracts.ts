export enum GitProviderKind {
  GitHub = "GIT_PROVIDER_KIND_GITHUB",
  GitLab = "GIT_PROVIDER_KIND_GITLAB",
  Bitbucket = "GIT_PROVIDER_KIND_BITBUCKET",
}

export enum MetricValueKind {
  UnitNumber = "METRIC_VALUE_KIND_UNIT_NUMBER",
  Ratio = "METRIC_VALUE_KIND_RATIO",
  DeltaOnly = "METRIC_VALUE_KIND_DELTA_ONLY",
  BooleanGate = "METRIC_VALUE_KIND_BOOLEAN_GATE",
  Histogram = "METRIC_VALUE_KIND_HISTOGRAM",
  Percentiles = "METRIC_VALUE_KIND_PERCENTILES",
}

export enum MetricDirection {
  IncreaseIsBetter = "METRIC_DIRECTION_INCREASE_IS_BETTER",
  DecreaseIsBetter = "METRIC_DIRECTION_DECREASE_IS_BETTER",
}

export enum EvaluationLevel {
  Pass = "EVALUATION_LEVEL_PASS",
  Warn = "EVALUATION_LEVEL_WARN",
  Fail = "EVALUATION_LEVEL_FAIL",
  Neutral = "EVALUATION_LEVEL_NEUTRAL",
}

export interface MetricSeriesPoint {
  metricKey: string;
  displayName: string;
  unit: string;
  valueKind: MetricValueKind;
  direction: MetricDirection;
  warningThresholdPercent: number;
  failThresholdPercent: number;
  commitSha: string;
  runId: string;
  value: number;
  measuredAt?: string;
}

export interface ListMetricSeriesResponse {
  points: MetricSeriesPoint[];
}

export interface MetricComparison {
  metricKey: string;
  displayName: string;
  unit: string;
  valueKind: MetricValueKind;
  direction: MetricDirection;
  warningThresholdPercent: number;
  failThresholdPercent: number;
  baseValue: number;
  headValue: number;
  delta: number;
  deltaPercent: number;
  evaluationLevel: EvaluationLevel;
  hasBaseValue: boolean;
  hasHeadValue: boolean;
}

export interface PullRequestComparisonResponse {
  provider: GitProviderKind;
  repository: string;
  baseCommitSha: string;
  headCommitSha: string;
  environment: string;
  comparisons: MetricComparison[];
  aggregateEvaluation: EvaluationLevel;
}

export interface PublishPullRequestReportResponse {
  aggregateEvaluation: EvaluationLevel;
  markdown: string;
  commentUrl: string;
  statusUrl: string;
}

export interface SeriesQuery {
  provider: GitProviderKind;
  repository: string;
  branch: string;
  environment: string;
  metricKey?: string;
  fromTime?: string;
  toTime?: string;
  limit?: number;
}

export interface ComparisonQuery {
  provider: GitProviderKind;
  repository: string;
  baseCommitSha: string;
  headCommitSha: string;
  environment: string;
  metricKeys?: string[];
}

export interface PublishReportRequest extends ComparisonQuery {
  pullRequest: number;
}
