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
    <section aria-label="commit tracker dashboard">
      <h2 style={{ marginTop: 0 }}>Commit Tracker Dashboard</h2>
      <p>
        Track commit-level metrics, compare base/head commits, and publish provider
        reports for pull requests.
      </p>

      <form onSubmit={handleLoadSeries} style={{ marginBottom: "1rem" }}>
        <fieldset style={{ border: "1px solid #d7e2ea", padding: "0.75rem" }}>
          <legend>Filters</legend>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
              gap: "0.75rem",
            }}
          >
            <label>
              Provider
              <select
                value={provider}
                onChange={(event) => setProvider(event.target.value as GitProviderKind)}
                style={{ width: "100%" }}
              >
                <option value={GitProviderKind.GitHub}>GitHub</option>
                <option value={GitProviderKind.GitLab}>GitLab</option>
                <option value={GitProviderKind.Bitbucket}>Bitbucket</option>
              </select>
            </label>
            <label>
              Repository
              <input
                value={repository}
                onChange={(event) => setRepository(event.target.value)}
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Branch
              <input
                value={branch}
                onChange={(event) => setBranch(event.target.value)}
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Environment
              <input
                value={environment}
                onChange={(event) => setEnvironment(event.target.value)}
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Metric Key
              <input
                value={metricKey}
                onChange={(event) => setMetricKey(event.target.value)}
                style={{ width: "100%" }}
                placeholder="optional"
              />
            </label>
            <label>
              From Time (ISO)
              <input
                value={fromTime}
                onChange={(event) => setFromTime(event.target.value)}
                style={{ width: "100%" }}
                placeholder="2026-01-01T00:00:00Z"
              />
            </label>
            <label>
              To Time (ISO)
              <input
                value={toTime}
                onChange={(event) => setToTime(event.target.value)}
                style={{ width: "100%" }}
                placeholder="2026-01-31T23:59:59Z"
              />
            </label>
            <label>
              Limit
              <input
                type="number"
                min={1}
                max={500}
                value={limit}
                onChange={(event) => setLimit(Number(event.target.value) || 50)}
                style={{ width: "100%" }}
              />
            </label>
          </div>
          <div style={{ marginTop: "0.75rem" }}>
            <button type="submit" disabled={loadingSeries}>
              {loadingSeries ? "Loading series..." : "Load Metric Series"}
            </button>
          </div>
        </fieldset>
      </form>

      <form onSubmit={handleLoadComparison} style={{ marginBottom: "1rem" }}>
        <fieldset style={{ border: "1px solid #d7e2ea", padding: "0.75rem" }}>
          <legend>Pull Request Comparison</legend>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
              gap: "0.75rem",
            }}
          >
            <label>
              Base Commit
              <input
                value={baseCommitSha}
                onChange={(event) => setBaseCommitSha(event.target.value)}
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Head Commit
              <input
                value={headCommitSha}
                onChange={(event) => setHeadCommitSha(event.target.value)}
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Metric Keys (comma-separated)
              <input
                value={metricKeysInput}
                onChange={(event) => setMetricKeysInput(event.target.value)}
                style={{ width: "100%" }}
                placeholder="binary-size,cpu-ms"
              />
            </label>
          </div>
          <div style={{ marginTop: "0.75rem" }}>
            <button type="submit" disabled={loadingComparison}>
              {loadingComparison ? "Loading comparison..." : "Compare Pull Request"}
            </button>
          </div>
        </fieldset>
      </form>

      <form onSubmit={handlePublishReport} style={{ marginBottom: "1rem" }}>
        <fieldset style={{ border: "1px solid #d7e2ea", padding: "0.75rem" }}>
          <legend>Publish Report</legend>
          <label>
            Pull Request Number
            <input
              type="number"
              min={1}
              value={pullRequest}
              onChange={(event) => setPullRequest(Number(event.target.value) || 1)}
              style={{ width: "100%", maxWidth: "220px" }}
            />
          </label>
          <div style={{ marginTop: "0.75rem" }}>
            <button type="submit" disabled={publishing}>
              {publishing ? "Publishing..." : "Publish Report to GitHub"}
            </button>
          </div>
        </fieldset>
      </form>

      {errorMessage ? (
        <p role="alert" style={{ color: "#9f1111" }}>
          {errorMessage}
        </p>
      ) : null}
      {reportMessage ? (
        <p role="status" style={{ color: "#0a6627" }}>
          {reportMessage}
        </p>
      ) : null}

      <section aria-label="metric series" style={{ marginBottom: "1rem" }}>
        <h3>Metric Series</h3>
        {series.length === 0 ? (
          <p>No series loaded.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr>
                <th style={{ textAlign: "left", borderBottom: "1px solid #d7e2ea" }}>
                  Metric
                </th>
                <th style={{ textAlign: "left", borderBottom: "1px solid #d7e2ea" }}>
                  Commit
                </th>
                <th style={{ textAlign: "left", borderBottom: "1px solid #d7e2ea" }}>
                  Run
                </th>
                <th style={{ textAlign: "right", borderBottom: "1px solid #d7e2ea" }}>
                  Value
                </th>
                <th style={{ textAlign: "left", borderBottom: "1px solid #d7e2ea" }}>
                  Measured At
                </th>
              </tr>
            </thead>
            <tbody>
              {series.map((point) => (
                <tr key={`${point.metricKey}-${point.commitSha}-${point.runId}`}>
                  <td style={{ padding: "0.35rem 0" }}>{point.metricKey}</td>
                  <td>{point.commitSha}</td>
                  <td>{point.runId}</td>
                  <td style={{ textAlign: "right" }}>{formatNumber(point.value)}</td>
                  <td>{point.measuredAt || "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section aria-label="pull request comparison">
        <h3>Pull Request Comparison</h3>
        {!comparison ? (
          <p>No comparison loaded.</p>
        ) : (
          <>
            <p>
              Aggregate Verdict: <strong>{verdictLabel(comparison.aggregateEvaluation)}</strong>
            </p>
            <table style={{ width: "100%", borderCollapse: "collapse" }}>
              <thead>
                <tr>
                  <th style={{ textAlign: "left", borderBottom: "1px solid #d7e2ea" }}>
                    Metric
                  </th>
                  <th style={{ textAlign: "right", borderBottom: "1px solid #d7e2ea" }}>
                    Base
                  </th>
                  <th style={{ textAlign: "right", borderBottom: "1px solid #d7e2ea" }}>
                    Head
                  </th>
                  <th style={{ textAlign: "right", borderBottom: "1px solid #d7e2ea" }}>
                    Delta
                  </th>
                  <th style={{ textAlign: "right", borderBottom: "1px solid #d7e2ea" }}>
                    Delta %
                  </th>
                  <th style={{ textAlign: "left", borderBottom: "1px solid #d7e2ea" }}>
                    Verdict
                  </th>
                </tr>
              </thead>
              <tbody>
                {comparison.comparisons.map((item: MetricComparison) => (
                  <tr key={item.metricKey}>
                    <td style={{ padding: "0.35rem 0" }}>{item.metricKey}</td>
                    <td style={{ textAlign: "right" }}>
                      {item.hasBaseValue ? formatNumber(item.baseValue) : "-"}
                    </td>
                    <td style={{ textAlign: "right" }}>
                      {item.hasHeadValue ? formatNumber(item.headValue) : "-"}
                    </td>
                    <td style={{ textAlign: "right" }}>{formatNumber(item.delta)}</td>
                    <td style={{ textAlign: "right" }}>{item.deltaPercent.toFixed(2)}%</td>
                    <td>{verdictLabel(item.evaluationLevel)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </section>
    </section>
  );
}
