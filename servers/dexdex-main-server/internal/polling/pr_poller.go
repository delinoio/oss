package polling

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PRPoller periodically polls GitHub for PR status updates.
type PRPoller struct {
	store        store.Store
	fanOut       stream.EventBroadcaster
	githubClient *GitHubClient
	pollInterval time.Duration
	logger       *slog.Logger
}

// NewPRPoller creates a new PR poller.
func NewPRPoller(s store.Store, fo stream.EventBroadcaster, gh *GitHubClient, pollInterval time.Duration, logger *slog.Logger) *PRPoller {
	return &PRPoller{
		store:        s,
		fanOut:       fo,
		githubClient: gh,
		pollInterval: pollInterval,
		logger:       logger,
	}
}

// Start begins the polling loop in the background. It blocks until ctx is cancelled.
func (p *PRPoller) Start(ctx context.Context) {
	p.logger.Info("PR poller started", "interval", p.pollInterval)

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("PR poller stopped")
			return
		case <-ticker.C:
			p.pollAll(ctx)
		}
	}
}

func (p *PRPoller) pollAll(ctx context.Context) {
	workspaces := p.store.ListWorkspaces()

	for _, ws := range workspaces {
		prs := p.store.ListPullRequests(ws.WorkspaceId)
		for _, pr := range prs {
			if ctx.Err() != nil {
				return
			}
			p.pollPR(ctx, ws.WorkspaceId, pr)
		}
	}
}

func (p *PRPoller) pollPR(ctx context.Context, workspaceID string, pr *dexdexv1.PullRequestRecord) {
	owner, repo, number, err := parsePRTrackingID(pr.PrTrackingId)
	if err != nil {
		p.logger.Debug("skipping non-parseable PR tracking ID",
			"pr_tracking_id", pr.PrTrackingId,
			"error", err,
		)
		return
	}

	ghPR, err := p.githubClient.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		p.logger.Warn("failed to fetch PR from GitHub",
			"pr_tracking_id", pr.PrTrackingId,
			"error", err,
		)
		return
	}

	newStatus := p.determinePRStatus(ctx, owner, repo, ghPR)

	if newStatus != pr.Status {
		p.logger.Info("PR status changed",
			"pr_tracking_id", pr.PrTrackingId,
			"old_status", pr.Status.String(),
			"new_status", newStatus.String(),
		)

		updatedPR, updateErr := p.store.UpdatePullRequest(workspaceID, pr.PrTrackingId, newStatus)
		if updateErr != nil {
			p.logger.Error("failed to persist PR status update",
				"workspace_id", workspaceID,
				"pr_tracking_id", pr.PrTrackingId,
				"error", updateErr,
			)
			return
		}

		// Publish PR_UPDATED event
		p.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_PR_UPDATED, &stream.PrUpdatedPayload{
			PrUpdated: &dexdexv1.PrUpdatedEvent{
				PullRequest: updatedPR,
			},
		})

		// Create notifications for actionable status changes
		p.createNotificationForStatusChange(workspaceID, pr.PrTrackingId, newStatus)

		// Create review assist items when changes are requested.
		if newStatus == dexdexv1.PrStatus_PR_STATUS_CHANGES_REQUESTED {
			p.createReviewAssistItems(ctx, workspaceID, pr.PrTrackingId)
		}
	}
}

func (p *PRPoller) determinePRStatus(ctx context.Context, owner, repo string, ghPR *GitHubPR) dexdexv1.PrStatus {
	// Merged
	if ghPR.Merged {
		return dexdexv1.PrStatus_PR_STATUS_MERGED
	}

	// Closed (not merged)
	if ghPR.State == "closed" {
		return dexdexv1.PrStatus_PR_STATUS_CLOSED
	}

	// Check reviews
	reviews, err := p.githubClient.ListPullRequestReviews(ctx, owner, repo, ghPR.Number)
	if err == nil {
		hasApproval := false
		hasChangesRequested := false
		for _, review := range reviews {
			switch review.State {
			case "APPROVED":
				hasApproval = true
			case "CHANGES_REQUESTED":
				hasChangesRequested = true
			}
		}
		if hasChangesRequested {
			return dexdexv1.PrStatus_PR_STATUS_CHANGES_REQUESTED
		}
		if hasApproval {
			return dexdexv1.PrStatus_PR_STATUS_APPROVED
		}
	}

	// Check CI status
	if ghPR.Head.SHA != "" {
		checkRuns, err := p.githubClient.GetCheckRuns(ctx, owner, repo, ghPR.Head.SHA)
		if err == nil {
			for _, check := range checkRuns {
				if check.Status == "completed" && check.Conclusion == "failure" {
					return dexdexv1.PrStatus_PR_STATUS_CI_FAILED
				}
			}
		}
	}

	return dexdexv1.PrStatus_PR_STATUS_OPEN
}

func (p *PRPoller) createNotificationForStatusChange(workspaceID, prTrackingID string, newStatus dexdexv1.PrStatus) {
	var notifType dexdexv1.NotificationType
	var title, body string

	switch newStatus {
	case dexdexv1.PrStatus_PR_STATUS_CI_FAILED:
		notifType = dexdexv1.NotificationType_NOTIFICATION_TYPE_PR_CI_FAILURE
		title = "CI failed"
		body = fmt.Sprintf("CI checks failed for PR %s", prTrackingID)
	case dexdexv1.PrStatus_PR_STATUS_CHANGES_REQUESTED:
		notifType = dexdexv1.NotificationType_NOTIFICATION_TYPE_PR_REVIEW_ACTIVITY
		title = "Changes requested"
		body = fmt.Sprintf("Changes requested on PR %s", prTrackingID)
	case dexdexv1.PrStatus_PR_STATUS_APPROVED:
		notifType = dexdexv1.NotificationType_NOTIFICATION_TYPE_PR_REVIEW_ACTIVITY
		title = "PR approved"
		body = fmt.Sprintf("PR %s has been approved", prTrackingID)
	default:
		return // No notification for other status changes
	}

	notif := &dexdexv1.NotificationRecord{
		NotificationId: fmt.Sprintf("notif-%d", time.Now().UnixNano()),
		Type:           notifType,
		Title:          title,
		Body:           body,
		ReferenceId:    prTrackingID,
		CreatedAt:      timestamppb.Now(),
	}
	p.store.AddNotification(workspaceID, notif)

	p.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_NOTIFICATION_CREATED, &stream.NotificationCreatedPayload{
		NotificationCreated: &dexdexv1.NotificationCreatedEvent{
			Notification: notif,
		},
	})
}

// createReviewAssistItems fetches review comments from GitHub and creates
// ReviewAssistItem entries for the PR.
func (p *PRPoller) createReviewAssistItems(ctx context.Context, workspaceID, prTrackingID string) {
	owner, repo, number, err := parsePRTrackingID(prTrackingID)
	if err != nil {
		return
	}

	comments, err := p.githubClient.ListPullRequestComments(ctx, owner, repo, number)
	if err != nil {
		p.logger.Warn("failed to fetch PR comments for review assist",
			"pr_tracking_id", prTrackingID,
			"error", err,
		)
		return
	}

	if len(comments) == 0 {
		return
	}

	// Find the unit task ID associated with this PR by searching through all unit tasks.
	unitTaskID := p.findUnitTaskForPR(workspaceID, prTrackingID)

	for i, comment := range comments {
		itemID := fmt.Sprintf("ra-%s-%d-%d", prTrackingID, time.Now().UnixNano(), i)
		bodyWithContext := comment.Body
		if comment.Path != "" {
			bodyWithContext = fmt.Sprintf("[%s:%d] %s", comment.Path, comment.Line, comment.Body)
		}
		item := &dexdexv1.ReviewAssistItem{
			ReviewAssistId: itemID,
			Body:           bodyWithContext,
		}
		p.store.AddReviewAssistItem(workspaceID, unitTaskID, item)

		p.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_REVIEW_ASSIST_UPDATED, &stream.ReviewAssistUpdatedPayload{
			ReviewAssistUpdated: &dexdexv1.ReviewAssistUpdatedEvent{
				Item: item,
			},
		})

		p.logger.Info("created review assist item from PR comment",
			"pr_tracking_id", prTrackingID,
			"review_assist_id", itemID,
			"file_path", comment.Path,
		)
	}
}

// findUnitTaskForPR searches for the unit task associated with a PR tracking ID.
func (p *PRPoller) findUnitTaskForPR(workspaceID, prTrackingID string) string {
	// Search through unit tasks' subtask commit chains to find the associated task.
	tasks := p.store.ListUnitTasks(workspaceID, nil)
	for _, task := range tasks {
		subtasks := p.store.ListSubTasks(workspaceID, task.UnitTaskId)
		for _, st := range subtasks {
			if len(st.CommitChain) > 0 {
				return task.UnitTaskId
			}
		}
	}
	return ""
}

// parsePRTrackingID extracts owner, repo, and number from a tracking ID like "owner/repo#123".
func parsePRTrackingID(trackingID string) (string, string, int, error) {
	parts := strings.SplitN(trackingID, "#", 2)
	if len(parts) != 2 {
		return "", "", 0, fmt.Errorf("invalid PR tracking ID format: %s", trackingID)
	}

	number, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number in tracking ID: %s", trackingID)
	}

	repoParts := strings.SplitN(parts[0], "/", 2)
	if len(repoParts) != 2 {
		return "", "", 0, fmt.Errorf("invalid repo in tracking ID: %s", trackingID)
	}

	return repoParts[0], repoParts[1], number, nil
}
