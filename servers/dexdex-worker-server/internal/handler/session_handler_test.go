package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func setupTestServer(t *testing.T) (dexdexv1connect.SessionServiceClient, *store.SessionStore) {
	t.Helper()

	logger := testLogger()
	sessionStore := store.NewSessionStore(logger)
	handler := NewSessionServiceHandler(sessionStore, logger)

	mux := http.NewServeMux()
	path, h := dexdexv1connect.NewSessionServiceHandler(handler)
	mux.Handle(path, h)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := dexdexv1connect.NewSessionServiceClient(
		http.DefaultClient,
		server.URL,
	)

	return client, sessionStore
}

func TestGetSessionOutputEmptySession(t *testing.T) {
	client, _ := setupTestServer(t)

	resp, err := client.GetSessionOutput(context.Background(), connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
		WorkspaceId: "ws-1",
		SessionId:   "nonexistent",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.Events) != 0 {
		t.Fatalf("expected 0 events for nonexistent session, got=%d", len(resp.Msg.Events))
	}
}

func TestGetSessionOutputWithData(t *testing.T) {
	client, sessionStore := setupTestServer(t)

	sessionStore.AppendOutput("sess-1", &dexdexv1.SessionOutputEvent{
		SessionId: "sess-1",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
		Body:      "hello",
	})
	sessionStore.AppendOutput("sess-1", &dexdexv1.SessionOutputEvent{
		SessionId: "sess-1",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL,
		Body:      "running tool",
	})

	resp, err := client.GetSessionOutput(context.Background(), connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
		WorkspaceId: "ws-1",
		SessionId:   "sess-1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.Events) != 2 {
		t.Fatalf("expected 2 events, got=%d", len(resp.Msg.Events))
	}
	if resp.Msg.Events[0].Body != "hello" {
		t.Fatalf("expected first event body=hello, got=%s", resp.Msg.Events[0].Body)
	}
	if resp.Msg.Events[1].Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL {
		t.Fatalf("expected second event kind=TOOL_CALL, got=%v", resp.Msg.Events[1].Kind)
	}
}

func TestGetSessionOutputIsolation(t *testing.T) {
	client, sessionStore := setupTestServer(t)

	sessionStore.AppendOutput("sess-a", &dexdexv1.SessionOutputEvent{
		SessionId: "sess-a",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
		Body:      "output a",
	})
	sessionStore.AppendOutput("sess-b", &dexdexv1.SessionOutputEvent{
		SessionId: "sess-b",
		Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR,
		Body:      "output b",
	})

	resp, err := client.GetSessionOutput(context.Background(), connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
		WorkspaceId: "ws-1",
		SessionId:   "sess-a",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.Events) != 1 {
		t.Fatalf("expected 1 event for sess-a, got=%d", len(resp.Msg.Events))
	}
	if resp.Msg.Events[0].Body != "output a" {
		t.Fatalf("expected body=output a, got=%s", resp.Msg.Events[0].Body)
	}
}
