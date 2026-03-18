/**
 * Create task dialog component.
 */

import { type CSSProperties, type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { AgentCliType } from "../../gen/v1/dexdex_pb";
import {
  useGetWorkspaceSettings,
  useListRepositories,
  useListRepositoryGroups,
  useListSessionCapabilities,
} from "../../hooks/use-dexdex-queries";
import { useEscapeToClose, useFocusOnShow } from "../../hooks/use-dialog-accessibility";
import { useDraftStore } from "../../stores/draft-store";

interface CreateDialogProps {
  isOpen: boolean;
  workspaceId: string;
  onClose: () => void;
  onCreate: (prompt: string, repositoryGroupId: string, agentCliType: AgentCliType, usePlanMode: boolean) => void;
}

interface AgentOption {
  agentCliType: AgentCliType;
  supportsPlanMode: boolean;
  displayName: string;
}

const FALLBACK_AGENT_OPTIONS: AgentOption[] = [
  { agentCliType: AgentCliType.CLAUDE_CODE, supportsPlanMode: true, displayName: "Claude Code" },
  { agentCliType: AgentCliType.CODEX_CLI, supportsPlanMode: true, displayName: "Codex CLI" },
  { agentCliType: AgentCliType.OPENCODE, supportsPlanMode: false, displayName: "OpenCode" },
];

export function CreateDialog({ isOpen, workspaceId, onClose, onCreate }: CreateDialogProps) {
  const [prompt, setPrompt] = useState("");
  const [selectedRepoGroupId, setSelectedRepoGroupId] = useState("");
  const [selectedAgent, setSelectedAgent] = useState<AgentCliType>(AgentCliType.UNSPECIFIED);
  const [usePlanMode, setUsePlanMode] = useState(false);
  const promptInputRef = useRef<HTMLTextAreaElement>(null);

  const { getDraft, setDraft: saveDraft, clearDraft } = useDraftStore();

  const repoGroupsQuery = useListRepositoryGroups(workspaceId);
  const repositoriesQuery = useListRepositories(workspaceId);
  const capabilitiesQuery = useListSessionCapabilities(workspaceId);
  const workspaceSettingsQuery = useGetWorkspaceSettings(workspaceId);

  const repositoryGroups = repoGroupsQuery.data?.repositoryGroups ?? [];
  const repositories = repositoriesQuery.data?.repositories ?? [];
  const agentOptions = useMemo<AgentOption[]>(() => {
    const capabilities = capabilitiesQuery.data?.capabilities ?? [];
    if (capabilities.length === 0) {
      return FALLBACK_AGENT_OPTIONS;
    }
    return capabilities
      .filter((capability) => capability.agentCliType !== AgentCliType.UNSPECIFIED)
      .map((capability) => ({
        agentCliType: capability.agentCliType,
        supportsPlanMode: capability.supportsPlanMode,
        displayName: capability.displayName || toAgentDisplayName(capability.agentCliType),
      }));
  }, [capabilitiesQuery.data?.capabilities]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    if (selectedAgent !== AgentCliType.UNSPECIFIED) {
      return;
    }

    const defaultAgent = workspaceSettingsQuery.data?.settings?.defaultAgentCliType ?? AgentCliType.CLAUDE_CODE;
    const resolvedDefault = agentOptions.find((option) => option.agentCliType === defaultAgent) ?? agentOptions[0];
    if (resolvedDefault) {
      setSelectedAgent(resolvedDefault.agentCliType);
    }
  }, [isOpen, selectedAgent, workspaceSettingsQuery.data?.settings?.defaultAgentCliType, agentOptions]);

  const selectedAgentOption = agentOptions.find((option) => option.agentCliType === selectedAgent);
  const selectedAgentSupportsPlanMode = selectedAgentOption?.supportsPlanMode ?? false;

  useEffect(() => {
    if (!selectedAgentSupportsPlanMode && usePlanMode) {
      setUsePlanMode(false);
    }
  }, [selectedAgentSupportsPlanMode, usePlanMode]);

  // Restore draft when dialog opens
  useEffect(() => {
    if (!isOpen) return;
    const draft = getDraft(workspaceId);
    if (draft) {
      setPrompt(draft.prompt);
      setSelectedRepoGroupId(draft.repositoryGroupId);
      setSelectedAgent(draft.agentCliType);
      setUsePlanMode(draft.usePlanMode);
    }
  }, [isOpen, workspaceId, getDraft]);

  // Debounced save of draft on form changes
  useEffect(() => {
    if (!isOpen) return;
    const timer = setTimeout(() => {
      saveDraft(workspaceId, {
        prompt,
        repositoryGroupId: selectedRepoGroupId,
        agentCliType: selectedAgent,
        usePlanMode,
      });
    }, 300);
    return () => clearTimeout(timer);
  }, [isOpen, workspaceId, prompt, selectedRepoGroupId, selectedAgent, usePlanMode, saveDraft]);

  useEscapeToClose(isOpen, onClose);
  useFocusOnShow(isOpen, promptInputRef);

  if (!isOpen) return null;

  const canSubmit = prompt.trim().length > 0 && selectedRepoGroupId.trim().length > 0 && selectedAgent !== AgentCliType.UNSPECIFIED;

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;

    onCreate(prompt.trim(), selectedRepoGroupId, selectedAgent, selectedAgentSupportsPlanMode && usePlanMode);
    clearDraft(workspaceId);
    setPrompt("");
    setSelectedRepoGroupId("");
    setSelectedAgent(AgentCliType.UNSPECIFIED);
    setUsePlanMode(false);
    onClose();
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
    width: "min(560px, 92vw)",
    backgroundColor: "var(--color-bg-primary)",
    borderRadius: "var(--radius-lg)",
    boxShadow: "var(--shadow-overlay)",
    border: "1px solid var(--color-border)",
    padding: "var(--space-6)",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-md)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
  };

  const textareaStyle: CSSProperties = {
    ...inputStyle,
    minHeight: "120px",
    resize: "vertical",
    fontSize: "var(--font-size-base)",
    lineHeight: 1.45,
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

        <form onSubmit={handleSubmit}>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="task-prompt" style={labelStyle}>
              Prompt
            </label>
            <textarea
              id="task-prompt"
              ref={promptInputRef}
              style={textareaStyle}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="Describe exactly what the coding agent should do..."
              data-testid="task-prompt-input"
            />
          </div>

          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="task-repo-group" style={labelStyle}>
              Repository / Group
            </label>
            <select
              id="task-repo-group"
              style={inputStyle}
              value={selectedRepoGroupId}
              onChange={(e) => setSelectedRepoGroupId(e.target.value)}
              data-testid="task-repo-group-select"
            >
              <option value="">Select a repository group or repository</option>
              <optgroup label="Repository Groups">
                {repositoryGroups.map((group) => (
                  <option key={group.repositoryGroupId} value={group.repositoryGroupId}>
                    {group.repositoryGroupId} ({group.members.length} repos)
                  </option>
                ))}
              </optgroup>
              <optgroup label="Repositories (single repo)">
                {repositories.map((repository) => (
                  <option key={repository.repositoryId} value={repository.repositoryId}>
                    {repository.repositoryId} (single repo)
                  </option>
                ))}
              </optgroup>
            </select>
          </div>

          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="task-agent" style={labelStyle}>
              Coding Agent
            </label>
            <select
              id="task-agent"
              style={inputStyle}
              value={selectedAgent}
              onChange={(e) => setSelectedAgent(Number(e.target.value) as AgentCliType)}
              data-testid="task-agent-select"
            >
              <option value={AgentCliType.UNSPECIFIED}>Select a coding agent</option>
              {agentOptions.map((option) => (
                <option key={option.agentCliType} value={option.agentCliType}>
                  {option.displayName}
                </option>
              ))}
            </select>
          </div>

          {selectedAgentSupportsPlanMode && (
            <label
              style={{
                display: "flex",
                alignItems: "center",
                gap: "var(--space-2)",
                marginBottom: "var(--space-4)",
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
              }}
            >
              <input
                type="checkbox"
                checked={usePlanMode}
                onChange={(e) => setUsePlanMode(e.target.checked)}
                data-testid="task-plan-mode-toggle"
              />
              Use plan mode for this task
            </label>
          )}

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
              type="submit"
              disabled={!canSubmit}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                backgroundColor: canSubmit ? "var(--color-accent)" : "var(--color-bg-tertiary)",
                color: canSubmit ? "var(--color-text-inverse)" : "var(--color-text-tertiary)",
                cursor: canSubmit ? "pointer" : "not-allowed",
              }}
              data-testid="submit-create-task"
            >
              Create Task
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function toAgentDisplayName(agent: AgentCliType): string {
  switch (agent) {
    case AgentCliType.CLAUDE_CODE:
      return "Claude Code";
    case AgentCliType.CODEX_CLI:
      return "Codex CLI";
    case AgentCliType.OPENCODE:
      return "OpenCode";
    default:
      return "Unknown Agent";
  }
}
