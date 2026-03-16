"use client";

import { useState } from "react";

import { EvaluationLevel, GitProviderKind } from "@/gen/committracker/v1/commit_tracker_pb";
import { CommitTrackerTransportProvider } from "@/apps/commit-tracker/hooks/use-commit-tracker-transport";
import {
  useListMetricSeries,
  useGetPullRequestComparison,
  type MetricSeriesParams,
  type PrComparisonParams,
} from "@/apps/commit-tracker/hooks/use-commit-tracker-queries";
import { RepoSelector, type RepoSelection } from "./repo-selector";
import { MetricSeriesChart } from "./metric-series-chart";
import { PrComparisonTable } from "./pr-comparison-table";

type CommitTrackerTab = "series" | "comparison";

function CommitTrackerContent() {
  const [selection, setSelection] = useState<RepoSelection | undefined>(undefined);
  const [activeTab, setActiveTab] = useState<CommitTrackerTab>("series");
  const [metricKey, setMetricKey] = useState("bundle_size");
  const [baseCommit, setBaseCommit] = useState("");
  const [headCommit, setHeadCommit] = useState("");

  const seriesParams: MetricSeriesParams | undefined = selection
    ? {
        provider: selection.provider,
        repository: selection.repository,
        branch: selection.branch,
        environment: selection.environment,
        metricKey,
      }
    : undefined;

  const comparisonParams: PrComparisonParams | undefined =
    selection && baseCommit && headCommit
      ? {
          provider: selection.provider,
          repository: selection.repository,
          baseCommitSha: baseCommit,
          headCommitSha: headCommit,
          environment: selection.environment,
        }
      : undefined;

  const { data: seriesData, isLoading: seriesLoading } = useListMetricSeries(seriesParams);
  const { data: comparisonData, isLoading: comparisonLoading } =
    useGetPullRequestComparison(comparisonParams);

  const tabs: { id: CommitTrackerTab; label: string }[] = [
    { id: "series", label: "Metric Series" },
    { id: "comparison", label: "PR Comparison" },
  ];

  const inputStyle = {
    padding: "0.4rem 0.6rem",
    border: "1px solid #d7e2ea",
    borderRadius: "6px",
    fontSize: "0.875rem",
    width: "180px",
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1.5rem" }}>
      <RepoSelector onSelectionChange={setSelection} />

      {selection && (
        <>
          <div style={{ display: "flex", gap: "0.5rem", borderBottom: "1px solid #e2e8f0" }}>
            {tabs.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                style={{
                  padding: "0.5rem 1rem",
                  border: "none",
                  borderBottom: activeTab === tab.id ? "2px solid #0c5fca" : "2px solid transparent",
                  backgroundColor: "transparent",
                  color: activeTab === tab.id ? "#0c5fca" : "#64748b",
                  cursor: "pointer",
                  fontSize: "0.875rem",
                  fontWeight: activeTab === tab.id ? 600 : 400,
                }}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {activeTab === "series" && (
            <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
              <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b", maxWidth: "200px" }}>
                Metric Key
                <input
                  style={inputStyle}
                  value={metricKey}
                  onChange={(e) => setMetricKey(e.target.value)}
                  placeholder="bundle_size"
                />
              </label>
              <MetricSeriesChart
                points={seriesData?.points ?? []}
                isLoading={seriesLoading}
              />
            </div>
          )}

          {activeTab === "comparison" && (
            <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
              <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap" }}>
                <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
                  Base Commit SHA
                  <input
                    style={inputStyle}
                    value={baseCommit}
                    onChange={(e) => setBaseCommit(e.target.value)}
                    placeholder="abc1234..."
                  />
                </label>
                <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
                  Head Commit SHA
                  <input
                    style={inputStyle}
                    value={headCommit}
                    onChange={(e) => setHeadCommit(e.target.value)}
                    placeholder="def5678..."
                  />
                </label>
              </div>
              <PrComparisonTable
                comparisons={comparisonData?.comparisons ?? []}
                aggregateEvaluation={comparisonData?.aggregateEvaluation ?? EvaluationLevel.UNSPECIFIED}
                isLoading={comparisonLoading}
              />
            </div>
          )}
        </>
      )}

      {!selection && (
        <p style={{ color: "#64748b", fontSize: "0.875rem" }}>
          Select a repository above to view commit metrics and PR comparisons.
        </p>
      )}
    </div>
  );
}

export function CommitTrackerApp() {
  return (
    <CommitTrackerTransportProvider>
      <CommitTrackerContent />
    </CommitTrackerTransportProvider>
  );
}
