package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// SessionHandler implements the SessionService Connect RPC handler.
type SessionHandler struct {
	dexdexv1connect.UnimplementedSessionServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewSessionHandler creates a new SessionHandler.
func NewSessionHandler(s store.Store, logger *slog.Logger) *SessionHandler {
	return &SessionHandler{
		store:  s,
		logger: logger,
	}
}

// GetSessionOutput returns session output events for a given session.
func (h *SessionHandler) GetSessionOutput(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetSessionOutputRequest],
) (*connect.Response[dexdexv1.GetSessionOutputResponse], error) {
	sessionID := req.Msg.SessionId
	h.logger.Info("GetSessionOutput called", "session_id", sessionID)

	events := h.store.GetSessionOutputs(sessionID)
	return connect.NewResponse(&dexdexv1.GetSessionOutputResponse{
		Events: events,
	}), nil
}
