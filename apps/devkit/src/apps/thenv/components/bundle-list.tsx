"use client";

import { timestampDate } from "@bufbuild/protobuf/wkt";

import { BundleStatus, FileType, type BundleVersionSummary } from "@/gen/thenv/v1/thenv_pb";

export interface BundleListProps {
  versions: BundleVersionSummary[];
  isLoading: boolean;
  onSelect: (versionId: string) => void;
  selectedVersionId?: string;
}

function statusLabel(status: BundleStatus): string {
  switch (status) {
    case BundleStatus.ACTIVE: return "Active";
    case BundleStatus.ARCHIVED: return "Archived";
    default: return "Unknown";
  }
}

function statusColor(status: BundleStatus): string {
  switch (status) {
    case BundleStatus.ACTIVE: return "#16a34a";
    case BundleStatus.ARCHIVED: return "#9ca3af";
    default: return "#6b7280";
  }
}

function fileTypeLabel(ft: FileType): string {
  switch (ft) {
    case FileType.ENV: return ".env";
    case FileType.DEV_VARS: return ".dev.vars";
    default: return "unknown";
  }
}

function formatTime(ts: BundleVersionSummary["createdAt"]): string {
  if (!ts) return "-";
  try {
    return timestampDate(ts).toLocaleString();
  } catch {
    return "-";
  }
}

export function BundleList({ versions, isLoading, onSelect, selectedVersionId }: BundleListProps) {
  if (isLoading) {
    return <p style={{ color: "#64748b" }}>Loading versions...</p>;
  }

  if (versions.length === 0) {
    return <p style={{ color: "#64748b" }}>No bundle versions found. Push your first bundle to get started.</p>;
  }

  return (
    <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.875rem" }}>
      <thead>
        <tr style={{ borderBottom: "2px solid #e2e8f0", textAlign: "left" }}>
          <th style={{ padding: "0.5rem" }}>Version ID</th>
          <th style={{ padding: "0.5rem" }}>Status</th>
          <th style={{ padding: "0.5rem" }}>Files</th>
          <th style={{ padding: "0.5rem" }}>Created By</th>
          <th style={{ padding: "0.5rem" }}>Created At</th>
        </tr>
      </thead>
      <tbody>
        {versions.map((v) => (
          <tr
            key={v.bundleVersionId}
            onClick={() => onSelect(v.bundleVersionId)}
            style={{
              borderBottom: "1px solid #f1f5f9",
              cursor: "pointer",
              backgroundColor: v.bundleVersionId === selectedVersionId ? "#eff6ff" : "transparent",
            }}
          >
            <td style={{ padding: "0.5rem", fontFamily: "monospace", fontSize: "0.8rem" }}>
              {v.bundleVersionId.slice(0, 12)}...
            </td>
            <td style={{ padding: "0.5rem" }}>
              <span
                style={{
                  display: "inline-block",
                  padding: "0.15rem 0.5rem",
                  borderRadius: "999px",
                  fontSize: "0.75rem",
                  fontWeight: 500,
                  color: "#fff",
                  backgroundColor: statusColor(v.status),
                }}
              >
                {statusLabel(v.status)}
              </span>
            </td>
            <td style={{ padding: "0.5rem" }}>
              {v.fileTypes.map((ft) => fileTypeLabel(ft)).join(", ")}
            </td>
            <td style={{ padding: "0.5rem" }}>{v.createdBy || "-"}</td>
            <td style={{ padding: "0.5rem", color: "#64748b" }}>
              {formatTime(v.createdAt)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
