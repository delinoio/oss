"use client";

import { timestampDate } from "@bufbuild/protobuf/wkt";

import {
  BundleStatus,
  FileType,
  type BundleVersionSummary,
  type BundleFile,
  type Scope,
} from "@/gen/thenv/v1/thenv_pb";
import {
  usePullActiveBundle,
  useActivateBundleVersionMutation,
  useRotateBundleVersionMutation,
} from "@/apps/thenv/hooks/use-thenv-queries";

export interface BundleDetailProps {
  versionId: string;
  scope: Scope;
}

function fileTypeLabel(ft: FileType): string {
  switch (ft) {
    case FileType.ENV: return ".env";
    case FileType.DEV_VARS: return ".dev.vars";
    default: return "unknown";
  }
}

function decodeFileContent(file: BundleFile): string {
  try {
    return new TextDecoder().decode(file.plaintext);
  } catch {
    return "(binary content)";
  }
}

export function BundleDetail({ versionId, scope }: BundleDetailProps) {
  const { data, isLoading } = usePullActiveBundle(scope);
  const activateMutation = useActivateBundleVersionMutation();
  const rotateMutation = useRotateBundleVersionMutation();

  if (isLoading) {
    return <p style={{ color: "#64748b" }}>Loading bundle detail...</p>;
  }

  const version = data?.version;
  if (!version) {
    return <p style={{ color: "#64748b" }}>No active bundle found.</p>;
  }

  const isActive = version.status === BundleStatus.ACTIVE;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <div style={{ display: "flex", gap: "1rem", alignItems: "center" }}>
        <h3 style={{ margin: 0 }}>Bundle Version</h3>
        <code style={{ fontSize: "0.8rem", color: "#475569" }}>{version.bundleVersionId}</code>
      </div>

      <div style={{ display: "flex", gap: "2rem", fontSize: "0.875rem" }}>
        <div>
          <span style={{ color: "#64748b" }}>Status: </span>
          <strong>{isActive ? "Active" : "Archived"}</strong>
        </div>
        <div>
          <span style={{ color: "#64748b" }}>Created by: </span>
          <strong>{version.createdBy || "-"}</strong>
        </div>
        <div>
          <span style={{ color: "#64748b" }}>Created at: </span>
          <strong>
            {version.createdAt ? timestampDate(version.createdAt).toLocaleString() : "-"}
          </strong>
        </div>
      </div>

      <div style={{ display: "flex", gap: "0.5rem" }}>
        {!isActive && (
          <button
            onClick={() =>
              activateMutation.mutate({ scope, bundleVersionId: versionId })
            }
            disabled={activateMutation.isPending}
            style={{
              padding: "0.4rem 0.75rem",
              backgroundColor: "#16a34a",
              color: "#fff",
              border: "none",
              borderRadius: "6px",
              cursor: "pointer",
              fontSize: "0.8rem",
            }}
          >
            {activateMutation.isPending ? "Activating..." : "Activate"}
          </button>
        )}
        <button
          onClick={() =>
            rotateMutation.mutate({ scope, fromVersionId: versionId })
          }
          disabled={rotateMutation.isPending}
          style={{
            padding: "0.4rem 0.75rem",
            backgroundColor: "#0c5fca",
            color: "#fff",
            border: "none",
            borderRadius: "6px",
            cursor: "pointer",
            fontSize: "0.8rem",
          }}
        >
          {rotateMutation.isPending ? "Rotating..." : "Rotate"}
        </button>
      </div>

      {data?.files && data.files.length > 0 && (
        <div>
          <h4 style={{ marginBottom: "0.5rem" }}>Files</h4>
          {data.files.map((file, i) => (
            <div
              key={i}
              style={{
                marginBottom: "0.75rem",
                border: "1px solid #e2e8f0",
                borderRadius: "6px",
                overflow: "hidden",
              }}
            >
              <div
                style={{
                  padding: "0.4rem 0.75rem",
                  backgroundColor: "#f8fafc",
                  borderBottom: "1px solid #e2e8f0",
                  fontSize: "0.8rem",
                  fontWeight: 500,
                }}
              >
                {fileTypeLabel(file.fileType)}
              </div>
              <pre
                style={{
                  margin: 0,
                  padding: "0.75rem",
                  fontSize: "0.8rem",
                  overflow: "auto",
                  maxHeight: "200px",
                  backgroundColor: "#fafafa",
                }}
              >
                {decodeFileContent(file)}
              </pre>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
