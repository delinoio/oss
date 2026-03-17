package handler

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReviewAssistHandler implements the ReviewAssistService Connect RPC handler.
type ReviewAssistHandler struct {
	dexdexv1connect.UnimplementedReviewAssistServiceHandler
	store  store.Store
	fanOut stream.EventBroadcaster
	logger *slog.Logger
}

// NewReviewAssistHandler creates a new ReviewAssistHandler.
func NewReviewAssistHandler(s store.Store, fanOut stream.EventBroadcaster, logger *slog.Logger) *ReviewAssistHandler {
	return &ReviewAssistHandler{
		store:  s,
		fanOut: fanOut,
		logger: logger,
	}
}

// ListReviewAssistItems returns review assist items for a PR.
func (h *ReviewAssistHandler) ListReviewAssistItems(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListReviewAssistItemsRequest],
) (*connect.Response[dexdexv1.ListReviewAssistItemsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	unitTaskID := req.Msg.UnitTaskId
	h.logger.Info("ListReviewAssistItems called", "workspace_id", workspaceID, "unit_task_id", unitTaskID)

	items := h.store.ListReviewAssistItems(workspaceID, unitTaskID)

	return connect.NewResponse(&dexdexv1.ListReviewAssistItemsResponse{
		Items: items,
	}), nil
}

// ResolveReviewAssistItem resolves a review assist item with a status.
func (h *ReviewAssistHandler) ResolveReviewAssistItem(
	ctx context.Context,
	req *connect.Request[dexdexv1.ResolveReviewAssistItemRequest],
) (*connect.Response[dexdexv1.ResolveReviewAssistItemResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	reviewAssistID := req.Msg.ReviewAssistId
	resolution := req.Msg.Resolution

	h.logger.Info("ResolveReviewAssistItem called",
		"workspace_id", workspaceID,
		"review_assist_id", reviewAssistID,
		"resolution", resolution.String(),
	)

	item, err := h.store.UpdateReviewAssistItemStatus(workspaceID, reviewAssistID, resolution)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_REVIEW_ASSIST_UPDATED, &stream.ReviewAssistUpdatedPayload{
		ReviewAssistUpdated: &dexdexv1.ReviewAssistUpdatedEvent{Item: item},
	})

	return connect.NewResponse(&dexdexv1.ResolveReviewAssistItemResponse{
		Item: item,
	}), nil
}

// ReviewCommentHandler implements the ReviewCommentService Connect RPC handler.
type ReviewCommentHandler struct {
	dexdexv1connect.UnimplementedReviewCommentServiceHandler
	store  store.Store
	fanOut stream.EventBroadcaster
	logger *slog.Logger
}

// NewReviewCommentHandler creates a new ReviewCommentHandler.
func NewReviewCommentHandler(s store.Store, fanOut stream.EventBroadcaster, logger *slog.Logger) *ReviewCommentHandler {
	return &ReviewCommentHandler{
		store:  s,
		fanOut: fanOut,
		logger: logger,
	}
}

// ListReviewComments returns review comments for a PR.
func (h *ReviewCommentHandler) ListReviewComments(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListReviewCommentsRequest],
) (*connect.Response[dexdexv1.ListReviewCommentsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prTrackingID := req.Msg.PrTrackingId
	h.logger.Info("ListReviewComments called", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID)

	comments := h.store.ListReviewComments(workspaceID, prTrackingID)

	return connect.NewResponse(&dexdexv1.ListReviewCommentsResponse{
		Comments: comments,
	}), nil
}

// CreateReviewComment creates a new review comment with anchor fields.
func (h *ReviewCommentHandler) CreateReviewComment(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateReviewCommentRequest],
) (*connect.Response[dexdexv1.CreateReviewCommentResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prTrackingID := req.Msg.PrTrackingId
	h.logger.Info("CreateReviewComment called", "workspace_id", workspaceID, "pr_tracking_id", prTrackingID)

	now := timestamppb.Now()
	comment := &dexdexv1.ReviewComment{
		ReviewCommentId: nextReviewCommentID(),
		Body:            req.Msg.Body,
		FilePath:        req.Msg.FilePath,
		Side:            req.Msg.Side,
		LineNumber:      req.Msg.LineNumber,
		Status:          dexdexv1.ReviewCommentStatus_REVIEW_COMMENT_STATUS_ACTIVE,
		PrTrackingId:    prTrackingID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	h.store.CreateReviewComment(workspaceID, prTrackingID, comment)

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_INLINE_COMMENT_UPDATED, &stream.InlineCommentUpdatedPayload{
		InlineCommentUpdated: &dexdexv1.InlineCommentUpdatedEvent{Comment: comment},
	})

	h.logger.Info("CreateReviewComment completed", "workspace_id", workspaceID, "review_comment_id", comment.ReviewCommentId)

	return connect.NewResponse(&dexdexv1.CreateReviewCommentResponse{
		Comment: comment,
	}), nil
}

// UpdateReviewComment updates the body of a review comment.
func (h *ReviewCommentHandler) UpdateReviewComment(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateReviewCommentRequest],
) (*connect.Response[dexdexv1.UpdateReviewCommentResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	reviewCommentID := req.Msg.ReviewCommentId
	h.logger.Info("UpdateReviewComment called", "workspace_id", workspaceID, "review_comment_id", reviewCommentID)

	comment, err := h.store.UpdateReviewComment(workspaceID, reviewCommentID, req.Msg.Body)
	if err != nil {
		h.logger.Warn("review comment not found for update", "workspace_id", workspaceID, "review_comment_id", reviewCommentID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_INLINE_COMMENT_UPDATED, &stream.InlineCommentUpdatedPayload{
		InlineCommentUpdated: &dexdexv1.InlineCommentUpdatedEvent{Comment: comment},
	})

	return connect.NewResponse(&dexdexv1.UpdateReviewCommentResponse{
		Comment: comment,
	}), nil
}

// DeleteReviewComment removes a review comment.
func (h *ReviewCommentHandler) DeleteReviewComment(
	ctx context.Context,
	req *connect.Request[dexdexv1.DeleteReviewCommentRequest],
) (*connect.Response[dexdexv1.DeleteReviewCommentResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	reviewCommentID := req.Msg.ReviewCommentId
	h.logger.Info("DeleteReviewComment called", "workspace_id", workspaceID, "review_comment_id", reviewCommentID)

	// Get comment before deleting for the event payload.
	comment, getErr := h.store.GetReviewComment(workspaceID, reviewCommentID)
	if getErr != nil {
		h.logger.Warn("review comment not found for deletion", "workspace_id", workspaceID, "review_comment_id", reviewCommentID, "error", getErr)
		return nil, connect.NewError(connect.CodeNotFound, getErr)
	}

	if err := h.store.DeleteReviewComment(workspaceID, reviewCommentID); err != nil {
		h.logger.Warn("failed to delete review comment", "workspace_id", workspaceID, "review_comment_id", reviewCommentID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_INLINE_COMMENT_UPDATED, &stream.InlineCommentUpdatedPayload{
		InlineCommentUpdated: &dexdexv1.InlineCommentUpdatedEvent{Comment: comment},
	})

	return connect.NewResponse(&dexdexv1.DeleteReviewCommentResponse{}), nil
}

// ResolveReviewComment sets a review comment status to RESOLVED.
func (h *ReviewCommentHandler) ResolveReviewComment(
	ctx context.Context,
	req *connect.Request[dexdexv1.ResolveReviewCommentRequest],
) (*connect.Response[dexdexv1.ResolveReviewCommentResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	reviewCommentID := req.Msg.ReviewCommentId
	h.logger.Info("ResolveReviewComment called", "workspace_id", workspaceID, "review_comment_id", reviewCommentID)

	comment, err := h.store.UpdateReviewCommentStatus(workspaceID, reviewCommentID, dexdexv1.ReviewCommentStatus_REVIEW_COMMENT_STATUS_RESOLVED)
	if err != nil {
		h.logger.Warn("review comment not found for resolve", "workspace_id", workspaceID, "review_comment_id", reviewCommentID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_INLINE_COMMENT_UPDATED, &stream.InlineCommentUpdatedPayload{
		InlineCommentUpdated: &dexdexv1.InlineCommentUpdatedEvent{Comment: comment},
	})

	return connect.NewResponse(&dexdexv1.ResolveReviewCommentResponse{
		Comment: comment,
	}), nil
}

// ReopenReviewComment sets a review comment status to ACTIVE.
func (h *ReviewCommentHandler) ReopenReviewComment(
	ctx context.Context,
	req *connect.Request[dexdexv1.ReopenReviewCommentRequest],
) (*connect.Response[dexdexv1.ReopenReviewCommentResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	reviewCommentID := req.Msg.ReviewCommentId
	h.logger.Info("ReopenReviewComment called", "workspace_id", workspaceID, "review_comment_id", reviewCommentID)

	comment, err := h.store.UpdateReviewCommentStatus(workspaceID, reviewCommentID, dexdexv1.ReviewCommentStatus_REVIEW_COMMENT_STATUS_ACTIVE)
	if err != nil {
		h.logger.Warn("review comment not found for reopen", "workspace_id", workspaceID, "review_comment_id", reviewCommentID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("review comment not found: %w", err))
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_INLINE_COMMENT_UPDATED, &stream.InlineCommentUpdatedPayload{
		InlineCommentUpdated: &dexdexv1.InlineCommentUpdatedEvent{Comment: comment},
	})

	return connect.NewResponse(&dexdexv1.ReopenReviewCommentResponse{
		Comment: comment,
	}), nil
}
