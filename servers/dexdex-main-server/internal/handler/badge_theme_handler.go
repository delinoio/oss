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

// ListBadgeThemes returns all badge themes for a workspace.
func (h *BadgeThemeHandler) ListBadgeThemes(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListBadgeThemesRequest],
) (*connect.Response[dexdexv1.ListBadgeThemesResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("ListBadgeThemes called", "workspace_id", workspaceID)

	themes := h.store.ListBadgeThemes(workspaceID)

	return connect.NewResponse(&dexdexv1.ListBadgeThemesResponse{
		Themes: themes,
	}), nil
}

// UpsertBadgeTheme creates or updates a badge theme.
func (h *BadgeThemeHandler) UpsertBadgeTheme(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpsertBadgeThemeRequest],
) (*connect.Response[dexdexv1.UpsertBadgeThemeResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	themeName := strings.TrimSpace(req.Msg.ThemeName)
	colorKey := req.Msg.ColorKey

	if themeName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("theme_name is required"))
	}

	h.logger.Info("UpsertBadgeTheme called", "workspace_id", workspaceID, "theme_name", themeName, "color_key", colorKey.String())

	theme := h.store.UpsertBadgeTheme(workspaceID, themeName, colorKey)
	if theme == nil {
		err := fmt.Errorf("failed to upsert badge theme")
		h.logger.Error("UpsertBadgeTheme failed", "workspace_id", workspaceID, "theme_name", themeName, "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&dexdexv1.UpsertBadgeThemeResponse{
		Theme: theme,
	}), nil
}
