package handler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/normalize"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/worktree"
)

// activeExecution tracks a running agent execution.
type activeExecution struct {
	cancel  context.CancelFunc
	inputCh chan string
}

// AdapterHandler implements the WorkerSessionAdapterService Connect RPC handler.
type AdapterHandler struct {
	dexdexv1connect.UnimplementedWorkerSessionAdapterServiceHandler
	store            *store.SessionStore
	wtManager        *worktree.Manager
	usageAccumulator *normalize.UsageAccumulator
	logger           *slog.Logger

	mu         sync.RWMutex
	executions map[string]*activeExecution // sessionID -> active execution
}

// NewAdapterHandler creates a new AdapterHandler.
func NewAdapterHandler(store *store.SessionStore, wtManager *worktree.Manager, logger *slog.Logger) *AdapterHandler {
	return &AdapterHandler{
		store:            store,
		wtManager:        wtManager,
		usageAccumulator: normalize.NewUsageAccumulator(),
		logger:           logger,
		executions:       make(map[string]*activeExecution),
	}
}

// GetAgentCapabilities returns the hardcoded list of supported agent CLI capabilities.
func (h *AdapterHandler) GetAgentCapabilities(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetAgentCapabilitiesRequest],
) (*connect.Response[dexdexv1.GetAgentCapabilitiesResponse], error) {
	h.logger.Info("GetAgentCapabilities called")

	capabilities := []*dexdexv1.AgentCapability{
		{
			AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
			SupportsFork: true,
			DisplayName:  "Claude Code",
		},
		{
			AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
			SupportsFork: false,
			DisplayName:  "Codex CLI",
		},
		{
			AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE,
			SupportsFork: false,
			DisplayName:  "OpenCode",
		},
	}

	return connect.NewResponse(&dexdexv1.GetAgentCapabilitiesResponse{
		Capabilities: capabilities,
	}), nil
}

// agentSupportsFork returns true if the given agent CLI type supports session forking.
func agentSupportsFork(agentType dexdexv1.AgentCliType) bool {
	return agentType == dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE
}

// ForkSessionAdapter creates a forked session from a parent session.
func (h *AdapterHandler) ForkSessionAdapter(
	ctx context.Context,
	req *connect.Request[dexdexv1.ForkSessionAdapterRequest],
) (*connect.Response[dexdexv1.ForkSessionAdapterResponse], error) {
	parentSessionID := req.Msg.SessionId

	h.logger.Info("ForkSessionAdapter called",
		"parent_session_id", parentSessionID,
		"fork_intent", req.Msg.ForkIntent.String(),
	)

	// Validate parent session exists.
	parentMeta, err := h.store.GetSessionMetadata(parentSessionID)
	if err != nil {
		h.logger.Warn("parent session not found",
			"parent_session_id", parentSessionID,
		)
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("parent session not found: %s", parentSessionID))
	}

	// Check if agent type supports forking.
	if !agentSupportsFork(parentMeta.AgentCliType) {
		h.logger.Warn("agent does not support session forking",
			"parent_session_id", parentSessionID,
			"agent_cli_type", parentMeta.AgentCliType.String(),
		)
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("agent does not support session forking"))
	}

	// Generate new session ID.
	forkedSessionID := fmt.Sprintf("fork-%s-%d", parentSessionID, time.Now().UnixNano())

	// Determine root session ID: if parent has a root, use that; otherwise parent is root.
	rootSessionID := parentMeta.RootSessionID
	if rootSessionID == "" {
		rootSessionID = parentSessionID
	}

	// Create and store forked session metadata.
	h.store.CreateSession(store.SessionMetadata{
		SessionID:       forkedSessionID,
		ParentSessionID: parentSessionID,
		RootSessionID:   rootSessionID,
		ForkStatus:      dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
		AgentCliType:    parentMeta.AgentCliType,
		CreatedAt:       time.Now(),
	})

	h.logger.Info("forked session created",
		"forked_session_id", forkedSessionID,
		"parent_session_id", parentSessionID,
		"root_session_id", rootSessionID,
	)

	return connect.NewResponse(&dexdexv1.ForkSessionAdapterResponse{
		ForkedSessionId: forkedSessionID,
	}), nil
}

// StartExecution begins agent execution in an isolated worktree and streams events back.
func (h *AdapterHandler) StartExecution(
	ctx context.Context,
	req *connect.Request[dexdexv1.StartExecutionRequest],
	stream *connect.ServerStream[dexdexv1.ExecutionEvent],
) error {
	sessionID := req.Msg.SessionId
	repoGroup := req.Msg.RepositoryGroup
	prompt := req.Msg.Prompt
	agentType := req.Msg.AgentCliType

	h.logger.Info("StartExecution called",
		"session_id", sessionID,
		"workspace_id", req.Msg.WorkspaceId,
		"unit_task_id", req.Msg.UnitTaskId,
		"sub_task_id", req.Msg.SubTaskId,
		"agent_cli_type", agentType.String(),
	)

	// Create cancellable context and input channel for this execution.
	execCtx, cancel := context.WithCancel(ctx)
	inputCh := make(chan string, 1)
	h.mu.Lock()
	h.executions[sessionID] = &activeExecution{cancel: cancel, inputCh: inputCh}
	h.mu.Unlock()

	defer func() {
		cancel()
		h.mu.Lock()
		delete(h.executions, sessionID)
		h.mu.Unlock()
	}()

	// Emit worktree PREPARING state
	_ = stream.Send(&dexdexv1.ExecutionEvent{
		Event: &dexdexv1.ExecutionEvent_WorktreeStatus{
			WorktreeStatus: &dexdexv1.WorktreeStatusEvent{
				SessionId: sessionID,
				State:     dexdexv1.WorktreeState_WORKTREE_STATE_PREPARING,
			},
		},
	})

	// Prepare worktree
	wCtx, err := h.wtManager.PrepareWorktree(execCtx, repoGroup, sessionID)
	if err != nil {
		h.logger.Error("failed to prepare worktree",
			"session_id", sessionID, "error", err)
		_ = stream.Send(&dexdexv1.ExecutionEvent{
			Event: &dexdexv1.ExecutionEvent_WorktreeStatus{
				WorktreeStatus: &dexdexv1.WorktreeStatusEvent{
					SessionId:    sessionID,
					State:        dexdexv1.WorktreeState_WORKTREE_STATE_FAILED,
					ErrorMessage: err.Error(),
				},
			},
		})
		_ = stream.Send(&dexdexv1.ExecutionEvent{
			Event: &dexdexv1.ExecutionEvent_StateChanged{
				StateChanged: &dexdexv1.SessionStateChangedEvent{
					SessionId: sessionID,
					Status:    dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED,
				},
			},
		})
		return connect.NewError(connect.CodeInternal, fmt.Errorf("worktree preparation failed: %w", err))
	}
	defer func() {
		_ = stream.Send(&dexdexv1.ExecutionEvent{
			Event: &dexdexv1.ExecutionEvent_WorktreeStatus{
				WorktreeStatus: &dexdexv1.WorktreeStatusEvent{
					SessionId:  sessionID,
					State:      dexdexv1.WorktreeState_WORKTREE_STATE_CLEANING_UP,
					PrimaryDir: wCtx.PrimaryDir,
				},
			},
		})
		_ = h.wtManager.CleanupWorktree(context.Background(), wCtx)
		h.wtManager.ReleaseSlot()
		_ = stream.Send(&dexdexv1.ExecutionEvent{
			Event: &dexdexv1.ExecutionEvent_WorktreeStatus{
				WorktreeStatus: &dexdexv1.WorktreeStatusEvent{
					SessionId: sessionID,
					State:     dexdexv1.WorktreeState_WORKTREE_STATE_CLEANED,
				},
			},
		})
	}()

	// Emit worktree READY state
	_ = stream.Send(&dexdexv1.ExecutionEvent{
		Event: &dexdexv1.ExecutionEvent_WorktreeStatus{
			WorktreeStatus: &dexdexv1.WorktreeStatusEvent{
				SessionId:  sessionID,
				State:      dexdexv1.WorktreeState_WORKTREE_STATE_READY,
				PrimaryDir: wCtx.PrimaryDir,
			},
		},
	})

	// Emit starting state
	_ = stream.Send(&dexdexv1.ExecutionEvent{
		Event: &dexdexv1.ExecutionEvent_StateChanged{
			StateChanged: &dexdexv1.SessionStateChangedEvent{
				SessionId: sessionID,
				Status:    dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_STARTING,
			},
		},
	})

	// Create session metadata
	sessionMeta := store.SessionMetadata{
		SessionID:    sessionID,
		ForkStatus:   dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
		AgentCliType: agentType,
		CreatedAt:    time.Now(),
	}
	if req.Msg.ParentSessionId != "" {
		sessionMeta.ParentSessionID = req.Msg.ParentSessionId
		parentMeta, pErr := h.store.GetSessionMetadata(req.Msg.ParentSessionId)
		if pErr == nil && parentMeta.RootSessionID != "" {
			sessionMeta.RootSessionID = parentMeta.RootSessionID
		} else {
			sessionMeta.RootSessionID = req.Msg.ParentSessionId
		}
	}
	h.store.CreateSession(sessionMeta)

	// Build agent command based on type
	parentSessionID := req.Msg.ParentSessionId
	agentCmd, err := buildAgentCommand(execCtx, agentType, wCtx.PrimaryDir, wCtx.AttachedDirs, prompt, sessionID, parentSessionID)
	if err != nil {
		h.logger.Error("failed to build agent command", "session_id", sessionID, "error", err)
		_ = stream.Send(&dexdexv1.ExecutionEvent{
			Event: &dexdexv1.ExecutionEvent_StateChanged{
				StateChanged: &dexdexv1.SessionStateChangedEvent{
					SessionId: sessionID,
					Status:    dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED,
				},
			},
		})
		return connect.NewError(connect.CodeInternal, err)
	}

	// Emit worktree EXECUTING state
	_ = stream.Send(&dexdexv1.ExecutionEvent{
		Event: &dexdexv1.ExecutionEvent_WorktreeStatus{
			WorktreeStatus: &dexdexv1.WorktreeStatusEvent{
				SessionId:  sessionID,
				State:      dexdexv1.WorktreeState_WORKTREE_STATE_EXECUTING,
				PrimaryDir: wCtx.PrimaryDir,
			},
		},
	})

	// Emit running state
	_ = stream.Send(&dexdexv1.ExecutionEvent{
		Event: &dexdexv1.ExecutionEvent_StateChanged{
			StateChanged: &dexdexv1.SessionStateChangedEvent{
				SessionId: sessionID,
				Status:    dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
			},
		},
	})

	// Run agent and stream output
	finalStatus := runAgentProcess(execCtx, agentCmd, sessionID, inputCh, stream, h.store, h.usageAccumulator, h.logger)

	// Persist accumulated usage to session metadata.
	if usage := h.usageAccumulator.GetSessionUsage(sessionID); usage != nil {
		h.store.UpdateUsage(sessionID, usage)
		h.logger.Info("session usage recorded",
			"session_id", sessionID,
			"input_tokens", usage.InputTokens,
			"output_tokens", usage.OutputTokens,
			"estimated_cost_usd", usage.EstimatedCostUsd,
		)
	}

	// Emit final state
	_ = stream.Send(&dexdexv1.ExecutionEvent{
		Event: &dexdexv1.ExecutionEvent_StateChanged{
			StateChanged: &dexdexv1.SessionStateChangedEvent{
				SessionId: sessionID,
				Status:    finalStatus,
			},
		},
	})

	h.logger.Info("StartExecution completed",
		"session_id", sessionID,
		"final_status", finalStatus.String(),
	)

	return nil
}

// SubmitWorkerInput sends user input to a running agent session.
func (h *AdapterHandler) SubmitWorkerInput(
	ctx context.Context,
	req *connect.Request[dexdexv1.SubmitWorkerInputRequest],
) (*connect.Response[dexdexv1.SubmitWorkerInputResponse], error) {
	sessionID := req.Msg.SessionId
	inputText := req.Msg.InputText

	h.logger.Info("SubmitWorkerInput called",
		"session_id", sessionID,
	)

	h.mu.RLock()
	exec, ok := h.executions[sessionID]
	h.mu.RUnlock()

	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no active execution for session %s", sessionID))
	}

	select {
	case exec.inputCh <- inputText:
		h.logger.Info("input submitted to session", "session_id", sessionID)
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("input channel full for session %s", sessionID))
	}

	return connect.NewResponse(&dexdexv1.SubmitWorkerInputResponse{}), nil
}

// CancelExecution cancels a running agent session.
func (h *AdapterHandler) CancelExecution(
	ctx context.Context,
	req *connect.Request[dexdexv1.CancelExecutionRequest],
) (*connect.Response[dexdexv1.CancelExecutionResponse], error) {
	sessionID := req.Msg.SessionId

	h.logger.Info("CancelExecution called", "session_id", sessionID)

	h.mu.RLock()
	exec, ok := h.executions[sessionID]
	h.mu.RUnlock()

	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no active execution for session %s", sessionID))
	}

	exec.cancel()
	h.logger.Info("execution cancelled", "session_id", sessionID)

	return connect.NewResponse(&dexdexv1.CancelExecutionResponse{}), nil
}
