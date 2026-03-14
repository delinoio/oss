/**
 * PR detail page showing PR information, review assist items,
 * inline review comments, and auto-fix controls.
 */

import { type CSSProperties, useCallback } from "react";
import { PrStatus as ProtoPrStatus, ReviewAssistStatus } from "../../gen/v1/dexdex_pb";
import { PrStatus, PR_STATUS_CONFIG } from "../../lib/status";
import {
  useGetPullRequest,
  useListReviewAssistItems,
  useListReviewComments,
  useRunAutoFixNowMutation,
  useSetAutoFixPolicyMutation,
  useResolveReviewAssistItemMutation,
} from "../../hooks/use-dexdex-queries";

const PR_STATUS_MAP: Record<number, PrStatus> = {
  [ProtoPrStatus.UNSPECIFIED]: PrStatus.UNSPECIFIED,
  [ProtoPrStatus.OPEN]: PrStatus.OPEN,
  [ProtoPrStatus.APPROVED]: PrStatus.APPROVED,
  [ProtoPrStatus.CHANGES_REQUESTED]: PrStatus.CHANGES_REQUESTED,
  [ProtoPrStatus.MERGED]: PrStatus.MERGED,
  [ProtoPrStatus.CLOSED]: PrStatus.CLOSED,
  [ProtoPrStatus.CI_FAILED]: PrStatus.CI_FAILED,
};

const REVIEW_ASSIST_STATUS_LABELS: Record<number, string> = {
  [ReviewAssistStatus.UNSPECIFIED]: "Unknown",
  [ReviewAssistStatus.OPEN]: "Open",
  [ReviewAssistStatus.AUTO_FIXING]: "Auto-fixing",
  [ReviewAssistStatus.FIXED]: "Fixed",
  [ReviewAssistStatus.DISMISSED]: "Dismissed",
};

interface PrDetailPageProps {
  workspaceId: string;
  prTrackingId: string;
  onBack: () => void;
}

export function PrDetailPage({ workspaceId, prTrackingId, onBack }: PrDetailPageProps) {
  const { data: prData } = useGetPullRequest(workspaceId, prTrackingId);
  const pr = prData?.pullRequest;

  const { data: reviewAssistData } = useListReviewAssistItems(workspaceId, pr?.unitTaskId ?? "");
  const reviewAssistItems = reviewAssistData?.items ?? [];

  const { data: commentsData } = useListReviewComments(workspaceId, prTrackingId);
  const reviewComments = commentsData?.comments ?? [];

  const runAutoFixMutation = useRunAutoFixNowMutation();
  const autoFixPolicyMutation = useSetAutoFixPolicyMutation();
  const resolveItemMutation = useResolveReviewAssistItemMutation();

  const maxAttemptsReached = pr ? pr.fixAttemptCount >= pr.maxFixAttempts : false;

  const handleRunAutoFix = useCallback(() => {
    if (!pr || maxAttemptsReached) return;
    runAutoFixMutation.mutate({ workspaceId, prTrackingId });
  }, [workspaceId, prTrackingId, pr, maxAttemptsReached, runAutoFixMutation]);

  const handleToggleAutoFix = useCallback(() => {
    if (!pr) return;
    autoFixPolicyMutation.mutate({
      workspaceId,
      prTrackingId,
      autoFixEnabled: !pr.autoFixEnabled,
    });
  }, [workspaceId, prTrackingId, pr, autoFixPolicyMutation]);

  const handleResolveItem = useCallback(
    (reviewAssistId: string, resolution: ReviewAssistStatus) => {
      resolveItemMutation.mutate({ workspaceId, reviewAssistId, resolution });
    },
    [workspaceId, resolveItemMutation],
  );

  const viewStatus = pr ? (PR_STATUS_MAP[pr.status] ?? PrStatus.UNSPECIFIED) : PrStatus.UNSPECIFIED;
  const statusConfig = PR_STATUS_CONFIG[viewStatus];

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    flexShrink: 0,
  };

  const contentStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
    padding: "var(--space-4) var(--space-6)",
  };

  const backButtonStyle: CSSProperties = {
    fontSize: "var(--font-size-sm)",
    color: "var(--color-text-secondary)",
    cursor: "pointer",
    display: "inline-flex",
    alignItems: "center",
    gap: "var(--space-1)",
    marginBottom: "var(--space-3)",
    backgroundColor: "transparent",
    border: "none",
    padding: 0,
  };

  const sectionStyle: CSSProperties = {
    marginBottom: "var(--space-6)",
  };

  const sectionTitleStyle: CSSProperties = {
    fontSize: "var(--font-size-base)",
    fontWeight: 600,
    color: "var(--color-text-primary)",
    marginBottom: "var(--space-3)",
  };

  if (!pr) {
    return (
      <div style={containerStyle} data-testid="pr-detail-page">
        <div style={headerStyle}>
          <button style={backButtonStyle} onClick={onBack} data-testid="back-button">
            {"\u2190"} Back to Pull Requests
          </button>
        </div>
        <div style={contentStyle}>
          <div style={{ color: "var(--color-text-tertiary)", textAlign: "center", padding: "var(--space-8)" }}>
            Loading pull request...
          </div>
        </div>
      </div>
    );
  }

  const badgeStyle: CSSProperties = {
    display: "inline-flex",
    alignItems: "center",
    gap: "var(--space-1)",
    padding: "2px 8px",
    borderRadius: "var(--radius-full)",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    color: statusConfig.color,
    backgroundColor: statusConfig.bgColor,
  };

  const detailRowStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: "var(--space-3)",
    marginBottom: "var(--space-2)",
    fontSize: "var(--font-size-sm)",
  };

  const actionButtonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: maxAttemptsReached || runAutoFixMutation.isPending ? "not-allowed" : "pointer",
    backgroundColor: maxAttemptsReached ? "var(--color-bg-tertiary)" : "var(--color-accent)",
    color: maxAttemptsReached ? "var(--color-text-tertiary)" : "var(--color-text-inverse)",
    opacity: maxAttemptsReached || runAutoFixMutation.isPending ? 0.6 : 1,
    border: "none",
  };

  const toggleButtonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: "pointer",
    backgroundColor: pr.autoFixEnabled ? "var(--color-status-completed-bg)" : "var(--color-bg-tertiary)",
    color: pr.autoFixEnabled ? "var(--color-status-completed)" : "var(--color-text-tertiary)",
    border: "none",
  };

  return (
    <div style={containerStyle} data-testid="pr-detail-page">
      <div style={headerStyle}>
        <button style={backButtonStyle} onClick={onBack} data-testid="back-button">
          {"\u2190"} Back to Pull Requests
        </button>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
          <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
            PR Detail
          </h1>
          <span style={badgeStyle}>
            <span>{statusConfig.icon}</span>
            {statusConfig.label}
          </span>
        </div>
      </div>

      <div style={contentStyle}>
        {/* PR Info Section */}
        <div style={sectionStyle}>
          <div style={detailRowStyle}>
            <span style={{ color: "var(--color-text-secondary)", fontWeight: 500 }}>URL:</span>
            <a
              href={pr.prUrl}
              target="_blank"
              rel="noopener noreferrer"
              style={{ color: "var(--color-accent)", textDecoration: "none" }}
            >
              {pr.prUrl}
            </a>
          </div>
          <div style={detailRowStyle}>
            <span style={{ color: "var(--color-text-secondary)", fontWeight: 500 }}>Tracking ID:</span>
            <span style={{ color: "var(--color-text-primary)" }}>{pr.prTrackingId}</span>
          </div>
          <div style={detailRowStyle}>
            <span style={{ color: "var(--color-text-secondary)", fontWeight: 500 }}>Fix Attempts:</span>
            <span style={{ color: "var(--color-text-primary)" }}>
              {pr.fixAttemptCount} / {pr.maxFixAttempts}
            </span>
          </div>
          {pr.unitTaskId && (
            <div style={detailRowStyle}>
              <span style={{ color: "var(--color-text-secondary)", fontWeight: 500 }}>Linked Task:</span>
              <span style={{ color: "var(--color-text-primary)" }}>{pr.unitTaskId}</span>
            </div>
          )}
        </div>

        {/* Actions Section */}
        <div style={{ ...sectionStyle, display: "flex", gap: "var(--space-3)" }}>
          <button
            style={actionButtonStyle}
            onClick={handleRunAutoFix}
            disabled={maxAttemptsReached || runAutoFixMutation.isPending}
            data-testid="run-autofix-button"
          >
            {runAutoFixMutation.isPending
              ? "Running..."
              : maxAttemptsReached
                ? "Max Attempts Reached"
                : "Run Auto-Fix Now"}
          </button>
          <button
            style={toggleButtonStyle}
            onClick={handleToggleAutoFix}
            data-testid="autofix-policy-toggle"
          >
            Auto-fix: {pr.autoFixEnabled ? "ON" : "OFF"}
          </button>
        </div>

        {/* Review Assist Items Section */}
        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Review Assist Items</h2>
          {reviewAssistItems.length === 0 ? (
            <div
              style={{
                padding: "var(--space-4)",
                color: "var(--color-text-tertiary)",
                fontSize: "var(--font-size-sm)",
              }}
            >
              No review assist items.
            </div>
          ) : (
            reviewAssistItems.map((item) => {
              const isResolvable =
                item.status === ReviewAssistStatus.OPEN || item.status === ReviewAssistStatus.AUTO_FIXING;

              const itemStyle: CSSProperties = {
                padding: "var(--space-3) var(--space-4)",
                borderRadius: "var(--radius-md)",
                border: "1px solid var(--color-border)",
                marginBottom: "var(--space-2)",
              };

              const itemStatusStyle: CSSProperties = {
                display: "inline-flex",
                padding: "1px 6px",
                borderRadius: "var(--radius-full)",
                fontSize: "var(--font-size-xs)",
                fontWeight: 500,
                color:
                  item.status === ReviewAssistStatus.FIXED
                    ? "var(--color-status-completed)"
                    : item.status === ReviewAssistStatus.DISMISSED
                      ? "var(--color-text-tertiary)"
                      : item.status === ReviewAssistStatus.AUTO_FIXING
                        ? "var(--color-status-in-progress)"
                        : "var(--color-status-action)",
                backgroundColor:
                  item.status === ReviewAssistStatus.FIXED
                    ? "var(--color-status-completed-bg)"
                    : item.status === ReviewAssistStatus.DISMISSED
                      ? "var(--color-bg-tertiary)"
                      : item.status === ReviewAssistStatus.AUTO_FIXING
                        ? "var(--color-status-in-progress-bg)"
                        : "var(--color-status-action-bg)",
              };

              const resolveButtonStyle: CSSProperties = {
                padding: "2px 8px",
                borderRadius: "var(--radius-sm)",
                fontSize: "var(--font-size-xs)",
                cursor: "pointer",
                border: "none",
                backgroundColor: "var(--color-bg-tertiary)",
                color: "var(--color-text-secondary)",
              };

              return (
                <div key={item.reviewAssistId} style={itemStyle} data-testid={`review-assist-item-${item.reviewAssistId}`}>
                  <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)", marginBottom: "var(--space-2)" }}>
                    <span style={itemStatusStyle}>
                      {REVIEW_ASSIST_STATUS_LABELS[item.status] ?? "Unknown"}
                    </span>
                    {isResolvable && (
                      <>
                        <button
                          style={{ ...resolveButtonStyle, color: "var(--color-status-completed)" }}
                          onClick={() => handleResolveItem(item.reviewAssistId, ReviewAssistStatus.FIXED)}
                        >
                          Mark Fixed
                        </button>
                        <button
                          style={resolveButtonStyle}
                          onClick={() => handleResolveItem(item.reviewAssistId, ReviewAssistStatus.DISMISSED)}
                        >
                          Dismiss
                        </button>
                      </>
                    )}
                  </div>
                  <div style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-primary)", whiteSpace: "pre-wrap" }}>
                    {item.body}
                  </div>
                </div>
              );
            })
          )}
        </div>

        {/* Review Comments Section */}
        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Inline Comments</h2>
          {reviewComments.length === 0 ? (
            <div
              style={{
                padding: "var(--space-4)",
                color: "var(--color-text-tertiary)",
                fontSize: "var(--font-size-sm)",
              }}
            >
              No inline comments.
            </div>
          ) : (
            reviewComments.map((comment) => {
              const isResolved = comment.status === "RESOLVED";
              const commentStyle: CSSProperties = {
                padding: "var(--space-3) var(--space-4)",
                borderRadius: "var(--radius-md)",
                border: "1px solid var(--color-border)",
                marginBottom: "var(--space-2)",
              };

              const resolvedStyle: CSSProperties = {
                display: "inline-flex",
                padding: "1px 6px",
                borderRadius: "var(--radius-full)",
                fontSize: "var(--font-size-xs)",
                fontWeight: 500,
                color: isResolved ? "var(--color-status-completed)" : "var(--color-status-action)",
                backgroundColor: isResolved ? "var(--color-status-completed-bg)" : "var(--color-status-action-bg)",
              };

              return (
                <div key={comment.reviewCommentId} style={commentStyle} data-testid={`review-comment-${comment.reviewCommentId}`}>
                  <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)", marginBottom: "var(--space-2)" }}>
                    <span style={resolvedStyle}>
                      {isResolved ? "Resolved" : "Open"}
                    </span>
                    {comment.filePath && (
                      <span style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-tertiary)" }}>
                        {comment.filePath}
                        {comment.lineNumber ? `:${comment.lineNumber}` : ""}
                      </span>
                    )}
                  </div>
                  <div style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-primary)", whiteSpace: "pre-wrap" }}>
                    {comment.body}
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
