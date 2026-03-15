/**
 * PR detail page showing PR information, review assist items,
 * inline review comments, and auto-fix controls.
 */

import { type CSSProperties, useCallback, useState } from "react";
import { PrStatus as ProtoPrStatus, ReviewAssistStatus, DiffSide } from "../../gen/v1/dexdex_pb";
import { PrStatus, PR_STATUS_CONFIG } from "../../lib/status";
import {
  useGetPullRequest,
  useListReviewAssistItems,
  useListReviewComments,
  useRunAutoFixNowMutation,
  useSetAutoFixPolicyMutation,
  useResolveReviewAssistItemMutation,
  useCreateReviewCommentMutation,
  useResolveReviewCommentMutation,
  useReopenReviewCommentMutation,
  useDeleteReviewCommentMutation,
  useUpdateReviewCommentMutation,
  useListSubTasksRaw,
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
  const createCommentMutation = useCreateReviewCommentMutation();
  const resolveCommentMutation = useResolveReviewCommentMutation();
  const reopenCommentMutation = useReopenReviewCommentMutation();
  const deleteCommentMutation = useDeleteReviewCommentMutation();
  const updateCommentMutation = useUpdateReviewCommentMutation();

  const { data: rawSubTasks = [] } = useListSubTasksRaw(workspaceId, pr?.unitTaskId ?? "");
  const commits = rawSubTasks.flatMap((st) => st.commitChain ?? []);

  const [showNewCommentForm, setShowNewCommentForm] = useState(false);
  const [newCommentFilePath, setNewCommentFilePath] = useState("");
  const [newCommentSide, setNewCommentSide] = useState<DiffSide>(DiffSide.NEW);
  const [newCommentLine, setNewCommentLine] = useState(1);
  const [newCommentBody, setNewCommentBody] = useState("");
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);
  const [editingCommentBody, setEditingCommentBody] = useState("");
  const [selectedCommitIndex, setSelectedCommitIndex] = useState(0);

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

  const handleCreateComment = useCallback(() => {
    if (!newCommentBody.trim()) return;
    createCommentMutation.mutate(
      {
        workspaceId,
        prTrackingId,
        body: newCommentBody.trim(),
        filePath: newCommentFilePath,
        side: newCommentSide,
        lineNumber: newCommentLine,
      },
      {
        onSuccess: () => {
          setNewCommentBody("");
          setNewCommentFilePath("");
          setNewCommentLine(1);
          setShowNewCommentForm(false);
        },
      },
    );
  }, [workspaceId, prTrackingId, newCommentBody, newCommentFilePath, newCommentSide, newCommentLine, createCommentMutation]);

  const handleResolveComment = useCallback(
    (commentId: string) => {
      resolveCommentMutation.mutate({ workspaceId, reviewCommentId: commentId });
    },
    [workspaceId, resolveCommentMutation],
  );

  const handleReopenComment = useCallback(
    (commentId: string) => {
      reopenCommentMutation.mutate({ workspaceId, reviewCommentId: commentId });
    },
    [workspaceId, reopenCommentMutation],
  );

  const handleDeleteComment = useCallback(
    (commentId: string) => {
      deleteCommentMutation.mutate({ workspaceId, reviewCommentId: commentId });
    },
    [workspaceId, deleteCommentMutation],
  );

  const handleSaveCommentEdit = useCallback(
    (commentId: string) => {
      if (!editingCommentBody.trim()) return;
      updateCommentMutation.mutate(
        {
          workspaceId,
          reviewCommentId: commentId,
          body: editingCommentBody.trim(),
        },
        {
          onSuccess: () => {
            setEditingCommentId(null);
            setEditingCommentBody("");
          },
        },
      );
    },
    [workspaceId, editingCommentBody, updateCommentMutation],
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

        {/* Changes / Diff Viewer Section */}
        {commits.length > 0 && (
          <div style={sectionStyle}>
            <h2 style={sectionTitleStyle}>Changes</h2>
            {commits.length > 1 && (
              <select
                value={selectedCommitIndex}
                onChange={(e) => setSelectedCommitIndex(Number(e.target.value))}
                style={{
                  padding: "var(--space-2) var(--space-3)",
                  borderRadius: "var(--radius-md)",
                  border: "1px solid var(--color-border)",
                  backgroundColor: "var(--color-bg-secondary)",
                  color: "var(--color-text-primary)",
                  fontSize: "var(--font-size-sm)",
                  marginBottom: "var(--space-3)",
                  width: "100%",
                }}
                data-testid="commit-selector"
              >
                {commits.map((c, i) => (
                  <option key={i} value={i}>
                    {c.sha?.slice(0, 7)} - {c.message}
                  </option>
                ))}
              </select>
            )}
            {commits[selectedCommitIndex] && (
              <div
                style={{
                  padding: "var(--space-3) var(--space-4)",
                  borderRadius: "var(--radius-md)",
                  border: "1px solid var(--color-border)",
                  fontSize: "var(--font-size-sm)",
                }}
                data-testid="commit-detail"
              >
                <div style={{ display: "flex", gap: "var(--space-2)", marginBottom: "var(--space-1)" }}>
                  <span style={{ color: "var(--color-text-secondary)", fontWeight: 500 }}>SHA:</span>
                  <code style={{ color: "var(--color-text-primary)", fontFamily: "monospace" }}>
                    {commits[selectedCommitIndex].sha}
                  </code>
                </div>
                <div style={{ color: "var(--color-text-primary)", whiteSpace: "pre-wrap" }}>
                  {commits[selectedCommitIndex].message}
                </div>
              </div>
            )}
          </div>
        )}

        {/* Review Comments Section */}
        <div style={sectionStyle}>
          <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)", marginBottom: "var(--space-3)" }}>
            <h2 style={{ ...sectionTitleStyle, marginBottom: 0 }}>Inline Comments</h2>
            <button
              style={{
                padding: "var(--space-1) var(--space-3)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-xs)",
                fontWeight: 500,
                cursor: "pointer",
                backgroundColor: "var(--color-bg-tertiary)",
                color: "var(--color-text-secondary)",
                border: "none",
              }}
              onClick={() => setShowNewCommentForm(!showNewCommentForm)}
              data-testid="new-comment-toggle"
            >
              {showNewCommentForm ? "Cancel" : "+ New Comment"}
            </button>
          </div>

          {/* New Comment Form */}
          {showNewCommentForm && (
            <div
              style={{
                padding: "var(--space-3) var(--space-4)",
                borderRadius: "var(--radius-md)",
                border: "1px solid var(--color-accent)",
                marginBottom: "var(--space-3)",
                backgroundColor: "var(--color-bg-secondary)",
              }}
              data-testid="new-comment-form"
            >
              <div style={{ display: "flex", gap: "var(--space-2)", marginBottom: "var(--space-2)" }}>
                <input
                  type="text"
                  placeholder="File path (optional)"
                  value={newCommentFilePath}
                  onChange={(e) => setNewCommentFilePath(e.target.value)}
                  style={{
                    flex: 1,
                    padding: "var(--space-2)",
                    borderRadius: "var(--radius-sm)",
                    border: "1px solid var(--color-border)",
                    backgroundColor: "var(--color-bg-primary)",
                    color: "var(--color-text-primary)",
                    fontSize: "var(--font-size-sm)",
                  }}
                  data-testid="new-comment-file-path"
                />
                <select
                  value={newCommentSide}
                  onChange={(e) => setNewCommentSide(Number(e.target.value) as DiffSide)}
                  style={{
                    padding: "var(--space-2)",
                    borderRadius: "var(--radius-sm)",
                    border: "1px solid var(--color-border)",
                    backgroundColor: "var(--color-bg-primary)",
                    color: "var(--color-text-primary)",
                    fontSize: "var(--font-size-sm)",
                  }}
                  data-testid="new-comment-side"
                >
                  <option value={DiffSide.OLD}>Old (left)</option>
                  <option value={DiffSide.NEW}>New (right)</option>
                </select>
                <input
                  type="number"
                  min={1}
                  placeholder="Line"
                  value={newCommentLine}
                  onChange={(e) => setNewCommentLine(Number(e.target.value))}
                  style={{
                    width: "80px",
                    padding: "var(--space-2)",
                    borderRadius: "var(--radius-sm)",
                    border: "1px solid var(--color-border)",
                    backgroundColor: "var(--color-bg-primary)",
                    color: "var(--color-text-primary)",
                    fontSize: "var(--font-size-sm)",
                  }}
                  data-testid="new-comment-line"
                />
              </div>
              <textarea
                placeholder="Write your comment..."
                value={newCommentBody}
                onChange={(e) => setNewCommentBody(e.target.value)}
                rows={3}
                style={{
                  width: "100%",
                  padding: "var(--space-2)",
                  borderRadius: "var(--radius-sm)",
                  border: "1px solid var(--color-border)",
                  backgroundColor: "var(--color-bg-primary)",
                  color: "var(--color-text-primary)",
                  fontSize: "var(--font-size-sm)",
                  resize: "vertical",
                  marginBottom: "var(--space-2)",
                  boxSizing: "border-box",
                }}
                data-testid="new-comment-body"
              />
              <button
                onClick={handleCreateComment}
                disabled={!newCommentBody.trim() || createCommentMutation.isPending}
                style={{
                  padding: "var(--space-2) var(--space-3)",
                  borderRadius: "var(--radius-md)",
                  fontSize: "var(--font-size-sm)",
                  fontWeight: 500,
                  cursor: !newCommentBody.trim() || createCommentMutation.isPending ? "not-allowed" : "pointer",
                  backgroundColor: "var(--color-accent)",
                  color: "var(--color-text-inverse)",
                  opacity: !newCommentBody.trim() || createCommentMutation.isPending ? 0.6 : 1,
                  border: "none",
                }}
                data-testid="submit-new-comment"
              >
                {createCommentMutation.isPending ? "Submitting..." : "Submit Comment"}
              </button>
            </div>
          )}

          {reviewComments.length === 0 && !showNewCommentForm ? (
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
              const isEditing = editingCommentId === comment.reviewCommentId;
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

              const commentActionButtonStyle: CSSProperties = {
                padding: "2px 8px",
                borderRadius: "var(--radius-sm)",
                fontSize: "var(--font-size-xs)",
                cursor: "pointer",
                border: "none",
                backgroundColor: "var(--color-bg-tertiary)",
                color: "var(--color-text-secondary)",
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
                    <div style={{ marginLeft: "auto", display: "flex", gap: "var(--space-1)" }}>
                      {!isResolved && (
                        <button
                          style={{ ...commentActionButtonStyle, color: "var(--color-status-completed)" }}
                          onClick={() => handleResolveComment(comment.reviewCommentId)}
                          data-testid={`resolve-comment-${comment.reviewCommentId}`}
                        >
                          Resolve
                        </button>
                      )}
                      {isResolved && (
                        <button
                          style={{ ...commentActionButtonStyle, color: "var(--color-status-action)" }}
                          onClick={() => handleReopenComment(comment.reviewCommentId)}
                          data-testid={`reopen-comment-${comment.reviewCommentId}`}
                        >
                          Reopen
                        </button>
                      )}
                      <button
                        style={commentActionButtonStyle}
                        onClick={() => {
                          setEditingCommentId(comment.reviewCommentId);
                          setEditingCommentBody(comment.body);
                        }}
                        data-testid={`edit-comment-${comment.reviewCommentId}`}
                      >
                        Edit
                      </button>
                      <button
                        style={{ ...commentActionButtonStyle, color: "var(--color-status-error, #e53e3e)" }}
                        onClick={() => handleDeleteComment(comment.reviewCommentId)}
                        data-testid={`delete-comment-${comment.reviewCommentId}`}
                      >
                        Delete
                      </button>
                    </div>
                  </div>
                  {isEditing ? (
                    <div>
                      <textarea
                        value={editingCommentBody}
                        onChange={(e) => setEditingCommentBody(e.target.value)}
                        rows={3}
                        style={{
                          width: "100%",
                          padding: "var(--space-2)",
                          borderRadius: "var(--radius-sm)",
                          border: "1px solid var(--color-border)",
                          backgroundColor: "var(--color-bg-primary)",
                          color: "var(--color-text-primary)",
                          fontSize: "var(--font-size-sm)",
                          resize: "vertical",
                          marginBottom: "var(--space-2)",
                          boxSizing: "border-box",
                        }}
                        data-testid={`edit-comment-body-${comment.reviewCommentId}`}
                      />
                      <div style={{ display: "flex", gap: "var(--space-2)" }}>
                        <button
                          onClick={() => handleSaveCommentEdit(comment.reviewCommentId)}
                          disabled={!editingCommentBody.trim() || updateCommentMutation.isPending}
                          style={{
                            padding: "2px 8px",
                            borderRadius: "var(--radius-sm)",
                            fontSize: "var(--font-size-xs)",
                            cursor: !editingCommentBody.trim() ? "not-allowed" : "pointer",
                            border: "none",
                            backgroundColor: "var(--color-accent)",
                            color: "var(--color-text-inverse)",
                            opacity: !editingCommentBody.trim() ? 0.6 : 1,
                          }}
                          data-testid={`save-edit-${comment.reviewCommentId}`}
                        >
                          {updateCommentMutation.isPending ? "Saving..." : "Save"}
                        </button>
                        <button
                          onClick={() => {
                            setEditingCommentId(null);
                            setEditingCommentBody("");
                          }}
                          style={{
                            padding: "2px 8px",
                            borderRadius: "var(--radius-sm)",
                            fontSize: "var(--font-size-xs)",
                            cursor: "pointer",
                            border: "none",
                            backgroundColor: "var(--color-bg-tertiary)",
                            color: "var(--color-text-secondary)",
                          }}
                          data-testid={`cancel-edit-${comment.reviewCommentId}`}
                        >
                          Cancel
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-primary)", whiteSpace: "pre-wrap" }}>
                      {comment.body}
                    </div>
                  )}
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
