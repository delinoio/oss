/**
 * Command palette component (Cmd+K) for quick navigation and actions.
 * Linear-style overlay dialog with search and keyboard navigation.
 */

import { type CSSProperties, useCallback, useEffect, useRef, useState } from "react";
import { MOCK_TASKS } from "../lib/mock-data";

export interface CommandAction {
  id: string;
  label: string;
  section: string;
  onSelect: () => void;
}

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  onNavigate: (path: string) => void;
  onCreateTask: () => void;
}

export function CommandPalette({ isOpen, onClose, onNavigate, onCreateTask }: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const actions: CommandAction[] = [
    { id: "nav-tasks", label: "Go to Tasks", section: "Navigation", onSelect: () => { onNavigate("/tasks"); onClose(); } },
    { id: "nav-inbox", label: "Go to Inbox", section: "Navigation", onSelect: () => { onNavigate("/inbox"); onClose(); } },
    { id: "nav-settings", label: "Go to Settings", section: "Navigation", onSelect: () => { onNavigate("/settings"); onClose(); } },
    { id: "create-task", label: "Create new task", section: "Actions", onSelect: () => { onCreateTask(); onClose(); } },
    ...MOCK_TASKS.map((task) => ({
      id: `task-${task.unitTaskId}`,
      label: task.title,
      section: "Tasks",
      onSelect: () => { onNavigate(`/tasks/${task.unitTaskId}`); onClose(); },
    })),
  ];

  const filteredActions = query.trim()
    ? actions.filter((a) => a.label.toLowerCase().includes(query.toLowerCase()))
    : actions;

  useEffect(() => {
    if (isOpen) {
      setQuery("");
      setSelectedIndex(0);
      // Focus input after render
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [isOpen]);

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  // Scroll selected item into view
  useEffect(() => {
    if (!listRef.current) return;
    const items = listRef.current.querySelectorAll("[data-command-item]");
    const target = items[selectedIndex];
    if (target) {
      target.scrollIntoView({ block: "nearest" });
    }
  }, [selectedIndex]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, filteredActions.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
      } else if (e.key === "Enter") {
        e.preventDefault();
        const action = filteredActions[selectedIndex];
        if (action) {
          action.onSelect();
        }
      } else if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    },
    [filteredActions, selectedIndex, onClose],
  );

  if (!isOpen) return null;

  const overlayStyle: CSSProperties = {
    position: "fixed",
    inset: 0,
    backgroundColor: "var(--color-bg-overlay)",
    display: "flex",
    alignItems: "flex-start",
    justifyContent: "center",
    paddingTop: "20vh",
    zIndex: 100,
  };

  const dialogStyle: CSSProperties = {
    width: "min(560px, 90vw)",
    maxHeight: "400px",
    backgroundColor: "var(--color-bg-primary)",
    borderRadius: "var(--radius-lg)",
    boxShadow: "var(--shadow-overlay)",
    border: "1px solid var(--color-border)",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-4)",
    fontSize: "var(--font-size-md)",
    backgroundColor: "transparent",
    borderBottom: "1px solid var(--color-border)",
    outline: "none",
    color: "var(--color-text-primary)",
  };

  const listStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
    padding: "var(--space-2)",
  };

  // Group by section
  const sections = new Map<string, CommandAction[]>();
  for (const action of filteredActions) {
    const list = sections.get(action.section) ?? [];
    list.push(action);
    sections.set(action.section, list);
  }

  let globalIndex = 0;

  return (
    <div
      style={overlayStyle}
      onClick={onClose}
      data-testid="command-palette"
      role="dialog"
      aria-label="Command palette"
    >
      <div
        style={dialogStyle}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <input
          ref={inputRef}
          style={inputStyle}
          type="text"
          placeholder="Type a command or search..."
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Search commands"
          data-testid="command-palette-input"
        />
        <div style={listStyle} ref={listRef}>
          {filteredActions.length === 0 && (
            <div
              style={{
                padding: "var(--space-6)",
                textAlign: "center",
                color: "var(--color-text-tertiary)",
                fontSize: "var(--font-size-sm)",
              }}
            >
              No results found
            </div>
          )}
          {Array.from(sections.entries()).map(([section, sectionActions]) => (
            <div key={section}>
              <div
                style={{
                  padding: "var(--space-2) var(--space-3)",
                  fontSize: "var(--font-size-xs)",
                  fontWeight: 600,
                  color: "var(--color-text-tertiary)",
                  textTransform: "uppercase",
                  letterSpacing: "0.05em",
                }}
              >
                {section}
              </div>
              {sectionActions.map((action) => {
                const itemIndex = globalIndex++;
                const isSelected = itemIndex === selectedIndex;
                const itemStyle: CSSProperties = {
                  display: "flex",
                  alignItems: "center",
                  padding: "var(--space-2) var(--space-3)",
                  borderRadius: "var(--radius-md)",
                  fontSize: "var(--font-size-base)",
                  color: isSelected ? "var(--color-text-primary)" : "var(--color-text-secondary)",
                  backgroundColor: isSelected ? "var(--color-bg-hover)" : "transparent",
                  cursor: "pointer",
                };

                return (
                  <div
                    key={action.id}
                    style={itemStyle}
                    data-command-item
                    onClick={() => action.onSelect()}
                    onMouseEnter={() => setSelectedIndex(itemIndex)}
                    role="option"
                    aria-selected={isSelected}
                  >
                    {action.label}
                  </div>
                );
              })}
            </div>
          ))}
        </div>
        <div
          style={{
            padding: "var(--space-2) var(--space-3)",
            borderTop: "1px solid var(--color-border)",
            fontSize: "var(--font-size-xs)",
            color: "var(--color-text-tertiary)",
            display: "flex",
            gap: "var(--space-4)",
          }}
        >
          <span>\u2191\u2193 Navigate</span>
          <span>\u21B5 Select</span>
          <span>Esc Close</span>
        </div>
      </div>
    </div>
  );
}
