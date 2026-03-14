/**
 * Diff comment view that groups review comments by file path and line number.
 * Renders file sections with line-anchored inline comment threads.
 */

import type { CSSProperties } from "react";
import type { ReviewComment } from "../../lib/mock-data";
import { InlineCommentThread } from "./inline-comment-thread";

interface DiffCommentViewProps {
  comments: ReviewComment[];
  onReply: (prTrackingId: string, filePath: string, side: string, lineNumber: number, body: string) => void;
  onResolve: (commentId: string) => void;
  onReopen: (commentId: string) => void;
  onDelete: (commentId: string) => void;
}

interface FileGroup {
  filePath: string;
  threads: ThreadGroup[];
}

interface ThreadGroup {
  lineNumber: number;
  side: string;
  comments: ReviewComment[];
}

function groupCommentsByFile(comments: ReviewComment[]): FileGroup[] {
  const fileMap = new Map<string, Map<string, ReviewComment[]>>();

  for (const comment of comments) {
    if (!fileMap.has(comment.filePath)) {
      fileMap.set(comment.filePath, new Map());
    }
    const lineMap = fileMap.get(comment.filePath)!;
    const key = `${comment.lineNumber}:${comment.side}`;
    if (!lineMap.has(key)) {
      lineMap.set(key, []);
    }
    lineMap.get(key)!.push(comment);
  }

  const groups: FileGroup[] = [];
  for (const [filePath, lineMap] of fileMap) {
    const threads: ThreadGroup[] = [];
    for (const [key, threadComments] of lineMap) {
      const [lineStr, side] = key.split(":");
      threads.push({
        lineNumber: Number(lineStr),
        side: side || "RIGHT",
        comments: threadComments,
      });
    }
    threads.sort((a, b) => a.lineNumber - b.lineNumber);
    groups.push({ filePath, threads });
  }

  groups.sort((a, b) => a.filePath.localeCompare(b.filePath));
  return groups;
}

export function DiffCommentView({ comments, onReply, onResolve, onReopen, onDelete }: DiffCommentViewProps) {
  const fileGroups = groupCommentsByFile(comments);

  if (fileGroups.length === 0) {
    return (
      <div
        style={{
          padding: "var(--space-6)",
          textAlign: "center",
          color: "var(--color-text-tertiary)",
          fontSize: "var(--font-size-sm)",
        }}
        data-testid="diff-comment-view-empty"
      >
        No inline comments
      </div>
    );
  }

  const fileSectionStyle: CSSProperties = {
    marginBottom: "var(--space-4)",
  };

  const fileHeaderStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-3)",
    backgroundColor: "var(--color-bg-tertiary)",
    borderRadius: "var(--radius-sm)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 600,
    color: "var(--color-text-secondary)",
    fontFamily: "var(--font-mono)",
    marginBottom: "var(--space-2)",
  };

  return (
    <div data-testid="diff-comment-view">
      {fileGroups.map((fileGroup) => (
        <div key={fileGroup.filePath} style={fileSectionStyle}>
          <div style={fileHeaderStyle}>{fileGroup.filePath}</div>
          {fileGroup.threads.map((thread) => (
            <InlineCommentThread
              key={`${thread.lineNumber}-${thread.side}`}
              filePath={fileGroup.filePath}
              lineNumber={thread.lineNumber}
              side={thread.side}
              comments={thread.comments}
              onReply={(body) =>
                onReply(
                  thread.comments[0]?.prTrackingId ?? "",
                  fileGroup.filePath,
                  thread.side,
                  thread.lineNumber,
                  body,
                )
              }
              onResolve={onResolve}
              onReopen={onReopen}
              onDelete={onDelete}
            />
          ))}
        </div>
      ))}
    </div>
  );
}
