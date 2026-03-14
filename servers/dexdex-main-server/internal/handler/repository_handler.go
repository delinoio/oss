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

// GetRepository returns a repository by ID.
func (h *RepositoryHandler) GetRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetRepositoryRequest],
) (*connect.Response[dexdexv1.GetRepositoryResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	repositoryID := strings.TrimSpace(req.Msg.RepositoryId)
	if workspaceID == "" || repositoryID == "" {
		err := fmt.Errorf("workspace_id and repository_id are required")
		h.logger.Warn("GetRepository validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("GetRepository called", "workspace_id", workspaceID, "repository_id", repositoryID)
	repo, err := h.store.GetRepository(workspaceID, repositoryID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&dexdexv1.GetRepositoryResponse{
		Repository: repo,
	}), nil
}

// ListRepositories returns all repositories for a workspace.
func (h *RepositoryHandler) ListRepositories(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListRepositoriesRequest],
) (*connect.Response[dexdexv1.ListRepositoriesResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	if workspaceID == "" {
		err := fmt.Errorf("workspace_id is required")
		h.logger.Warn("ListRepositories validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("ListRepositories called", "workspace_id", workspaceID)
	return connect.NewResponse(&dexdexv1.ListRepositoriesResponse{
		Repositories: h.store.ListRepositories(workspaceID),
	}), nil
}

// CreateRepository creates a repository in the workspace.
func (h *RepositoryHandler) CreateRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateRepositoryRequest],
) (*connect.Response[dexdexv1.CreateRepositoryResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	repositoryURL := strings.TrimSpace(req.Msg.RepositoryUrl)
	if workspaceID == "" || repositoryURL == "" {
		err := fmt.Errorf("workspace_id and repository_url are required")
		h.logger.Warn("CreateRepository validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("CreateRepository called", "workspace_id", workspaceID, "repository_url", repositoryURL)
	repo, err := h.store.CreateRepository(workspaceID, repositoryURL)
	if err != nil {
		h.logger.Error("CreateRepository failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&dexdexv1.CreateRepositoryResponse{
		Repository: repo,
	}), nil
}

// UpdateRepository updates repository URL.
func (h *RepositoryHandler) UpdateRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateRepositoryRequest],
) (*connect.Response[dexdexv1.UpdateRepositoryResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	repositoryID := strings.TrimSpace(req.Msg.RepositoryId)
	repositoryURL := strings.TrimSpace(req.Msg.RepositoryUrl)
	if workspaceID == "" || repositoryID == "" || repositoryURL == "" {
		err := fmt.Errorf("workspace_id, repository_id, and repository_url are required")
		h.logger.Warn("UpdateRepository validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("UpdateRepository called", "workspace_id", workspaceID, "repository_id", repositoryID)
	repo, err := h.store.UpdateRepository(workspaceID, repositoryID, repositoryURL)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&dexdexv1.UpdateRepositoryResponse{
		Repository: repo,
	}), nil
}

// DeleteRepository deletes a repository from workspace.
func (h *RepositoryHandler) DeleteRepository(
	ctx context.Context,
	req *connect.Request[dexdexv1.DeleteRepositoryRequest],
) (*connect.Response[dexdexv1.DeleteRepositoryResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	repositoryID := strings.TrimSpace(req.Msg.RepositoryId)
	if workspaceID == "" || repositoryID == "" {
		err := fmt.Errorf("workspace_id and repository_id are required")
		h.logger.Warn("DeleteRepository validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("DeleteRepository called", "workspace_id", workspaceID, "repository_id", repositoryID)
	if err := h.store.DeleteRepository(workspaceID, repositoryID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "in use") {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&dexdexv1.DeleteRepositoryResponse{}), nil
}

// GetRepositoryGroup returns a repository group by ID.
func (h *RepositoryHandler) GetRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetRepositoryGroupRequest],
) (*connect.Response[dexdexv1.GetRepositoryGroupResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	groupID := strings.TrimSpace(req.Msg.RepositoryGroupId)
	if workspaceID == "" || groupID == "" {
		err := fmt.Errorf("workspace_id and repository_group_id are required")
		h.logger.Warn("GetRepositoryGroup validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

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
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	if workspaceID == "" {
		err := fmt.Errorf("workspace_id is required")
		h.logger.Warn("ListRepositoryGroups validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("ListRepositoryGroups called", "workspace_id", workspaceID)

	groups := h.store.ListRepositoryGroups(workspaceID)
	return connect.NewResponse(&dexdexv1.ListRepositoryGroupsResponse{
		RepositoryGroups: groups,
	}), nil
}

// CreateRepositoryGroup creates a repository group with ordered members.
func (h *RepositoryHandler) CreateRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateRepositoryGroupRequest],
) (*connect.Response[dexdexv1.CreateRepositoryGroupResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	groupID := strings.TrimSpace(req.Msg.RepositoryGroupId)
	if workspaceID == "" || groupID == "" {
		err := fmt.Errorf("workspace_id and repository_group_id are required")
		h.logger.Warn("CreateRepositoryGroup validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	members, err := h.normalizeGroupMembers(workspaceID, req.Msg.Members)
	if err != nil {
		return nil, err
	}

	h.logger.Info("CreateRepositoryGroup called", "workspace_id", workspaceID, "repository_group_id", groupID, "member_count", len(members))
	group, createErr := h.store.CreateRepositoryGroup(workspaceID, groupID, members)
	if createErr != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, createErr)
	}

	return connect.NewResponse(&dexdexv1.CreateRepositoryGroupResponse{
		RepositoryGroup: group,
	}), nil
}

// UpdateRepositoryGroup updates repository group members.
func (h *RepositoryHandler) UpdateRepositoryGroup(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateRepositoryGroupRequest],
) (*connect.Response[dexdexv1.UpdateRepositoryGroupResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	groupID := strings.TrimSpace(req.Msg.RepositoryGroupId)
	if workspaceID == "" || groupID == "" {
		err := fmt.Errorf("workspace_id and repository_group_id are required")
		h.logger.Warn("UpdateRepositoryGroup validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	members, err := h.normalizeGroupMembers(workspaceID, req.Msg.Members)
	if err != nil {
		return nil, err
	}

	h.logger.Info("UpdateRepositoryGroup called", "workspace_id", workspaceID, "repository_group_id", groupID, "member_count", len(members))
	group, updateErr := h.store.UpdateRepositoryGroup(workspaceID, groupID, members)
	if updateErr != nil {
		return nil, connect.NewError(connect.CodeNotFound, updateErr)
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
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	groupID := strings.TrimSpace(req.Msg.RepositoryGroupId)
	if workspaceID == "" || groupID == "" {
		err := fmt.Errorf("workspace_id and repository_group_id are required")
		h.logger.Warn("DeleteRepositoryGroup validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("DeleteRepositoryGroup called", "workspace_id", workspaceID, "repository_group_id", groupID)
	if err := h.store.DeleteRepositoryGroup(workspaceID, groupID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&dexdexv1.DeleteRepositoryGroupResponse{}), nil
}

func (h *RepositoryHandler) normalizeGroupMembers(
	workspaceID string,
	input []*dexdexv1.RepositoryGroupMemberInput,
) ([]*dexdexv1.RepositoryGroupMember, error) {
	if len(input) == 0 {
		err := fmt.Errorf("repository group must include at least one member")
		h.logger.Warn("repository group validation failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	seen := make(map[string]struct{}, len(input))
	members := make([]*dexdexv1.RepositoryGroupMember, 0, len(input))
	for i, memberInput := range input {
		repositoryID := strings.TrimSpace(memberInput.RepositoryId)
		if repositoryID == "" {
			err := fmt.Errorf("repository_id is required for member %d", i)
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if _, exists := seen[repositoryID]; exists {
			err := fmt.Errorf("duplicate repository_id in members: %s", repositoryID)
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		seen[repositoryID] = struct{}{}

		repo, err := h.store.GetRepository(workspaceID, repositoryID)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("repository not found for member %d: %s", i, repositoryID))
		}

		members = append(members, &dexdexv1.RepositoryGroupMember{
			RepositoryId: repositoryID,
			BranchRef:    strings.TrimSpace(memberInput.BranchRef),
			DisplayOrder: int32(i),
			Repository:   repo,
		})
	}

	return members, nil
}
