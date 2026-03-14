/**
 * Settings page with theme toggle and app preferences.
 */

import type { CSSProperties } from "react";
import { useAppStore } from "../../stores/app-store";

export function SettingsPage() {
  const { theme, setTheme } = useAppStore();

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

  return (
    <div style={containerStyle} data-testid="settings-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Settings</h1>
      </div>
      <div style={contentStyle}>
        {/* Theme Section */}
        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Appearance</h2>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <div
              style={{
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                color: "var(--color-text-secondary)",
                marginBottom: "var(--space-2)",
              }}
            >
              Theme
            </div>
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
        </div>

        {/* Keyboard Shortcuts Section */}
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

        {/* About Section */}
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
