/**
 * Tab bar component for navigation between open items.
 */

import type { CSSProperties } from "react";

interface Tab {
  id: string;
  label: string;
  path: string;
}

interface TabBarProps {
  tabs: Tab[];
  activeTabId: string;
  onTabClick: (tab: Tab) => void;
  onTabClose: (tab: Tab) => void;
}

export function TabBar({ tabs, activeTabId, onTabClick, onTabClose }: TabBarProps) {
  const containerStyle: CSSProperties = {
    height: "var(--tab-bar-height)",
    minHeight: "var(--tab-bar-height)",
    display: "flex",
    alignItems: "stretch",
    backgroundColor: "var(--color-bg-secondary)",
    borderBottom: "1px solid var(--color-border)",
    overflow: "hidden",
  };

  if (tabs.length === 0) {
    return <div style={containerStyle} data-testid="tab-bar" />;
  }

  return (
    <div style={containerStyle} data-testid="tab-bar" role="tablist">
      {tabs.map((tab) => {
        const isActive = tab.id === activeTabId;
        const tabStyle: CSSProperties = {
          display: "flex",
          alignItems: "center",
          gap: "var(--space-2)",
          padding: "0 var(--space-3)",
          fontSize: "var(--font-size-sm)",
          color: isActive ? "var(--color-text-primary)" : "var(--color-text-secondary)",
          backgroundColor: isActive ? "var(--color-bg-primary)" : "transparent",
          borderRight: "1px solid var(--color-border)",
          borderBottom: isActive ? "2px solid var(--color-accent)" : "2px solid transparent",
          cursor: "pointer",
          whiteSpace: "nowrap",
          maxWidth: "200px",
          overflow: "hidden",
          textOverflow: "ellipsis",
        };

        const closeStyle: CSSProperties = {
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: "16px",
          height: "16px",
          borderRadius: "var(--radius-sm)",
          fontSize: "10px",
          color: "var(--color-text-tertiary)",
          flexShrink: 0,
        };

        return (
          <div
            key={tab.id}
            style={tabStyle}
            role="tab"
            aria-selected={isActive}
            onClick={() => onTabClick(tab)}
          >
            <span
              style={{
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {tab.label}
            </span>
            <button
              style={closeStyle}
              onClick={(e) => {
                e.stopPropagation();
                onTabClose(tab);
              }}
              aria-label={`Close ${tab.label}`}
            >
              \u2715
            </button>
          </div>
        );
      })}
    </div>
  );
}
