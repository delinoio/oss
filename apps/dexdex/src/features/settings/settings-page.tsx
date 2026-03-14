/**
 * Settings page with tabbed workspace configuration.
 */

import { type CSSProperties, useEffect, useMemo, useState } from "react";
import { AgentCliType } from "../../gen/v1/dexdex_pb";
import {
  useCreateRepositoryGroupMutation,
  useCreateRepositoryMutation,
  useDeleteRepositoryGroupMutation,
  useDeleteRepositoryMutation,
  useGetBadgeTheme,
  useGetWorkspaceSettings,
  useListRepositories,
  useListRepositoryGroups,
  useListSessionCapabilities,
  useUpdateRepositoryGroupMutation,
  useUpdateRepositoryMutation,
  useUpdateWorkspaceSettingsMutation,
} from "../../hooks/use-dexdex-queries";
import { useAppStore } from "../../stores/app-store";
import { CredentialManager } from "./credential-manager";

const WORKSPACE_ID = "workspace-default";

type SettingsTab = "general" | "agents" | "repository-groups" | "repositories";

interface EditableGroupMember {
  repositoryId: string;
  branchRef: string;
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

export function SettingsPage() {
  const { theme, setTheme } = useAppStore();
  const [activeTab, setActiveTab] = useState<SettingsTab>("general");

  const repositoriesQuery = useListRepositories(WORKSPACE_ID);
  const repositoryGroupsQuery = useListRepositoryGroups(WORKSPACE_ID);
  const capabilitiesQuery = useListSessionCapabilities(WORKSPACE_ID);
  const workspaceSettingsQuery = useGetWorkspaceSettings(WORKSPACE_ID);
  const badgeThemeQuery = useGetBadgeTheme(WORKSPACE_ID);

  const createRepositoryMutation = useCreateRepositoryMutation();
  const updateRepositoryMutation = useUpdateRepositoryMutation();
  const deleteRepositoryMutation = useDeleteRepositoryMutation();
  const createRepositoryGroupMutation = useCreateRepositoryGroupMutation();
  const updateRepositoryGroupMutation = useUpdateRepositoryGroupMutation();
  const deleteRepositoryGroupMutation = useDeleteRepositoryGroupMutation();
  const updateWorkspaceSettingsMutation = useUpdateWorkspaceSettingsMutation();

  const [newRepositoryUrl, setNewRepositoryUrl] = useState("");
  const [repositoryEdits, setRepositoryEdits] = useState<Record<string, string>>({});

  const [selectedDefaultAgent, setSelectedDefaultAgent] = useState<AgentCliType>(AgentCliType.UNSPECIFIED);
  const [editingGroupId, setEditingGroupId] = useState("");
  const [groupIdInput, setGroupIdInput] = useState("");
  const [groupMembers, setGroupMembers] = useState<EditableGroupMember[]>([{ repositoryId: "", branchRef: "main" }]);
  const [groupFormError, setGroupFormError] = useState("");

  const repositories = repositoriesQuery.data?.repositories ?? [];
  const repositoryGroups = repositoryGroupsQuery.data?.repositoryGroups ?? [];
  const currentBadgeTheme = badgeThemeQuery.data?.theme?.themeName ?? "Default";

  const agentOptions = useMemo<AgentOption[]>(() => {
    const capabilities = capabilitiesQuery.data?.capabilities ?? [];
    if (capabilities.length === 0) return FALLBACK_AGENT_OPTIONS;
    return capabilities
      .filter((capability) => capability.agentCliType !== AgentCliType.UNSPECIFIED)
      .map((capability) => ({
        agentCliType: capability.agentCliType,
        supportsPlanMode: capability.supportsPlanMode,
        displayName: capability.displayName || toAgentDisplayName(capability.agentCliType),
      }));
  }, [capabilitiesQuery.data?.capabilities]);

  useEffect(() => {
    const defaultAgent = workspaceSettingsQuery.data?.settings?.defaultAgentCliType;
    if (defaultAgent && selectedDefaultAgent === AgentCliType.UNSPECIFIED) {
      setSelectedDefaultAgent(defaultAgent);
    }
  }, [workspaceSettingsQuery.data?.settings?.defaultAgentCliType, selectedDefaultAgent]);

  useEffect(() => {
    if (repositories.length === 0) return;
    setRepositoryEdits((prev) => {
      const next = { ...prev };
      for (const repository of repositories) {
        if (!next[repository.repositoryId]) {
          next[repository.repositoryId] = repository.repositoryUrl;
        }
      }
      return next;
    });
  }, [repositories]);

  function selectGroupForEdit(repositoryGroupId: string) {
    setEditingGroupId(repositoryGroupId);
    setGroupFormError("");
    if (!repositoryGroupId) {
      setGroupIdInput("");
      setGroupMembers([{ repositoryId: "", branchRef: "main" }]);
      return;
    }

    const group = repositoryGroups.find((item) => item.repositoryGroupId === repositoryGroupId);
    if (!group) return;

    setGroupIdInput(group.repositoryGroupId);
    setGroupMembers(
      [...group.members]
        .sort((a, b) => a.displayOrder - b.displayOrder)
        .map((member) => ({ repositoryId: member.repositoryId, branchRef: member.branchRef || "main" })),
    );
  }

  function resetGroupEditor() {
    setEditingGroupId("");
    setGroupIdInput("");
    setGroupMembers([{ repositoryId: "", branchRef: "main" }]);
    setGroupFormError("");
  }

  function addGroupMemberRow() {
    setGroupMembers((prev) => [...prev, { repositoryId: "", branchRef: "main" }]);
  }

  function updateGroupMember(index: number, patch: Partial<EditableGroupMember>) {
    setGroupMembers((prev) => prev.map((member, i) => (i === index ? { ...member, ...patch } : member)));
  }

  function removeGroupMember(index: number) {
    setGroupMembers((prev) => prev.filter((_, i) => i !== index));
  }

  function moveGroupMember(index: number, direction: -1 | 1) {
    const nextIndex = index + direction;
    if (nextIndex < 0 || nextIndex >= groupMembers.length) return;
    setGroupMembers((prev) => {
      const copy = [...prev];
      const current = copy[index];
      copy[index] = copy[nextIndex];
      copy[nextIndex] = current;
      return copy;
    });
  }

  function handleCreateRepository() {
    const repositoryUrl = newRepositoryUrl.trim();
    if (!repositoryUrl) return;
    createRepositoryMutation.mutate(
      { workspaceId: WORKSPACE_ID, repositoryUrl },
      {
        onSuccess: () => {
          setNewRepositoryUrl("");
        },
      },
    );
  }

  function handleUpdateRepository(repositoryId: string) {
    const repositoryUrl = (repositoryEdits[repositoryId] ?? "").trim();
    if (!repositoryUrl) return;
    updateRepositoryMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryId,
      repositoryUrl,
    });
  }

  function handleDeleteRepository(repositoryId: string) {
    deleteRepositoryMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryId,
    });
  }

  function handleSaveDefaultAgent() {
    if (selectedDefaultAgent === AgentCliType.UNSPECIFIED) return;
    updateWorkspaceSettingsMutation.mutate({
      workspaceId: WORKSPACE_ID,
      defaultAgentCliType: selectedDefaultAgent,
    });
  }

  function handleSaveRepositoryGroup() {
    const repositoryGroupId = groupIdInput.trim();
    if (!repositoryGroupId) {
      setGroupFormError("Repository group ID is required.");
      return;
    }

    const normalizedMembers = groupMembers
      .map((member) => ({
        repositoryId: member.repositoryId.trim(),
        branchRef: member.branchRef.trim() || "main",
      }))
      .filter((member) => member.repositoryId.length > 0);

    if (normalizedMembers.length === 0) {
      setGroupFormError("At least one repository member is required.");
      return;
    }

    const dedupe = new Set<string>();
    for (const member of normalizedMembers) {
      if (dedupe.has(member.repositoryId)) {
        setGroupFormError("Duplicate repository IDs are not allowed.");
        return;
      }
      dedupe.add(member.repositoryId);
    }

    setGroupFormError("");
    const members = normalizedMembers.map((member, index) => ({
      repositoryId: member.repositoryId,
      branchRef: member.branchRef,
      displayOrder: index,
    }));

    if (editingGroupId) {
      updateRepositoryGroupMutation.mutate(
        {
          workspaceId: WORKSPACE_ID,
          repositoryGroupId: editingGroupId,
          members,
        },
        {
          onSuccess: () => {
            resetGroupEditor();
          },
        },
      );
      return;
    }

    createRepositoryGroupMutation.mutate(
      {
        workspaceId: WORKSPACE_ID,
        repositoryGroupId,
        members,
      },
      {
        onSuccess: () => {
          resetGroupEditor();
        },
      },
    );
  }

  function handleDeleteRepositoryGroup(repositoryGroupId: string) {
    deleteRepositoryGroupMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryGroupId,
    });
    if (editingGroupId === repositoryGroupId) {
      resetGroupEditor();
    }
  }

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    flexShrink: 0,
  };

  const tabRowStyle: CSSProperties = {
    display: "flex",
    gap: "var(--space-2)",
    padding: "var(--space-2) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    backgroundColor: "var(--color-bg-secondary)",
  };

  const contentStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
    padding: "var(--space-6)",
  };

  const sectionStyle: CSSProperties = {
    marginBottom: "var(--space-8)",
  };

  const sectionTitleStyle: CSSProperties = {
    fontSize: "var(--font-size-md)",
    fontWeight: 600,
    marginBottom: "var(--space-4)",
    color: "var(--color-text-primary)",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-sm)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
  };

  return (
    <div style={containerStyle} data-testid="settings-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Settings</h1>
      </div>

      <div style={tabRowStyle}>
        <TabButton tab="general" activeTab={activeTab} onSelect={setActiveTab} label="General" />
        <TabButton tab="agents" activeTab={activeTab} onSelect={setActiveTab} label="Agents" />
        <TabButton tab="repository-groups" activeTab={activeTab} onSelect={setActiveTab} label="Repository Groups" />
        <TabButton tab="repositories" activeTab={activeTab} onSelect={setActiveTab} label="Repositories" />
      </div>

      <div style={contentStyle}>
        {activeTab === "general" && (
          <>
            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Appearance</h2>
              <div style={{ display: "flex", gap: "var(--space-2)" }}>
                <button style={getToggleStyle(theme === "light")} onClick={() => setTheme("light")} data-testid="theme-light">
                  Light
                </button>
                <button style={getToggleStyle(theme === "dark")} onClick={() => setTheme("dark")} data-testid="theme-dark">
                  Dark
                </button>
              </div>
            </div>

            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Badge Theme</h2>
              <div style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-secondary)" }}>
                Current theme: <strong>{currentBadgeTheme}</strong>
              </div>
            </div>

            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Credentials</h2>
              <CredentialManager />
            </div>
          </>
        )}

        {activeTab === "agents" && (
          <div style={sectionStyle}>
            <h2 style={sectionTitleStyle}>Default Coding Agent</h2>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr auto",
                gap: "var(--space-2)",
                alignItems: "center",
                maxWidth: 560,
              }}
            >
              <select
                style={inputStyle}
                value={selectedDefaultAgent}
                onChange={(e) => setSelectedDefaultAgent(Number(e.target.value) as AgentCliType)}
                data-testid="default-agent-select"
              >
                <option value={AgentCliType.UNSPECIFIED}>Select an agent</option>
                {agentOptions.map((option) => (
                  <option key={option.agentCliType} value={option.agentCliType}>
                    {option.displayName} {option.supportsPlanMode ? "(Plan Mode)" : ""}
                  </option>
                ))}
              </select>
              <button
                style={primaryButtonStyle}
                onClick={handleSaveDefaultAgent}
                disabled={selectedDefaultAgent === AgentCliType.UNSPECIFIED || updateWorkspaceSettingsMutation.isPending}
                data-testid="save-default-agent"
              >
                Save
              </button>
            </div>
          </div>
        )}

        {activeTab === "repositories" && (
          <>
            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Create Repository</h2>
              <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: "var(--space-2)", maxWidth: 700 }}>
                <input
                  style={inputStyle}
                  type="text"
                  value={newRepositoryUrl}
                  onChange={(e) => setNewRepositoryUrl(e.target.value)}
                  placeholder="https://github.com/org/repo"
                  data-testid="create-repository-url"
                />
                <button style={primaryButtonStyle} onClick={handleCreateRepository} disabled={createRepositoryMutation.isPending}>
                  Add Repository
                </button>
              </div>
            </div>

            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Repositories</h2>
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
                {repositories.map((repository) => (
                  <div
                    key={repository.repositoryId}
                    style={{
                      display: "grid",
                      gridTemplateColumns: "180px 1fr auto auto",
                      gap: "var(--space-2)",
                      alignItems: "center",
                      padding: "var(--space-2)",
                      border: "1px solid var(--color-border)",
                      borderRadius: "var(--radius-md)",
                    }}
                  >
                    <span style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-tertiary)" }}>{repository.repositoryId}</span>
                    <input
                      style={inputStyle}
                      value={repositoryEdits[repository.repositoryId] ?? repository.repositoryUrl}
                      onChange={(e) =>
                        setRepositoryEdits((prev) => ({
                          ...prev,
                          [repository.repositoryId]: e.target.value,
                        }))
                      }
                    />
                    <button style={secondaryButtonStyle} onClick={() => handleUpdateRepository(repository.repositoryId)}>
                      Save
                    </button>
                    <button style={dangerButtonStyle} onClick={() => handleDeleteRepository(repository.repositoryId)}>
                      Delete
                    </button>
                  </div>
                ))}
              </div>
            </div>
          </>
        )}

        {activeTab === "repository-groups" && (
          <>
            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Repository Group Editor</h2>
              <div style={{ marginBottom: "var(--space-3)", display: "grid", gap: "var(--space-2)", maxWidth: 640 }}>
                <select
                  style={inputStyle}
                  value={editingGroupId}
                  onChange={(e) => selectGroupForEdit(e.target.value)}
                  data-testid="group-edit-select"
                >
                  <option value="">Create new group</option>
                  {repositoryGroups.map((group) => (
                    <option key={group.repositoryGroupId} value={group.repositoryGroupId}>
                      {group.repositoryGroupId}
                    </option>
                  ))}
                </select>
                <input
                  style={inputStyle}
                  value={groupIdInput}
                  onChange={(e) => setGroupIdInput(e.target.value)}
                  placeholder="repository-group-id"
                  disabled={editingGroupId.length > 0}
                  data-testid="group-id-input"
                />
              </div>

              <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)", maxWidth: 760 }}>
                {groupMembers.map((member, index) => (
                  <div
                    key={`${member.repositoryId}-${index}`}
                    style={{
                      display: "grid",
                      gridTemplateColumns: "1fr 180px auto auto auto",
                      gap: "var(--space-2)",
                      alignItems: "center",
                    }}
                  >
                    <select
                      style={inputStyle}
                      value={member.repositoryId}
                      onChange={(e) => updateGroupMember(index, { repositoryId: e.target.value })}
                    >
                      <option value="">Select repository</option>
                      {repositories.map((repository) => (
                        <option key={repository.repositoryId} value={repository.repositoryId}>
                          {repository.repositoryId}
                        </option>
                      ))}
                    </select>
                    <input
                      style={inputStyle}
                      value={member.branchRef}
                      onChange={(e) => updateGroupMember(index, { branchRef: e.target.value })}
                      placeholder="branch"
                    />
                    <button style={secondaryButtonStyle} onClick={() => moveGroupMember(index, -1)}>
                      ↑
                    </button>
                    <button style={secondaryButtonStyle} onClick={() => moveGroupMember(index, 1)}>
                      ↓
                    </button>
                    <button style={dangerButtonStyle} onClick={() => removeGroupMember(index)} disabled={groupMembers.length <= 1}>
                      Remove
                    </button>
                  </div>
                ))}
              </div>

              {groupFormError && (
                <div style={{ marginTop: "var(--space-2)", color: "var(--color-status-failed)", fontSize: "var(--font-size-sm)" }}>
                  {groupFormError}
                </div>
              )}

              <div style={{ marginTop: "var(--space-3)", display: "flex", gap: "var(--space-2)" }}>
                <button style={secondaryButtonStyle} onClick={addGroupMemberRow}>
                  Add Member
                </button>
                <button style={primaryButtonStyle} onClick={handleSaveRepositoryGroup}>
                  {editingGroupId ? "Update Group" : "Create Group"}
                </button>
                <button style={secondaryButtonStyle} onClick={resetGroupEditor}>
                  Reset
                </button>
              </div>
            </div>

            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Repository Groups</h2>
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
                {repositoryGroups.map((group) => (
                  <div
                    key={group.repositoryGroupId}
                    style={{
                      border: "1px solid var(--color-border)",
                      borderRadius: "var(--radius-md)",
                      padding: "var(--space-3)",
                    }}
                  >
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "var(--space-2)" }}>
                      <strong style={{ fontSize: "var(--font-size-sm)" }}>{group.repositoryGroupId}</strong>
                      <div style={{ display: "flex", gap: "var(--space-2)" }}>
                        <button style={secondaryButtonStyle} onClick={() => selectGroupForEdit(group.repositoryGroupId)}>
                          Edit
                        </button>
                        <button style={dangerButtonStyle} onClick={() => handleDeleteRepositoryGroup(group.repositoryGroupId)}>
                          Delete
                        </button>
                      </div>
                    </div>
                    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
                      {[...group.members]
                        .sort((a, b) => a.displayOrder - b.displayOrder)
                        .map((member) => (
                          <div key={`${group.repositoryGroupId}-${member.repositoryId}-${member.displayOrder}`} style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-secondary)" }}>
                            #{member.displayOrder + 1} {member.repositoryId} ({member.branchRef || "main"})
                          </div>
                        ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function TabButton({
  tab,
  activeTab,
  onSelect,
  label,
}: {
  tab: SettingsTab;
  activeTab: SettingsTab;
  onSelect: (tab: SettingsTab) => void;
  label: string;
}) {
  const isActive = tab === activeTab;
  return (
    <button
      style={{
        padding: "var(--space-2) var(--space-3)",
        borderRadius: "var(--radius-md)",
        border: "1px solid var(--color-border)",
        backgroundColor: isActive ? "var(--color-accent-subtle)" : "var(--color-bg-primary)",
        color: isActive ? "var(--color-accent)" : "var(--color-text-secondary)",
        fontSize: "var(--font-size-sm)",
        fontWeight: isActive ? 600 : 500,
      }}
      onClick={() => onSelect(tab)}
    >
      {label}
    </button>
  );
}

function getToggleStyle(isActive: boolean): CSSProperties {
  return {
    padding: "var(--space-2) var(--space-4)",
    borderRadius: "var(--radius-md)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    border: "1px solid var(--color-border)",
    backgroundColor: isActive ? "var(--color-accent)" : "var(--color-bg-secondary)",
    color: isActive ? "var(--color-text-inverse)" : "var(--color-text-secondary)",
    cursor: "pointer",
    transition: "all 0.15s ease",
  };
}

const primaryButtonStyle: CSSProperties = {
  padding: "var(--space-2) var(--space-3)",
  borderRadius: "var(--radius-md)",
  border: "1px solid var(--color-accent)",
  backgroundColor: "var(--color-accent)",
  color: "var(--color-text-inverse)",
  fontSize: "var(--font-size-sm)",
  fontWeight: 500,
  cursor: "pointer",
};

const secondaryButtonStyle: CSSProperties = {
  padding: "var(--space-2) var(--space-3)",
  borderRadius: "var(--radius-md)",
  border: "1px solid var(--color-border)",
  backgroundColor: "var(--color-bg-primary)",
  color: "var(--color-text-secondary)",
  fontSize: "var(--font-size-sm)",
  fontWeight: 500,
  cursor: "pointer",
};

const dangerButtonStyle: CSSProperties = {
  ...secondaryButtonStyle,
  border: "1px solid var(--color-status-failed)",
  color: "var(--color-status-failed)",
};

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
