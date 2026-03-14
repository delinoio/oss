package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// ReviewAssistHandler implements the ReviewAssistService Connect RPC handler.
type ReviewAssistHandler struct {
	dexdexv1connect.UnimplementedReviewAssistServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewReviewAssistHandler creates a new ReviewAssistHandler.
func NewReviewAssistHandler(s store.Store, logger *slog.Logger) *ReviewAssistHandler {
	return &ReviewAssistHandler{
		store:  s,
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

// ReviewCommentHandler implements the ReviewCommentService Connect RPC handler.
type ReviewCommentHandler struct {
	dexdexv1connect.UnimplementedReviewCommentServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewReviewCommentHandler creates a new ReviewCommentHandler.
func NewReviewCommentHandler(s store.Store, logger *slog.Logger) *ReviewCommentHandler {
	return &ReviewCommentHandler{
		store:  s,
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
