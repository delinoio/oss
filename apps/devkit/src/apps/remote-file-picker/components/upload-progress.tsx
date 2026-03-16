"use client";

import type { UploadState } from "@/apps/remote-file-picker/hooks/use-upload";

export interface UploadProgressProps {
  state: UploadState;
  fileName?: string;
}

function statusLabel(status: UploadState["status"]): string {
  switch (status) {
    case "creating-url": return "Preparing upload...";
    case "uploading": return "Uploading...";
    case "confirming": return "Confirming...";
    case "completed": return "Upload complete";
    case "failed": return "Upload failed";
    default: return "";
  }
}

export function UploadProgress({ state, fileName }: UploadProgressProps) {
  if (state.status === "idle") return null;

  const isActive = state.status === "uploading" || state.status === "creating-url" || state.status === "confirming";

  return (
    <div
      style={{
        padding: "1rem",
        borderRadius: "8px",
        border: "1px solid #e2e8f0",
        backgroundColor: state.status === "failed" ? "#fef2f2" : "#fafafa",
      }}
    >
      {fileName && (
        <p style={{ margin: "0 0 0.5rem", fontSize: "0.875rem", fontWeight: 500 }}>
          {fileName}
        </p>
      )}
      <p style={{ margin: "0 0 0.75rem", fontSize: "0.8rem", color: state.status === "failed" ? "#dc2626" : "#64748b" }}>
        {statusLabel(state.status)}
      </p>

      {isActive && (
        <div
          style={{
            width: "100%",
            height: "8px",
            backgroundColor: "#e2e8f0",
            borderRadius: "4px",
            overflow: "hidden",
          }}
        >
          <div
            style={{
              width: `${state.progress}%`,
              height: "100%",
              backgroundColor: "#3b82f6",
              borderRadius: "4px",
              transition: "width 0.3s ease",
            }}
          />
        </div>
      )}

      {state.status === "completed" && (
        <div
          style={{
            padding: "0.5rem 0.75rem",
            backgroundColor: "#f0fdf4",
            borderRadius: "6px",
            border: "1px solid #bbf7d0",
            fontSize: "0.8rem",
            color: "#16a34a",
            fontWeight: 500,
          }}
        >
          Upload successful
        </div>
      )}

      {state.error && (
        <p style={{ margin: "0.5rem 0 0", fontSize: "0.8rem", color: "#dc2626" }}>
          {state.error}
        </p>
      )}
    </div>
  );
}
