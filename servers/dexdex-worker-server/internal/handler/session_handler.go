package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
)

// SessionServiceHandler implements the dexdex.v1.SessionService Connect RPC handler.
type SessionServiceHandler struct {
	store  *store.SessionStore
	logger *slog.Logger
}

// NewSessionServiceHandler creates a new SessionServiceHandler.
func NewSessionServiceHandler(store *store.SessionStore, logger *slog.Logger) *SessionServiceHandler {
	return &SessionServiceHandler{
		store:  store,
		logger: logger,
	}
}

// GetSessionOutput returns all session output events for the requested session.
func (h *SessionServiceHandler) GetSessionOutput(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetSessionOutputRequest],
) (*connect.Response[dexdexv1.GetSessionOutputResponse], error) {
	sessionID := req.Msg.SessionId

	h.logger.Info("GetSessionOutput called",
		"session_id", sessionID,
		"workspace_id", req.Msg.WorkspaceId,
	)

	events := h.store.GetOutputs(sessionID)
	if events == nil {
		events = []*dexdexv1.SessionOutputEvent{}
	}

	h.logger.Debug("returning session outputs",
		"session_id", sessionID,
		"event_count", len(events),
	)

	return connect.NewResponse(&dexdexv1.GetSessionOutputResponse{
		Events: events,
	}), nil
}
