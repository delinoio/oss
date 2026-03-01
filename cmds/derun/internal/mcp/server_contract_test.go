package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/session"
	"github.com/delinoio/oss/cmds/derun/internal/state"
	"github.com/delinoio/oss/cmds/derun/internal/testutil"
)

func TestServerContractHistoricalReplayFromCursorZero(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := state.New(root)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	logger, err := logging.New(root)
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}
	defer logger.Close()

	sessionID := "01J1E111111111111111111111"
	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    SchemaVersion,
		SessionID:        sessionID,
		Command:          []string{"echo", "hello"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              100,
	}); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}
	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("hello"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput stdout returned error: %v", err)
	}
	if _, err := store.AppendOutput(sessionID, contracts.DerunOutputChannelStderr, []byte("-world"), time.Now().UTC()); err != nil {
		t.Fatalf("AppendOutput stderr returned error: %v", err)
	}
	if err := store.WriteFinal(session.Final{
		SchemaVersion: SchemaVersion,
		SessionID:     sessionID,
		State:         contracts.DerunSessionStateExited,
		EndedAt:       time.Now().UTC(),
		ExitCode:      intPtr(0),
	}); err != nil {
		t.Fatalf("WriteFinal returned error: %v", err)
	}

	server := NewServer(store, logger, 0, 24*time.Hour)
	client := newFramedRPCClient(t, server)

	client.call(t, "initialize", map[string]any{})
	client.callNotification(t, "notifications/initialized", map[string]any{})

	payload := client.callTool(t, contracts.DerunMCPToolReadOutput, map[string]any{
		"session_id": sessionID,
		"cursor":     "0",
		"max_bytes":  1024,
	})
	if payload["schema_version"] != SchemaVersion {
		t.Fatalf("unexpected schema_version: %v", payload["schema_version"])
	}
	if payload["next_cursor"] != "11" {
		t.Fatalf("unexpected next_cursor: got=%v want=11", payload["next_cursor"])
	}
	if eof, ok := payload["eof"].(bool); !ok || !eof {
		t.Fatalf("expected eof=true, got=%v", payload["eof"])
	}

	text := decodePayloadChunks(t, payload)
	if text != "hello-world" {
		t.Fatalf("unexpected replay payload: got=%q want=%q", text, "hello-world")
	}
}

func TestServerContractLiveTailThroughWaitOutput(t *testing.T) {
	root := testutil.TempStateRoot(t)
	store, err := state.New(root)
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	logger, err := logging.New(root)
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}
	defer logger.Close()

	sessionID := "01J1E222222222222222222222"
	if err := store.WriteMeta(session.Meta{
		SchemaVersion:    SchemaVersion,
		SessionID:        sessionID,
		Command:          []string{"sleep", "1"},
		WorkingDirectory: "/tmp",
		StartedAt:        time.Now().UTC().Add(-time.Minute),
		RetentionSeconds: int64((24 * time.Hour).Seconds()),
		TransportMode:    contracts.DerunTransportModePipe,
		TTYAttached:      false,
		PID:              os.Getpid(),
	}); err != nil {
		t.Fatalf("WriteMeta returned error: %v", err)
	}

	server := NewServer(store, logger, 0, 24*time.Hour)
	client := newFramedRPCClient(t, server)

	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = store.AppendOutput(sessionID, contracts.DerunOutputChannelStdout, []byte("live-tail"), time.Now().UTC())
	}()

	payload := client.callTool(t, contracts.DerunMCPToolWaitOutput, map[string]any{
		"session_id": sessionID,
		"cursor":     "0",
		"max_bytes":  1024,
		"timeout_ms": 1000,
	})
	if payload["schema_version"] != SchemaVersion {
		t.Fatalf("unexpected schema_version: %v", payload["schema_version"])
	}
	if timedOut, ok := payload["timed_out"].(bool); !ok || timedOut {
		t.Fatalf("expected timed_out=false, got=%v", payload["timed_out"])
	}
	if payload["next_cursor"] != "9" {
		t.Fatalf("unexpected next_cursor: got=%v want=9", payload["next_cursor"])
	}
	if decodePayloadChunks(t, payload) != "live-tail" {
		t.Fatalf("unexpected live tail payload")
	}
}

type framedRPCClient struct {
	reader *bufio.Reader
	writer *io.PipeWriter
	done   chan error
	cancel context.CancelFunc
	id     int
}

func newFramedRPCClient(t *testing.T, server *Server) *framedRPCClient {
	t.Helper()

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, serverIn, serverOut)
		_ = serverOut.Close()
	}()

	client := &framedRPCClient{
		reader: bufio.NewReader(clientIn),
		writer: clientOut,
		done:   done,
		cancel: cancel,
		id:     1,
	}
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		if err := <-done; err != nil && err != context.Canceled {
			t.Fatalf("server.Serve returned error: %v", err)
		}
	})

	return client
}

func (c *framedRPCClient) callTool(t *testing.T, tool contracts.DerunMCPTool, args map[string]any) map[string]any {
	t.Helper()

	resp := c.call(t, "tools/call", toolCallParams{Name: tool, Arguments: args})
	if resp.Error != nil {
		t.Fatalf("tools/call returned rpc error: code=%d message=%s", resp.Error.Code, resp.Error.Message)
	}
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result payload type: %T", resp.Result)
	}
	payload, ok := resultMap["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredContent in result payload")
	}
	return payload
}

func (c *framedRPCClient) callNotification(t *testing.T, method string, params any) {
	t.Helper()
	body := marshalRequestBody(t, rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  marshalRawParams(t, params),
	})
	writeFrame(t, c.writer, body)
}

func (c *framedRPCClient) call(t *testing.T, method string, params any) rpcResponse {
	t.Helper()

	id := c.id
	c.id++

	body := marshalRequestBody(t, rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  marshalRawParams(t, params),
	})
	writeFrame(t, c.writer, body)

	responseBody, err := readFrame(c.reader)
	if err != nil {
		t.Fatalf("readFrame returned error: %v", err)
	}

	var response rpcResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		t.Fatalf("Unmarshal response returned error: %v", err)
	}
	return response
}

func marshalRawParams(t *testing.T, params any) json.RawMessage {
	t.Helper()
	if params == nil {
		return nil
	}
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal params returned error: %v", err)
	}
	return payload
}

func marshalRequestBody(t *testing.T, req rpcRequest) []byte {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal request returned error: %v", err)
	}
	return body
}

func writeFrame(t *testing.T, writer *io.PipeWriter, body []byte) {
	t.Helper()
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		t.Fatalf("write frame header returned error: %v", err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write frame body returned error: %v", err)
	}
}

func decodePayloadChunks(t *testing.T, payload map[string]any) string {
	t.Helper()

	rawChunks, ok := payload["chunks"].([]any)
	if !ok {
		t.Fatalf("chunks payload has unexpected type: %T", payload["chunks"])
	}
	var builder strings.Builder
	for _, rawChunk := range rawChunks {
		chunkMap, ok := rawChunk.(map[string]any)
		if !ok {
			t.Fatalf("chunk payload has unexpected type: %T", rawChunk)
		}
		encodedData, ok := chunkMap["data_base64"].(string)
		if !ok {
			t.Fatalf("data_base64 has unexpected type: %T", chunkMap["data_base64"])
		}
		decoded, err := base64.StdEncoding.DecodeString(encodedData)
		if err != nil {
			t.Fatalf("DecodeString returned error: %v", err)
		}
		builder.Write(decoded)
	}
	return builder.String()
}
