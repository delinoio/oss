"use client";

import {
  EvaluationLevel,
  type MetricComparison,
} from "@/gen/committracker/v1/commit_tracker_pb";

export interface PrComparisonTableProps {
  comparisons: MetricComparison[];
  aggregateEvaluation: EvaluationLevel;
  isLoading: boolean;
}

function evaluationLabel(level: EvaluationLevel): string {
  switch (level) {
    case EvaluationLevel.PASS: return "Pass";
    case EvaluationLevel.WARN: return "Warn";
    case EvaluationLevel.FAIL: return "Fail";
    case EvaluationLevel.NEUTRAL: return "Neutral";
    default: return "-";
  }
}

function evaluationColor(level: EvaluationLevel): string {
  switch (level) {
    case EvaluationLevel.PASS: return "#16a34a";
    case EvaluationLevel.WARN: return "#ea580c";
    case EvaluationLevel.FAIL: return "#dc2626";
    case EvaluationLevel.NEUTRAL: return "#6b7280";
    default: return "#6b7280";
  }
}

function formatDelta(delta: number, deltaPercent: number): string {
  const sign = delta >= 0 ? "+" : "";
  return `${sign}${delta.toFixed(2)} (${sign}${deltaPercent.toFixed(1)}%)`;
}

export function PrComparisonTable({
  comparisons,
  aggregateEvaluation,
  isLoading,
}: PrComparisonTableProps) {
  if (isLoading) {
    return <p style={{ color: "#64748b" }}>Loading comparison...</p>;
  }

  if (comparisons.length === 0) {
    return <p style={{ color: "#64748b", fontSize: "0.875rem" }}>No metric comparisons available.</p>;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
        <h4 style={{ margin: 0 }}>PR Comparison</h4>
        <span
          style={{
            display: "inline-block",
            padding: "0.15rem 0.5rem",
            borderRadius: "999px",
            fontSize: "0.75rem",
            fontWeight: 600,
            color: "#fff",
            backgroundColor: evaluationColor(aggregateEvaluation),
          }}
        >
          {evaluationLabel(aggregateEvaluation)}
        </span>
      </div>

      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.875rem" }}>
        <thead>
          <tr style={{ borderBottom: "2px solid #e2e8f0", textAlign: "left" }}>
            <th style={{ padding: "0.5rem" }}>Metric</th>
            <th style={{ padding: "0.5rem", textAlign: "right" }}>Base</th>
            <th style={{ padding: "0.5rem", textAlign: "right" }}>Head</th>
            <th style={{ padding: "0.5rem", textAlign: "right" }}>Delta</th>
            <th style={{ padding: "0.5rem" }}>Evaluation</th>
          </tr>
        </thead>
        <tbody>
          {comparisons.map((c) => (
            <tr key={c.metricKey} style={{ borderBottom: "1px solid #f1f5f9" }}>
              <td style={{ padding: "0.5rem" }}>
                <div>{c.displayName || c.metricKey}</div>
                {c.unit && <div style={{ fontSize: "0.75rem", color: "#94a3b8" }}>{c.unit}</div>}
              </td>
              <td style={{ padding: "0.5rem", textAlign: "right", fontFamily: "monospace" }}>
                {c.hasBaseValue ? c.baseValue.toFixed(2) : "-"}
              </td>
              <td style={{ padding: "0.5rem", textAlign: "right", fontFamily: "monospace" }}>
                {c.hasHeadValue ? c.headValue.toFixed(2) : "-"}
              </td>
              <td
                style={{
                  padding: "0.5rem",
                  textAlign: "right",
                  fontFamily: "monospace",
                  color: c.delta >= 0 ? "#16a34a" : "#dc2626",
                }}
              >
                {c.hasBaseValue && c.hasHeadValue
                  ? formatDelta(c.delta, c.deltaPercent)
                  : "-"}
              </td>
              <td style={{ padding: "0.5rem" }}>
                <span
                  style={{
                    display: "inline-block",
                    padding: "0.1rem 0.4rem",
                    borderRadius: "4px",
                    fontSize: "0.75rem",
                    fontWeight: 500,
                    color: "#fff",
                    backgroundColor: evaluationColor(c.evaluationLevel),
                  }}
                >
                  {evaluationLabel(c.evaluationLevel)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
