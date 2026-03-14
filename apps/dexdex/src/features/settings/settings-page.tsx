/**
 * Settings page with tab-based layout.
 */

import { type CSSProperties, useState } from "react";
import { useAppStore } from "../../stores/app-store";
import { CredentialManager } from "./credential-manager";
import { useGetBadgeTheme, useListSessionCapabilities, useGetWorkspaceSettings, useUpdateWorkspaceSettingsMutation } from "../../hooks/use-dexdex-queries";

const WORKSPACE_ID = "workspace-default";

type SettingsTab = "general" | "appearance" | "shortcuts" | "credentials" | "about";

const TABS: { id: SettingsTab; label: string }[] = [
  { id: "general", label: "General" },
  { id: "appearance", label: "Appearance" },
  { id: "shortcuts", label: "Shortcuts" },
  { id: "credentials", label: "Credentials" },
  { id: "about", label: "About" },
];

export function SettingsPage() {
  const { theme, setTheme } = useAppStore();
  const [activeTab, setActiveTab] = useState<SettingsTab>("general");
  const { data: badgeThemeData } = useGetBadgeTheme(WORKSPACE_ID);
  const currentBadgeTheme = badgeThemeData?.theme?.themeName ?? "Default";

  const capabilitiesQuery = useListSessionCapabilities(WORKSPACE_ID);
  const capabilities = capabilitiesQuery.data?.capabilities ?? [];

  const settingsQuery = useGetWorkspaceSettings(WORKSPACE_ID);
  const currentSettings = settingsQuery.data;
  const updateSettingsMutation = useUpdateWorkspaceSettingsMutation();

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

  const tabBarStyle: CSSProperties = {
    display: "flex",
    gap: "var(--space-1)",
    padding: "0 var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    flexShrink: 0,
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

  const toggleGroupStyle: CSSProperties = {
    display: "flex",
    gap: "var(--space-2)",
  };

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

  function getTabStyle(isActive: boolean): CSSProperties {
    return {
      padding: "var(--space-2) var(--space-4)",
      fontSize: "var(--font-size-sm)",
      fontWeight: isActive ? 600 : 400,
      color: isActive ? "var(--color-text-primary)" : "var(--color-text-secondary)",
      borderBottom: isActive ? "2px solid var(--color-accent)" : "2px solid transparent",
      cursor: "pointer",
      background: "none",
      transition: "all 0.15s ease",
    };
  }

  const selectStyle: CSSProperties = {
    width: "100%",
    maxWidth: "320px",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-base)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
    cursor: "pointer",
  };

  return (
    <div style={containerStyle} data-testid="settings-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Settings</h1>
      </div>
      <div style={tabBarStyle}>
        {TABS.map((tab) => (
          <button
            key={tab.id}
            style={getTabStyle(activeTab === tab.id)}
            onClick={() => setActiveTab(tab.id)}
            data-testid={`settings-tab-${tab.id}`}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div style={contentStyle}>
        {activeTab === "general" && (
          <div style={sectionStyle}>
            <h2 style={sectionTitleStyle}>Default Coding Agent</h2>
            <div
              style={{
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
                marginBottom: "var(--space-2)",
              }}
            >
              Select the default coding agent for new tasks in this workspace.
            </div>
            <select
              style={selectStyle}
              value={currentSettings?.defaultAgentCliType ?? 0}
              onChange={(e) => {
                updateSettingsMutation.mutate({
                  workspaceId: WORKSPACE_ID,
                  defaultAgentCliType: Number(e.target.value),
                });
              }}
              data-testid="default-agent-select"
            >
              <option value={0}>Unspecified</option>
              {capabilities.map((cap) => (
                <option key={cap.agentCliType} value={cap.agentCliType}>
                  {cap.displayName}
                </option>
              ))}
            </select>
          </div>
        )}

        {activeTab === "appearance" && (
          <>
            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Theme</h2>
              <div style={toggleGroupStyle}>
                <button
                  style={getToggleStyle(theme === "light")}
                  onClick={() => setTheme("light")}
                  data-testid="theme-light"
                >
                  Light
                </button>
                <button
                  style={getToggleStyle(theme === "dark")}
                  onClick={() => setTheme("dark")}
                  data-testid="theme-dark"
                >
                  Dark
                </button>
              </div>
            </div>
            <div style={sectionStyle}>
              <h2 style={sectionTitleStyle}>Badge Theme</h2>
              <div
                style={{
                  fontSize: "var(--font-size-sm)",
                  color: "var(--color-text-secondary)",
                  marginBottom: "var(--space-2)",
                }}
              >
                Current theme: <strong>{currentBadgeTheme}</strong>
              </div>
              <div
                style={{
                  fontSize: "var(--font-size-xs)",
                  color: "var(--color-text-tertiary)",
                }}
              >
                Badge theme customization is managed by workspace settings.
              </div>
            </div>
          </>
        )}

        {activeTab === "shortcuts" && (
          <div style={sectionStyle}>
            <h2 style={sectionTitleStyle}>Keyboard Shortcuts</h2>
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
              <ShortcutRow keys={["\u2318K"]} description="Open command palette" />
              <ShortcutRow keys={["\u2318B"]} description="Toggle sidebar" />
              <ShortcutRow keys={["G", "T"]} description="Go to Tasks" />
              <ShortcutRow keys={["G", "I"]} description="Go to Inbox" />
              <ShortcutRow keys={["C"]} description="Create new task" />
            </div>
          </div>
        )}

        {activeTab === "credentials" && (
          <div style={sectionStyle}>
            <h2 style={sectionTitleStyle}>Credentials</h2>
            <CredentialManager />
          </div>
        )}

        {activeTab === "about" && (
          <div style={sectionStyle}>
            <h2 style={sectionTitleStyle}>About</h2>
            <div
              style={{
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
                lineHeight: 1.6,
              }}
            >
              <div>DexDex Desktop v0.1.0</div>
              <div style={{ marginTop: "var(--space-1)" }}>
                Coding agent orchestration platform.
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function ShortcutRow({ keys, description }: { keys: string[]; description: string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "var(--space-2) 0",
      }}
    >
      <span style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-secondary)" }}>
        {description}
      </span>
      <div style={{ display: "flex", gap: "var(--space-1)" }}>
        {keys.map((key, i) => (
          <span key={i}>
            <kbd
              style={{
                display: "inline-block",
                padding: "2px 8px",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--color-border)",
                backgroundColor: "var(--color-bg-tertiary)",
                fontSize: "var(--font-size-xs)",
                fontFamily: "var(--font-sans)",
                color: "var(--color-text-secondary)",
                lineHeight: "18px",
              }}
            >
              {key}
            </kbd>
            {i < keys.length - 1 && (
              <span
                style={{
                  margin: "0 2px",
                  fontSize: "var(--font-size-xs)",
                  color: "var(--color-text-tertiary)",
                }}
              >
                then
              </span>
            )}
          </span>
        ))}
      </div>
    </div>
  );
}
