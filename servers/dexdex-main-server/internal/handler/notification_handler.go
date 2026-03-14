package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// NotificationHandler implements the NotificationService Connect RPC handler.
type NotificationHandler struct {
	dexdexv1connect.UnimplementedNotificationServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewNotificationHandler creates a new NotificationHandler.
func NewNotificationHandler(s store.Store, logger *slog.Logger) *NotificationHandler {
	return &NotificationHandler{
		store:  s,
		logger: logger,
	}
}

// ListNotifications returns all notifications for a workspace.
func (h *NotificationHandler) ListNotifications(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListNotificationsRequest],
) (*connect.Response[dexdexv1.ListNotificationsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("ListNotifications called", "workspace_id", workspaceID)

	notifications := h.store.ListNotifications(workspaceID)

	h.logger.Info("ListNotifications returning", "workspace_id", workspaceID, "count", len(notifications))

	return connect.NewResponse(&dexdexv1.ListNotificationsResponse{
		Notifications: notifications,
	}), nil
}
