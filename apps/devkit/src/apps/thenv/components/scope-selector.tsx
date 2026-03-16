"use client";

import { create } from "@bufbuild/protobuf";
import { useCallback, useState } from "react";

import { ScopeSchema, type Scope } from "@/gen/thenv/v1/thenv_pb";

export interface ScopeSelectorProps {
  onScopeChange: (scope: Scope) => void;
}

export function ScopeSelector({ onScopeChange }: ScopeSelectorProps) {
  const [workspaceId, setWorkspaceId] = useState("default");
  const [projectId, setProjectId] = useState("default");
  const [environmentId, setEnvironmentId] = useState("development");

  const handleApply = useCallback(() => {
    if (!workspaceId || !projectId || !environmentId) return;
    const scope = create(ScopeSchema, { workspaceId, projectId, environmentId });
    onScopeChange(scope);
  }, [workspaceId, projectId, environmentId, onScopeChange]);

  const inputStyle = {
    padding: "0.4rem 0.6rem",
    border: "1px solid #d7e2ea",
    borderRadius: "6px",
    fontSize: "0.875rem",
    width: "140px",
  };

  return (
    <div
      style={{
        display: "flex",
        gap: "0.75rem",
        alignItems: "flex-end",
        flexWrap: "wrap",
        padding: "0.75rem 1rem",
        backgroundColor: "#f8fafc",
        borderRadius: "8px",
        border: "1px solid #e2e8f0",
      }}
    >
      <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
        Workspace
        <input
          style={inputStyle}
          value={workspaceId}
          onChange={(e) => setWorkspaceId(e.target.value)}
          placeholder="workspace-id"
        />
      </label>
      <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
        Project
        <input
          style={inputStyle}
          value={projectId}
          onChange={(e) => setProjectId(e.target.value)}
          placeholder="project-id"
        />
      </label>
      <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
        Environment
        <input
          style={inputStyle}
          value={environmentId}
          onChange={(e) => setEnvironmentId(e.target.value)}
          placeholder="environment-id"
        />
      </label>
      <button
        onClick={handleApply}
        style={{
          padding: "0.4rem 1rem",
          backgroundColor: "#0c5fca",
          color: "#fff",
          border: "none",
          borderRadius: "6px",
          cursor: "pointer",
          fontSize: "0.875rem",
          fontWeight: 500,
        }}
      >
        Apply
      </button>
    </div>
  );
}
