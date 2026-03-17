/**
 * Settings page with tabbed workspace configuration.
 */

import { type CSSProperties, useEffect, useMemo, useState } from "react";
import { AgentCliType, WorkspaceType } from "../../gen/v1/dexdex_pb";
import {
  useGetBadgeTheme,
  useGetWorkspace,
  useGetWorkspaceSettings,
  useListSessionCapabilities,
  useUpdateWorkspaceSettingsMutation,
} from "../../hooks/use-dexdex-queries";
import { useAppStore } from "../../stores/app-store";
import { CredentialManager } from "./credential-manager";

type SettingsTab = "general" | "agents";

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
  const { theme, setTheme, activeWorkspaceId } = useAppStore();
  const [activeTab, setActiveTab] = useState<SettingsTab>("general");

  const workspaceQuery = useGetWorkspace(activeWorkspaceId);
  const capabilitiesQuery = useListSessionCapabilities(activeWorkspaceId);
  const workspaceSettingsQuery = useGetWorkspaceSettings(activeWorkspaceId);
  const badgeThemeQuery = useGetBadgeTheme(activeWorkspaceId);

  const updateWorkspaceSettingsMutation = useUpdateWorkspaceSettingsMutation();

  const [selectedDefaultAgent, setSelectedDefaultAgent] = useState<AgentCliType>(AgentCliType.UNSPECIFIED);

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

  function handleSaveDefaultAgent() {
    if (selectedDefaultAgent === AgentCliType.UNSPECIFIED) return;
    updateWorkspaceSettingsMutation.mutate({
      workspaceId: activeWorkspaceId,
      defaultAgentCliType: selectedDefaultAgent,
    });
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
      </div>

      <div style={contentStyle}>
        {activeTab === "general" && (
          <>
            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Workspace</h2>
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }} data-testid="workspace-info">
                <InfoRow label="Name" value={workspaceQuery.data?.workspace?.name || activeWorkspaceId} />
                <InfoRow label="Type" value={toWorkspaceTypeLabel(workspaceQuery.data?.workspace?.type)} />
                <InfoRow label="Workspace ID" value={activeWorkspaceId} />
              </div>
            </div>

            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Server Connection</h2>
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }} data-testid="server-connection-info">
                <InfoRow
                  label="Status"
                  value={workspaceQuery.isError ? "Disconnected" : workspaceQuery.isLoading ? "Connecting..." : "Connected"}
                />
                <InfoRow
                  label="Default Agent"
                  value={toAgentDisplayName(workspaceSettingsQuery.data?.settings?.defaultAgentCliType ?? AgentCliType.UNSPECIFIED)}
                />
              </div>
            </div>

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

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "flex", gap: "var(--space-3)", fontSize: "var(--font-size-sm)" }}>
      <span style={{ color: "var(--color-text-tertiary)", minWidth: 120 }}>{label}</span>
      <span style={{ color: "var(--color-text-primary)", fontWeight: 500 }}>{value}</span>
    </div>
  );
}

function toWorkspaceTypeLabel(type: WorkspaceType | undefined): string {
  switch (type) {
    case WorkspaceType.LOCAL_ENDPOINT:
      return "Local Endpoint";
    case WorkspaceType.REMOTE_ENDPOINT:
      return "Remote Endpoint";
    default:
      return "Unknown";
  }
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
