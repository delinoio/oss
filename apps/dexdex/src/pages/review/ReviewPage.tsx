import { useQuery } from "@connectrpc/connect-query";
import { useEffect } from "react";
import { getPullRequest, listPullRequests } from "../../gen/v1/dexdex-PrManagementService_connectquery";
import { listReviewAssistItems } from "../../gen/v1/dexdex-ReviewAssistService_connectquery";
import { listReviewComments } from "../../gen/v1/dexdex-ReviewCommentService_connectquery";
import { PrStatus, type PullRequestRecord } from "../../gen/v1/dexdex_pb";
import type { SharedSelectionState } from "../../contracts/selection-state";
import {
  visualPullRequests,
  visualReviewAssistItems,
  visualReviewComments,
} from "../../lib/visual-fixtures";
import { prDotClass } from "../../components/ui/StatusDot";

const defaultListPageSize = 50;

function enumLabel<T extends Record<string, string | number>>(enumType: T, value: number): string {
  const maybeLabel = enumType[value as unknown as keyof T];
  return typeof maybeLabel === "string" ? maybeLabel : "UNSPECIFIED";
}

type ReviewPageProps = {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  visualMode: boolean;
};

export function ReviewPage({ workspaceId, selection, onSelectionChange, visualMode }: ReviewPageProps) {
  const pullRequestListQuery = useQuery(
    listPullRequests,
    { workspaceId, status: PrStatus.UNSPECIFIED, pageSize: defaultListPageSize, pageToken: "" },
    { enabled: !visualMode },
  );

  const selectedPullRequestQuery = useQuery(
    getPullRequest,
    selection.selectedPrTrackingId
      ? { workspaceId, prTrackingId: selection.selectedPrTrackingId }
      : undefined,
    { enabled: !visualMode && selection.selectedPrTrackingId !== null },
  );

  const reviewCommentQuery = useQuery(
    listReviewComments,
    selection.selectedPrTrackingId
      ? { workspaceId, prTrackingId: selection.selectedPrTrackingId }
      : undefined,
    { enabled: !visualMode && selection.selectedPrTrackingId !== null },
  );

  const reviewAssistQuery = useQuery(
    listReviewAssistItems,
    selection.selectedUnitTaskId
      ? { workspaceId, unitTaskId: selection.selectedUnitTaskId }
      : undefined,
    { enabled: !visualMode && selection.selectedUnitTaskId !== null },
  );

  const pullRequests: PullRequestRecord[] = visualMode
    ? visualPullRequests
    : (pullRequestListQuery.data?.items ?? []);
  const selectedPullRequest = visualMode
    ? visualPullRequests.find((item) => item.prTrackingId === selection.selectedPrTrackingId)
    : selectedPullRequestQuery.data?.pullRequest;
  const reviewComments = visualMode ? visualReviewComments : reviewCommentQuery.data?.comments ?? [];
  const reviewAssistItems = visualMode ? visualReviewAssistItems : reviewAssistQuery.data?.items ?? [];

  useEffect(() => {
    if (selection.selectedPrTrackingId || pullRequests.length === 0) return;
    onSelectionChange({ selectedPrTrackingId: pullRequests[0].prTrackingId });
  }, [onSelectionChange, pullRequests, selection.selectedPrTrackingId]);

  return (
    <div className="content-split">
      <section className="content-list-pane">
        <div className="section-label">Pull Requests</div>
        {pullRequests.length > 0 ? (
          <ul className="item-list">
            {pullRequests.map((pullRequest) => (
              <li key={pullRequest.prTrackingId}>
                <button
                  type="button"
                  className={`item-row ${selection.selectedPrTrackingId === pullRequest.prTrackingId ? "item-row-active" : ""}`}
                  onClick={() => onSelectionChange({ selectedPrTrackingId: pullRequest.prTrackingId })}
                >
                  <span className={`item-row-dot ${prDotClass(pullRequest.status)}`} />
                  <span className="item-row-body">
                    <span className="item-row-title">{pullRequest.prTrackingId}</span>
                    <span className="item-row-sub">{enumLabel(PrStatus, pullRequest.status)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : pullRequestListQuery.isPending ? (
          <p className="text-muted text-sm">Loading pull requests...</p>
        ) : (
          <p className="empty-state">No pull requests.</p>
        )}
      </section>

      <section className="content-detail-pane">
        <div className="panel">
          <header className="panel-header">Review Context</header>
          <div className="panel-body">
            {selectedPullRequest ? (
              <div className="kv-grid">
                <span className="kv-key">PR tracking ID</span>
                <span className="kv-value">{selectedPullRequest.prTrackingId}</span>
                <span className="kv-key">Status</span>
                <span className="kv-value">{enumLabel(PrStatus, selectedPullRequest.status)}</span>
              </div>
            ) : (
              <p className="empty-state">Select a pull request to view details.</p>
            )}
          </div>
        </div>

        <div className="panel">
          <header className="panel-header">Review Assist</header>
          <div className="panel-body">
            {reviewAssistItems.length > 0 ? (
              <ul className="item-list">
                {reviewAssistItems.map((item) => (
                  <li key={item.reviewAssistId} className="panel-list-item">
                    <p className="item-row-title">{item.reviewAssistId}</p>
                    <p className="item-row-sub">{item.body}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty-state">No review assist records.</p>
            )}
          </div>
        </div>

        <div className="panel">
          <header className="panel-header">Inline Comments</header>
          <div className="panel-body">
            {reviewComments.length > 0 ? (
              <ul className="item-list">
                {reviewComments.map((comment) => (
                  <li key={comment.reviewCommentId} className="panel-list-item">
                    <p className="item-row-title">{comment.reviewCommentId}</p>
                    <p className="item-row-sub">{comment.body}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty-state">No review comments.</p>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
