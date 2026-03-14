/**
 * Inline comment thread component for line-level review comments.
 * Renders a thread of comments anchored to a specific file/line position.
 */

import { type CSSProperties, useState } from "react";
import type { ReviewComment } from "../../lib/mock-data";

interface InlineCommentThreadProps {
  filePath: string;
  lineNumber: number;
  side: string;
  comments: ReviewComment[];
  onReply: (body: string) => void;
  onResolve: (commentId: string) => void;
  onReopen: (commentId: string) => void;
  onDelete: (commentId: string) => void;
}

export function InlineCommentThread({
  filePath,
  lineNumber,
  side,
  comments,
  onReply,
  onResolve,
  onReopen,
  onDelete,
}: InlineCommentThreadProps) {
  const [replyText, setReplyText] = useState("");
  const [showReply, setShowReply] = useState(false);

  const isResolved = comments.length > 0 && comments[0].status === "RESOLVED";

  const handleSubmit = () => {
    if (replyText.trim()) {
      onReply(replyText.trim());
      setReplyText("");
      setShowReply(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const containerStyle: CSSProperties = {
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-md)",
    margin: "var(--space-2) 0",
    opacity: isResolved ? 0.6 : 1,
  };

  const headerStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    padding: "var(--space-2) var(--space-3)",
    backgroundColor: "var(--color-bg-secondary)",
    borderBottom: "1px solid var(--color-border)",
    fontSize: "var(--font-size-xs)",
    color: "var(--color-text-secondary)",
  };

  const commentStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-3)",
    fontSize: "var(--font-size-sm)",
    lineHeight: 1.6,
    borderBottom: "1px solid var(--color-border-subtle)",
  };

  const actionButtonStyle: CSSProperties = {
    padding: "2px 6px",
    borderRadius: "var(--radius-sm)",
    fontSize: "var(--font-size-xs)",
    cursor: "pointer",
    color: "var(--color-text-secondary)",
    backgroundColor: "transparent",
  };

  return (
    <div style={containerStyle} data-testid={`comment-thread-${filePath}-${lineNumber}`}>
      <div style={headerStyle}>
        <span>
          {filePath}:{lineNumber} ({side})
        </span>
        <span style={{ fontWeight: 500 }}>
          {isResolved ? "Resolved" : `${comments.length} comment${comments.length !== 1 ? "s" : ""}`}
        </span>
      </div>

      {comments.map((comment) => (
        <div key={comment.reviewCommentId} style={commentStyle}>
          <div style={{ color: "var(--color-text-primary)", whiteSpace: "pre-wrap" }}>{comment.body}</div>
          <div style={{ display: "flex", gap: "var(--space-2)", marginTop: "var(--space-1)" }}>
            {comment.status === "ACTIVE" ? (
              <button style={actionButtonStyle} onClick={() => onResolve(comment.reviewCommentId)}>
                Resolve
              </button>
            ) : (
              <button style={actionButtonStyle} onClick={() => onReopen(comment.reviewCommentId)}>
                Reopen
              </button>
            )}
            <button style={{ ...actionButtonStyle, color: "var(--color-status-failed)" }} onClick={() => onDelete(comment.reviewCommentId)}>
              Delete
            </button>
          </div>
        </div>
      ))}

      <div style={{ padding: "var(--space-2) var(--space-3)" }}>
        {showReply ? (
          <div>
            <textarea
              value={replyText}
              onChange={(e) => setReplyText(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Reply... (Cmd+Enter to submit)"
              style={{
                width: "100%",
                minHeight: "60px",
                padding: "var(--space-2)",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--color-border)",
                fontSize: "var(--font-size-sm)",
                resize: "vertical",
                backgroundColor: "var(--color-bg-primary)",
                color: "var(--color-text-primary)",
              }}
            />
            <div style={{ display: "flex", gap: "var(--space-2)", marginTop: "var(--space-1)" }}>
              <button
                style={{
                  ...actionButtonStyle,
                  backgroundColor: "var(--color-accent)",
                  color: "var(--color-text-inverse)",
                }}
                onClick={handleSubmit}
              >
                Submit
              </button>
              <button style={actionButtonStyle} onClick={() => setShowReply(false)}>
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <button
            style={{ ...actionButtonStyle, color: "var(--color-accent)" }}
            onClick={() => setShowReply(true)}
          >
            + Reply
          </button>
        )}
      </div>
    </div>
  );
}
