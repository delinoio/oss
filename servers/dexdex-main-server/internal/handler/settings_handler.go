package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// SettingsHandler implements the SettingsService Connect RPC handler.
type SettingsHandler struct {
	dexdexv1connect.UnimplementedSettingsServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewSettingsHandler creates a new SettingsHandler.
func NewSettingsHandler(s store.Store, logger *slog.Logger) *SettingsHandler {
	return &SettingsHandler{
		store:  s,
		logger: logger,
	}
}

// GetWorkspaceSettings returns workspace settings.
func (h *SettingsHandler) GetWorkspaceSettings(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetWorkspaceSettingsRequest],
) (*connect.Response[dexdexv1.GetWorkspaceSettingsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("GetWorkspaceSettings called", "workspace_id", workspaceID)

	settings := h.store.GetWorkspaceSettings(workspaceID)
	return connect.NewResponse(&dexdexv1.GetWorkspaceSettingsResponse{
		Settings: settings,
	}), nil
}

// UpdateWorkspaceSettings updates workspace settings.
func (h *SettingsHandler) UpdateWorkspaceSettings(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateWorkspaceSettingsRequest],
) (*connect.Response[dexdexv1.UpdateWorkspaceSettingsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("UpdateWorkspaceSettings called", "workspace_id", workspaceID, "default_agent_cli_type", req.Msg.DefaultAgentCliType.String())

	settings := &dexdexv1.WorkspaceSettings{
		WorkspaceId:         workspaceID,
		DefaultAgentCliType: req.Msg.DefaultAgentCliType,
	}
	h.store.UpdateWorkspaceSettings(workspaceID, settings)

	return connect.NewResponse(&dexdexv1.UpdateWorkspaceSettingsResponse{
		Settings: settings,
	}), nil
}
