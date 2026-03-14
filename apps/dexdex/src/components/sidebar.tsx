/**
 * Sidebar navigation component with Linear-style layout.
 */

import type { CSSProperties } from "react";
import { useAppStore } from "../stores/app-store";

interface SidebarProps {
  activePath: string;
  onNavigate: (path: string) => void;
}

const NAV_ITEMS = [
  { path: "/inbox", label: "Inbox", icon: "\u{1F4E5}" },
  { path: "/tasks", label: "Tasks", icon: "\u{1F4CB}" },
  { path: "/settings", label: "Settings", icon: "\u2699\uFE0F" },
];

export function Sidebar({ activePath, onNavigate }: SidebarProps) {
  const { sidebarOpen, connectionStatus } = useAppStore();

  if (!sidebarOpen) {
    return null;
  }

  const containerStyle: CSSProperties = {
    width: "var(--sidebar-width)",
    minWidth: "var(--sidebar-width)",
    height: "100%",
    backgroundColor: "var(--color-bg-sidebar)",
    borderRight: "1px solid var(--color-border)",
    display: "flex",
    flexDirection: "column",
    userSelect: "none",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-4) var(--space-3)",
    display: "flex",
    alignItems: "center",
    gap: "var(--space-2)",
    fontSize: "var(--font-size-md)",
    fontWeight: 600,
    color: "var(--color-text-primary)",
  };

  const connectionDotStyle: CSSProperties = {
    width: "8px",
    height: "8px",
    borderRadius: "50%",
    backgroundColor:
      connectionStatus === "connected"
        ? "var(--color-connected)"
        : connectionStatus === "reconnecting"
          ? "var(--color-reconnecting)"
          : "var(--color-disconnected)",
    flexShrink: 0,
  };

  const navStyle: CSSProperties = {
    padding: "0 var(--space-2)",
    display: "flex",
    flexDirection: "column",
    gap: "1px",
    flex: 1,
  };

  return (
    <nav style={containerStyle} data-testid="sidebar" aria-label="Main navigation">
      <div style={headerStyle}>
        <span
          style={connectionDotStyle}
          title={`Connection: ${connectionStatus}`}
          data-testid="connection-dot"
        />
        <span>DexDex</span>
      </div>
      <div style={navStyle}>
        {NAV_ITEMS.map((item) => {
          const isActive = activePath.startsWith(item.path);
          const itemStyle: CSSProperties = {
            display: "flex",
            alignItems: "center",
            gap: "var(--space-2)",
            padding: "var(--space-2) var(--space-3)",
            borderRadius: "var(--radius-md)",
            fontSize: "var(--font-size-base)",
            color: isActive ? "var(--color-text-primary)" : "var(--color-text-secondary)",
            backgroundColor: isActive ? "var(--color-bg-active)" : "transparent",
            cursor: "pointer",
            transition: "background-color 0.1s",
          };

          return (
            <button
              key={item.path}
              style={itemStyle}
              onClick={() => onNavigate(item.path)}
              onMouseEnter={(e) => {
                if (!isActive) {
                  (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
                }
              }}
              onMouseLeave={(e) => {
                if (!isActive) {
                  (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
                }
              }}
              data-testid={`nav-${item.path.slice(1)}`}
            >
              <span style={{ fontSize: "var(--font-size-md)" }}>{item.icon}</span>
              {item.label}
            </button>
          );
        })}
      </div>
      <div
        style={{
          padding: "var(--space-3) var(--space-4)",
          fontSize: "var(--font-size-xs)",
          color: "var(--color-text-tertiary)",
          borderTop: "1px solid var(--color-border)",
        }}
      >
        DexDex v0.1.0
      </div>
    </nav>
  );
}
