package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// RepositoryHandler implements the RepositoryService Connect RPC handler.
type RepositoryHandler struct {
	dexdexv1connect.UnimplementedRepositoryServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewRepositoryHandler creates a new RepositoryHandler.
func NewRepositoryHandler(s store.Store, logger *slog.Logger) *RepositoryHandler {
	return &RepositoryHandler{
		store:  s,
		logger: logger,
	}
}

// GetRepositoryGroup returns a repository group by ID.
func (h *RepositoryHandler) GetRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetRepositoryGroupRequest],
) (*connect.Response[dexdexv1.GetRepositoryGroupResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	groupID := req.Msg.RepositoryGroupId
	h.logger.Info("GetRepositoryGroup called", "workspace_id", workspaceID, "group_id", groupID)

	group, err := h.store.GetRepositoryGroup(workspaceID, groupID)
	if err != nil {
		h.logger.Warn("repository group not found", "workspace_id", workspaceID, "group_id", groupID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.GetRepositoryGroupResponse{
		RepositoryGroup: group,
	}), nil
}

// ListRepositoryGroups returns all repository groups for a workspace.
func (h *RepositoryHandler) ListRepositoryGroups(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListRepositoryGroupsRequest],
) (*connect.Response[dexdexv1.ListRepositoryGroupsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("ListRepositoryGroups called", "workspace_id", workspaceID)

	groups := h.store.ListRepositoryGroups(workspaceID)
	return connect.NewResponse(&dexdexv1.ListRepositoryGroupsResponse{
		RepositoryGroups: groups,
	}), nil
}
