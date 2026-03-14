package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// PrHandler implements the PrManagementService Connect RPC handler.
type PrHandler struct {
	dexdexv1connect.UnimplementedPrManagementServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewPrHandler creates a new PrHandler.
func NewPrHandler(s store.Store, logger *slog.Logger) *PrHandler {
	return &PrHandler{
		store:  s,
		logger: logger,
	}
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
