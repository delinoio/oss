/**
 * Diff viewer component with unified and split view modes.
 * Renders a diff string with syntax-colored lines, line number gutters,
 * file navigation, and optional comment anchoring.
 */

import { useRef, useState } from "react";
import type { CSSProperties } from "react";

interface DiffViewerProps {
  diff: string;
  mode?: "unified" | "split";
  onAddComment?: (filePath: string, side: "LEFT" | "RIGHT", lineNumber: number) => void;
}

interface DiffLine {
  type: "add" | "remove" | "context" | "header";
  content: string;
  oldLineNum: number | null;
  newLineNum: number | null;
}

interface DiffFile {
  filePath: string;
  lines: DiffLine[];
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

function splitDiffByFile(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  const sections = diff.split(/^(?=diff --git)/m);

  for (const section of sections) {
    if (!section.trim()) continue;
    // Extract file path from "+++ b/path"
    const pathMatch = section.match(/^\+\+\+ b\/(.+)$/m);
    const filePath = pathMatch?.[1] ?? "unknown";
    const lines = parseDiffLines(section);
    files.push({ filePath, lines });
  }

  // If no "diff --git" headers found, treat entire diff as a single file
  if (files.length === 0 && diff.trim()) {
    const pathMatch = diff.match(/^\+\+\+ b\/(.+)$/m);
    const filePath = pathMatch?.[1] ?? "unknown";
    files.push({ filePath, lines: parseDiffLines(diff) });
  }

  return files;
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

const modeButtonStyle = (isActive: boolean): CSSProperties => ({
  padding: "2px 8px",
  borderRadius: "var(--radius-sm)",
  fontSize: "var(--font-size-xs)",
  fontWeight: isActive ? 600 : 400,
  color: isActive ? "var(--color-accent)" : "var(--color-text-secondary)",
  backgroundColor: isActive ? "var(--color-accent-subtle)" : "transparent",
  border: "none",
  cursor: "pointer",
});

const baseGutterStyle: CSSProperties = {
  width: "48px",
  padding: "0 8px",
  textAlign: "right",
  userSelect: "none",
  whiteSpace: "nowrap",
  verticalAlign: "top",
  position: "relative",
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

function CommentAnchorGutter({
  line,
  side,
  filePath,
  onAddComment,
  borderRight,
}: {
  line: DiffLine | null;
  side: "LEFT" | "RIGHT";
  filePath: string;
  onAddComment?: DiffViewerProps["onAddComment"];
  borderRight?: boolean;
}) {
  const [hovered, setHovered] = useState(false);
  const lineNumber = side === "LEFT" ? line?.oldLineNum : line?.newLineNum;
  const showButton = onAddComment && line && line.type !== "header" && lineNumber != null;

  return (
    <td
      style={{
        ...baseGutterStyle,
        ...(borderRight ? { borderRight: "1px solid var(--color-border-subtle)" } : {}),
        ...(line ? GUTTER_STYLES[line.type] : {}),
      }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      {showButton && hovered ? (
        <button
          onClick={() => onAddComment(filePath, side, lineNumber)}
          style={{
            position: "absolute",
            left: "2px",
            top: "50%",
            transform: "translateY(-50%)",
            width: "16px",
            height: "16px",
            borderRadius: "50%",
            border: "none",
            backgroundColor: "var(--color-accent)",
            color: "white",
            fontSize: "12px",
            lineHeight: "16px",
            padding: 0,
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}
          aria-label={`Add comment on ${side.toLowerCase()} line ${lineNumber}`}
        >
          +
        </button>
      ) : null}
      {lineNumber ?? ""}
    </td>
  );
}

function UnifiedDiffTable({
  lines,
  filePath,
  onAddComment,
}: {
  lines: DiffLine[];
  filePath: string;
  onAddComment?: DiffViewerProps["onAddComment"];
}) {
  return (
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
            <CommentAnchorGutter
              line={line}
              side="LEFT"
              filePath={filePath}
              onAddComment={onAddComment}
            />
            {/* New line number gutter */}
            <CommentAnchorGutter
              line={line}
              side="RIGHT"
              filePath={filePath}
              onAddComment={onAddComment}
              borderRight
            />
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
  );
}

function SplitDiffTable({
  lines,
  filePath,
  onAddComment,
}: {
  lines: DiffLine[];
  filePath: string;
  onAddComment?: DiffViewerProps["onAddComment"];
}) {
  // Build paired left/right rows by grouping consecutive remove+add blocks
  const rows: { left: DiffLine | null; right: DiffLine | null }[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.type === "header" || line.type === "context") {
      rows.push({ left: line, right: line });
      i++;
    } else if (line.type === "remove") {
      // Collect consecutive removes
      const removes: DiffLine[] = [];
      while (i < lines.length && lines[i].type === "remove") {
        removes.push(lines[i]);
        i++;
      }
      // Collect consecutive adds
      const adds: DiffLine[] = [];
      while (i < lines.length && lines[i].type === "add") {
        adds.push(lines[i]);
        i++;
      }
      // Pair them up
      const maxLen = Math.max(removes.length, adds.length);
      for (let j = 0; j < maxLen; j++) {
        rows.push({
          left: j < removes.length ? removes[j] : null,
          right: j < adds.length ? adds[j] : null,
        });
      }
    } else if (line.type === "add") {
      // Standalone add (not preceded by remove)
      rows.push({ left: null, right: line });
      i++;
    } else {
      i++;
    }
  }

  const splitGutterStyle = (line: DiffLine | null): CSSProperties => ({
    ...baseGutterStyle,
    ...(line ? GUTTER_STYLES[line.type] : {}),
  });

  const splitContentStyle = (line: DiffLine | null): CSSProperties => ({
    padding: "0 8px",
    whiteSpace: "pre",
    overflow: "hidden",
    width: "50%",
    ...(line ? LINE_STYLES[line.type] : {}),
  });

  return (
    <table
      style={{
        width: "100%",
        borderCollapse: "collapse",
        tableLayout: "fixed",
      }}
    >
      <tbody>
        {rows.map((row, idx) => {
          const { left, right } = row;
          return (
            <tr key={idx}>
              {/* Left side gutter */}
              <CommentAnchorGutter
                line={left}
                side="LEFT"
                filePath={filePath}
                onAddComment={onAddComment}
              />
              {/* Left side content */}
              <td
                style={{
                  ...splitContentStyle(left),
                  borderRight: "1px solid var(--color-border-subtle)",
                }}
              >
                {left ? (
                  <>
                    {left.type !== "header" && <LinePrefix type={left.type} />}
                    {left.content}
                  </>
                ) : (
                  ""
                )}
              </td>
              {/* Right side gutter */}
              <CommentAnchorGutter
                line={right}
                side="RIGHT"
                filePath={filePath}
                onAddComment={onAddComment}
              />
              {/* Right side content */}
              <td style={splitContentStyle(right)}>
                {right ? (
                  <>
                    {right.type !== "header" && <LinePrefix type={right.type} />}
                    {right.content}
                  </>
                ) : (
                  ""
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

export function DiffViewer({ diff, mode, onAddComment }: DiffViewerProps) {
  const [viewMode, setViewMode] = useState<"unified" | "split">(mode ?? "unified");
  const containerRef = useRef<HTMLDivElement>(null);

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

  const files = splitDiffByFile(diff);

  const scrollToFile = (filePath: string) => {
    const el = containerRef.current?.querySelector(`[data-file-path="${CSS.escape(filePath)}"]`);
    el?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const containerStyle: CSSProperties = {
    overflow: "auto",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontFamily: "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace",
    fontSize: "var(--font-size-xs)",
    lineHeight: "20px",
  };

  return (
    <div style={containerStyle} data-testid="diff-viewer" ref={containerRef}>
      {/* View mode toggle */}
      <div
        style={{
          display: "flex",
          justifyContent: "flex-end",
          padding: "var(--space-2) var(--space-3)",
          borderBottom: "1px solid var(--color-border)",
          backgroundColor: "var(--color-bg-secondary)",
          gap: "var(--space-1)",
        }}
      >
        <button style={modeButtonStyle(viewMode === "unified")} onClick={() => setViewMode("unified")}>
          Unified
        </button>
        <button style={modeButtonStyle(viewMode === "split")} onClick={() => setViewMode("split")}>
          Split
        </button>
      </div>

      {/* File navigation header */}
      {files.length > 1 && (
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "var(--space-1)",
            padding: "var(--space-2) var(--space-3)",
            borderBottom: "1px solid var(--color-border)",
            backgroundColor: "var(--color-bg-secondary)",
            fontSize: "var(--font-size-xs)",
          }}
        >
          {files.map((file, i) => (
            <button
              key={i}
              onClick={() => scrollToFile(file.filePath)}
              style={{
                padding: "2px 6px",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--color-border)",
                backgroundColor: "var(--color-bg-primary)",
                color: "var(--color-text-secondary)",
                fontSize: "var(--font-size-xs)",
                cursor: "pointer",
                fontFamily: "inherit",
              }}
            >
              {file.filePath}
            </button>
          ))}
        </div>
      )}

      {/* Diff content */}
      {files.map((file, i) => (
        <div key={i} data-file-path={file.filePath}>
          {files.length > 1 && (
            <div
              style={{
                padding: "var(--space-1) var(--space-3)",
                backgroundColor: "var(--color-bg-secondary)",
                borderBottom: "1px solid var(--color-border)",
                borderTop: i > 0 ? "1px solid var(--color-border)" : undefined,
                fontWeight: 600,
                fontSize: "var(--font-size-xs)",
                color: "var(--color-text-primary)",
              }}
            >
              {file.filePath}
            </div>
          )}
          {viewMode === "unified" ? (
            <UnifiedDiffTable lines={file.lines} filePath={file.filePath} onAddComment={onAddComment} />
          ) : (
            <SplitDiffTable lines={file.lines} filePath={file.filePath} onAddComment={onAddComment} />
          )}
        </div>
      ))}
    </div>
  );
}
