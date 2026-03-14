/**
 * Create task dialog component.
 */

import { type CSSProperties, type KeyboardEvent, useState } from "react";
import { useListRepositoryGroups, useListSessionCapabilities } from "../../hooks/use-dexdex-queries";
import { AgentCliType as ProtoAgentCliType } from "../../gen/v1/dexdex_pb";

interface RepositoryGroup {
  repositoryGroupId: string;
  repositories: Array<{
    repositoryId: string;
    repositoryUrl: string;
    branchRef: string;
  }>;
}

interface CreateDialogProps {
  isOpen: boolean;
  workspaceId: string;
  onClose: () => void;
  onCreate: (prompt: string, repositoryGroupId: string, agentCliType: number, planMode: boolean) => void;
}

export function CreateDialog({ isOpen, workspaceId, onClose, onCreate }: CreateDialogProps) {
  const [prompt, setPrompt] = useState("");
  const [selectedRepoGroupId, setSelectedRepoGroupId] = useState("");
  const [selectedAgentCliType, setSelectedAgentCliType] = useState<number>(0);
  const [planMode, setPlanMode] = useState(false);

  const repoGroupsQuery = useListRepositoryGroups(workspaceId);
  const repoGroups: RepositoryGroup[] = (repoGroupsQuery.data?.repositoryGroups ?? []) as RepositoryGroup[];

  const capabilitiesQuery = useListSessionCapabilities(workspaceId);
  const capabilities = capabilitiesQuery.data?.capabilities ?? [];

  if (!isOpen) return null;

  function handleSubmit() {
    const trimmed = prompt.trim();
    if (!trimmed) return;
    onCreate(trimmed, selectedRepoGroupId, selectedAgentCliType, planMode);
    setPrompt("");
    setSelectedRepoGroupId("");
    setSelectedAgentCliType(0);
    setPlanMode(false);
    onClose();
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    // Cmd+Enter or Ctrl+Enter submits; plain Enter inserts newline
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSubmit();
    }
  }

  const overlayStyle: CSSProperties = {
    position: "fixed",
    inset: 0,
    backgroundColor: "var(--color-bg-overlay)",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    zIndex: 90,
  };

  const dialogStyle: CSSProperties = {
    width: "min(480px, 90vw)",
    backgroundColor: "var(--color-bg-primary)",
    borderRadius: "var(--radius-lg)",
    boxShadow: "var(--shadow-overlay)",
    border: "1px solid var(--color-border)",
    padding: "var(--space-6)",
  };

  const textareaStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-base)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
    minHeight: "120px",
    resize: "vertical",
  };

  const selectStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-base)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
    cursor: "pointer",
  };

  const labelStyle: CSSProperties = {
    display: "block",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    marginBottom: "var(--space-1)",
    color: "var(--color-text-secondary)",
  };

  return (
    <div style={overlayStyle} onClick={onClose} data-testid="create-dialog">
      <div style={dialogStyle} onClick={(e) => e.stopPropagation()}>
        <h2
          style={{
            fontSize: "var(--font-size-lg)",
            fontWeight: 600,
            marginBottom: "var(--space-4)",
          }}
        >
          Create Task
        </h2>
        <div>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="task-prompt" style={labelStyle}>
              Prompt
            </label>
            <textarea
              id="task-prompt"
              style={textareaStyle}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Enter your prompt..."
              autoFocus
              data-testid="task-prompt-input"
            />
            <div
              style={{
                fontSize: "var(--font-size-xs)",
                color: "var(--color-text-tertiary)",
                marginTop: "var(--space-1)",
              }}
            >
              Press Cmd+Enter to submit
            </div>
          </div>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="task-repo-group" style={labelStyle}>
              Repository Group
            </label>
            <select
              id="task-repo-group"
              style={selectStyle}
              value={selectedRepoGroupId}
              onChange={(e) => setSelectedRepoGroupId(e.target.value)}
              data-testid="task-repo-group-select"
            >
              <option value="">None (no execution)</option>
              {repoGroups.map((group) => (
                <option key={group.repositoryGroupId} value={group.repositoryGroupId}>
                  {group.repositoryGroupId} ({group.repositories.length} repos)
                </option>
              ))}
            </select>
          </div>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="task-agent-type" style={labelStyle}>
              Coding Agent
            </label>
            <select
              id="task-agent-type"
              style={selectStyle}
              value={selectedAgentCliType}
              onChange={(e) => setSelectedAgentCliType(Number(e.target.value))}
              data-testid="task-agent-type-select"
            >
              <option value={0}>Default (workspace setting)</option>
              {capabilities.map((cap) => (
                <option key={cap.agentCliType} value={cap.agentCliType}>
                  {cap.displayName}
                </option>
              ))}
            </select>
          </div>
          <div style={{ marginBottom: "var(--space-4)" }}>
            <label
              style={{
                display: "flex",
                alignItems: "center",
                gap: "var(--space-2)",
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
                cursor: "pointer",
              }}
            >
              <input
                type="checkbox"
                checked={planMode}
                onChange={(e) => setPlanMode(e.target.checked)}
                data-testid="task-plan-mode-checkbox"
              />
              Plan mode (generate plan before execution)
            </label>
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--space-2)" }}>
            <button
              type="button"
              onClick={onClose}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
                border: "1px solid var(--color-border)",
              }}
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleSubmit}
              disabled={!prompt.trim()}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                backgroundColor: prompt.trim() ? "var(--color-accent)" : "var(--color-bg-tertiary)",
                color: prompt.trim() ? "var(--color-text-inverse)" : "var(--color-text-tertiary)",
                cursor: prompt.trim() ? "pointer" : "not-allowed",
              }}
              data-testid="submit-create-task"
            >
              Create Task
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
