/**
 * Review assist panel showing review suggestions and inline comments for a task.
 * Supports accept/dismiss actions, code block rendering, and collapsible sections.
 */

import { type CSSProperties, useState } from "react";
import {
  useListReviewAssistItems,
  useListReviewComments,
  useCreateReviewCommentMutation,
  useResolveReviewCommentMutation,
  useReopenReviewCommentMutation,
  useDeleteReviewCommentMutation,
} from "../../hooks/use-dexdex-queries";
import { DiffCommentView } from "./diff-comment-view";

const WORKSPACE_ID = "workspace-default";

interface ReviewAssistPanelProps {
  unitTaskId: string;
  prTrackingId?: string;
}

export function ReviewAssistPanel({ unitTaskId, prTrackingId }: ReviewAssistPanelProps) {
  const { data: assistData, isLoading: assistLoading } = useListReviewAssistItems(WORKSPACE_ID, unitTaskId);
  const { data: commentsData, isLoading: commentsLoading } = useListReviewComments(WORKSPACE_ID, prTrackingId ?? "");

  const createCommentMutation = useCreateReviewCommentMutation();
  const resolveCommentMutation = useResolveReviewCommentMutation();
  const reopenCommentMutation = useReopenReviewCommentMutation();
  const deleteCommentMutation = useDeleteReviewCommentMutation();

  const [suggestionsCollapsed, setSuggestionsCollapsed] = useState(false);
  const [commentsCollapsed, setCommentsCollapsed] = useState(false);
  const [dismissedItems, setDismissedItems] = useState<Set<string>>(new Set());

  const items = assistData?.items ?? [];
  const comments = commentsData?.comments ?? [];
  const visibleItems = items.filter((item) => !dismissedItems.has(item.reviewAssistId));

  const isLoading = assistLoading || commentsLoading;

  if (!isLoading && visibleItems.length === 0 && comments.length === 0) {
    return null;
  }

  const handleDismiss = (reviewAssistId: string) => {
    setDismissedItems((prev) => new Set(prev).add(reviewAssistId));
  };

  const handleAccept = (reviewAssistId: string) => {
    console.log("[ReviewAssist] Accepted suggestion:", reviewAssistId);
    setDismissedItems((prev) => new Set(prev).add(reviewAssistId));
  };

  const containerStyle: CSSProperties = {
    marginTop: "var(--space-4)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-md)",
    overflow: "hidden",
  };

  const sectionHeaderStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    padding: "var(--space-3) var(--space-4)",
    backgroundColor: "var(--color-bg-secondary)",
    fontWeight: 600,
    fontSize: "var(--font-size-sm)",
    borderBottom: "1px solid var(--color-border)",
    cursor: "pointer",
    userSelect: "none",
  };

  const itemStyle: CSSProperties = {
    padding: "var(--space-3) var(--space-4)",
    fontSize: "var(--font-size-sm)",
    lineHeight: 1.6,
    borderBottom: "1px solid var(--color-border)",
  };

  const actionButtonStyle: CSSProperties = {
    padding: "2px 8px",
    borderRadius: "var(--radius-sm)",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    cursor: "pointer",
  };

  if (isLoading) {
    return (
      <div style={containerStyle} data-testid="review-assist-panel">
        <div style={sectionHeaderStyle}>Loading review data...</div>
      </div>
    );
  }

  return (
    <div style={containerStyle} data-testid="review-assist-panel">
      {visibleItems.length > 0 && (
        <>
          <div
            style={sectionHeaderStyle}
            onClick={() => setSuggestionsCollapsed(!suggestionsCollapsed)}
          >
            <span>{suggestionsCollapsed ? "\u25B6" : "\u25BC"} Review Suggestions ({visibleItems.length})</span>
          </div>
          {!suggestionsCollapsed && visibleItems.map((item) => {
            const hasCodeBlock = item.body.includes("```");
            return (
              <div key={item.reviewAssistId} style={itemStyle}>
                <div style={{ color: "var(--color-text-primary)", whiteSpace: "pre-wrap" }}>
                  {hasCodeBlock ? renderWithCodeBlocks(item.body) : item.body}
                </div>
                <div style={{ display: "flex", gap: "var(--space-2)", marginTop: "var(--space-2)" }}>
                  <button
                    style={{
                      ...actionButtonStyle,
                      backgroundColor: "var(--color-accent)",
                      color: "var(--color-text-inverse)",
                    }}
                    onClick={() => handleAccept(item.reviewAssistId)}
                  >
                    Accept
                  </button>
                  <button
                    style={{
                      ...actionButtonStyle,
                      color: "var(--color-text-secondary)",
                      backgroundColor: "var(--color-bg-tertiary)",
                    }}
                    onClick={() => handleDismiss(item.reviewAssistId)}
                  >
                    Dismiss
                  </button>
                </div>
              </div>
            );
          })}
        </>
      )}
      {comments.length > 0 && (
        <>
          <div
            style={sectionHeaderStyle}
            onClick={() => setCommentsCollapsed(!commentsCollapsed)}
          >
            <span>{commentsCollapsed ? "\u25B6" : "\u25BC"} Inline Comments ({comments.length})</span>
          </div>
          {!commentsCollapsed && (
            <div style={{ padding: "var(--space-3) var(--space-4)" }}>
              <DiffCommentView
                comments={comments}
                onReply={(prId, filePath, side, lineNumber, body) => {
                  createCommentMutation.mutate({
                    workspaceId: WORKSPACE_ID,
                    prTrackingId: prId,
                    body,
                    filePath,
                    side,
                    lineNumber,
                  });
                }}
                onResolve={(commentId) => {
                  resolveCommentMutation.mutate({
                    workspaceId: WORKSPACE_ID,
                    reviewCommentId: commentId,
                  });
                }}
                onReopen={(commentId) => {
                  reopenCommentMutation.mutate({
                    workspaceId: WORKSPACE_ID,
                    reviewCommentId: commentId,
                  });
                }}
                onDelete={(commentId) => {
                  deleteCommentMutation.mutate({
                    workspaceId: WORKSPACE_ID,
                    reviewCommentId: commentId,
                  });
                }}
              />
            </div>
          )}
        </>
      )}
    </div>
  );
}

/**
 * Simple renderer that splits text by code blocks (```) and renders
 * code sections with monospace styling.
 */
function renderWithCodeBlocks(text: string) {
  const parts = text.split(/(```[\s\S]*?```)/g);
  return parts.map((part, i) => {
    if (part.startsWith("```") && part.endsWith("```")) {
      const code = part.slice(3, -3).replace(/^\w+\n/, "");
      return (
        <pre
          key={i}
          style={{
            margin: "var(--space-2) 0",
            padding: "var(--space-2) var(--space-3)",
            backgroundColor: "var(--color-bg-tertiary)",
            borderRadius: "var(--radius-sm)",
            fontFamily: "var(--font-mono)",
            fontSize: "var(--font-size-xs)",
            overflow: "auto",
          }}
        >
          {code}
        </pre>
      );
    }
    return <span key={i}>{part}</span>;
  });
}
