"use client";

import { useCallback, useState } from "react";

import { GitProviderKind } from "@/gen/committracker/v1/commit_tracker_pb";

export interface RepoSelection {
  provider: GitProviderKind;
  repository: string;
  branch: string;
  environment: string;
}

export interface RepoSelectorProps {
  onSelectionChange: (selection: RepoSelection) => void;
}

const PROVIDER_OPTIONS = [
  { value: GitProviderKind.GITHUB, label: "GitHub" },
  { value: GitProviderKind.GITLAB, label: "GitLab" },
  { value: GitProviderKind.BITBUCKET, label: "Bitbucket" },
];

export function RepoSelector({ onSelectionChange }: RepoSelectorProps) {
  const [provider, setProvider] = useState(GitProviderKind.GITHUB);
  const [repository, setRepository] = useState("");
  const [branch, setBranch] = useState("main");
  const [environment, setEnvironment] = useState("ci");

  const handleApply = useCallback(() => {
    if (!repository) return;
    onSelectionChange({ provider, repository, branch, environment });
  }, [provider, repository, branch, environment, onSelectionChange]);

  const inputStyle = {
    padding: "0.4rem 0.6rem",
    border: "1px solid #d7e2ea",
    borderRadius: "6px",
    fontSize: "0.875rem",
    width: "160px",
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
        Provider
        <select
          value={provider}
          onChange={(e) => setProvider(Number(e.target.value) as GitProviderKind)}
          style={{ ...inputStyle, width: "120px" }}
        >
          {PROVIDER_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
      </label>
      <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
        Repository
        <input
          style={inputStyle}
          value={repository}
          onChange={(e) => setRepository(e.target.value)}
          placeholder="owner/repo"
        />
      </label>
      <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
        Branch
        <input
          style={{ ...inputStyle, width: "120px" }}
          value={branch}
          onChange={(e) => setBranch(e.target.value)}
          placeholder="main"
        />
      </label>
      <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
        Environment
        <input
          style={{ ...inputStyle, width: "100px" }}
          value={environment}
          onChange={(e) => setEnvironment(e.target.value)}
          placeholder="ci"
        />
      </label>
      <button
        onClick={handleApply}
        disabled={!repository}
        style={{
          padding: "0.4rem 1rem",
          backgroundColor: !repository ? "#94a3b8" : "#0c5fca",
          color: "#fff",
          border: "none",
          borderRadius: "6px",
          cursor: repository ? "pointer" : "default",
          fontSize: "0.875rem",
          fontWeight: 500,
        }}
      >
        Apply
      </button>
    </div>
  );
}
