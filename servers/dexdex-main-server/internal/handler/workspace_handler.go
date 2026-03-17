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

// ListWorkspaces returns all workspaces.
func (h *WorkspaceHandler) ListWorkspaces(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListWorkspacesRequest],
) (*connect.Response[dexdexv1.ListWorkspacesResponse], error) {
	h.logger.Info("ListWorkspaces called")

	workspaces := h.store.ListWorkspaces()
	return connect.NewResponse(&dexdexv1.ListWorkspacesResponse{
		Workspaces: workspaces,
	}), nil
}

// GetWorkspaceWorkStatus returns the aggregated work status for a workspace.
func (h *WorkspaceHandler) GetWorkspaceWorkStatus(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetWorkspaceWorkStatusRequest],
) (*connect.Response[dexdexv1.GetWorkspaceWorkStatusResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("GetWorkspaceWorkStatus called", "workspace_id", workspaceID)

	status := h.store.GetWorkspaceWorkStatus(workspaceID)

	return connect.NewResponse(&dexdexv1.GetWorkspaceWorkStatusResponse{
		Status: status,
	}), nil
}

// GetWorkspaceSettings returns workspace-level settings.
func (h *WorkspaceHandler) GetWorkspaceSettings(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetWorkspaceSettingsRequest],
) (*connect.Response[dexdexv1.GetWorkspaceSettingsResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	if workspaceID == "" {
		err := fmt.Errorf("workspace_id is required")
		h.logger.Warn("GetWorkspaceSettings validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("GetWorkspaceSettings called", "workspace_id", workspaceID)
	settings, err := h.store.GetWorkspaceSettings(workspaceID)
	if err != nil {
		// Default to CLAUDE_CODE for newly initialized workspaces without settings.
		settings, err = h.store.UpsertWorkspaceSettings(workspaceID, dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE)
		if err != nil {
			h.logger.Error("failed to initialize workspace settings", "workspace_id", workspaceID, "error", err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	return connect.NewResponse(&dexdexv1.GetWorkspaceSettingsResponse{
		Settings: settings,
	}), nil
}

// UpdateWorkspaceSettings updates workspace-level settings.
func (h *WorkspaceHandler) UpdateWorkspaceSettings(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateWorkspaceSettingsRequest],
) (*connect.Response[dexdexv1.UpdateWorkspaceSettingsResponse], error) {
	workspaceID := strings.TrimSpace(req.Msg.WorkspaceId)
	if workspaceID == "" {
		err := fmt.Errorf("workspace_id is required")
		h.logger.Warn("UpdateWorkspaceSettings validation failed", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	defaultAgent := req.Msg.DefaultAgentCliType
	if defaultAgent == dexdexv1.AgentCliType_AGENT_CLI_TYPE_UNSPECIFIED {
		err := fmt.Errorf("default_agent_cli_type is required")
		h.logger.Warn("UpdateWorkspaceSettings validation failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	h.logger.Info("UpdateWorkspaceSettings called",
		"workspace_id", workspaceID,
		"default_agent_cli_type", defaultAgent.String(),
	)

	settings, err := h.store.UpsertWorkspaceSettings(workspaceID, defaultAgent)
	if err != nil {
		h.logger.Error("UpdateWorkspaceSettings failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&dexdexv1.UpdateWorkspaceSettingsResponse{
		Settings: settings,
	}), nil
}

// CreateWorkspace creates a new workspace.
func (h *WorkspaceHandler) CreateWorkspace(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateWorkspaceRequest],
) (*connect.Response[dexdexv1.CreateWorkspaceResponse], error) {
	name := strings.TrimSpace(req.Msg.Name)
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	wsType := req.Msg.Type
	if wsType == dexdexv1.WorkspaceType_WORKSPACE_TYPE_UNSPECIFIED {
		wsType = dexdexv1.WorkspaceType_WORKSPACE_TYPE_LOCAL_ENDPOINT
	}

	h.logger.Info("CreateWorkspace called", "name", name, "type", wsType.String())

	ws := h.store.CreateWorkspace(name, wsType)
	if ws == nil {
		err := fmt.Errorf("failed to create workspace")
		h.logger.Error("CreateWorkspace failed", "name", name, "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	h.logger.Info("CreateWorkspace completed", "workspace_id", ws.WorkspaceId)

	return connect.NewResponse(&dexdexv1.CreateWorkspaceResponse{
		Workspace: ws,
	}), nil
}

// UpdateWorkspace updates a workspace's name.
func (h *WorkspaceHandler) UpdateWorkspace(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateWorkspaceRequest],
) (*connect.Response[dexdexv1.UpdateWorkspaceResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	name := strings.TrimSpace(req.Msg.Name)
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	h.logger.Info("UpdateWorkspace called", "workspace_id", workspaceID, "name", name)

	ws, err := h.store.UpdateWorkspace(workspaceID, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.UpdateWorkspaceResponse{
		Workspace: ws,
	}), nil
}

// DeleteWorkspace removes a workspace.
func (h *WorkspaceHandler) DeleteWorkspace(
	ctx context.Context,
	req *connect.Request[dexdexv1.DeleteWorkspaceRequest],
) (*connect.Response[dexdexv1.DeleteWorkspaceResponse], error) {
	workspaceID := req.Msg.WorkspaceId

	h.logger.Info("DeleteWorkspace called", "workspace_id", workspaceID)

	if err := h.store.DeleteWorkspace(workspaceID); err != nil {
		if strings.Contains(err.Error(), "active tasks") {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.DeleteWorkspaceResponse{}), nil
}

// SetActiveWorkspace sets the active workspace.
func (h *WorkspaceHandler) SetActiveWorkspace(
	ctx context.Context,
	req *connect.Request[dexdexv1.SetActiveWorkspaceRequest],
) (*connect.Response[dexdexv1.SetActiveWorkspaceResponse], error) {
	workspaceID := req.Msg.WorkspaceId

	h.logger.Info("SetActiveWorkspace called", "workspace_id", workspaceID)

	ws, err := h.store.GetWorkspace(workspaceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.SetActiveWorkspaceResponse{
		Workspace: ws,
	}), nil
}
