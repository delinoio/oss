package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// WorkspaceHandler implements the WorkspaceService Connect RPC handler.
type WorkspaceHandler struct {
	dexdexv1connect.UnimplementedWorkspaceServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewWorkspaceHandler creates a new WorkspaceHandler.
func NewWorkspaceHandler(s store.Store, logger *slog.Logger) *WorkspaceHandler {
	return &WorkspaceHandler{
		store:  s,
		logger: logger,
	}
}

// GetWorkspace returns a workspace by ID.
func (h *WorkspaceHandler) GetWorkspace(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetWorkspaceRequest],
) (*connect.Response[dexdexv1.GetWorkspaceResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("GetWorkspace called", "workspace_id", workspaceID)

	ws, err := h.store.GetWorkspace(workspaceID)
	if err != nil {
		h.logger.Warn("workspace not found", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.GetWorkspaceResponse{
		Workspace: ws,
	}), nil
}
