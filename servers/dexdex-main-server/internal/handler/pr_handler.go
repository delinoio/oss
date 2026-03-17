package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PrHandler implements the PrManagementService Connect RPC handler.
type PrHandler struct {
	dexdexv1connect.UnimplementedPrManagementServiceHandler
	store  store.Store
	fanOut stream.EventBroadcaster
	logger *slog.Logger
}

// NewPrHandler creates a new PrHandler.
func NewPrHandler(s store.Store, fanOut stream.EventBroadcaster, logger *slog.Logger) *PrHandler {
	return &PrHandler{
		store:  s,
		fanOut: fanOut,
		logger: logger,
	}
}

// ListPullRequests returns all pull requests for a workspace.
func (h *PrHandler) ListPullRequests(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListPullRequestsRequest],
) (*connect.Response[dexdexv1.ListPullRequestsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("ListPullRequests called", "workspace_id", workspaceID)

	prs := h.store.ListPullRequests(workspaceID)

	return connect.NewResponse(&dexdexv1.ListPullRequestsResponse{
		PullRequests: prs,
	}), nil
}

// GetPullRequest returns a pull request by tracking ID.
func (h *PrHandler) GetPullRequest(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetPullRequestRequest],
) (*connect.Response[dexdexv1.GetPullRequestResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prTrackingID := req.Msg.PrTrackingId
	h.logger.Info("GetPullRequest called", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID)

	pr, err := h.store.GetPullRequest(workspaceID, prTrackingID)
	if err != nil {
		h.logger.Warn("pull request not found", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.GetPullRequestResponse{
		PullRequest: pr,
	}), nil
}

// UpdatePullRequest updates the status of a pull request.
func (h *PrHandler) UpdatePullRequest(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdatePullRequestRequest],
) (*connect.Response[dexdexv1.UpdatePullRequestResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prTrackingID := req.Msg.PrTrackingId
	status := req.Msg.Status

	h.logger.Info("UpdatePullRequest called",
		"workspace_id", workspaceID,
		"pr_tracking_id", prTrackingID,
		"status", status.String(),
	)

	pr, err := h.store.UpdatePullRequest(workspaceID, prTrackingID, status)
	if err != nil {
		h.logger.Warn("pull request not found for update",
			"workspace_id", workspaceID,
			"pr_tracking_id", prTrackingID,
			"error", err,
		)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.UpdatePullRequestResponse{
		PullRequest: pr,
	}), nil
}

// TrackPullRequest starts tracking a pull request by URL.
func (h *PrHandler) TrackPullRequest(
	ctx context.Context,
	req *connect.Request[dexdexv1.TrackPullRequestRequest],
) (*connect.Response[dexdexv1.TrackPullRequestResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prURL := strings.TrimSpace(req.Msg.PrUrl)
	unitTaskID := req.Msg.UnitTaskId

	if prURL == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pr_url is required"))
	}

	h.logger.Info("TrackPullRequest called", "workspace_id", workspaceID, "pr_url", prURL)

	prTrackingID := nextPRTrackingID()

	pr := &dexdexv1.PullRequestRecord{
		PrTrackingId:   prTrackingID,
		Status:         dexdexv1.PrStatus_PR_STATUS_OPEN,
		PrUrl:          prURL,
		WorkspaceId:    workspaceID,
		UnitTaskId:     unitTaskID,
		MaxFixAttempts: 3,
		CreatedAt:      timestamppb.Now(),
		UpdatedAt:      timestamppb.Now(),
	}

	h.store.AddPullRequest(workspaceID, pr)

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_PR_UPDATED, &stream.PrUpdatedPayload{
		PrUpdated: &dexdexv1.PrUpdatedEvent{PullRequest: pr},
	})

	h.logger.Info("TrackPullRequest completed", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID)

	return connect.NewResponse(&dexdexv1.TrackPullRequestResponse{
		PullRequest: pr,
	}), nil
}

// RunAutoFixNow triggers an immediate auto-fix for a tracked pull request.
func (h *PrHandler) RunAutoFixNow(
	ctx context.Context,
	req *connect.Request[dexdexv1.RunAutoFixNowRequest],
) (*connect.Response[dexdexv1.RunAutoFixNowResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prTrackingID := req.Msg.PrTrackingId

	h.logger.Info("RunAutoFixNow called", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID)

	pr, err := h.store.GetPullRequest(workspaceID, prTrackingID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if pr.FixAttemptCount >= pr.MaxFixAttempts {
		return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("max fix attempts reached for PR %s", prTrackingID))
	}

	// Create a remediation sub task placeholder
	subTask := &dexdexv1.SubTask{
		SubTaskId:  nextSubTaskID(),
		UnitTaskId: pr.UnitTaskId,
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_REVIEW_FIX,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}
	h.store.UpsertSubTask(workspaceID, subTask)

	pr.FixAttemptCount++
	pr.UpdatedAt = timestamppb.Now()

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	h.logger.Info("RunAutoFixNow completed", "workspace_id", workspaceID, "sub_task_id", subTask.SubTaskId)

	return connect.NewResponse(&dexdexv1.RunAutoFixNowResponse{
		SubTask: subTask,
	}), nil
}

// SetAutoFixPolicy sets the auto-fix policy for a tracked pull request.
func (h *PrHandler) SetAutoFixPolicy(
	ctx context.Context,
	req *connect.Request[dexdexv1.SetAutoFixPolicyRequest],
) (*connect.Response[dexdexv1.SetAutoFixPolicyResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prTrackingID := req.Msg.PrTrackingId
	enabled := req.Msg.AutoFixEnabled

	h.logger.Info("SetAutoFixPolicy called", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID, "enabled", enabled)

	pr, err := h.store.SetAutoFixPolicy(workspaceID, prTrackingID, enabled)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_PR_UPDATED, &stream.PrUpdatedPayload{
		PrUpdated: &dexdexv1.PrUpdatedEvent{PullRequest: pr},
	})

	return connect.NewResponse(&dexdexv1.SetAutoFixPolicyResponse{
		PullRequest: pr,
	}), nil
}
