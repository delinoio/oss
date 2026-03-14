/**
 * Review assist panel showing review suggestions and inline comments for a task.
 */

import type { CSSProperties } from "react";
import { useListReviewAssistItems, useListReviewComments } from "../../hooks/use-dexdex-queries";

const WORKSPACE_ID = "workspace-default";

interface ReviewAssistPanelProps {
  unitTaskId: string;
  prTrackingId?: string;
}

export function ReviewAssistPanel({ unitTaskId, prTrackingId }: ReviewAssistPanelProps) {
  const { data: assistData } = useListReviewAssistItems(WORKSPACE_ID, unitTaskId);
  const { data: commentsData } = useListReviewComments(WORKSPACE_ID, prTrackingId ?? "");

  const items = assistData?.items ?? [];
  const comments = commentsData?.comments ?? [];

  if (items.length === 0 && comments.length === 0) {
    return null;
  }

  const containerStyle: CSSProperties = {
    marginTop: "var(--space-4)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-md)",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-3) var(--space-4)",
    backgroundColor: "var(--color-bg-secondary)",
    fontWeight: 600,
    fontSize: "var(--font-size-sm)",
    borderBottom: "1px solid var(--color-border)",
  };

  const itemStyle: CSSProperties = {
    padding: "var(--space-3) var(--space-4)",
    fontSize: "var(--font-size-sm)",
    lineHeight: 1.6,
    borderBottom: "1px solid var(--color-border)",
  };

  const labelStyle: CSSProperties = {
    display: "inline-block",
    padding: "1px 6px",
    borderRadius: "var(--radius-sm)",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    marginRight: "var(--space-2)",
  };

  return (
    <div style={containerStyle} data-testid="review-assist-panel">
      {items.length > 0 && (
        <>
          <div style={headerStyle}>Review Suggestions</div>
          {items.map((item, idx) => (
            <div key={item.reviewAssistId + idx} style={itemStyle}>
              <span
                style={{
                  ...labelStyle,
                  color: "var(--color-status-in-progress)",
                  backgroundColor: "var(--color-status-in-progress-bg)",
                }}
              >
                Suggestion
              </span>
              {item.body}
            </div>
          ))}
        </>
      )}
      {comments.length > 0 && (
        <>
          <div style={headerStyle}>Review Comments</div>
          {comments.map((comment, idx) => (
            <div key={comment.reviewCommentId + idx} style={itemStyle}>
              <span
                style={{
                  ...labelStyle,
                  color: "var(--color-status-action)",
                  backgroundColor: "var(--color-status-action-bg)",
                }}
              >
                Comment
              </span>
              {comment.body}
            </div>
          ))}
        </>
      )}
    </div>
  );
}
