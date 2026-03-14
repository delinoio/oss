package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// BadgeThemeHandler implements the BadgeThemeService Connect RPC handler.
type BadgeThemeHandler struct {
	dexdexv1connect.UnimplementedBadgeThemeServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewBadgeThemeHandler creates a new BadgeThemeHandler.
func NewBadgeThemeHandler(s store.Store, logger *slog.Logger) *BadgeThemeHandler {
	return &BadgeThemeHandler{
		store:  s,
		logger: logger,
	}
}

// GetBadgeTheme returns the badge theme for a workspace, or a default theme if none is set.
func (h *BadgeThemeHandler) GetBadgeTheme(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetBadgeThemeRequest],
) (*connect.Response[dexdexv1.GetBadgeThemeResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("GetBadgeTheme called", "workspace_id", workspaceID)

	theme := h.store.GetBadgeTheme(workspaceID)
	if theme == nil {
		// Return a default theme when none is stored.
		theme = &dexdexv1.BadgeTheme{
			BadgeThemeId: "default",
			ThemeName:    "default",
		}
	}

	return connect.NewResponse(&dexdexv1.GetBadgeThemeResponse{
		Theme: theme,
	}), nil
}
