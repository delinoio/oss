/**
 * PR management page showing tracked pull requests with status badges.
 */

import type { CSSProperties } from "react";
import { PrStatus as ProtoPrStatus } from "../../gen/v1/dexdex_pb";
import type { PullRequestRecord } from "../../gen/v1/dexdex_pb";
import { PrListSkeleton } from "../../components/skeleton-loader";
import { PrStatus, PR_STATUS_CONFIG } from "../../lib/status";

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
}

export function PrManagementPage({ pullRequests, isLoading }: PrManagementPageProps) {
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

  const listStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
    padding: "var(--space-2) var(--space-4)",
  };

  return (
    <div style={containerStyle} data-testid="pr-management-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Pull Requests</h1>
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
          >
            No pull requests tracked yet.
          </div>
        ) : null}
        {!isLoading && pullRequests.map((pr) => {
          const viewStatus = PR_STATUS_MAP[pr.status] ?? PrStatus.UNSPECIFIED;
          const config = PR_STATUS_CONFIG[viewStatus];

          const rowStyle: CSSProperties = {
            display: "flex",
            alignItems: "center",
            gap: "var(--space-3)",
            padding: "var(--space-3) var(--space-4)",
            borderRadius: "var(--radius-md)",
            borderBottom: "1px solid var(--color-border)",
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
          };

          return (
            <div key={pr.prTrackingId} style={rowStyle} data-testid={`pr-row-${pr.prTrackingId}`}>
              <span style={badgeStyle}>
                <span>{config.icon}</span>
                {config.label}
              </span>
              <span style={{ fontSize: "var(--font-size-sm)", fontWeight: 500 }}>
                {pr.prTrackingId}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
