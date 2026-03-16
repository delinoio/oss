"use client";

import { timestampDate } from "@bufbuild/protobuf/wkt";

import type { MetricSeriesPoint } from "@/gen/committracker/v1/commit_tracker_pb";

export interface MetricSeriesChartProps {
  points: MetricSeriesPoint[];
  isLoading: boolean;
}

function formatDate(point: MetricSeriesPoint): string {
  if (!point.measuredAt) return "";
  try {
    return timestampDate(point.measuredAt).toLocaleDateString();
  } catch {
    return "";
  }
}

export function MetricSeriesChart({ points, isLoading }: MetricSeriesChartProps) {
  if (isLoading) {
    return <p style={{ color: "#64748b" }}>Loading metric series...</p>;
  }

  if (points.length === 0) {
    return <p style={{ color: "#64748b", fontSize: "0.875rem" }}>No data points found for this metric.</p>;
  }

  const values = points.map((p) => p.value);
  const maxVal = Math.max(...values);
  const minVal = Math.min(...values);
  const range = maxVal - minVal || 1;

  const chartHeight = 160;
  const chartWidth = Math.min(points.length * 40, 800);
  const barWidth = Math.max(chartWidth / points.length - 4, 8);

  return (
    <div style={{ overflowX: "auto" }}>
      <div style={{ display: "flex", gap: "0.5rem", alignItems: "center", marginBottom: "0.5rem" }}>
        <span style={{ fontSize: "0.8rem", fontWeight: 500 }}>
          {points[0]?.displayName || points[0]?.metricKey}
        </span>
        {points[0]?.unit && (
          <span style={{ fontSize: "0.75rem", color: "#94a3b8" }}>({points[0].unit})</span>
        )}
      </div>
      <svg
        width={chartWidth}
        height={chartHeight + 40}
        style={{ display: "block" }}
        role="img"
        aria-label="Metric series chart"
      >
        {points.map((point, i) => {
          const barHeight = ((point.value - minVal) / range) * chartHeight;
          const x = i * (chartWidth / points.length) + 2;
          const y = chartHeight - barHeight;
          return (
            <g key={i}>
              <rect
                x={x}
                y={y}
                width={barWidth}
                height={barHeight}
                fill="#3b82f6"
                rx={2}
              >
                <title>{`${point.value.toFixed(2)} (${point.commitSha?.slice(0, 7)})`}</title>
              </rect>
              {i % Math.max(1, Math.floor(points.length / 8)) === 0 && (
                <text
                  x={x + barWidth / 2}
                  y={chartHeight + 16}
                  textAnchor="middle"
                  fontSize="9"
                  fill="#94a3b8"
                >
                  {formatDate(point)}
                </text>
              )}
            </g>
          );
        })}
        <line x1="0" y1={chartHeight} x2={chartWidth} y2={chartHeight} stroke="#e2e8f0" strokeWidth="1" />
      </svg>
      <div style={{ display: "flex", justifyContent: "space-between", fontSize: "0.75rem", color: "#94a3b8", marginTop: "0.25rem" }}>
        <span>Min: {minVal.toFixed(2)}</span>
        <span>Max: {maxVal.toFixed(2)}</span>
        <span>{points.length} data points</span>
      </div>
    </div>
  );
}
