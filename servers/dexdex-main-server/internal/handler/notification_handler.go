package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
)

// NotificationHandler implements the NotificationService Connect RPC handler.
type NotificationHandler struct {
	dexdexv1connect.UnimplementedNotificationServiceHandler
	store  store.Store
	fanOut *stream.FanOut
	logger *slog.Logger
}

// NewNotificationHandler creates a new NotificationHandler.
func NewNotificationHandler(s store.Store, fanOut *stream.FanOut, logger *slog.Logger) *NotificationHandler {
	return &NotificationHandler{
		store:  s,
		fanOut: fanOut,
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

// MarkNotificationRead marks a notification as read.
func (h *NotificationHandler) MarkNotificationRead(
	ctx context.Context,
	req *connect.Request[dexdexv1.MarkNotificationReadRequest],
) (*connect.Response[dexdexv1.MarkNotificationReadResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	notificationID := req.Msg.NotificationId
	h.logger.Info("MarkNotificationRead called", "workspace_id", workspaceID, "notification_id", notificationID)

	notif, err := h.store.MarkNotificationRead(workspaceID, notificationID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Publish notification update event
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_NOTIFICATION_CREATED, &stream.NotificationCreatedPayload{
		NotificationCreated: &dexdexv1.NotificationCreatedEvent{Notification: notif},
	})

	return connect.NewResponse(&dexdexv1.MarkNotificationReadResponse{
		Notification: notif,
	}), nil
}
