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

func TestExecutionSleepLostReceiptKeepsOneOriginalObservation(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	duration := uint64(10)
	native := &codex.Tool{ID: "original-sleep", Kind: codex.SleepTool, Status: codex.ToolRunning, SleepDurationMS: &duration}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ToolStartedEvent, ItemID: native.ID, Tool: native})
	native.Status = codex.ToolCompleted
	event := codex.Event{Kind: codex.ToolCompletedEvent, ThreadID: f.thread, TurnID: f.turn, ItemID: native.ID, Correlated: true, Tool: native}
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
	if len(client.calls) != 5 || client.calls[3] != client.calls[4] {
		t.Fatal("replay changed exact request")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("sleep replay duplicated transcript", err)
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.Tool == nil || m.Tool.Completed == nil || *m.Tool.Started.Sleep.DurationMS != 10 || *m.Tool.Completed.Sleep.DurationMS != 10 || m.FirstSequence != 3 || m.LastSequence != 4 || m.NativeID != native.ID {
		t.Fatal("sleep lost duration/order")
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

func TestExecutionSleepServerRejectsDurationSubstitutionAndReplaysReceipt(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	duration := uint64(10)
	e := f.toolEvent(domain.ExecutionToolStarted, 3, id, "original-sleep")
	e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.SleepTool, Status: domain.ToolRunning, Sleep: &domain.SleepObservation{DurationMS: &duration}}
	f.publish(t, e)
	changed := uint64(11)
	e = f.toolEvent(domain.ExecutionToolCompleted, 4, id, "original-sleep")
	e.Tool.Snapshot = &domain.ToolSnapshot{Kind: domain.SleepTool, Status: domain.ToolCompleted, Sleep: &domain.SleepObservation{DurationMS: &changed}}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("server accepted changed duration")
	}
	e.Tool.Snapshot.Sleep.DurationMS = &duration
	request := f.publish(t, e)
	if reply, err := f.call(request); err != nil || !reply.Msg.Replayed {
		t.Fatal("sleep receipt replay failed", err)
	}
	e = f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	row, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](row)
	if err != nil || s.Execution.Outcome != domain.ExecutionSucceeded || s.Execution.CleanupVerified {
		t.Fatal("separate turn result or cleanup altered")
	}
}
