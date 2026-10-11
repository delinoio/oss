// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestFunctionCallOutputLostReceiptKeepsOneOriginalObservation(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 3}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	text := "inert output"
	output := &domain.FunctionCallOutputObservation{Name: "original_function", Text: &text}
	event := codex.Event{Kind: codex.FunctionCallOutputEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: "original-result", Correlated: true, FunctionCallOutput: output}
	if handled, err := mapper.PublishCore(context.Background(), event); !handled || err == nil {
		t.Fatal("lost receipt not retained")
	}
	if _, err := mapper.PublishCore(context.Background(), event); err == nil {
		t.Fatal("uncertainty permitted another publication")
	}
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
	if len(client.calls) != 4 || client.calls[2] != client.calls[3] {
		t.Fatal("replay changed exact request")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("sleep replay duplicated transcript", err)
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.Tool == nil || m.Tool.Completed == nil || m.Tool.Completed.Status != domain.ToolResultObserved || m.Tool.Completed.FunctionCallOutput.Name != output.Name || m.Tool.Output == nil || *m.Tool.Output != text || m.FirstSequence != 3 || m.LastSequence != 3 || m.NativeID != event.ItemID {
		t.Fatal("result lost identity, text or completion-only order")
	}

	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](row)
	if err != nil || session.Execution.Outcome == domain.ExecutionSucceeded || session.Execution.CleanupVerified {
		t.Fatal("sleep fabricated turn result or cleanup")
	}
}

func TestFunctionCallOutputServerReceiptAndIdentity(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	text := "original"
	e := f.toolEvent(domain.ExecutionToolCompleted, 3, domain.NewID(), "original-result")
	e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.FunctionCallOutputTool, Status: domain.ToolResultObserved, FunctionCallOutput: &domain.FunctionCallOutputObservation{Name: "tool", Text: &text}}
	request := f.publish(t, e)
	if reply, err := f.call(request); err != nil || !reply.Msg.Replayed {
		t.Fatal("original receipt did not replay", err)
	}
	e.Sequence = 4
	e.Tool.ID = domain.NewID()
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("duplicate native identity admitted")
	}
	e.Tool.NativeID = "foreign"
	e.NativeTurnID = string(domain.NewID())
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("foreign turn admitted")
	}
}
