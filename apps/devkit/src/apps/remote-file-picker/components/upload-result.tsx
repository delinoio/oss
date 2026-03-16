"use client";

import { useCallback, useState } from "react";

import type { UploadState } from "@/apps/remote-file-picker/hooks/use-upload";
import { LogEvent, logInfo } from "@/lib/logger";
import { DevkitRoute } from "@/lib/mini-app-registry";

export interface UploadResultProps {
  state: UploadState;
  fileName?: string;
  onReset: () => void;
}

export function UploadResult({ state, fileName, onReset }: UploadResultProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    if (!state.publicUrl) return;
    navigator.clipboard.writeText(state.publicUrl).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);

      logInfo({
        event: LogEvent.RemoteFilePickerCallbackResult,
        route: DevkitRoute.RemoteFilePicker,
        message: "Public URL copied to clipboard",
      });
    });
  }, [state.publicUrl]);

  if (state.status !== "completed") return null;

  return (
    <div
      style={{
        padding: "1.5rem",
        borderRadius: "8px",
        border: "1px solid #bbf7d0",
        backgroundColor: "#f0fdf4",
        display: "flex",
        flexDirection: "column",
        gap: "1rem",
      }}
    >
      <div>
        <h4 style={{ margin: "0 0 0.25rem", color: "#16a34a" }}>Upload Complete</h4>
        {fileName && <p style={{ margin: 0, fontSize: "0.875rem", color: "#475569" }}>{fileName}</p>}
      </div>

      {state.publicUrl && (
        <div style={{ display: "flex", gap: "0.5rem", alignItems: "center" }}>
          <code
            style={{
              flex: 1,
              padding: "0.4rem 0.6rem",
              backgroundColor: "#fff",
              border: "1px solid #d7e2ea",
              borderRadius: "4px",
              fontSize: "0.8rem",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {state.publicUrl}
          </code>
          <button
            onClick={handleCopy}
            style={{
              padding: "0.4rem 0.75rem",
              backgroundColor: "#fff",
              color: "#475569",
              border: "1px solid #d7e2ea",
              borderRadius: "6px",
              cursor: "pointer",
              fontSize: "0.8rem",
              whiteSpace: "nowrap",
            }}
          >
            {copied ? "Copied" : "Copy URL"}
          </button>
        </div>
      )}

      {state.uploadId && (
        <p style={{ margin: 0, fontSize: "0.75rem", color: "#94a3b8" }}>
          Upload ID: <code>{state.uploadId}</code>
        </p>
      )}

      <button
        onClick={onReset}
        style={{
          padding: "0.4rem 1rem",
          backgroundColor: "#0c5fca",
          color: "#fff",
          border: "none",
          borderRadius: "6px",
          cursor: "pointer",
          fontSize: "0.8rem",
          alignSelf: "flex-start",
        }}
      >
        Upload Another
      </button>
    </div>
  );
}
