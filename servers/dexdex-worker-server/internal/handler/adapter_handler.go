package handler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
)

// AdapterHandler implements the WorkerSessionAdapterService Connect RPC handler.
type AdapterHandler struct {
	dexdexv1connect.UnimplementedWorkerSessionAdapterServiceHandler
	store  *store.SessionStore
	logger *slog.Logger
}

// NewAdapterHandler creates a new AdapterHandler.
func NewAdapterHandler(store *store.SessionStore, logger *slog.Logger) *AdapterHandler {
	return &AdapterHandler{
		store:  store,
		logger: logger,
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
