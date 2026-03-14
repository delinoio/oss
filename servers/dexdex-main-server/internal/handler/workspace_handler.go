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
