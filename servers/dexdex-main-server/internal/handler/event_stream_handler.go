package handler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	eventstream "github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
)

const heartbeatInterval = 30 * time.Second

// EventStreamHandler implements the EventStreamService Connect RPC handler.
type EventStreamHandler struct {
	dexdexv1connect.UnimplementedEventStreamServiceHandler
	fanOut *eventstream.FanOut
	logger *slog.Logger
}

// NewEventStreamHandler creates a new EventStreamHandler.
func NewEventStreamHandler(fanOut *eventstream.FanOut, logger *slog.Logger) *EventStreamHandler {
	return &EventStreamHandler{
		fanOut: fanOut,
		logger: logger,
	}
}

// StreamWorkspaceEvents implements the server-streaming RPC. It first replays
// buffered events from the requested sequence, then subscribes for live events
// and forwards them to the client. A heartbeat is sent every 30 seconds to keep
// the connection alive.
func (h *EventStreamHandler) StreamWorkspaceEvents(
	ctx context.Context,
	req *connect.Request[dexdexv1.StreamWorkspaceEventsRequest],
	stream *connect.ServerStream[dexdexv1.StreamWorkspaceEventsResponse],
) error {
	workspaceID := req.Msg.WorkspaceId
	fromSequence := req.Msg.FromSequence

	h.logger.Info("stream workspace events started",
		"workspace_id", workspaceID,
		"from_sequence", fromSequence,
	)

	// Phase 1: Replay buffered events.
	replayed, err := h.fanOut.Replay(workspaceID, fromSequence)
	if err != nil {
		var oorErr *eventstream.ReplayOutOfRangeError
		if errors.As(err, &oorErr) {
			h.logger.Warn("stream replay out of range",
				"workspace_id", workspaceID,
				"from_sequence", fromSequence,
				"earliest_available", oorErr.EarliestAvailableSequence,
			)

			detail, detailErr := connect.NewErrorDetail(&dexdexv1.EventStreamCursorOutOfRangeDetail{
				EarliestAvailableSequence: oorErr.EarliestAvailableSequence,
				RequestedFromSequence:     oorErr.RequestedFromSequence,
			})
			if detailErr != nil {
				h.logger.Error("failed to create error detail", "error", detailErr)
				return connect.NewError(connect.CodeOutOfRange, err)
			}

			connErr := connect.NewError(connect.CodeOutOfRange, err)
			connErr.AddDetail(detail)
			return connErr
		}
		return connect.NewError(connect.CodeInternal, err)
	}

	for _, resp := range replayed {
		if err := stream.Send(resp); err != nil {
			h.logger.Debug("stream send failed during replay",
				"workspace_id", workspaceID,
				"error", err,
			)
			return err
		}
	}

	h.logger.Debug("replay phase completed",
		"workspace_id", workspaceID,
		"replayed_count", len(replayed),
	)

	// Phase 2: Subscribe for live events.
	ch, unsub := h.fanOut.Subscribe(workspaceID)
	defer unsub()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("stream workspace events ended by client",
				"workspace_id", workspaceID,
			)
			return nil

		case resp, ok := <-ch:
			if !ok {
				h.logger.Info("subscriber channel closed",
					"workspace_id", workspaceID,
				)
				return nil
			}
			if err := stream.Send(resp); err != nil {
				h.logger.Debug("stream send failed",
					"workspace_id", workspaceID,
					"sequence", resp.Sequence,
					"error", err,
				)
				return err
			}

		case <-heartbeat.C:
			// Send an empty heartbeat response to keep the connection alive.
			heartbeatResp := &dexdexv1.StreamWorkspaceEventsResponse{
				WorkspaceId: workspaceID,
				EventType:   dexdexv1.StreamEventType_STREAM_EVENT_TYPE_UNSPECIFIED,
			}
			if err := stream.Send(heartbeatResp); err != nil {
				h.logger.Debug("heartbeat send failed",
					"workspace_id", workspaceID,
					"error", err,
				)
				return err
			}
			h.logger.Debug("heartbeat sent", "workspace_id", workspaceID)
		}
	}
}
