"use client";

import { FormEvent, useMemo, useState } from "react";

import {
  getPullRequestComparison,
  listMetricSeries,
  publishPullRequestReport,
} from "@/apps/commit-tracker/api-client";
import {
  EvaluationLevel,
  GitProviderKind,
  MetricComparison,
  MetricSeriesPoint,
  PullRequestComparisonResponse,
} from "@/apps/commit-tracker/contracts";
import { DevkitMiniAppId } from "@/lib/mini-app-registry";
import { LogEvent, logError, logInfo } from "@/lib/logger";

function verdictLabel(level: EvaluationLevel): string {
  switch (level) {
    case EvaluationLevel.Pass:
      return "PASS";
    case EvaluationLevel.Warn:
      return "WARN";
    case EvaluationLevel.Fail:
      return "FAIL";
    case EvaluationLevel.Neutral:
      return "NEUTRAL";
    default:
      return level;
  }
}

function formatNumber(value: number): string {
  return Number.isFinite(value) ? value.toFixed(3) : "-";
}

function formatDeltaPercent(value: number): string {
  return Number.isFinite(value) ? `${value.toFixed(2)}%` : "-";
}

function verdictBadgeClass(level: EvaluationLevel): string {
  switch (level) {
    case EvaluationLevel.Pass:
      return "dk-ct-badge-pass";
    case EvaluationLevel.Warn:
      return "dk-ct-badge-warn";
    case EvaluationLevel.Fail:
      return "dk-ct-badge-fail";
    case EvaluationLevel.Neutral:
      return "dk-ct-badge-neutral";
    default:
      return "dk-ct-badge-neutral";
  }
}

function verdictRowClass(level: EvaluationLevel): string {
  switch (level) {
    case EvaluationLevel.Pass:
      return "dk-ct-row-pass";
    case EvaluationLevel.Warn:
      return "dk-ct-row-warn";
    case EvaluationLevel.Fail:
      return "dk-ct-row-fail";
    case EvaluationLevel.Neutral:
      return "dk-ct-row-neutral";
    default:
      return "dk-ct-row-neutral";
  }
}

function deltaClass(value: number): string {
  if (!Number.isFinite(value) || value === 0) {
    return "dk-ct-delta-neutral";
  }
  return value > 0 ? "dk-ct-delta-positive" : "dk-ct-delta-negative";
}

export function CommitTrackerApp() {
  const [provider, setProvider] = useState<GitProviderKind>(GitProviderKind.GitHub);
  const [repository, setRepository] = useState<string>("acme/repo");
  const [branch, setBranch] = useState<string>("main");
  const [environment, setEnvironment] = useState<string>("ci");
  const [metricKey, setMetricKey] = useState<string>("");
  const [fromTime, setFromTime] = useState<string>("");
  const [toTime, setToTime] = useState<string>("");
  const [limit, setLimit] = useState<number>(50);

  const [baseCommitSha, setBaseCommitSha] = useState<string>("");
  const [headCommitSha, setHeadCommitSha] = useState<string>("");
  const [metricKeysInput, setMetricKeysInput] = useState<string>("");
  const [pullRequest, setPullRequest] = useState<number>(1);

  const [series, setSeries] = useState<MetricSeriesPoint[]>([]);
  const [comparison, setComparison] = useState<PullRequestComparisonResponse | null>(
    null,
  );
  const [reportMessage, setReportMessage] = useState<string>("");
  const [errorMessage, setErrorMessage] = useState<string>("");

  const [loadingSeries, setLoadingSeries] = useState<boolean>(false);
  const [loadingComparison, setLoadingComparison] = useState<boolean>(false);
  const [publishing, setPublishing] = useState<boolean>(false);

  const metricKeys = useMemo(
    () =>
      metricKeysInput
        .split(",")
        .map((value) => value.trim())
        .filter((value) => value.length > 0),
    [metricKeysInput],
  );

  const handleLoadSeries = async (event: FormEvent) => {
    event.preventDefault();
    setLoadingSeries(true);
    setErrorMessage("");
    setReportMessage("");

    try {
      const response = await listMetricSeries({
        provider,
        repository,
        branch,
        environment,
        metricKey: metricKey.trim() || undefined,
        fromTime: fromTime.trim() || undefined,
        toTime: toTime.trim() || undefined,
        limit,
      });
      setSeries(response.points);
      logInfo({
        event: LogEvent.CommitTrackerSeriesLoad,
        route: "/apps/commit-tracker",
        miniAppId: DevkitMiniAppId.CommitTracker,
        provider,
        outcome: "success",
        message: "Loaded commit tracker metric series.",
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to load metric series.";
      setErrorMessage(message);
      logError({
        event: LogEvent.CommitTrackerSeriesLoad,
        route: "/apps/commit-tracker",
        miniAppId: DevkitMiniAppId.CommitTracker,
        provider,
        outcome: "failed",
        message,
        error,
      });
    } finally {
      setLoadingSeries(false);
    }
  };

  const handleLoadComparison = async (event: FormEvent) => {
    event.preventDefault();
    setLoadingComparison(true);
    setErrorMessage("");
    setReportMessage("");

    try {
      const response = await getPullRequestComparison({
        provider,
        repository,
        baseCommitSha: baseCommitSha.trim(),
        headCommitSha: headCommitSha.trim(),
        environment,
        metricKeys,
      });
      setComparison(response);
      logInfo({
        event: LogEvent.CommitTrackerComparisonLoad,
        route: "/apps/commit-tracker",
        miniAppId: DevkitMiniAppId.CommitTracker,
        provider,
        outcome: "success",
        message: "Loaded pull request comparison.",
      });
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : "Failed to load pull request comparison.";
      setErrorMessage(message);
      logError({
        event: LogEvent.CommitTrackerComparisonLoad,
        route: "/apps/commit-tracker",
        miniAppId: DevkitMiniAppId.CommitTracker,
        provider,
        outcome: "failed",
        message,
        error,
      });
    } finally {
      setLoadingComparison(false);
    }
  };

  const handlePublishReport = async (event: FormEvent) => {
    event.preventDefault();
    setPublishing(true);
    setErrorMessage("");
    setReportMessage("");

    try {
      const response = await publishPullRequestReport({
        provider,
        repository,
        pullRequest,
        baseCommitSha: baseCommitSha.trim(),
        headCommitSha: headCommitSha.trim(),
        environment,
        metricKeys,
      });

      setReportMessage(
        `Published report. Comment: ${response.commentUrl} | Status: ${response.statusUrl}`,
      );
      logInfo({
        event: LogEvent.CommitTrackerReportPublish,
        route: "/apps/commit-tracker",
        miniAppId: DevkitMiniAppId.CommitTracker,
        provider,
        outcome: "success",
        message: "Published pull request report.",
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Failed to publish pull request report.";
      setErrorMessage(message);
      logError({
        event: LogEvent.CommitTrackerReportPublish,
        route: "/apps/commit-tracker",
        miniAppId: DevkitMiniAppId.CommitTracker,
        provider,
        outcome: "failed",
        message,
        error,
      });
    } finally {
      setPublishing(false);
    }
  };

  return (
    <section aria-label="commit tracker dashboard" className="dk-stack dk-ct-root">
      <section className="dk-card dk-ct-header">
        <p className="dk-eyebrow">Operational Dashboard</p>
        <h2 className="dk-section-title">Commit Tracker Dashboard</h2>
        <p className="dk-paragraph">
          Track commit-level metrics, compare base/head commits, and publish provider
          reports for pull requests.
        </p>
      </section>

      <form onSubmit={handleLoadSeries} className="dk-card">
        <fieldset className="dk-fieldset">
          <legend className="dk-fieldset-legend">Filters</legend>
          <div className="dk-form-grid dk-ct-form-grid">
            <label className="dk-field">
              Provider
              <select
                className="dk-select"
                value={provider}
                onChange={(event) => setProvider(event.target.value as GitProviderKind)}
              >
                <option value={GitProviderKind.GitHub}>GitHub</option>
                <option value={GitProviderKind.GitLab}>GitLab</option>
                <option value={GitProviderKind.Bitbucket}>Bitbucket</option>
              </select>
            </label>
            <label className="dk-field">
              Repository
              <input
                className="dk-input"
                value={repository}
                onChange={(event) => setRepository(event.target.value)}
              />
            </label>
            <label className="dk-field">
              Branch
              <input
                className="dk-input"
                value={branch}
                onChange={(event) => setBranch(event.target.value)}
              />
            </label>
            <label className="dk-field">
              Environment
              <input
                className="dk-input"
                value={environment}
                onChange={(event) => setEnvironment(event.target.value)}
              />
            </label>
            <label className="dk-field">
              Metric Key
              <input
                className="dk-input"
                value={metricKey}
                onChange={(event) => setMetricKey(event.target.value)}
                placeholder="optional"
              />
            </label>
            <label className="dk-field">
              From Time (ISO)
              <input
                className="dk-input"
                value={fromTime}
                onChange={(event) => setFromTime(event.target.value)}
                placeholder="2026-01-01T00:00:00Z"
              />
            </label>
            <label className="dk-field">
              To Time (ISO)
              <input
                className="dk-input"
                value={toTime}
                onChange={(event) => setToTime(event.target.value)}
                placeholder="2026-01-31T23:59:59Z"
              />
            </label>
            <label className="dk-field">
              Limit
              <input
                className="dk-input"
                type="number"
                min={1}
                max={500}
                value={limit}
                onChange={(event) => setLimit(Number(event.target.value) || 50)}
              />
            </label>
          </div>
          <div className="dk-button-group dk-ct-actions">
            <button type="submit" className="dk-button" disabled={loadingSeries}>
              {loadingSeries ? "Loading series..." : "Load Metric Series"}
            </button>
          </div>
        </fieldset>
      </form>

      <form onSubmit={handleLoadComparison} className="dk-card">
        <fieldset className="dk-fieldset">
          <legend className="dk-fieldset-legend">Pull Request Comparison</legend>
          <div className="dk-form-grid dk-ct-form-grid">
            <label className="dk-field">
              Base Commit
              <input
                className="dk-input"
                value={baseCommitSha}
                onChange={(event) => setBaseCommitSha(event.target.value)}
              />
            </label>
            <label className="dk-field">
              Head Commit
              <input
                className="dk-input"
                value={headCommitSha}
                onChange={(event) => setHeadCommitSha(event.target.value)}
              />
            </label>
            <label className="dk-field">
              Metric Keys (comma-separated)
              <input
                className="dk-input"
                value={metricKeysInput}
                onChange={(event) => setMetricKeysInput(event.target.value)}
                placeholder="binary-size,cpu-ms"
              />
            </label>
          </div>
          <div className="dk-button-group dk-ct-actions">
            <button type="submit" className="dk-button" disabled={loadingComparison}>
              {loadingComparison ? "Loading comparison..." : "Compare Pull Request"}
            </button>
          </div>
        </fieldset>
      </form>

      <form onSubmit={handlePublishReport} className="dk-card">
        <fieldset className="dk-fieldset">
          <legend className="dk-fieldset-legend">Publish Report</legend>
          <label className="dk-field dk-ct-compact-field">
            Pull Request Number
            <input
              className="dk-input"
              type="number"
              min={1}
              value={pullRequest}
              onChange={(event) => setPullRequest(Number(event.target.value) || 1)}
            />
          </label>
          <div className="dk-button-group dk-ct-actions">
            <button type="submit" className="dk-button" disabled={publishing}>
              {publishing ? "Publishing..." : "Publish Report to GitHub"}
            </button>
          </div>
        </fieldset>
      </form>

      {errorMessage ? (
        <p role="alert" className="dk-alert">
          {errorMessage}
        </p>
      ) : null}
      {reportMessage ? (
        <p role="status" className="dk-success">
          {reportMessage}
        </p>
      ) : null}

      <section aria-label="metric series" className="dk-card">
        <h3 className="dk-subsection-title">Metric Series</h3>
        {series.length === 0 ? (
          <p className="dk-empty">No series loaded.</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table">
              <thead>
                <tr>
                  <th>Metric</th>
                  <th>Commit</th>
                  <th>Run</th>
                  <th className="dk-ct-num">Value</th>
                  <th>Measured At</th>
                </tr>
              </thead>
              <tbody>
                {series.map((point) => (
                  <tr key={`${point.metricKey}-${point.commitSha}-${point.runId}`}>
                    <td className="dk-ct-metric-cell">{point.metricKey}</td>
                    <td>
                      <code className="dk-mono">{point.commitSha}</code>
                    </td>
                    <td>
                      <code className="dk-mono">{point.runId}</code>
                    </td>
                    <td className="dk-ct-num">{formatNumber(point.value)}</td>
                    <td>{point.measuredAt || "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section aria-label="pull request comparison" className="dk-card">
        <h3 className="dk-subsection-title">Pull Request Comparison</h3>
        {!comparison ? (
          <p className="dk-empty">No comparison loaded.</p>
        ) : (
          <>
            <p className="dk-status dk-ct-summary">
              Aggregate Verdict:{" "}
              <span
                className={`dk-ct-verdict-badge ${verdictBadgeClass(
                  comparison.aggregateEvaluation,
                )}`}
              >
                {verdictLabel(comparison.aggregateEvaluation)}
              </span>
            </p>
            <div className="dk-table-wrap">
              <table className="dk-table">
                <thead>
                  <tr>
                    <th>Metric</th>
                    <th className="dk-ct-num">Base</th>
                    <th className="dk-ct-num">Head</th>
                    <th className="dk-ct-num">Delta</th>
                    <th className="dk-ct-num">Delta %</th>
                    <th>Verdict</th>
                  </tr>
                </thead>
                <tbody>
                  {comparison.comparisons.map((item: MetricComparison) => (
                    <tr key={item.metricKey} className={verdictRowClass(item.evaluationLevel)}>
                      <td className="dk-ct-metric-cell">{item.metricKey}</td>
                      <td className="dk-ct-num">
                        {item.hasBaseValue ? formatNumber(item.baseValue) : "-"}
                      </td>
                      <td className="dk-ct-num">
                        {item.hasHeadValue ? formatNumber(item.headValue) : "-"}
                      </td>
                      <td className={`dk-ct-num ${deltaClass(item.delta)}`}>
                        {formatNumber(item.delta)}
                      </td>
                      <td className={`dk-ct-num ${deltaClass(item.deltaPercent)}`}>
                        {formatDeltaPercent(item.deltaPercent)}
                      </td>
                      <td>
                        <span
                          className={`dk-ct-verdict-badge ${verdictBadgeClass(
                            item.evaluationLevel,
                          )}`}
                        >
                          {verdictLabel(item.evaluationLevel)}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </section>
    </section>
  );
}
