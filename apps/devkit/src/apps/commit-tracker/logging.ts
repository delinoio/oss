import {
  EvaluationLevel,
  GitProviderKind,
  MetricComparison,
} from "@/apps/commit-tracker/contracts";
import { DevkitMiniAppId } from "@/lib/mini-app-registry";
import { DevkitLogEntry, LogEvent, logError, logInfo } from "@/lib/logger";

const COMMIT_TRACKER_ROUTE = "/apps/commit-tracker";

interface CommitTrackerLogContext {
  repository: string;
  pull_request: number;
  commit: string;
  run_id: string;
  metric_key: string;
  evaluation_level: string;
  delta_percent: number;
}

const DEFAULT_COMMIT_TRACKER_CONTEXT: Omit<
  CommitTrackerLogContext,
  "repository"
> = {
  pull_request: 0,
  commit: "",
  run_id: "",
  metric_key: "",
  evaluation_level: "",
  delta_percent: 0,
};

interface CommitTrackerLogContextInput {
  repository: string;
  pullRequest?: number;
  commit?: string;
  runId?: string;
  metricKey?: string;
  evaluationLevel?: EvaluationLevel | "";
  deltaPercent?: number;
}

interface CommitTrackerLogInput extends CommitTrackerLogContextInput {
  event: LogEvent;
  provider: GitProviderKind;
  outcome: "success" | "failed";
  message: string;
  error?: unknown;
}

function buildCommitTrackerContext(
  input: CommitTrackerLogContextInput,
): CommitTrackerLogContext {
  const pullRequest = input.pullRequest;
  const deltaPercent = input.deltaPercent;

  return {
    repository: input.repository,
    pull_request:
      typeof pullRequest === "number" && Number.isFinite(pullRequest)
        ? pullRequest
        : DEFAULT_COMMIT_TRACKER_CONTEXT.pull_request,
    commit: input.commit ?? DEFAULT_COMMIT_TRACKER_CONTEXT.commit,
    run_id: input.runId ?? DEFAULT_COMMIT_TRACKER_CONTEXT.run_id,
    metric_key: input.metricKey ?? DEFAULT_COMMIT_TRACKER_CONTEXT.metric_key,
    evaluation_level:
      input.evaluationLevel ?? DEFAULT_COMMIT_TRACKER_CONTEXT.evaluation_level,
    delta_percent:
      typeof deltaPercent === "number" && Number.isFinite(deltaPercent)
        ? deltaPercent
        : DEFAULT_COMMIT_TRACKER_CONTEXT.delta_percent,
  };
}

function toCommitTrackerLogEntry(input: CommitTrackerLogInput): DevkitLogEntry {
  return {
    event: input.event,
    route: COMMIT_TRACKER_ROUTE,
    miniAppId: DevkitMiniAppId.CommitTracker,
    provider: input.provider,
    outcome: input.outcome,
    message: input.message,
    error: input.error,
    context: buildCommitTrackerContext(input),
  };
}

export function extractSingleMetricKey(metricKeys: string[]): string {
  return metricKeys.length === 1 ? metricKeys[0] : "";
}

export function extractSingleComparisonDeltaPercent(
  comparisons: MetricComparison[],
): number {
  return comparisons.length === 1
    ? comparisons[0].deltaPercent
    : DEFAULT_COMMIT_TRACKER_CONTEXT.delta_percent;
}

export function logCommitTrackerInfo(
  input: Omit<CommitTrackerLogInput, "error">,
): void {
  logInfo(toCommitTrackerLogEntry(input));
}

export function logCommitTrackerError(
  input: CommitTrackerLogInput & { error: unknown },
): void {
  logError(toCommitTrackerLogEntry(input));
}
