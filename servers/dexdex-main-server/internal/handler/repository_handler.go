package handler

import (
	"context"
	"fmt"
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

// CreateRepositoryGroup creates a new repository group.
func (h *RepositoryHandler) CreateRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateRepositoryGroupRequest],
) (*connect.Response[dexdexv1.CreateRepositoryGroupResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	groupID := req.Msg.RepositoryGroupId
	h.logger.Info("CreateRepositoryGroup called", "workspace_id", workspaceID, "group_id", groupID)

	if groupID == "" {
		err := fmt.Errorf("repository_group_id is required")
		h.logger.Warn("CreateRepositoryGroup validation failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	group := h.store.CreateRepositoryGroup(workspaceID, &dexdexv1.RepositoryGroup{
		RepositoryGroupId: groupID,
		Repositories:      req.Msg.Repositories,
	})

	return connect.NewResponse(&dexdexv1.CreateRepositoryGroupResponse{
		RepositoryGroup: group,
	}), nil
}

// UpdateRepositoryGroup updates a repository group's repositories.
func (h *RepositoryHandler) UpdateRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateRepositoryGroupRequest],
) (*connect.Response[dexdexv1.UpdateRepositoryGroupResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	groupID := req.Msg.RepositoryGroupId
	h.logger.Info("UpdateRepositoryGroup called", "workspace_id", workspaceID, "group_id", groupID)

	group, err := h.store.UpdateRepositoryGroup(workspaceID, groupID, req.Msg.Repositories)
	if err != nil {
		h.logger.Warn("repository group not found for update", "workspace_id", workspaceID, "group_id", groupID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.UpdateRepositoryGroupResponse{
		RepositoryGroup: group,
	}), nil
}

// DeleteRepositoryGroup deletes a repository group.
func (h *RepositoryHandler) DeleteRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.DeleteRepositoryGroupRequest],
) (*connect.Response[dexdexv1.DeleteRepositoryGroupResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	groupID := req.Msg.RepositoryGroupId
	h.logger.Info("DeleteRepositoryGroup called", "workspace_id", workspaceID, "group_id", groupID)

	if err := h.store.DeleteRepositoryGroup(workspaceID, groupID); err != nil {
		h.logger.Warn("repository group not found for delete", "workspace_id", workspaceID, "group_id", groupID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.DeleteRepositoryGroupResponse{}), nil
}

// ListRepositories returns all repositories for a workspace.
func (h *RepositoryHandler) ListRepositories(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListRepositoriesRequest],
) (*connect.Response[dexdexv1.ListRepositoriesResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("ListRepositories called", "workspace_id", workspaceID)

	repos := h.store.ListRepositories(workspaceID)
	return connect.NewResponse(&dexdexv1.ListRepositoriesResponse{
		Repositories: repos,
	}), nil
}

// CreateRepository creates a new repository.
func (h *RepositoryHandler) CreateRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateRepositoryRequest],
) (*connect.Response[dexdexv1.CreateRepositoryResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("CreateRepository called", "workspace_id", workspaceID)

	repo := h.store.CreateRepository(workspaceID, &dexdexv1.Repository{
		RepositoryUrl:    req.Msg.RepositoryUrl,
		DefaultBranchRef: req.Msg.DefaultBranchRef,
		DisplayName:      req.Msg.DisplayName,
	})

	return connect.NewResponse(&dexdexv1.CreateRepositoryResponse{
		Repository: repo,
	}), nil
}

// UpdateRepository updates a repository.
func (h *RepositoryHandler) UpdateRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateRepositoryRequest],
) (*connect.Response[dexdexv1.UpdateRepositoryResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	repoID := req.Msg.RepositoryId
	h.logger.Info("UpdateRepository called", "workspace_id", workspaceID, "repository_id", repoID)

	repo, err := h.store.UpdateRepository(workspaceID, &dexdexv1.Repository{
		RepositoryId:     repoID,
		RepositoryUrl:    req.Msg.RepositoryUrl,
		DefaultBranchRef: req.Msg.DefaultBranchRef,
		DisplayName:      req.Msg.DisplayName,
	})
	if err != nil {
		h.logger.Warn("repository not found for update", "workspace_id", workspaceID, "repository_id", repoID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.UpdateRepositoryResponse{
		Repository: repo,
	}), nil
}

// DeleteRepository deletes a repository.
func (h *RepositoryHandler) DeleteRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.DeleteRepositoryRequest],
) (*connect.Response[dexdexv1.DeleteRepositoryResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	repoID := req.Msg.RepositoryId
	h.logger.Info("DeleteRepository called", "workspace_id", workspaceID, "repository_id", repoID)

	if err := h.store.DeleteRepository(workspaceID, repoID); err != nil {
		h.logger.Warn("repository not found for delete", "workspace_id", workspaceID, "repository_id", repoID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.DeleteRepositoryResponse{}), nil
}
