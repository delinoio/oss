/**
 * Session output panel for displaying agent session output in the task detail view.
 * Shows streaming output events with appropriate styling per kind.
 */

import { type CSSProperties, useEffect, useRef, useState } from "react";
import type { SessionOutputEvent } from "../../lib/mock-data";
import { SessionOutputKind } from "../../lib/status";

interface SessionOutputPanelProps {
  events: SessionOutputEvent[];
  sessionId: string;
}

export function SessionOutputPanel({ events, sessionId }: SessionOutputPanelProps) {
  const [collapsed, setCollapsed] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Auto-scroll to bottom on new events
  useEffect(() => {
    if (!collapsed && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [events, collapsed]);

  const sessionEvents = events.filter((e) => e.sessionId === sessionId);

  const headerStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    padding: "var(--space-2) var(--space-4)",
    backgroundColor: "var(--color-bg-tertiary)",
    borderTop: "1px solid var(--color-border)",
    cursor: "pointer",
    userSelect: "none",
    fontSize: "var(--font-size-sm)",
    fontWeight: 600,
    color: "var(--color-text-secondary)",
  };

  const contentStyle: CSSProperties = {
    maxHeight: collapsed ? "0" : "300px",
    overflow: collapsed ? "hidden" : "auto",
    transition: "max-height 0.2s ease",
    backgroundColor: "var(--color-bg-secondary)",
    fontFamily: "var(--font-mono)",
    fontSize: "var(--font-size-sm)",
  };

  return (
    <div data-testid="session-output-panel">
      <div
        style={headerStyle}
        onClick={() => setCollapsed(!collapsed)}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            setCollapsed(!collapsed);
          }
        }}
      >
        <span>{collapsed ? "\u25B6" : "\u25BC"} Session Output</span>
        <span style={{ fontWeight: 400, color: "var(--color-text-tertiary)" }}>
          {sessionEvents.length} events
        </span>
      </div>
      <div style={contentStyle} ref={scrollRef}>
        {sessionEvents.length === 0 && !collapsed && (
          <div
            style={{
              padding: "var(--space-4)",
              color: "var(--color-text-tertiary)",
              textAlign: "center",
            }}
          >
            No output events yet
          </div>
        )}
        {!collapsed && sessionEvents.map((event, index) => (
          <OutputEventRow key={`${event.sessionId}-${index}`} event={event} />
        ))}
      </div>
    </div>
  );
}

function OutputEventRow({ event }: { event: SessionOutputEvent }) {
  const [resultCollapsed, setResultCollapsed] = useState(true);

  const baseStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-4)",
    borderBottom: "1px solid var(--color-border-subtle)",
    lineHeight: 1.5,
    whiteSpace: "pre-wrap",
    wordBreak: "break-word",
  };

  switch (event.kind) {
    case SessionOutputKind.TEXT:
      return (
        <div style={{ ...baseStyle, color: "var(--color-text-primary)" }} data-testid="output-text">
          {event.body}
        </div>
      );

    case SessionOutputKind.TOOL_CALL:
      return (
        <div
          style={{
            ...baseStyle,
            backgroundColor: "var(--color-output-tool-call-bg)",
            borderLeft: "3px solid var(--color-output-tool-call-border)",
            color: "var(--color-accent)",
          }}
          data-testid="output-tool-call"
        >
          <span style={{ fontWeight: 600, fontSize: "var(--font-size-xs)", opacity: 0.7 }}>TOOL CALL</span>
          <br />
          {event.body}
        </div>
      );

    case SessionOutputKind.TOOL_RESULT: {
      const previewLength = 80;
      const isLong = event.body.length > previewLength;

      return (
        <div
          style={{
            ...baseStyle,
            backgroundColor: "var(--color-output-tool-result-bg)",
            borderLeft: "3px solid var(--color-output-tool-result-border)",
            color: "var(--color-text-secondary)",
            cursor: isLong ? "pointer" : "default",
          }}
          onClick={() => isLong && setResultCollapsed(!resultCollapsed)}
          data-testid="output-tool-result"
        >
          <span style={{ fontWeight: 600, fontSize: "var(--font-size-xs)", opacity: 0.7 }}>
            RESULT {isLong && (resultCollapsed ? "\u25B6" : "\u25BC")}
          </span>
          <br />
          {resultCollapsed && isLong ? event.body.slice(0, previewLength) + "..." : event.body}
        </div>
      );
    }

    case SessionOutputKind.PROGRESS:
      return (
        <div
          style={{
            ...baseStyle,
            color: "var(--color-output-progress-text)",
            fontStyle: "italic",
          }}
          data-testid="output-progress"
        >
          {event.body}
        </div>
      );

    case SessionOutputKind.WARNING:
      return (
        <div
          style={{
            ...baseStyle,
            backgroundColor: "var(--color-output-warning-bg)",
            color: "var(--color-output-warning-text)",
          }}
          data-testid="output-warning"
        >
          \u26A0 {event.body}
        </div>
      );

    case SessionOutputKind.ERROR:
      return (
        <div
          style={{
            ...baseStyle,
            backgroundColor: "var(--color-output-error-bg)",
            color: "var(--color-output-error-text)",
          }}
          data-testid="output-error"
        >
          \u2717 {event.body}
        </div>
      );

    default:
      return (
        <div style={{ ...baseStyle, color: "var(--color-text-tertiary)" }}>
          {event.body}
        </div>
      );
  }
}
