package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
)

func setupAdapterTestServer(t *testing.T) (dexdexv1connect.WorkerSessionAdapterServiceClient, *store.SessionStore) {
	t.Helper()

	logger := testLogger()
	sessionStore := store.NewSessionStore(logger)
	handler := NewAdapterHandler(sessionStore, nil, logger)

	mux := http.NewServeMux()
	path, h := dexdexv1connect.NewWorkerSessionAdapterServiceHandler(handler)
	mux.Handle(path, h)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := dexdexv1connect.NewWorkerSessionAdapterServiceClient(
		http.DefaultClient,
		server.URL,
	)

	return client, sessionStore
}

func TestAdapterHandler_GetAgentCapabilities(t *testing.T) {
	client, _ := setupAdapterTestServer(t)

	resp, err := client.GetAgentCapabilities(context.Background(), connect.NewRequest(&dexdexv1.GetAgentCapabilitiesRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.Capabilities) != 3 {
		t.Fatalf("expected 3 capabilities, got=%d", len(resp.Msg.Capabilities))
	}

	// Verify Claude Code capability.
	claude := resp.Msg.Capabilities[0]
	if claude.AgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE {
		t.Fatalf("expected first capability to be CLAUDE_CODE, got=%v", claude.AgentCliType)
	}
	if !claude.SupportsFork {
		t.Fatal("expected CLAUDE_CODE to support fork")
	}
	if claude.DisplayName != "Claude Code" {
		t.Fatalf("expected display_name=Claude Code, got=%s", claude.DisplayName)
	}

	// Verify Codex CLI capability.
	codex := resp.Msg.Capabilities[1]
	if codex.AgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI {
		t.Fatalf("expected second capability to be CODEX_CLI, got=%v", codex.AgentCliType)
	}
	if codex.SupportsFork {
		t.Fatal("expected CODEX_CLI to not support fork")
	}

	// Verify OpenCode capability.
	opencode := resp.Msg.Capabilities[2]
	if opencode.AgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE {
		t.Fatalf("expected third capability to be OPENCODE, got=%v", opencode.AgentCliType)
	}
	if opencode.SupportsFork {
		t.Fatal("expected OPENCODE to not support fork")
	}
}

func TestAdapterHandler_ForkSessionAdapter_Success(t *testing.T) {
	client, sessionStore := setupAdapterTestServer(t)

	// Create a parent session with CLAUDE_CODE agent type.
	sessionStore.CreateSession(store.SessionMetadata{
		SessionID:    "parent-1",
		ForkStatus:   dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
		CreatedAt:    time.Now(),
	})

	resp, err := client.ForkSessionAdapter(context.Background(), connect.NewRequest(&dexdexv1.ForkSessionAdapterRequest{
		SessionId:  "parent-1",
		ForkIntent: dexdexv1.SessionForkIntent_SESSION_FORK_INTENT_EXPLORE_ALTERNATIVE,
		Prompt:     "explore alternative approach",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	forkedID := resp.Msg.ForkedSessionId
	if forkedID == "" {
		t.Fatal("expected non-empty forked_session_id")
	}

	// Verify the forked session metadata was stored.
	forkedMeta, err := sessionStore.GetSessionMetadata(forkedID)
	if err != nil {
		t.Fatalf("forked session metadata not found: %v", err)
	}
	if forkedMeta.ParentSessionID != "parent-1" {
		t.Fatalf("expected parent_session_id=parent-1, got=%s", forkedMeta.ParentSessionID)
	}
	if forkedMeta.RootSessionID != "parent-1" {
		t.Fatalf("expected root_session_id=parent-1, got=%s", forkedMeta.RootSessionID)
	}
	if forkedMeta.ForkStatus != dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE {
		t.Fatalf("expected fork_status=ACTIVE, got=%v", forkedMeta.ForkStatus)
	}
	if forkedMeta.AgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE {
		t.Fatalf("expected agent_cli_type=CLAUDE_CODE, got=%v", forkedMeta.AgentCliType)
	}
}

func TestAdapterHandler_ForkSessionAdapter_ParentNotFound(t *testing.T) {
	client, _ := setupAdapterTestServer(t)

	_, err := client.ForkSessionAdapter(context.Background(), connect.NewRequest(&dexdexv1.ForkSessionAdapterRequest{
		SessionId:  "nonexistent",
		ForkIntent: dexdexv1.SessionForkIntent_SESSION_FORK_INTENT_EXPLORE_ALTERNATIVE,
		Prompt:     "test",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent parent session")
	}

	connectErr, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected *connect.Error, got=%T", err)
	}
	if connectErr.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got=%v", connectErr.Code())
	}
}

func TestAdapterHandler_ForkSessionAdapter_UnsupportedFork(t *testing.T) {
	client, sessionStore := setupAdapterTestServer(t)

	// Create a parent session with CODEX_CLI agent type (does not support fork).
	sessionStore.CreateSession(store.SessionMetadata{
		SessionID:    "codex-session",
		ForkStatus:   dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
		CreatedAt:    time.Now(),
	})

	_, err := client.ForkSessionAdapter(context.Background(), connect.NewRequest(&dexdexv1.ForkSessionAdapterRequest{
		SessionId:  "codex-session",
		ForkIntent: dexdexv1.SessionForkIntent_SESSION_FORK_INTENT_BRANCH_EXPERIMENT,
		Prompt:     "test fork",
	}))
	if err == nil {
		t.Fatal("expected error for unsupported fork")
	}

	connectErr, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected *connect.Error, got=%T", err)
	}
	if connectErr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got=%v", connectErr.Code())
	}
}
