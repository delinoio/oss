/**
 * Simple unified diff viewer component.
 * Renders a unified diff string with syntax-colored lines and line number gutter.
 */

import type { CSSProperties } from "react";

interface DiffViewerProps {
  diff: string;
}

interface DiffLine {
  type: "add" | "remove" | "context" | "header";
  content: string;
  oldLineNum: number | null;
  newLineNum: number | null;
}

function parseDiffLines(diff: string): DiffLine[] {
  const rawLines = diff.split("\n");
  const result: DiffLine[] = [];
  let oldLine = 0;
  let newLine = 0;

  for (const raw of rawLines) {
    if (raw.startsWith("@@")) {
      // Parse hunk header: @@ -oldStart,oldCount +newStart,newCount @@
      const match = raw.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (match) {
        oldLine = parseInt(match[1], 10);
        newLine = parseInt(match[2], 10);
      }
      result.push({ type: "header", content: raw, oldLineNum: null, newLineNum: null });
    } else if (raw.startsWith("---") || raw.startsWith("+++")) {
      result.push({ type: "header", content: raw, oldLineNum: null, newLineNum: null });
    } else if (raw.startsWith("diff ") || raw.startsWith("index ")) {
      result.push({ type: "header", content: raw, oldLineNum: null, newLineNum: null });
    } else if (raw.startsWith("+")) {
      result.push({ type: "add", content: raw.slice(1), oldLineNum: null, newLineNum: newLine });
      newLine++;
    } else if (raw.startsWith("-")) {
      result.push({ type: "remove", content: raw.slice(1), oldLineNum: oldLine, newLineNum: null });
      oldLine++;
    } else if (raw.startsWith(" ")) {
      result.push({ type: "context", content: raw.slice(1), oldLineNum: oldLine, newLineNum: newLine });
      oldLine++;
      newLine++;
    } else if (raw === "") {
      // Empty line at end of diff, skip
    } else {
      // Treat other lines (like "\ No newline at end of file") as context
      result.push({ type: "context", content: raw, oldLineNum: null, newLineNum: null });
    }
  }

  return result;
}

const LINE_STYLES: Record<DiffLine["type"], CSSProperties> = {
  add: {
    backgroundColor: "rgba(46, 160, 67, 0.15)",
    color: "var(--color-text-primary)",
  },
  remove: {
    backgroundColor: "rgba(248, 81, 73, 0.15)",
    color: "var(--color-text-primary)",
  },
  context: {
    backgroundColor: "transparent",
    color: "var(--color-text-primary)",
  },
  header: {
    backgroundColor: "rgba(56, 139, 253, 0.1)",
    color: "var(--color-text-secondary)",
    fontWeight: 600,
  },
};

const GUTTER_STYLES: Record<DiffLine["type"], CSSProperties> = {
  add: {
    backgroundColor: "rgba(46, 160, 67, 0.3)",
    color: "rgba(46, 160, 67, 0.8)",
  },
  remove: {
    backgroundColor: "rgba(248, 81, 73, 0.3)",
    color: "rgba(248, 81, 73, 0.8)",
  },
  context: {
    backgroundColor: "transparent",
    color: "var(--color-text-tertiary)",
  },
  header: {
    backgroundColor: "rgba(56, 139, 253, 0.1)",
    color: "var(--color-text-tertiary)",
  },
};

function LinePrefix({ type }: { type: DiffLine["type"] }) {
  const prefixMap: Record<DiffLine["type"], string> = {
    add: "+",
    remove: "-",
    context: " ",
    header: " ",
  };

  return (
    <span
      style={{
        display: "inline-block",
        width: "16px",
        textAlign: "center",
        flexShrink: 0,
        userSelect: "none",
        color: type === "add" ? "rgba(46, 160, 67, 0.8)" : type === "remove" ? "rgba(248, 81, 73, 0.8)" : "transparent",
        fontWeight: 600,
      }}
    >
      {prefixMap[type]}
    </span>
  );
}

export function DiffViewer({ diff }: DiffViewerProps) {
  if (!diff.trim()) {
    return (
      <div
        style={{
          padding: "var(--space-4)",
          textAlign: "center",
          color: "var(--color-text-tertiary)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        No changes
      </div>
    );
  }

  const lines = parseDiffLines(diff);

  const containerStyle: CSSProperties = {
    overflow: "auto",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontFamily: "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace",
    fontSize: "var(--font-size-xs)",
    lineHeight: "20px",
  };

  return (
    <div style={containerStyle} data-testid="diff-viewer">
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          tableLayout: "fixed",
        }}
      >
        <tbody>
          {lines.map((line, idx) => (
            <tr key={idx} style={LINE_STYLES[line.type]}>
              {/* Old line number gutter */}
              <td
                style={{
                  width: "48px",
                  padding: "0 8px",
                  textAlign: "right",
                  userSelect: "none",
                  whiteSpace: "nowrap",
                  verticalAlign: "top",
                  ...GUTTER_STYLES[line.type],
                }}
              >
                {line.oldLineNum ?? ""}
              </td>
              {/* New line number gutter */}
              <td
                style={{
                  width: "48px",
                  padding: "0 8px",
                  textAlign: "right",
                  userSelect: "none",
                  whiteSpace: "nowrap",
                  verticalAlign: "top",
                  borderRight: "1px solid var(--color-border-subtle)",
                  ...GUTTER_STYLES[line.type],
                }}
              >
                {line.newLineNum ?? ""}
              </td>
              {/* Content */}
              <td
                style={{
                  padding: "0 8px",
                  whiteSpace: "pre",
                  overflow: "hidden",
                }}
              >
                {line.type !== "header" && <LinePrefix type={line.type} />}
                {line.content}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
