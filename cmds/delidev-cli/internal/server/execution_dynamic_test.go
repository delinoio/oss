package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestDynamicLifecyclePublishesOriginalNegativeWithoutRootFailure(t *testing.T) {
	f := newPublicationFixture(t)
	_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	native := &codex.Tool{ID: "dynamic-call", Kind: codex.DynamicTool, Status: codex.ToolRunning, Dynamic: &codex.DynamicToolCall{Tool: "fixture_tool", Arguments: json.RawMessage(`{}`)}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: native.ID, Tool: native})
	negative, duration := false, int64(17)
	native.Status = codex.ToolCompleted
	native.Dynamic.Success = &negative
	native.Dynamic.DurationMS = &duration
	native.Dynamic.ContentItems = []json.RawMessage{json.RawMessage(`{"type":"inputText","text":"This dynamic tool is unavailable in DeliDev."}`), json.RawMessage(`{"type":"inputImage","imageUrl":"https://private.invalid/image"}`), json.RawMessage(`{"type":"inputAudio","audioUrl":"data:audio/private"}`)}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolCompletedEvent, ItemID: native.ID, Tool: native})
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatal("lifecycle created duplicate records")
	}
	value, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || value.Tool == nil || value.Tool.Completed == nil || value.Tool.Completed.Dynamic.Success == nil || *value.Tool.Completed.Dynamic.Success || len(value.Tool.Completed.Dynamic.ContentItems) != 3 {
		t.Fatal(value, err)
	}
	if strings.Contains(string(rows[0].Data), "private.invalid") || strings.Contains(string(rows[0].Data), "data:audio") {
		t.Fatal("media source published")
	}
	sessionRow, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](sessionRow)
	if err != nil || session.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified {
		t.Fatal("tool outcome fabricated root/cleanup", session, err)
	}
}

func TestDynamicLostContentReceiptReplaysOnlyOriginalOutbox(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	native := &codex.Tool{ID: "dynamic-call", Kind: codex.DynamicTool, Status: codex.ToolRunning, Dynamic: &codex.DynamicToolCall{Tool: "fixture_tool", Arguments: json.RawMessage(`{}`)}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: native.ID, Tool: native})
	negative := false
	native.Status = codex.ToolCompleted
	native.Dynamic.Success = &negative
	native.Dynamic.ContentItems = []json.RawMessage{json.RawMessage(`{"type":"inputText","text":"original negative"}`)}
	event := codex.Event{Kind: codex.ToolCompletedEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, ItemID: native.ID, Tool: native}
	if handled, err := mapper.PublishCore(context.Background(), event); !handled || err == nil {
		t.Fatal("lost content acknowledgment was not retained")
	}
	native.Dynamic.Tool = "replacement"
	native.Dynamic.ContentItems = nil
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 5 || client.calls[3] != client.calls[4] {
		t.Fatal("outbox replay changed exact request")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 20})
	if err != nil || len(rows) != 1 {
		t.Fatal("replay duplicated lifecycle", err)
	}
	value, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || value.Tool.Completed.Dynamic.Tool != "fixture_tool" || value.Tool.Completed.Dynamic.ContentItems[0].Text == nil || *value.Tool.Completed.Dynamic.ContentItems[0].Text != "original negative" {
		t.Fatal("replay adopted replacement", err)
	}
}
