package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WorkerClientInterface abstracts worker server operations for testing.
type WorkerClientInterface interface {
	GetAgentCapabilities(ctx context.Context) ([]*dexdexv1.AgentCapability, error)
	ForkSession(ctx context.Context, sessionID string, forkIntent dexdexv1.SessionForkIntent, prompt string) (string, error)
}

// SessionHandler implements the SessionService Connect RPC handler.
type SessionHandler struct {
	dexdexv1connect.UnimplementedSessionServiceHandler
	store        store.Store
	workerClient WorkerClientInterface
	dispatcher   Dispatcher
	fanOut       stream.EventBroadcaster
	logger       *slog.Logger
}

// NewSessionHandler creates a new SessionHandler.
func NewSessionHandler(s store.Store, wc WorkerClientInterface, dispatcher Dispatcher, fo stream.EventBroadcaster, logger *slog.Logger) *SessionHandler {
	return &SessionHandler{
		store:        s,
		workerClient: wc,
		dispatcher:   dispatcher,
		fanOut:       fo,
		logger:       logger,
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

// ListSessionCapabilities returns agent capabilities from the worker server.
func (h *SessionHandler) ListSessionCapabilities(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListSessionCapabilitiesRequest],
) (*connect.Response[dexdexv1.ListSessionCapabilitiesResponse], error) {
	h.logger.Info("ListSessionCapabilities called", "workspace_id", req.Msg.WorkspaceId)

	caps, err := h.workerClient.GetAgentCapabilities(ctx)
	if err != nil {
		h.logger.Error("failed to get agent capabilities", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&dexdexv1.ListSessionCapabilitiesResponse{
		Capabilities: caps,
	}), nil
}

// ForkSession creates a forked session via the worker server and records it in the store.
func (h *SessionHandler) ForkSession(
	ctx context.Context,
	req *connect.Request[dexdexv1.ForkSessionRequest],
) (*connect.Response[dexdexv1.ForkSessionResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	parentSessionID := req.Msg.ParentSessionId
	forkIntent := req.Msg.ForkIntent
	prompt := req.Msg.Prompt

	h.logger.Info("ForkSession called",
		"workspace_id", workspaceID,
		"parent_session_id", parentSessionID,
		"fork_intent", forkIntent.String(),
	)

	// Validate parent session exists.
	parentSummary, err := h.store.GetSessionSummary(workspaceID, parentSessionID)
	if err != nil {
		h.logger.Warn("parent session not found",
			"workspace_id", workspaceID,
			"parent_session_id", parentSessionID,
			"error", err,
		)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Call worker to fork the session.
	forkedSessionID, err := h.workerClient.ForkSession(ctx, parentSessionID, forkIntent, prompt)
	if err != nil {
		h.logger.Error("worker ForkSession failed",
			"parent_session_id", parentSessionID,
			"error", err,
		)
		return nil, err
	}

	// Compute root session ID: if parent has a root, use that; otherwise parent IS root.
	rootSessionID := parentSummary.RootSessionId
	if rootSessionID == "" {
		rootSessionID = parentSessionID
	}

	now := timestamppb.Now()
	summary := &dexdexv1.SessionSummary{
		SessionId:          forkedSessionID,
		ParentSessionId:    parentSessionID,
		RootSessionId:      rootSessionID,
		ForkStatus:         dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
		AgentSessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_STARTING,
		CreatedAt:          now,
	}

	h.store.AddSessionSummary(workspaceID, summary)

	// Publish SESSION_FORK_UPDATED stream event.
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_FORK_UPDATED, &stream.SessionForkUpdatedPayload{
		SessionForkUpdated: &dexdexv1.SessionForkUpdatedEvent{
			SessionSummary: summary,
		},
	})

	h.logger.Info("session forked successfully",
		"forked_session_id", forkedSessionID,
		"parent_session_id", parentSessionID,
		"root_session_id", rootSessionID,
	)

	// Dispatch fork execution: find the parent session's subtask to get repo group and agent type
	if h.dispatcher != nil {
		subTask, stErr := h.store.FindSubTaskBySessionID(workspaceID, parentSessionID)
		if stErr == nil && subTask != nil {
			unitTask, utErr := h.store.GetUnitTask(workspaceID, subTask.UnitTaskId)
			if utErr == nil && unitTask != nil && unitTask.RepositoryGroupId != "" {
				repoGroup, rgErr := resolveRepositoryGroupForExecution(h.store, workspaceID, unitTask.RepositoryGroupId)
				if rgErr == nil && repoGroup != nil {
					// Get agent CLI type from worker capabilities
					agentCliType := dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE
					caps, capsErr := h.workerClient.GetAgentCapabilities(ctx)
					if capsErr == nil {
						for _, cap := range caps {
							if cap.SupportsFork {
								agentCliType = cap.AgentCliType
								break
							}
						}
					}

					go func() {
						if dispatchErr := h.dispatcher.DispatchForkExecution(
							context.Background(),
							workspaceID,
							forkedSessionID,
							parentSessionID,
							forkIntent,
							prompt,
							repoGroup,
							agentCliType,
						); dispatchErr != nil {
							h.logger.Error("failed to dispatch fork execution",
								"forked_session_id", forkedSessionID,
								"error", dispatchErr,
							)
						}
					}()
				} else {
					h.logger.Warn(
						"failed to resolve repository group for fork execution",
						"workspace_id", workspaceID,
						"unit_task_id", unitTask.UnitTaskId,
						"repository_group_id", unitTask.RepositoryGroupId,
						"error", rgErr,
					)
				}
			}
		}
	}

	return connect.NewResponse(&dexdexv1.ForkSessionResponse{
		ForkedSession: summary,
	}), nil
}

// ListForkedSessions returns all forked sessions for a given parent session.
func (h *SessionHandler) ListForkedSessions(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListForkedSessionsRequest],
) (*connect.Response[dexdexv1.ListForkedSessionsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	parentSessionID := req.Msg.ParentSessionId

	h.logger.Info("ListForkedSessions called",
		"workspace_id", workspaceID,
		"parent_session_id", parentSessionID,
	)

	sessions := h.store.ListForkedSessions(workspaceID, parentSessionID)
	return connect.NewResponse(&dexdexv1.ListForkedSessionsResponse{
		Sessions: sessions,
	}), nil
}

// ArchiveForkedSession archives a forked session.
func (h *SessionHandler) ArchiveForkedSession(
	ctx context.Context,
	req *connect.Request[dexdexv1.ArchiveForkedSessionRequest],
) (*connect.Response[dexdexv1.ArchiveForkedSessionResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	sessionID := req.Msg.SessionId

	h.logger.Info("ArchiveForkedSession called",
		"workspace_id", workspaceID,
		"session_id", sessionID,
	)

	if err := h.store.ArchiveSession(workspaceID, sessionID); err != nil {
		h.logger.Warn("session not found for archiving",
			"workspace_id", workspaceID,
			"session_id", sessionID,
			"error", err,
		)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Get updated session summary for stream event.
	summary, err := h.store.GetSessionSummary(workspaceID, sessionID)
	if err == nil {
		h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_FORK_UPDATED, &stream.SessionForkUpdatedPayload{
			SessionForkUpdated: &dexdexv1.SessionForkUpdatedEvent{
				SessionSummary: summary,
			},
		})
	}

	return connect.NewResponse(&dexdexv1.ArchiveForkedSessionResponse{}), nil
}

// GetLatestWaitingSession returns the latest session in WAITING_FOR_INPUT status.
// Returns an empty response (not an error) if no waiting session exists.
func (h *SessionHandler) GetLatestWaitingSession(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetLatestWaitingSessionRequest],
) (*connect.Response[dexdexv1.GetLatestWaitingSessionResponse], error) {
	workspaceID := req.Msg.WorkspaceId

	h.logger.Info("GetLatestWaitingSession called", "workspace_id", workspaceID)

	session, err := h.store.GetLatestWaitingSession(workspaceID)
	if err != nil {
		// No waiting session is not an error - return empty response.
		h.logger.Debug("no waiting session found", "workspace_id", workspaceID)
		return connect.NewResponse(&dexdexv1.GetLatestWaitingSessionResponse{}), nil
	}

	return connect.NewResponse(&dexdexv1.GetLatestWaitingSessionResponse{
		Session: session,
	}), nil
}

// SubmitSessionInput submits user input for a waiting session and updates its status.
func (h *SessionHandler) SubmitSessionInput(
	ctx context.Context,
	req *connect.Request[dexdexv1.SubmitSessionInputRequest],
) (*connect.Response[dexdexv1.SubmitSessionInputResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	sessionID := req.Msg.SessionId

	h.logger.Info("SubmitSessionInput called",
		"workspace_id", workspaceID,
		"session_id", sessionID,
	)

	// Get session summary.
	summary, err := h.store.GetSessionSummary(workspaceID, sessionID)
	if err != nil {
		h.logger.Warn("session not found for input submission",
			"workspace_id", workspaceID,
			"session_id", sessionID,
			"error", err,
		)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Relay input to the worker via dispatcher
	if h.dispatcher != nil {
		if err := h.dispatcher.SubmitInput(ctx, sessionID, req.Msg.InputText); err != nil {
			h.logger.Warn("failed to relay input to worker",
				"workspace_id", workspaceID,
				"session_id", sessionID,
				"error", err,
			)
		}
	}

	// Update session status to RUNNING.
	summary.AgentSessionStatus = dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING

	// Publish SESSION_STATE_CHANGED stream event.
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED, &stream.SessionStateChangedPayload{
		SessionStateChanged: &dexdexv1.SessionStateChangedEvent{
			SessionId: sessionID,
			Status:    dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
		},
	})

	h.logger.Info("session input submitted, status updated to RUNNING",
		"workspace_id", workspaceID,
		"session_id", sessionID,
	)

	return connect.NewResponse(&dexdexv1.SubmitSessionInputResponse{}), nil
}

// ListAgentSessions returns session summaries for a workspace, optionally filtered by unit task ID.
func (h *SessionHandler) ListAgentSessions(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListAgentSessionsRequest],
) (*connect.Response[dexdexv1.ListAgentSessionsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	unitTaskID := req.Msg.UnitTaskId

	h.logger.Info("ListAgentSessions called", "workspace_id", workspaceID, "unit_task_id", unitTaskID)

	sessions := h.store.ListSessionSummaries(workspaceID, unitTaskID)
	return connect.NewResponse(&dexdexv1.ListAgentSessionsResponse{
		Sessions: sessions,
	}), nil
}

// GetAgentSessionLog returns session output events with session metadata.
func (h *SessionHandler) GetAgentSessionLog(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetAgentSessionLogRequest],
) (*connect.Response[dexdexv1.GetAgentSessionLogResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	sessionID := req.Msg.SessionId

	h.logger.Info("GetAgentSessionLog called", "workspace_id", workspaceID, "session_id", sessionID)

	events := h.store.GetSessionOutputs(sessionID)
	summary, _ := h.store.GetSessionSummary(workspaceID, sessionID)

	return connect.NewResponse(&dexdexv1.GetAgentSessionLogResponse{
		Events:  events,
		Session: summary,
	}), nil
}

// StopAgentSession stops a running agent session.
func (h *SessionHandler) StopAgentSession(
	ctx context.Context,
	req *connect.Request[dexdexv1.StopAgentSessionRequest],
) (*connect.Response[dexdexv1.StopAgentSessionResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	sessionID := req.Msg.SessionId

	h.logger.Info("StopAgentSession called", "workspace_id", workspaceID, "session_id", sessionID)

	// Find the subtask associated with this session and cancel it
	subTask, err := h.store.FindSubTaskBySessionID(workspaceID, sessionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if h.dispatcher != nil {
		_ = h.dispatcher.CancelSubTask(subTask.SubTaskId)
	}

	// Update session status
	summary, summaryErr := h.store.GetSessionSummary(workspaceID, sessionID)
	if summaryErr == nil {
		summary.AgentSessionStatus = dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_CANCELLED
		h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED, &stream.SessionStateChangedPayload{
			SessionStateChanged: &dexdexv1.SessionStateChangedEvent{
				SessionId: sessionID,
				Status:    dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_CANCELLED,
			},
		})
	}

	h.logger.Info("StopAgentSession completed", "workspace_id", workspaceID, "session_id", sessionID)

	return connect.NewResponse(&dexdexv1.StopAgentSessionResponse{
		Session: summary,
	}), nil
}
