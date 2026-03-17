/**
 * Sidebar navigation component with Linear-style layout.
 */

import { type CSSProperties, useCallback, useEffect, useRef, useState } from "react";
import { useAppStore } from "../stores/app-store";
import { useListWorkspaces, useSetActiveWorkspaceMutation } from "../hooks/use-dexdex-queries";

interface SidebarProps {
  activePath: string;
  onNavigate: (path: string) => void;
}

const NAV_ITEMS = [
  { path: "/inbox", label: "Inbox", icon: "\u{1F4E5}" },
  { path: "/tasks", label: "Tasks", icon: "\u{1F4CB}" },
  { path: "/prs", label: "Pull Requests", icon: "\u{1F500}" },
  { path: "/repository-groups", label: "Repository Groups", icon: "\u{1F5C2}" },
  { path: "/repositories", label: "Repositories", icon: "\u{1F4C1}" },
  { path: "/settings", label: "Settings", icon: "\u2699\uFE0F" },
];

export function Sidebar({ activePath, onNavigate }: SidebarProps) {
  const { sidebarOpen, connectionStatus, activeWorkspaceId, setActiveWorkspaceId } = useAppStore();
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const { data: workspacesData } = useListWorkspaces();
  const setActiveWorkspaceMutation = useSetActiveWorkspaceMutation();

  const workspaces = workspacesData?.workspaces ?? [];
  const currentWorkspace = workspaces.find((w) => w.workspaceId === activeWorkspaceId);
  const currentWorkspaceName = currentWorkspace?.name || activeWorkspaceId;

  const handleWorkspaceSwitch = useCallback(
    (workspaceId: string) => {
      setActiveWorkspaceId(workspaceId);
      setActiveWorkspaceMutation.mutate({ workspaceId });
      setDropdownOpen(false);
    },
    [setActiveWorkspaceId, setActiveWorkspaceMutation],
  );

  useEffect(() => {
    function handleMouseDown(event: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setDropdownOpen(false);
      }
    }
    if (dropdownOpen) {
      document.addEventListener("mousedown", handleMouseDown);
    }
    return () => {
      document.removeEventListener("mousedown", handleMouseDown);
    };
  }, [dropdownOpen]);

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

  const workspaceSelectorStyle: CSSProperties = {
    padding: "0 var(--space-4)",
    marginBottom: "var(--space-2)",
    position: "relative",
  };

  const workspaceButtonStyle: CSSProperties = {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    gap: "var(--space-2)",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    color: "var(--color-text-secondary)",
    backgroundColor: "transparent",
    cursor: "pointer",
    border: "1px solid var(--color-border)",
    transition: "background-color 0.1s",
    textAlign: "left",
  };

  const dropdownMenuStyle: CSSProperties = {
    position: "absolute",
    top: "100%",
    left: 0,
    right: 0,
    marginTop: "var(--space-1)",
    backgroundColor: "var(--color-bg-sidebar)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-md)",
    boxShadow: "0 4px 12px rgba(0, 0, 0, 0.15)",
    zIndex: 50,
    maxHeight: "200px",
    overflowY: "auto",
  };

  const dropdownItemStyle = (isSelected: boolean): CSSProperties => ({
    width: "100%",
    display: "block",
    padding: "var(--space-2) var(--space-3)",
    fontSize: "var(--font-size-sm)",
    color: isSelected ? "var(--color-text-primary)" : "var(--color-text-secondary)",
    backgroundColor: isSelected ? "var(--color-bg-active)" : "transparent",
    cursor: "pointer",
    border: "none",
    textAlign: "left",
    transition: "background-color 0.1s",
  });

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
      <div style={workspaceSelectorStyle} ref={dropdownRef} data-testid="workspace-selector">
        <button
          style={workspaceButtonStyle}
          onClick={() => setDropdownOpen((prev) => !prev)}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
          }}
          data-testid="workspace-selector-button"
          title={`Current workspace: ${currentWorkspaceName}`}
        >
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {currentWorkspaceName}
          </span>
          <span style={{ fontSize: "var(--font-size-xs)", flexShrink: 0 }}>
            {dropdownOpen ? "\u25B2" : "\u25BC"}
          </span>
        </button>
        {dropdownOpen && workspaces.length > 0 && (
          <div style={dropdownMenuStyle} data-testid="workspace-dropdown">
            {workspaces.map((ws) => {
              const isSelected = ws.workspaceId === activeWorkspaceId;
              return (
                <button
                  key={ws.workspaceId}
                  style={dropdownItemStyle(isSelected)}
                  onClick={() => handleWorkspaceSwitch(ws.workspaceId)}
                  onMouseEnter={(e) => {
                    if (!isSelected) {
                      (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
                    }
                  }}
                  onMouseLeave={(e) => {
                    if (!isSelected) {
                      (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
                    }
                  }}
                  data-testid={`workspace-option-${ws.workspaceId}`}
                >
                  {ws.name || ws.workspaceId}
                </button>
              );
            })}
            <button
              style={{
                width: "100%",
                display: "block",
                padding: "var(--space-2) var(--space-3)",
                fontSize: "var(--font-size-sm)",
                color: "var(--color-accent)",
                backgroundColor: "transparent",
                cursor: "pointer",
                border: "none",
                borderTop: "1px solid var(--color-border)",
                textAlign: "left",
                fontWeight: 500,
                transition: "background-color 0.1s",
              }}
              onClick={() => {
                console.log("Create workspace");
                setDropdownOpen(false);
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
              }}
              data-testid="create-workspace-button"
            >
              + Create Workspace
            </button>
          </div>
        )}
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
