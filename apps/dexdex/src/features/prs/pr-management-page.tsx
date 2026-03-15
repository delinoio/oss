/**
 * PR management page showing tracked pull requests with status badges,
 * auto-fix toggle, and a dialog to track new PRs.
 */

import { type CSSProperties, useState, useCallback } from "react";
import { PrStatus as ProtoPrStatus } from "../../gen/v1/dexdex_pb";
import type { PullRequestRecord } from "../../gen/v1/dexdex_pb";
import { PrListSkeleton } from "../../components/skeleton-loader";
import { PrStatus, PR_STATUS_CONFIG } from "../../lib/status";
import {
  useTrackPullRequestMutation,
  useSetAutoFixPolicyMutation,
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

interface PrManagementPageProps {
  pullRequests: PullRequestRecord[];
  isLoading?: boolean;
  workspaceId: string;
  onPrSelect: (prTrackingId: string) => void;
}

export function PrManagementPage({ pullRequests, isLoading, workspaceId, onPrSelect }: PrManagementPageProps) {
  const [trackDialogOpen, setTrackDialogOpen] = useState(false);
  const [prUrlInput, setPrUrlInput] = useState("");
  const [unitTaskIdInput, setUnitTaskIdInput] = useState("");

  const trackMutation = useTrackPullRequestMutation();
  const autoFixPolicyMutation = useSetAutoFixPolicyMutation();

  const handleTrackPr = useCallback(() => {
    if (!prUrlInput.trim()) return;
    trackMutation.mutate(
      { workspaceId, prUrl: prUrlInput.trim(), unitTaskId: unitTaskIdInput.trim() },
      {
        onSuccess: () => {
          setPrUrlInput("");
          setUnitTaskIdInput("");
          setTrackDialogOpen(false);
        },
      },
    );
  }, [workspaceId, prUrlInput, unitTaskIdInput, trackMutation]);

  const handleToggleAutoFix = useCallback(
    (prTrackingId: string, currentEnabled: boolean) => {
      autoFixPolicyMutation.mutate({
        workspaceId,
        prTrackingId,
        autoFixEnabled: !currentEnabled,
      });
    },
    [workspaceId, autoFixPolicyMutation],
  );

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    flexShrink: 0,
  };

  const listStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
  };

  const trackButtonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    backgroundColor: "var(--color-accent)",
    color: "var(--color-text-inverse)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: "pointer",
  };

  return (
    <div style={containerStyle} data-testid="pr-management-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Pull Requests</h1>
        <button
          style={trackButtonStyle}
          onClick={() => setTrackDialogOpen(true)}
          data-testid="track-pr-button"
        >
          + Track PR
        </button>
      </div>
      <div style={listStyle}>
        {isLoading ? (
          <PrListSkeleton />
        ) : pullRequests.length === 0 ? (
          <div
            style={{
              padding: "var(--space-8)",
              textAlign: "center",
              color: "var(--color-text-tertiary)",
              fontSize: "var(--font-size-sm)",
            }}
            data-testid="pr-empty-state"
          >
            No pull requests tracked yet. Click "+ Track PR" to start tracking a pull request.
          </div>
        ) : (
          pullRequests.map((pr) => (
            <PrRow
              key={pr.prTrackingId}
              pr={pr}
              onClick={() => onPrSelect(pr.prTrackingId)}
              onToggleAutoFix={() => handleToggleAutoFix(pr.prTrackingId, pr.autoFixEnabled)}
            />
          ))
        )}
      </div>

      {trackDialogOpen && (
        <TrackPrDialog
          prUrl={prUrlInput}
          unitTaskId={unitTaskIdInput}
          isSubmitting={trackMutation.isPending}
          onPrUrlChange={setPrUrlInput}
          onUnitTaskIdChange={setUnitTaskIdInput}
          onSubmit={handleTrackPr}
          onClose={() => setTrackDialogOpen(false)}
        />
      )}
    </div>
  );
}

function PrRow({
  pr,
  onClick,
  onToggleAutoFix,
}: {
  pr: PullRequestRecord;
  onClick: () => void;
  onToggleAutoFix: () => void;
}) {
  const viewStatus = PR_STATUS_MAP[pr.status] ?? PrStatus.UNSPECIFIED;
  const config = PR_STATUS_CONFIG[viewStatus];

  const rowStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: "var(--space-3)",
    padding: "var(--space-3) var(--space-6)",
    borderBottom: "1px solid var(--color-border-subtle)",
    cursor: "pointer",
    transition: "background-color 0.1s",
  };

  const badgeStyle: CSSProperties = {
    display: "inline-flex",
    alignItems: "center",
    gap: "var(--space-1)",
    padding: "2px 8px",
    borderRadius: "var(--radius-full)",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    color: config.color,
    backgroundColor: config.bgColor,
    flexShrink: 0,
  };

  const urlStyle: CSSProperties = {
    flex: 1,
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    color: "var(--color-text-primary)",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  };

  const metaStyle: CSSProperties = {
    fontSize: "var(--font-size-xs)",
    color: "var(--color-text-tertiary)",
    whiteSpace: "nowrap",
  };

  const toggleStyle: CSSProperties = {
    display: "inline-flex",
    alignItems: "center",
    gap: "var(--space-1)",
    padding: "2px 6px",
    borderRadius: "var(--radius-sm)",
    fontSize: "var(--font-size-xs)",
    color: pr.autoFixEnabled ? "var(--color-status-completed)" : "var(--color-text-tertiary)",
    backgroundColor: pr.autoFixEnabled ? "var(--color-status-completed-bg)" : "var(--color-bg-tertiary)",
    cursor: "pointer",
    flexShrink: 0,
  };

  return (
    <div
      style={rowStyle}
      onClick={onClick}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
      }}
      data-testid={`pr-row-${pr.prTrackingId}`}
    >
      <span style={badgeStyle}>
        <span>{config.icon}</span>
        {config.label}
      </span>
      <span style={urlStyle}>{pr.prUrl || pr.prTrackingId}</span>
      <span style={metaStyle}>
        Fixes: {pr.fixAttemptCount}/{pr.maxFixAttempts}
      </span>
      <button
        style={toggleStyle}
        onClick={(e) => {
          e.stopPropagation();
          onToggleAutoFix();
        }}
        data-testid={`pr-autofix-toggle-${pr.prTrackingId}`}
      >
        {pr.autoFixEnabled ? "Auto-fix ON" : "Auto-fix OFF"}
      </button>
    </div>
  );
}

function TrackPrDialog({
  prUrl,
  unitTaskId,
  isSubmitting,
  onPrUrlChange,
  onUnitTaskIdChange,
  onSubmit,
  onClose,
}: {
  prUrl: string;
  unitTaskId: string;
  isSubmitting: boolean;
  onPrUrlChange: (v: string) => void;
  onUnitTaskIdChange: (v: string) => void;
  onSubmit: () => void;
  onClose: () => void;
}) {
  const overlayStyle: CSSProperties = {
    position: "fixed",
    inset: 0,
    backgroundColor: "rgba(0, 0, 0, 0.5)",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    zIndex: 100,
  };

  const dialogStyle: CSSProperties = {
    backgroundColor: "var(--color-bg-primary)",
    borderRadius: "var(--radius-lg)",
    padding: "var(--space-6)",
    width: "420px",
    maxWidth: "90vw",
    boxShadow: "0 8px 32px rgba(0, 0, 0, 0.2)",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    fontSize: "var(--font-size-sm)",
    marginBottom: "var(--space-3)",
    boxSizing: "border-box",
  };

  const labelStyle: CSSProperties = {
    display: "block",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    color: "var(--color-text-secondary)",
    marginBottom: "var(--space-1)",
  };

  const buttonRowStyle: CSSProperties = {
    display: "flex",
    justifyContent: "flex-end",
    gap: "var(--space-2)",
    marginTop: "var(--space-4)",
  };

  const primaryButtonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-4)",
    borderRadius: "var(--radius-md)",
    backgroundColor: "var(--color-accent)",
    color: "var(--color-text-inverse)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: isSubmitting ? "not-allowed" : "pointer",
    opacity: isSubmitting || !prUrl.trim() ? 0.6 : 1,
  };

  const cancelButtonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-4)",
    borderRadius: "var(--radius-md)",
    backgroundColor: "transparent",
    color: "var(--color-text-secondary)",
    fontSize: "var(--font-size-sm)",
    cursor: "pointer",
  };

  return (
    <div style={overlayStyle} onClick={onClose} data-testid="track-pr-dialog">
      <div style={dialogStyle} onClick={(e) => e.stopPropagation()}>
        <h2 style={{ fontSize: "var(--font-size-lg)", fontWeight: 600, marginBottom: "var(--space-4)" }}>
          Track Pull Request
        </h2>
        <label style={labelStyle}>PR URL</label>
        <input
          style={inputStyle}
          type="text"
          placeholder="https://github.com/owner/repo/pull/123"
          value={prUrl}
          onChange={(e) => onPrUrlChange(e.target.value)}
          autoFocus
          data-testid="track-pr-url-input"
        />
        <label style={labelStyle}>Unit Task ID (optional)</label>
        <input
          style={inputStyle}
          type="text"
          placeholder="Link to an existing task"
          value={unitTaskId}
          onChange={(e) => onUnitTaskIdChange(e.target.value)}
          data-testid="track-pr-task-input"
        />
        <div style={buttonRowStyle}>
          <button style={cancelButtonStyle} onClick={onClose}>
            Cancel
          </button>
          <button
            style={primaryButtonStyle}
            onClick={onSubmit}
            disabled={isSubmitting || !prUrl.trim()}
            data-testid="track-pr-submit"
          >
            {isSubmitting ? "Tracking..." : "Track"}
          </button>
        </div>
      </div>
    </div>
  );
}
